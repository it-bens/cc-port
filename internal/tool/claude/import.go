package claude

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/rewrite"
)

// projectPathKey is the manifest key cc-port always pre-fills with the
// import target path.
const projectPathKey = "{{PROJECT_PATH}}"

// homePathKey is the manifest key cc-port supplies from the import
// machine's real home directory.
const homePathKey = "{{HOME}}"

// projectDirKey is the manifest key cc-port resolves to the import
// target's encoded storage directory.
const projectDirKey = "{{PROJECT_DIR}}"

// filePerm is the mode used for files written during import that carry no
// secrets. rw-r--r-- matches the permissions Claude Code itself writes for
// project data files.
const filePerm = os.FileMode(0o644)

// secretFilePerm is the mode used for files that may carry user secrets
// (history.jsonl, .claude.json, session transcripts). Locked to owner so a
// multi-user or shared-home layout does not leak pasted tokens or MCP env
// values to group or others.
const secretFilePerm = os.FileMode(0o600)

// dirPerm is the mode used for directories import creates. 0o755 so
// group/others can traverse project subdirs shared with tooling.
const dirPerm = os.FileMode(0o755)

// UnknownArchiveEntryError reports an archive entry, within Claude's
// namespace, whose tool-relative name does not match any known prefix.
type UnknownArchiveEntryError struct {
	Name string
}

func (e *UnknownArchiveEntryError) Error() string {
	return fmt.Sprintf("unknown claude archive entry: %q", e.Name)
}

// PreflightDirs implements tool.Importer: every directory the importer
// will write under, so the generic import command can resolve symlink
// parents for all of them before touching the archive.
func (workspace *Workspace) PreflightDirs(project string) []string {
	home := workspace.home
	return []string{
		home.ProjectDir(project),
		filepath.Dir(home.HistoryFile()),
		filepath.Dir(home.ConfigFile),
		home.FileHistoryDir(),
		home.TodosDir(),
		filepath.Join(home.UsageDataDir(), "session-meta"),
		filepath.Join(home.UsageDataDir(), "facets"),
		home.PluginsDataDir(),
		home.TasksDir(),
	}
}

// ImplicitAnchors implements tool.Importer: {{PROJECT_PATH}} is the import
// target itself, {{HOME}} is the import machine's real home directory
// (independent of any --claude-home override), and {{PROJECT_DIR}} is the
// target's encoded storage directory.
func (workspace *Workspace) ImplicitAnchors(project string) (map[string]string, error) {
	home, err := homeAnchor()
	if err != nil {
		return nil, err
	}
	return map[string]string{
		projectPathKey: project,
		homePathKey:    home,
		projectDirKey:  workspace.home.ProjectDir(project),
	}, nil
}

// Stage implements tool.Importer. It routes one archive entry to its
// destination: session-keyed groups and plain project files (sessions/,
// memory/) each stage to a sibling temp beside their final path via
// archive.StageSibling; history.jsonl and config.json defer to Finalize,
// since both need a read-merge-write against existing content rather than
// plain promotion, and return no Staged record.
func (workspace *Workspace) Stage(
	_ context.Context, project string, entry archive.Entry, resolutions map[string]string,
) ([]archive.Staged, error) {
	name := entry.Name

	if target, relative, ok := matchSessionKeyedPrefix(name); ok {
		staged, _, err := archive.StageSibling(target.HomeBaseDir(workspace.home), relative, entry, resolutions, filePerm, entry.Modified)
		if err != nil {
			return stagedError(staged, err)
		}
		return []archive.Staged{staged}, nil
	}

	switch {
	case strings.HasPrefix(name, "sessions/"):
		relative := strings.TrimPrefix(name, "sessions/")
		staged, _, err := archive.StageSibling(
			workspace.home.ProjectDir(project), relative, entry, resolutions, secretFilePerm, entry.Modified,
		)
		if err != nil {
			return stagedError(staged, err)
		}
		return []archive.Staged{staged}, nil

	case strings.HasPrefix(name, "memory/"):
		relative := filepath.Join("memory", strings.TrimPrefix(name, "memory/"))
		staged, _, err := archive.StageSibling(
			workspace.home.ProjectDir(project), relative, entry, resolutions, filePerm, entry.Modified,
		)
		if err != nil {
			return stagedError(staged, err)
		}
		return []archive.Staged{staged}, nil

	case name == "history/history.jsonl":
		data, err := entry.ReadAll()
		if err != nil {
			return nil, err
		}
		workspace.historyAppends = append(workspace.historyAppends, archive.ApplyResolutions(data, resolutions))
		return nil, nil

	case strings.HasPrefix(name, "file-history/"):
		relative := strings.TrimPrefix(name, "file-history/")
		// File-history snapshots are opaque bytes by policy: no
		// placeholder resolution runs over them (resolutions == nil).
		staged, _, err := archive.StageSibling(workspace.home.FileHistoryDir(), relative, entry, nil, filePerm, entry.Modified)
		if err != nil {
			return stagedError(staged, err)
		}
		return []archive.Staged{staged}, nil

	case name == "config.json":
		data, err := entry.ReadAll()
		if err != nil {
			return nil, err
		}
		workspace.configBlock = archive.ApplyResolutions(data, resolutions)
		return nil, nil

	default:
		return nil, &UnknownArchiveEntryError{Name: name}
	}
}

func stagedError(staged archive.Staged, err error) ([]archive.Staged, error) {
	if staged.Temp == "" {
		return nil, err
	}
	return []archive.Staged{staged}, err
}

