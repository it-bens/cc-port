package pipeline_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/pipeline"
)

func TestMaterializeStage_PassesThroughWhenUpstreamHasReaderAt(t *testing.T) {
	upstream := pipeline.View{
		Reader:   bytes.NewReader([]byte("payload")),
		ReaderAt: bytes.NewReader([]byte("payload")),
		Size:     int64(len("payload")),
	}

	got, meta, closer, err := (&pipeline.MaterializeStage{}).Open(context.Background(), upstream)

	require.NoError(t, err)
	assert.Equal(t, upstream, got, "must forward upstream unchanged on the short-circuit path")
	assert.Equal(t, pipeline.Meta{}, meta)
	assert.Nil(t, closer, "short-circuit must contribute no closer; runner already owns upstream's closer")
}

func TestMaterializeStage_DrainsStreamingUpstreamToTempfile(t *testing.T) {
	want := []byte("streaming payload that gets materialized")
	upstream := pipeline.View{Reader: bytes.NewReader(want)}

	got, _, closer, err := (&pipeline.MaterializeStage{}).Open(context.Background(), upstream)

	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })
	require.NotNil(t, got.ReaderAt, "drain path must populate ReaderAt")
	assert.Equal(t, int64(len(want)), got.Size)
	buf := make([]byte, len(want))
	_, err = got.ReaderAt.ReadAt(buf, 0)
	require.NoError(t, err)
	assert.Equal(t, want, buf)
}

func TestMaterializeStage_CloseRemovesTempfile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tempdir semantics differ on Windows")
	}
	upstream := pipeline.View{Reader: bytes.NewReader([]byte("close removes tempfile"))}

	view, _, closer, err := (&pipeline.MaterializeStage{}).Open(context.Background(), upstream)
	require.NoError(t, err)

	temp, ok := view.ReaderAt.(*os.File)
	require.True(t, ok, "ReaderAt should be the underlying tempfile")
	tempPath := temp.Name()
	assert.True(t, strings.HasPrefix(tempPath, os.TempDir()),
		"tempfile must live under os.TempDir(), got %q", tempPath)

	require.NoError(t, closer.Close())
	_, statErr := os.Stat(tempPath)
	assert.True(t, os.IsNotExist(statErr), "tempfile must be removed after Close, statErr=%v", statErr)
}

func TestMaterializeStage_TempfileMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("0600 mode semantics differ on Windows")
	}
	upstream := pipeline.View{Reader: bytes.NewReader([]byte("mode test"))}

	view, _, closer, err := (&pipeline.MaterializeStage{}).Open(context.Background(), upstream)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	temp, ok := view.ReaderAt.(*os.File)
	require.True(t, ok)
	info, err := os.Stat(temp.Name())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestMaterializeStage_RejectsNilUpstream(t *testing.T) {
	_, _, _, err := (&pipeline.MaterializeStage{}).Open(context.Background(), pipeline.View{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "MaterializeStage")
}

func TestMaterializeStage_DrainPathIntegratesWithRunReader(t *testing.T) {
	src := &streamingSource{data: []byte("end-to-end materialize")}

	source, err := pipeline.RunReader(context.Background(), []pipeline.ReaderStage{
		src,
		&pipeline.MaterializeStage{},
	})

	require.NoError(t, err)
	t.Cleanup(func() { _ = source.Close() })
	require.NotNil(t, source.ReaderAt)
	assert.Equal(t, int64(len("end-to-end materialize")), source.Size)
}

// streamingSource is a ReaderStage whose View carries only a Reader (no
// ReaderAt). Forces MaterializeStage onto the drain path.
type streamingSource struct{ data []byte }

func (s *streamingSource) Open(_ context.Context, _ pipeline.View) (pipeline.View, pipeline.Meta, io.Closer, error) {
	return pipeline.View{Reader: bytes.NewReader(s.data)}, pipeline.Meta{}, nil, nil
}
func (s *streamingSource) Name() string { return "streaming source" }
