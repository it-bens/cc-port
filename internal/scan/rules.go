// Package scan reports warnings found in ~/.claude/rules/*.md files.
package scan

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Warning is a single line in a rules file that contains a search path.
type Warning struct {
	File string // File is the base filename, not a full path.
	Line int
	Text string
	Path string
}

// maxScannerLine caps a single line that bufio.Scanner will read from a
// rules file. Above this, the scanner returns bufio.ErrTooLong rather
// than silently truncating. Independently chosen for this package; not
// derived from claude.MaxHistoryLine despite sharing 16 MiB today.
const maxScannerLine = 16 << 20

// Rules scans .md files directly in rulesDir (non-recursive); emits one Warning per matched line, not per matched path.
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
	scanner.Buffer(make([]byte, 64<<10), maxScannerLine)

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
