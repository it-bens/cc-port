package claude

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
)

// VerifyProjectIdentityForTest exposes verifyProjectIdentity so fuzz tests in
// package claude_test can exercise the guard without routing through
// LocateProject. Production code continues to reach the guard only via
// LocateProject.
var VerifyProjectIdentityForTest = verifyProjectIdentity

func TestExport_RendersRulesFileWarnings(t *testing.T) {
	projectPath := "/Users/test/Projects/myproject"
	home := &Home{Dir: t.TempDir()}
	require.NoError(t, os.MkdirAll(home.ProjectDir(projectPath), 0o750))
	require.NoError(t, os.MkdirAll(home.RulesDir(), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(home.RulesDir(), "test-rule.md"),
		[]byte("# Rule\n\nThis rule applies to /Users/test/Projects/myproject only.\n"),
		0o600,
	))
	workspace := NewWorkspace(home)
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	sink := archive.NewSink(writer, "claude", nil)

	result, err := workspace.Export(context.Background(), projectPath, map[string]bool{}, sink)

	require.NoError(t, err)
	require.NoError(t, writer.Close())
	assert.Contains(t, result.Warnings, "rules file test-rule.md (line 3) references this project")
}