// matchSessionKeyedPrefix returns the first registries.go entry whose
// ZipPrefix matches name, along with name relative to that prefix.
func matchSessionKeyedPrefix(name string) (RegistryEntry, string, bool) {
	for _, target := range Registries {
		if target.ZipPrefix == "" {
			continue
		}
		if strings.HasPrefix(name, target.ZipPrefix) {
			return target, strings.TrimPrefix(name, target.ZipPrefix), true
		}
	}
	return RegistryEntry{}, "", false
}

// Finalize implements tool.Importer: it merges the accumulated history
// append and config block, each idempotently, so a re-run of the same
// import never duplicates a history line or re-splices an identical config
// block differently.
func (workspace *Workspace) Finalize(_ context.Context, project string, _ *archive.StagedSet) error {
	if len(workspace.historyAppends) > 0 {
		if err := workspace.finalizeHistory(); err != nil {
			return err
		}
	}
	if workspace.configBlock != nil {
		if err := workspace.finalizeConfig(project); err != nil {
			return err
		}
	}
	return nil
}

// finalizeHistory appends every new line from workspace.historyAppends to
// history.jsonl, deduplicating by exact line against both the file's
// existing content and lines already appended in this run. history.jsonl
// carries no natural primary key, so exact-line equality is the dedup
// contract: a re-import of the same archive appends nothing new.
func (workspace *Workspace) finalizeHistory() error {
	historyPath := workspace.home.HistoryFile()
	existing, err := readExistingOrEmpty(historyPath)
	if err != nil {
		return fmt.Errorf("read existing history.jsonl: %w", err)
	}

	seen := make(map[string]struct{})
	existingLines, err := scanHistoryLines(existing)
	if err != nil {
		return fmt.Errorf("scan existing history.jsonl: %w", err)
	}
	for _, line := range existingLines {
		seen[string(line)] = struct{}{}
	}

	var newLines [][]byte
	for _, chunk := range workspace.historyAppends {
		if err := validateIncomingHistoryLineLimits(chunk); err != nil {
			return err
		}
		for _, line := range splitNonEmptyLines(chunk) {
			key := string(line)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			newLines = append(newLines, line)
		}
	}
	if len(newLines) == 0 {
		return nil
	}

	var merged bytes.Buffer
	merged.Write(existing)
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		merged.WriteByte('\n')
	}
	for _, line := range newLines {
		merged.Write(line)
		merged.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(historyPath), dirPerm); err != nil {
		return fmt.Errorf("create directories for %q: %w", historyPath, err)
	}
	if err := rewrite.SafeWriteFile(historyPath, merged.Bytes(), secretFilePerm); err != nil {
		return fmt.Errorf("write history.jsonl: %w", err)
	}
	return nil
}

func validateIncomingHistoryLineLimits(data []byte) error {
	for lineNumber, line := range bytes.Split(data, []byte("\n")) {
		if len(line) >= MaxHistoryLine {
			return fmt.Errorf(
				"incoming history/history.jsonl line %d must be shorter than %d bytes: %w",
				lineNumber+1, MaxHistoryLine, bufio.ErrTooLong,
			)
		}
	}
	return nil
}

// splitNonEmptyLines splits data on '\n' and drops blank lines, so a
// trailing terminator or a blank separator line never becomes a spurious
// dedup entry.
func splitNonEmptyLines(data []byte) [][]byte {
	trimmed := bytes.TrimRight(data, "\n")
	if len(trimmed) == 0 {
		return nil
	}
	var lines [][]byte
	for _, line := range bytes.Split(trimmed, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func scanHistoryLines(data []byte) ([][]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64<<10), MaxHistoryLine)
	var lines [][]byte
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		lines = append(lines, append([]byte(nil), line...))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

// finalizeConfig splices workspace.configBlock into ~/.claude.json under
// the target project's key, preserving every byte outside the inserted
// entry. Re-running with the same block produces byte-identical output, so
// the merge is naturally idempotent without extra bookkeeping.
func (workspace *Workspace) finalizeConfig(project string) error {
	configPath := workspace.home.ConfigFile
	existing, err := readExistingOrEmpty(configPath)
	if err != nil {
		return fmt.Errorf("read existing config for merge: %w", err)
	}
	merged, err := mergeProjectConfigBytes(existing, configPath, project, workspace.configBlock)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), dirPerm); err != nil {
		return fmt.Errorf("create directories for %q: %w", configPath, err)
	}
	if err := rewrite.SafeWriteFile(configPath, merged, secretFilePerm); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	return nil
}

func readExistingOrEmpty(path string) ([]byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: trusted Home-derived path
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// InvalidConfigJSONError reports that mergeProjectConfigBytes rejected
// existingData because it is not valid JSON.
type InvalidConfigJSONError struct {
	Path string
}

func (e *InvalidConfigJSONError) Error() string {
	return fmt.Sprintf("invalid JSON in config file %q", e.Path)
}

// mergeProjectConfigBytes returns the JSON bytes of existingData with
// blockData spliced in as the project entry under targetPath, using sjson
// so every byte outside the inserted entry survives untouched.
func mergeProjectConfigBytes(existingData []byte, configPath, targetPath string, blockData []byte) ([]byte, error) {
	if len(existingData) == 0 {
		existingData = []byte(`{}`)
	} else if !gjson.ValidBytes(existingData) {
		return nil, &InvalidConfigJSONError{Path: configPath}
	}

	path := "projects." + rewrite.EscapeSJSONKey(targetPath)
	updatedData, err := sjson.SetRawBytes(existingData, path, blockData)
	if err != nil {
		return nil, fmt.Errorf("set project block in config file %q: %w", configPath, err)
	}
	return updatedData, nil
}
