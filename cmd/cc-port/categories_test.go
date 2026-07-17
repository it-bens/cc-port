package main

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

func TestRegisterCategoryFlags_RegistersAllAndInclude(t *testing.T) {
	cmd := &cobra.Command{}

	registerCategoryFlags(cmd, "push")

	require.NotNil(t, cmd.Flag("all"), "--all must be registered")
	require.NotNil(t, cmd.Flag("include"), "--include must be registered")
}

func TestResolveSelectionFromCmd_AllSetsEveryCategory(t *testing.T) {
	cmd := &cobra.Command{}
	registerCategoryFlags(cmd, "push")
	require.NoError(t, cmd.Flags().Set("all", "true"))

	tools := []tool.Tool{claude.New()}
	selection, err := resolveSelectionFromCmd(cmd, tools)

	require.NoError(t, err)
	for _, category := range claude.New().Categories() {
		assert.True(t, selection["claude"][category.Name], "--all must enable %s", category.Name)
	}
}

func TestResolveSelectionFromCmd_IncludeSetsOnlyNamedCategory(t *testing.T) {
	cmd := &cobra.Command{}
	registerCategoryFlags(cmd, "push")
	require.NoError(t, cmd.Flags().Set("include", "claude/sessions"))

	tools := []tool.Tool{claude.New()}
	selection, err := resolveSelectionFromCmd(cmd, tools)

	require.NoError(t, err)
	assert.True(t, selection["claude"]["sessions"], "explicit --include claude/sessions must set sessions")
	for _, category := range claude.New().Categories() {
		if category.Name == "sessions" {
			continue
		}
		assert.False(t, selection["claude"][category.Name], "--include claude/sessions must not enable %s", category.Name)
	}
}

func TestResolveSelectionFromCmd_AllAndIncludeAreMutuallyExclusive(t *testing.T) {
	cmd := &cobra.Command{}
	registerCategoryFlags(cmd, "push")
	require.NoError(t, cmd.Flags().Set("all", "true"))
	require.NoError(t, cmd.Flags().Set("include", "claude/sessions"))

	_, err := resolveSelectionFromCmd(cmd, []tool.Tool{claude.New()})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestResolveSelectionFromCmd_UnknownCategoryIsRejected(t *testing.T) {
	cmd := &cobra.Command{}
	registerCategoryFlags(cmd, "push")
	require.NoError(t, cmd.Flags().Set("include", "claude/unknown"))

	_, err := resolveSelectionFromCmd(cmd, []tool.Tool{claude.New()})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "valid categories")
	assert.Contains(t, err.Error(), "sessions")
}

func TestResolveSelectionFromCmd_BareCategoryNameRejected(t *testing.T) {
	cmd := &cobra.Command{}
	registerCategoryFlags(cmd, "push")
	require.NoError(t, cmd.Flags().Set("include", "sessions"))

	_, err := resolveSelectionFromCmd(cmd, []tool.Tool{claude.New()})

	require.Error(t, err)
}

func TestResolveSelectionFromCmd_NoFlagsReturnsNil(t *testing.T) {
	cmd := &cobra.Command{}
	registerCategoryFlags(cmd, "push")

	selection, err := resolveSelectionFromCmd(cmd, []tool.Tool{claude.New()})

	require.NoError(t, err)
	assert.Nil(t, selection)
}
