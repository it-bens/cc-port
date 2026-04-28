package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolvePassphrase_EmptyReturnsEmpty(t *testing.T) {
	value, err := resolvePassphrase("", "")
	require.NoError(t, err)
	require.Empty(t, value)
}

func TestResolvePassphrase_BothFlagsRefused(t *testing.T) {
	_, err := resolvePassphrase("VAR", "/tmp/path")
	require.Error(t, err)
	require.Contains(t, err.Error(), "mutually exclusive")
}

func TestResolvePassphrase_EnvSet(t *testing.T) {
	t.Setenv("CC_PORT_TEST_PASS", "value-from-env")
	value, err := resolvePassphrase("CC_PORT_TEST_PASS", "")
	require.NoError(t, err)
	require.Equal(t, "value-from-env", value)
}

func TestResolvePassphrase_EnvUnsetFailsLoudly(t *testing.T) {
	_, err := resolvePassphrase("CC_PORT_TEST_NONEXISTENT", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "CC_PORT_TEST_NONEXISTENT")
}

func TestResolvePassphrase_EnvEmptyValueFails(t *testing.T) {
	t.Setenv("CC_PORT_TEST_PASS_EMPTY", "")
	_, err := resolvePassphrase("CC_PORT_TEST_PASS_EMPTY", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "CC_PORT_TEST_PASS_EMPTY")
}

func TestResolvePassphrase_FileTrimsTrailingNewlines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "phrase")
	require.NoError(t, os.WriteFile(path, []byte("file-pass\n\r\n"), 0o600))
	value, err := resolvePassphrase("", path)
	require.NoError(t, err)
	require.Equal(t, "file-pass", value)
}

func TestResolvePassphrase_FileMissingFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := resolvePassphrase("", missing)
	require.Error(t, err)
}

func TestResolvePassphrase_FileEmptyFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty")
	require.NoError(t, os.WriteFile(path, []byte(""), 0o600))
	_, err := resolvePassphrase("", path)
	require.Error(t, err)
}
