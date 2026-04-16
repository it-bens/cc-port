package importer_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/importer"
)

func TestResolvePlaceholders(t *testing.T) {
	content := []byte("path is __PROJECT_PATH__ and again __PROJECT_PATH__")
	resolutions := map[string]string{
		"__PROJECT_PATH__": "/home/user/myproject",
	}

	result := importer.ResolvePlaceholders(content, resolutions)

	assert.Equal(t, []byte("path is /home/user/myproject and again /home/user/myproject"), result)
}

func TestResolvePlaceholders_UnresolvedLeft(t *testing.T) {
	content := []byte("known: __KNOWN__ unknown: __UNKNOWN__")
	resolutions := map[string]string{
		"__KNOWN__": "/home/user/known",
	}

	result := importer.ResolvePlaceholders(content, resolutions)

	assert.Equal(t, []byte("known: /home/user/known unknown: __UNKNOWN__"), result)
}

func TestValidateResolutions(t *testing.T) {
	tests := []struct {
		name        string
		resolutions map[string]string
		wantErr     bool
	}{
		{
			name: "valid absolute paths",
			resolutions: map[string]string{
				"__PLACEHOLDER__": "/home/user/project",
			},
			wantErr: false,
		},
		{
			name: "UNRESOLVED key with empty value is allowed",
			resolutions: map[string]string{
				"UNRESOLVED": "",
			},
			wantErr: false,
		},
		{
			name: "empty value for non-UNRESOLVED placeholder",
			resolutions: map[string]string{
				"__PLACEHOLDER__": "",
			},
			wantErr: true,
		},
		{
			name: "relative path is rejected",
			resolutions: map[string]string{
				"__PLACEHOLDER__": "relative/path",
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := importer.ValidateResolutions(testCase.resolutions)
			if testCase.wantErr {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCheckConflict(t *testing.T) {
	t.Run("no conflict when directory does not exist", func(t *testing.T) {
		nonExistentDir := filepath.Join(t.TempDir(), "does-not-exist")

		err := importer.CheckConflict(nonExistentDir)

		assert.NoError(t, err)
	})

	t.Run("conflict when directory exists", func(t *testing.T) {
		existingDir := t.TempDir()
		require.DirExists(t, existingDir)

		err := importer.CheckConflict(existingDir)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})
}
