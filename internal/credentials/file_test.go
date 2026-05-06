package credentials

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFile_AllFieldsPresent_ReturnsAllValues(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "creds.env")
	require.NoError(t, os.WriteFile(path, []byte(
		"AWS_ACCESS_KEY_ID=AKIA123\n"+
			"AWS_SECRET_ACCESS_KEY=secret456\n"+
			"AWS_SESSION_TOKEN=token789\n",
	), 0o600))

	fields, err := parseFile(path)

	require.NoError(t, err)
	assert.Equal(t, "AKIA123", fields.accessKeyID)
	assert.Equal(t, "secret456", fields.secretAccessKey)
	assert.Equal(t, "token789", fields.sessionToken)
}

func TestParseFile_ModeTooPermissive_ReturnsSentinel(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "creds.env")
	require.NoError(t, os.WriteFile(path, []byte("AWS_ACCESS_KEY_ID=AKIA\n"), 0o644)) //nolint:gosec // G306: intentionally permissive to test rejection

	_, err := parseFile(path)

	assert.ErrorIs(t, err, ErrFilePermissionsTooPermissive)
}

func TestParseFile_NoRecognizedKeys_ReturnsLineZeroError(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "creds.env")
	require.NoError(t, os.WriteFile(path, []byte("# header comment only\n\nMY_OTHER_VAR=foo\n"), 0o600))

	_, err := parseFile(path)

	var parseErr *FileParseError
	require.ErrorAs(t, err, &parseErr)
	assert.Equal(t, 0, parseErr.Line)
	assert.ErrorIs(t, parseErr.Err, errEmptyFile)
}

func TestParseFile_MalformedLine_ReturnsLineNumber(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "creds.env")
	require.NoError(t, os.WriteFile(path, []byte(
		"AWS_ACCESS_KEY_ID=AKIA\n"+
			"AWS_SECRET_ACCESS_KEY=secret\n"+
			"BROKEN_LINE_NO_EQUALS\n",
	), 0o600))

	_, err := parseFile(path)

	var parseErr *FileParseError
	require.ErrorAs(t, err, &parseErr)
	assert.Equal(t, 3, parseErr.Line)
	assert.ErrorIs(t, parseErr.Err, errMalformedLine)
}

func TestParseFile_UnknownKeysIgnored_ResolvesRecognizedOnes(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "creds.env")
	require.NoError(t, os.WriteFile(path, []byte(
		"MY_VAR=foo\n"+
			"AWS_ACCESS_KEY_ID=AKIA\n"+
			"ANOTHER_VAR=bar\n"+
			"AWS_SECRET_ACCESS_KEY=secret\n",
	), 0o600))

	fields, err := parseFile(path)

	require.NoError(t, err)
	assert.Equal(t, "AKIA", fields.accessKeyID)
	assert.Equal(t, "secret", fields.secretAccessKey)
}

func TestParseFile_LineExceedingCap_ReturnsErrTooLong(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "creds.env")
	huge := strings.Repeat("A", maxScannerLine+1)
	require.NoError(t, os.WriteFile(path, []byte("AWS_ACCESS_KEY_ID="+huge+"\n"), 0o600))

	_, err := parseFile(path)

	var parseErr *FileParseError
	require.ErrorAs(t, err, &parseErr)
	assert.Equal(t, 0, parseErr.Line)
	assert.ErrorIs(t, parseErr.Err, bufio.ErrTooLong)
}
