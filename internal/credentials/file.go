package credentials

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// credentialFields is the in-memory shape produced by every source.
// Empty strings mean "not contributed by this source."
type credentialFields struct {
	accessKeyID     string
	secretAccessKey string
	sessionToken    string
}

const (
	envKeyAccessKeyID     = "AWS_ACCESS_KEY_ID"
	envKeySecretAccessKey = "AWS_SECRET_ACCESS_KEY" //nolint:gosec // G101: env var name, not a credential value
	envKeySessionToken    = "AWS_SESSION_TOKEN"     //nolint:gosec // G101: env var name, not a credential value
)

const credentialsFileMaxMode os.FileMode = 0o600

// maxScannerLine bounds a single line read from a credentials file.
// Credentials lines are short (key=value, ~100 bytes); 64 KiB is
// generous headroom while bounding adversarial or accidentally-huge
// inputs. Above this limit, the scanner returns bufio.ErrTooLong
// rather than silently truncating.
const maxScannerLine = 64 << 10

func parseFile(path string) (fields credentialFields, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return credentialFields{}, &FileParseError{Path: path, Line: 0, Err: err}
	}
	if info.Mode().Perm()&^credentialsFileMaxMode != 0 {
		return credentialFields{}, ErrFilePermissionsTooPermissive
	}

	file, err := os.Open(path) //nolint:gosec // G304: caller-supplied path
	if err != nil {
		return credentialFields{}, &FileParseError{Path: path, Line: 0, Err: err}
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close credentials file: %w", closeErr))
		}
	}()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4<<10), maxScannerLine)
	lineNumber := 0
	recognized := 0
	for scanner.Scan() {
		lineNumber++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		key, value, ok := strings.Cut(raw, "=")
		if !ok {
			return credentialFields{}, &FileParseError{Path: path, Line: lineNumber, Err: errMalformedLine}
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case envKeyAccessKeyID:
			fields.accessKeyID = value
			recognized++
		case envKeySecretAccessKey:
			fields.secretAccessKey = value
			recognized++
		case envKeySessionToken:
			fields.sessionToken = value
			recognized++
		default:
			// Unknown but well-formed key: silently skip for forward compatibility.
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return credentialFields{}, &FileParseError{Path: path, Line: 0, Err: scanErr}
	}
	if recognized == 0 {
		return credentialFields{}, &FileParseError{Path: path, Line: 0, Err: errEmptyFile}
	}
	return fields, nil
}
