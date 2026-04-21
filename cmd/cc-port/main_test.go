package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootCommand_SilenceUsageOnRuntimeError(t *testing.T) {
	require.True(t, rootCmd.SilenceUsage, "rootCmd must silence usage on runtime errors")
	require.True(t, rootCmd.SilenceErrors, "rootCmd must silence error print (main owns it)")
}

func TestRootCommand_VersionFlagPresent(t *testing.T) {
	assert.NotEmpty(t, rootCmd.Version, "rootCmd.Version must be set so --version registers")
}

func TestUsageError_IsIdentifiable(t *testing.T) {
	underlying := errors.New("bad flag")
	wrapped := &usageError{err: underlying}

	var target *usageError
	require.ErrorAs(t, wrapped, &target)

	assert.Equal(t, underlying, target.Unwrap())
}
