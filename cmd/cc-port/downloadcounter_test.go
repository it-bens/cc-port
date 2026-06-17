package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/progress/progresstest"
)

func TestDownloadCounterStage_EmitsPhaseStartWithRemoteSize(t *testing.T) {
	const data = "hello download counter"
	recorder := progresstest.NewRecorder()
	stage := &downloadCounterStage{reporter: recorder.Reporter(progress.LevelInfo)}

	view, _, closer, err := stage.Open(context.Background(), pipeline.View{
		Reader: strings.NewReader(data),
		Size:   int64(len(data)),
	})
	require.NoError(t, err)
	require.Nil(t, closer, "download counter stage owns no resource; closer must be nil")

	_, copyErr := io.Copy(io.Discard, view.Reader)
	require.NoError(t, copyErr)
	stage.End()

	starts := progresstest.OfType[progress.PhaseStart](recorder.Events())
	require.Len(t, starts, 1, "expected exactly one PhaseStart")
	assert.Equal(t, []string{"download"}, starts[0].Path)
	assert.Equal(t, int64(len(data)), starts[0].Total)

	advances := progresstest.OfType[progress.PhaseAdvance](recorder.Events())
	require.NotEmpty(t, advances, "expected at least one PhaseAdvance")
	last := advances[len(advances)-1]
	assert.Equal(t, int64(len(data)), last.Done, "cumulative done must equal total (no overshoot)")

	ends := progresstest.OfType[progress.PhaseEnd](recorder.Events())
	assert.Len(t, ends, 1, "expected exactly one PhaseEnd")
}

func TestDownloadCounterStage_RejectsNilUpstreamReader(t *testing.T) {
	recorder := progresstest.NewRecorder()
	stage := &downloadCounterStage{reporter: recorder.Reporter(progress.LevelInfo)}

	_, _, _, err := stage.Open(context.Background(), pipeline.View{Reader: nil, Size: 100})

	require.Error(t, err)
}
