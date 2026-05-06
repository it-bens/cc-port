package credentials

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinels.
var (
	ErrFilePermissionsTooPermissive = errors.New("credentials: file mode more permissive than 0600")
	ErrPromptCanceled               = errors.New("credentials: prompt canceled")
	ErrPromptUnavailable            = errors.New("credentials: prompt requested but no TTY available")
)

// Internal sentinels wrapped by FileParseError.Err.
var (
	errEmptyFile     = errors.New("file contributes no recognized credential fields")
	errMalformedLine = errors.New("malformed line: expected KEY=VALUE")
)

// IncompleteCredentialsError reports that one or more required fields
// remained unset after every configured source was tried. Callers use
// errors.As to recover MissingFields and TriedSources for diagnostics.
type IncompleteCredentialsError struct {
	MissingFields []string
	TriedSources  []string
}

func (e *IncompleteCredentialsError) Error() string {
	return fmt.Sprintf("credentials: incomplete (missing %s; tried sources: %s)",
		strings.Join(e.MissingFields, ", "),
		strings.Join(e.TriedSources, ", "),
	)
}

// FileParseError reports a parse failure in a credentials file. Line is
// 0 when the failure is whole-file (empty / no recognized keys).
type FileParseError struct {
	Path string
	Line int
	Err  error
}

func (e *FileParseError) Error() string {
	if e.Line == 0 {
		return fmt.Sprintf("credentials: %s: %s", e.Path, e.Err.Error())
	}
	return fmt.Sprintf("credentials: %s: line %d: %s", e.Path, e.Line, e.Err.Error())
}

func (e *FileParseError) Unwrap() error { return e.Err }
