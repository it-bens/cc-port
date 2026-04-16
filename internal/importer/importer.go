// Package importer handles importing cc-port ZIP archives into a Claude Code home directory.
package importer

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/lock"
)

// dirPerm is the mode used for directories created during import.
// rwxr-xr-x — group/others need execute to traverse into project subdirs that
// the user may share read access to (e.g. memory files surfaced to tooling).
const dirPerm = os.FileMode(0755)

// filePerm is the mode used for files written during import.
// rw-r--r-- — owner read/write, group and others read-only, matching the
// permissions Claude Code itself writes for project data files.
const filePerm = os.FileMode(0644)

// Options configures an import operation.
type Options struct {
	ArchivePath string
	TargetPath  string
	Resolutions map[string]string
}

// Run imports a cc-port ZIP archive into claudeHome, routing each file to the
// correct destination and resolving all placeholders using importOptions.Resolutions.
//
// Before any work, Run acquires an exclusive advisory lock over claudeHome
// and aborts if a Claude Code session is currently live or if another
// cc-port invocation is already operating on the same directory.
func Run(claudeHome *claude.Home, importOptions Options) error {
	lockHandle, err := lock.Acquire(claudeHome)
	if err != nil {
		return err
	}
	defer func() { _ = lockHandle.Release() }()

	if err := ValidateResolutions(importOptions.Resolutions); err != nil {
		return fmt.Errorf("validate resolutions: %w", err)
	}

	encodedProjectDir := claudeHome.ProjectDir(importOptions.TargetPath)
	if err := CheckConflict(encodedProjectDir); err != nil {
		return fmt.Errorf("conflict check: %w", err)
	}

	zipReader, err := zip.OpenReader(importOptions.ArchivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = zipReader.Close() }()

	// Validate the archive structure by parsing metadata.xml. The parsed value
	// is not yet consumed; the call's purpose is to fail fast on a malformed or
	// non-cc-port archive before we touch the destination.
	if _, err := export.ReadManifestFromZip(importOptions.ArchivePath); err != nil {
		return fmt.Errorf("read metadata from archive: %w", err)
	}

	// Ensure {{PROJECT_PATH}} has a resolution; default to TargetPath.
	resolutions := importOptions.Resolutions
	if resolutions == nil {
		resolutions = make(map[string]string)
	}
	if _, hasProjectPath := resolutions["{{PROJECT_PATH}}"]; !hasProjectPath {
		resolutions["{{PROJECT_PATH}}"] = importOptions.TargetPath
	}

	if err := os.MkdirAll(encodedProjectDir, dirPerm); err != nil {
		return fmt.Errorf("create project directory %q: %w", encodedProjectDir, err)
	}

	for _, zipFile := range zipReader.File {
		if zipFile.Name == "metadata.xml" {
			continue
		}

		content, err := readZipFile(zipFile)
		if err != nil {
			return fmt.Errorf("read zip entry %q: %w", zipFile.Name, err)
		}

		resolvedContent := ResolvePlaceholders(content, resolutions)

		if err := writeImportedFile(
			claudeHome, encodedProjectDir, importOptions.TargetPath, zipFile.Name, resolvedContent,
		); err != nil {
			return fmt.Errorf("write imported file %q: %w", zipFile.Name, err)
		}
	}

	return nil
}

// readZipFile opens zipFile, reads all its content, and closes it.
func readZipFile(zipFile *zip.File) ([]byte, error) {
	readCloser, err := zipFile.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip file entry: %w", err)
	}
	defer func() { _ = readCloser.Close() }()

	data, err := io.ReadAll(readCloser)
	if err != nil {
		return nil, fmt.Errorf("read zip file entry: %w", err)
	}

	return data, nil
}

