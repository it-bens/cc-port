package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/it-bens/cc-port/internal/rewrite"
)

// StreamHistoryJSONL streams src line by line, rewriting every well-formed
// entry so oldProject becomes newProject (in the structured `project` field
// and in any free-text reference), using path-boundary-aware substitution.
// It returns the changed-line count and 1-based malformed-line numbers while
// preserving malformed lines and input line terminators verbatim.
func StreamHistoryJSONL(
	ctx context.Context,
	src io.Reader,
	dst io.Writer,
	oldProject, newProject string,
) (count int, malformed []int, err error) {
	reader := bufio.NewReaderSize(src, 64<<10)
	writer := bufio.NewWriterSize(dst, 64<<10)

	lineNumber := 0
	for {
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}

		line, readErr := reader.ReadBytes('\n')
		if len(line) > MaxHistoryLine {
			return 0, nil, fmt.Errorf(
				"history.jsonl line %d exceeds %d bytes: %w",
				lineNumber+1, MaxHistoryLine, bufio.ErrTooLong,
			)
		}

		if len(line) > 0 {
			lineNumber++
			body, terminator := splitLineTerminator(line)

			out, isMalformed := rewriteHistoryLine(body, oldProject, newProject, &count)
			if isMalformed {
				malformed = append(malformed, lineNumber)
			}

			if _, err := writer.Write(out); err != nil {
				return 0, nil, fmt.Errorf("write history line %d: %w", lineNumber, err)
			}
			if len(terminator) > 0 {
				if _, err := writer.Write(terminator); err != nil {
					return 0, nil, fmt.Errorf("write history line %d terminator: %w", lineNumber, err)
				}
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return 0, nil, fmt.Errorf("read history line %d: %w", lineNumber+1, readErr)
		}
	}

	if err := writer.Flush(); err != nil {
		return 0, nil, fmt.Errorf("flush history output: %w", err)
	}
	return count, malformed, nil
}

func splitLineTerminator(line []byte) (body, terminator []byte) {
	if len(line) > 0 && line[len(line)-1] == '\n' {
		return line[:len(line)-1], line[len(line)-1:]
	}
	return line, nil
}

func rewriteHistoryLine(body []byte, oldProject, newProject string, count *int) ([]byte, bool) {
	if len(bytes.TrimSpace(body)) == 0 {
		return body, false
	}
	var probe HistoryEntry
	if err := json.Unmarshal(body, &probe); err != nil {
		return body, true
	}
	rewritten, replaced := rewrite.ReplacePathInBytesWithJSONEscape(body, oldProject, newProject)
	if replaced > 0 {
		*count++
	}
	return rewritten, false
}

// RewriteSessionFile rewrites every bounded occurrence of oldProject inside a
// session JSON document after validating it against SessionFile.
func RewriteSessionFile(data []byte, oldProject, newProject string) (rewritten []byte, changed bool, err error) {
	var sessionFile SessionFile
	if err := json.Unmarshal(data, &sessionFile); err != nil {
		return nil, false, fmt.Errorf("unmarshal session file: %w", err)
	}

	body, count := rewrite.ReplacePathInBytesWithJSONEscape(data, oldProject, newProject)
	return body, count > 0, nil
}

// RewriteUserConfig re-keys the oldProject entry in ~/.claude.json to
// newProject and rewrites bounded project-path references inside that entry.
func RewriteUserConfig(data []byte, oldProject, newProject string) (updated []byte, rekeyed bool, err error) {
	if !gjson.ValidBytes(data) {
		return nil, false, fmt.Errorf("invalid user config JSON")
	}

	oldPath := "projects." + rewrite.EscapeSJSONKey(oldProject)
	existing := gjson.GetBytes(data, oldPath)
	if !existing.Exists() {
		return data, false, nil
	}

	rewrittenBlock, _ := rewrite.ReplacePathInBytesWithJSONEscape([]byte(existing.Raw), oldProject, newProject)

	updated, err = sjson.DeleteBytes(data, oldPath)
	if err != nil {
		return nil, false, fmt.Errorf("delete old project key: %w", err)
	}
	newPath := "projects." + rewrite.EscapeSJSONKey(newProject)
	updated, err = sjson.SetRawBytes(updated, newPath, rewrittenBlock)
	if err != nil {
		return nil, false, fmt.Errorf("insert new project key: %w", err)
	}
	return updated, true, nil
}
