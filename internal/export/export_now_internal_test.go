package export

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

func TestRun_UsesNowForManifestCreated(t *testing.T) {
	pinned := time.Date(2026, time.July, 19, 10, 30, 0, 0, time.UTC)
	originalNow := now
	now = func() time.Time { return pinned }
	t.Cleanup(func() { now = originalNow })

	projectPath := "/Users/test/Projects/myproject"
	home := &claude.Home{Dir: t.TempDir()}
	require.NoError(t, os.MkdirAll(home.ProjectDir(projectPath), 0o750))
	claudeTool := claude.New()
	targets := []tool.Target{{Tool: claudeTool, Workspace: claude.NewWorkspace(home)}}
	var output bytes.Buffer

	_, err := Run(context.Background(), targets, &Options{
		ProjectPath: projectPath,
		Output:      &output,
		Selected:    map[string]map[string]bool{"claude": {}},
	})

	require.NoError(t, err)
	metadata, err := manifest.ReadManifestFromZip(bytes.NewReader(output.Bytes()), int64(output.Len()), archive.DefaultCaps().MaxEntries)
	require.NoError(t, err)
	assert.Equal(t, pinned, metadata.Created)
}