// writeImportedFile routes a zip entry to the correct destination on disk
// based on its path prefix.
//
// Routing rules:
//   - sessions/*              → encoded project dir (structure preserved)
//   - memory/*                → encoded project dir/memory/
//   - history/history.jsonl   → append to claudeHome history file
//   - file-history/*          → claudeHome file-history dir
//   - config.json             → merge into claudeHome config file under targetPath key
func writeImportedFile(
	claudeHome *claude.Home, encodedProjectDir, targetPath, zipEntryName string, content []byte,
) error {
	switch {
	case strings.HasPrefix(zipEntryName, "sessions/"):
		relativePath := strings.TrimPrefix(zipEntryName, "sessions/")
		destinationPath := filepath.Join(encodedProjectDir, relativePath)
		return writeFile(destinationPath, content)

	case strings.HasPrefix(zipEntryName, "memory/"):
		relativePath := strings.TrimPrefix(zipEntryName, "memory/")
		destinationPath := filepath.Join(encodedProjectDir, "memory", relativePath)
		return writeFile(destinationPath, content)

	case zipEntryName == "history/history.jsonl":
		return appendToHistory(claudeHome.HistoryFile(), content)

	case strings.HasPrefix(zipEntryName, "file-history/"):
		relativePath := strings.TrimPrefix(zipEntryName, "file-history/")
		destinationPath := filepath.Join(claudeHome.FileHistoryDir(), relativePath)
		return writeFile(destinationPath, content)

	case zipEntryName == "config.json":
		return mergeProjectConfig(claudeHome.ConfigFile, targetPath, content)

	default:
		// Unknown entry — write into the project directory preserving name.
		destinationPath := filepath.Join(encodedProjectDir, filepath.Base(zipEntryName))
		return writeFile(destinationPath, content)
	}
}

// writeFile writes content to path, creating any missing parent directories.
func writeFile(destinationPath string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(destinationPath), dirPerm); err != nil {
		return fmt.Errorf("create directories for %q: %w", destinationPath, err)
	}
	if err := os.WriteFile(destinationPath, content, filePerm); err != nil {
		return fmt.Errorf("write file %q: %w", destinationPath, err)
	}
	return nil
}

// appendToHistory appends data to the history file at historyPath, creating
// the file (and its parent directories) if they do not exist.
func appendToHistory(historyPath string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(historyPath), dirPerm); err != nil {
		return fmt.Errorf("create directories for history file: %w", err)
	}

	historyFile, err := os.OpenFile( //nolint:gosec // G304: trusted ClaudeHome path
		historyPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerm,
	)
	if err != nil {
		return fmt.Errorf("open history file for append: %w", err)
	}
	defer func() { _ = historyFile.Close() }()

	if _, err := historyFile.Write(data); err != nil {
		return fmt.Errorf("append to history file: %w", err)
	}

	return nil
}

// escapeSJSONKey escapes the characters sjson treats as path meta-characters
// (`\` and `.`) so an arbitrary string can be used as a single key segment
// in an sjson path expression.
func escapeSJSONKey(key string) string {
	key = strings.ReplaceAll(key, `\`, `\\`)
	key = strings.ReplaceAll(key, `.`, `\.`)
	return key
}

// mergeProjectConfig inserts blockData as the project entry under
// targetPath inside the JSON document at configPath. It uses sjson to
// splice only the projects object, preserving every byte outside the
// inserted entry — original key order, indent style, and trailing newlines
// all survive a merge. If the config file does not exist, a minimal `{}`
// is used as the base document.
func mergeProjectConfig(configPath, targetPath string, blockData []byte) error {
	existingData, err := os.ReadFile(configPath) //nolint:gosec // G304: path comes from trusted ClaudeHome config
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read config file %q: %w", configPath, err)
	}

	if len(existingData) == 0 {
		existingData = []byte(`{}`)
	} else if !gjson.ValidBytes(existingData) {
		return fmt.Errorf("invalid JSON in config file %q", configPath)
	}

	path := "projects." + escapeSJSONKey(targetPath)
	updatedData, err := sjson.SetRawBytes(existingData, path, blockData)
	if err != nil {
		return fmt.Errorf("set project block in config file %q: %w", configPath, err)
	}

	if err := os.MkdirAll(filepath.Dir(configPath), dirPerm); err != nil {
		return fmt.Errorf("create directories for config file: %w", err)
	}

	//nolint:gosec // G703: writing attacker-controlled archive contents is the purpose of import
	if err := os.WriteFile(configPath, updatedData, filePerm); err != nil {
		return fmt.Errorf("write config file %q: %w", configPath, err)
	}

	return nil
}
