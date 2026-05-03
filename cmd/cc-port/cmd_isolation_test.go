package main

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCommandConstructorsAreIsolated asserts that two instances of the same
// command, constructed via the new newXCmd() functions, do not share flag
// state. A regression here would mean a package-level flag var has been
// reintroduced.
func TestCommandConstructorsAreIsolated(t *testing.T) {
	type pair struct {
		name  string
		ctor  func(*string) *cobra.Command
		flag  string
		value string // valid value for the flag's type
	}
	cases := []pair{
		{"export", newExportCmd, "from-manifest", "/tmp/m.xml"},
		{"import", newImportCmd, "from-manifest", "/tmp/m.xml"},
		{"push", newPushCmd, "from-manifest", "/tmp/m.xml"},
		{"pull", newPullCmd, "from-manifest", "/tmp/m.xml"},
		{"move", newMoveCmd, "apply", "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var claudeDirA, claudeDirB string
			a := tc.ctor(&claudeDirA)
			b := tc.ctor(&claudeDirB)

			require.NoError(t, a.Flags().Set(tc.flag, tc.value))
			// b should not observe a's setting.
			valueOnB, err := b.Flags().GetString(tc.flag)
			if err != nil {
				// For bool flags GetString returns an error; use GetBool.
				boolOnB, errBool := b.Flags().GetBool(tc.flag)
				require.NoError(t, errBool)
				assert.False(t, boolOnB,
					"%s.%s leaked from instance a to instance b", tc.name, tc.flag)
				return
			}
			assert.Empty(t, valueOnB,
				"%s.%s leaked from instance a to instance b: got %q",
				tc.name, tc.flag, valueOnB)
		})
	}
}
