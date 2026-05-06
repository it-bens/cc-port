package credentials

import (
	"os"
	"path/filepath"
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
