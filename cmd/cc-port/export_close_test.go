package main

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

// closeFailingSinkStage is a pipeline leaf whose writer accepts every byte
// but errors on Close. Used to assert that runExportWithStages wraps the
// close-time fault as "close output pipeline".
type closeFailingSinkStage struct{}

func (s *closeFailingSinkStage) Open(_ context.Context, _ io.Writer) (io.Writer, io.Closer, error) {
	return io.Discard, &closeFailingCloser{}, nil
}

func (s *closeFailingSinkStage) Name() string { return "close-failing-sink" }

type closeFailingCloser struct{}

func (c *closeFailingCloser) Close() error {
	return errors.New("synthetic sink close failure")
}

// TestRunExportWithStages_OutputCloseErrorWrapsAsCloseOutputPipeline drives
// the cmd-layer helper directly so the deferred writer.Close error path
// surfaces.
func TestRunExportWithStages_OutputCloseErrorWrapsAsCloseOutputPipeline(t *testing.T) {
	home := testutil.SetupFixture(t)
	targets := []tool.Target{{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}}

	_, err := runExportWithStages(
		t.Context(), targets,
		&export.Options{
			ProjectPath: "/Users/test/Projects/myproject",
			Selected:    map[string]map[string]bool{"claude": {"sessions": true}},
			Placeholders: map[string][]manifest.Placeholder{
				"claude": {
					{Key: "{{PROJECT_PATH}}", Original: "/Users/test/Projects/myproject"},
					{Key: "{{HOME}}", Original: "/Users/test"},
				},
			},
		},
		[]pipeline.WriterStage{&closeFailingSinkStage{}},
	)

	require.Error(t, err, "deferred writer.Close failure must surface from runExportWithStages")
	require.ErrorContains(t, err, "close output pipeline",
		"cmd-layer must wrap the pipeline close error")
	require.ErrorContains(t, err, "synthetic sink close failure",
		"underlying sink error must remain in the chain")
}
