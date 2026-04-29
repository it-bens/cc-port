package main

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/manifest"
)

func TestRegisterCategoryFlags_RegistersAllRegistrySpecsPlusAll(t *testing.T) {
	cmd := &cobra.Command{}

	registerCategoryFlags(cmd, "push")

	require.NotNil(t, cmd.Flag("all"), "--all must be registered")
	for _, spec := range manifest.AllCategories {
		assert.NotNil(t, cmd.Flag(spec.Name), "--%s must be registered", spec.Name)
	}
}

func TestResolveCategoriesFromCmd_AllSetsEverySpec(t *testing.T) {
	cmd := &cobra.Command{}
	registerCategoryFlags(cmd, "push")
	require.NoError(t, cmd.Flags().Set("all", "true"))

	set, err := resolveCategoriesFromCmd(cmd)

	require.NoError(t, err)
	for _, spec := range manifest.AllCategories {
		assert.True(t, spec.Value(&set), "--all must enable %s", spec.Name)
	}
}

func TestResolveCategoriesFromCmd_ExplicitFlagSetsOnlyThatSpec(t *testing.T) {
	cmd := &cobra.Command{}
	registerCategoryFlags(cmd, "push")
	require.NoError(t, cmd.Flags().Set("sessions", "true"))

	set, err := resolveCategoriesFromCmd(cmd)

	require.NoError(t, err)
	assert.True(t, set.Sessions, "explicit --sessions must set Sessions")
	for _, spec := range manifest.AllCategories {
		if spec.Name == "sessions" {
			continue
		}
		assert.False(t, spec.Value(&set), "--sessions must not enable %s", spec.Name)
	}
}

func TestResolveCategoriesFromCmd_NoFlagsReturnsZeroSet(t *testing.T) {
	cmd := &cobra.Command{}
	registerCategoryFlags(cmd, "push")

	set, err := resolveCategoriesFromCmd(cmd)

	require.NoError(t, err)
	assert.Equal(t, manifest.CategorySet{}, set)
}
