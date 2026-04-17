// Package scan provides utilities for scanning Claude Code rules files.
package scan

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Warning represents a single line in a rules file that contains a search path.
type Warning struct {
	File string // Filename (not full path)
	Line int    // 1-based line number
	Text string // The full line text
	Path string // Which search path matched
}

// Rules scans all .md files in rulesDir for occurrences of any of the given paths.
// Returns nil, nil if the directory does not exist.
func Rules(rulesDir string, paths ...string) ([]Warning, error) {
	_, err := os.Stat(rulesDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	entries, err := os.ReadDir(rulesDir)
	if err != nil {
		return nil, err
	}

	var warnings []Warning

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		fileName := entry.Name()
		if !strings.HasSuffix(fileName, ".md") {
			continue
		}

		filePath := filepath.Join(rulesDir, fileName)
		fileWarnings, err := scanFile(filePath, fileName, paths)
		if err != nil {
			return nil, err
		}

		warnings = append(warnings, fileWarnings...)
	}

	return warnings, nil
}

func scanFile(filePath string, fileName string, paths []string) ([]Warning, error) {
	file, err := os.Open(filePath) //nolint:gosec // G304: entry from caller-supplied rulesDir
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var warnings []Warning
	lineNumber := 0
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		lineNumber++
		lineText := scanner.Text()

		for _, searchPath := range paths {
			if strings.Contains(lineText, searchPath) {
				warnings = append(warnings, Warning{
					File: fileName,
					Line: lineNumber,
					Text: lineText,
					Path: searchPath,
				})
				break
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return warnings, nil
}
