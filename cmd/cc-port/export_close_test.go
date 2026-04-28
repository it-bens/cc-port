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
)

// closeFailingSinkStage is a pipeline leaf whose writer accepts every byte
// but errors on Close. Used to assert that runExportWithStages wraps the
// close-time fault as "close output pipeline" — the wrap that previously
// lived inside internal/export as "close archive file".
type closeFailingSinkStage struct{}

func (s *closeFailingSinkStage) Open(_ context.Context, _ io.Writer) (io.WriteCloser, error) {
	return &closeFailingWriter{}, nil
}

func (s *closeFailingSinkStage) Name() string { return "close-failing-sink" }

type closeFailingWriter struct{}

func (w *closeFailingWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *closeFailingWriter) Close() error {
	return errors.New("synthetic sink close failure")
}

// TestRunExportWithStages_OutputCloseErrorWrapsAsCloseOutputPipeline drives
// the cmd-layer helper directly so the deferred writer.Close error path
// surfaces. Confirms: (a) a successful export still propagates the Close
// fault, (b) the error message carries the cmd-layer "close output
// pipeline" wrap, and (c) the underlying sink error sits in the chain.
func TestRunExportWithStages_OutputCloseErrorWrapsAsCloseOutputPipeline(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	_, err := runExportWithStages(
		t.Context(), claudeHome,
		&export.Options{
			ProjectPath: "/Users/test/Projects/myproject",
			Categories:  manifest.CategorySet{Sessions: true},
			Placeholders: []manifest.Placeholder{
				{Key: "{{PROJECT_PATH}}", Original: "/Users/test/Projects/myproject"},
				{Key: "{{HOME}}", Original: "/Users/test"},
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
