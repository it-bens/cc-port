package progresstest_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/progress/progresstest"
)

func TestRecorderCapturesFullLifecycleInOrder(t *testing.T) {
	recorder := progresstest.NewRecorder()
	reporter := recorder.Reporter(progress.LevelInfo)

	phase := reporter.Phase("copy project data", 2, progress.UnitFiles)
	phase.Advance(1)
	sub := phase.SubPhase("history", 0, progress.UnitLines)
	sub.End("0 lines")
	phase.End("2 files")
	reporter.Done()

	events := recorder.Events()
	require.Len(t, events, 6)

	start, ok := events[0].(progress.PhaseStart)
	require.True(t, ok)
	assert.Equal(t, []string{"copy project data"}, start.Path)

	advance, ok := events[1].(progress.PhaseAdvance)
	require.True(t, ok)
	assert.Equal(t, int64(1), advance.Done)

	subStart, ok := events[2].(progress.PhaseStart)
	require.True(t, ok)
	assert.Equal(t, []string{"copy project data", "history"}, subStart.Path)

	_, ok = events[3].(progress.PhaseEnd)
	require.True(t, ok)
	_, ok = events[4].(progress.PhaseEnd)
	require.True(t, ok)
	_, ok = events[5].(progress.Done)
	require.True(t, ok)
}

func TestOfTypePreservesOrderAndType(t *testing.T) {
	recorder := progresstest.NewRecorder()
	reporter := recorder.Reporter(progress.LevelInfo)

	reporter.Phase("a", 0, progress.UnitItems)
	reporter.Warn(errors.New("w"))
	reporter.Phase("b", 0, progress.UnitItems)

	starts := progresstest.OfType[progress.PhaseStart](recorder.Events())
	require.Len(t, starts, 2)
	assert.Equal(t, []string{"a"}, starts[0].Path)
	assert.Equal(t, []string{"b"}, starts[1].Path)
}
