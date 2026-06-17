package progress

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sliceRenderer records consumed events for in-package reporter assertions.
type sliceRenderer struct {
	events []Event
}

func (renderer *sliceRenderer) Consume(event Event) { renderer.events = append(renderer.events, event) }
func (renderer *sliceRenderer) Finalize() error     { return nil }

// OfTypeEvents filters events to concrete type T, preserving order. An
// in-package twin of progresstest.OfType, kept here so these tests need no
// cross-package import.
func OfTypeEvents[T Event](events []Event) []T {
	var matched []T
	for _, event := range events {
		if typed, ok := event.(T); ok {
			matched = append(matched, typed)
		}
	}
	return matched
}

// fixClock pins now to a base instant that advances one second per call, so a
// phase's open-to-end duration is deterministic.
func fixClock(t *testing.T) {
	t.Helper()
	base := time.Date(2026, time.June, 16, 12, 0, 0, 0, time.UTC)
	var ticks int64
	originalNow := now
	now = func() time.Time {
		instant := base.Add(time.Duration(ticks) * time.Second)
		ticks++
		return instant
	}
	t.Cleanup(func() { now = originalNow })
}

func TestSubPhaseAppendsOneSegmentToParentPath(t *testing.T) {
	renderer := &sliceRenderer{}
	reporter := NewReporter(renderer, LevelInfo)

	root := reporter.Phase("rewrite global references", 0, UnitItems)
	root.SubPhase("sessions", 0, UnitItems)

	starts := OfTypeEvents[PhaseStart](renderer.events)
	require.Len(t, starts, 2)
	assert.Equal(t, []string{"rewrite global references"}, starts[0].Path)
	assert.Equal(t, []string{"rewrite global references", "sessions"}, starts[1].Path)
}

func TestPhaseAliasesSubPhaseOnHandle(t *testing.T) {
	renderer := &sliceRenderer{}
	reporter := NewReporter(renderer, LevelInfo)

	root := reporter.Phase("copy project data", 0, UnitItems)
	root.Phase("history", 0, UnitItems)

	starts := OfTypeEvents[PhaseStart](renderer.events)
	require.Len(t, starts, 2)
	assert.Equal(t, []string{"copy project data", "history"}, starts[1].Path)
}

func TestSiblingSubPhasePathsDoNotCorruptEachOther(t *testing.T) {
	renderer := &sliceRenderer{}
	reporter := NewReporter(renderer, LevelInfo)

	root := reporter.Phase("root", 0, UnitItems)
	root.SubPhase("first", 0, UnitItems)
	root.SubPhase("second", 0, UnitItems)

	starts := OfTypeEvents[PhaseStart](renderer.events)
	require.Len(t, starts, 3)
	assert.Equal(t, []string{"root", "first"}, starts[1].Path)
	assert.Equal(t, []string{"root", "second"}, starts[2].Path)
}

func TestAdvanceReportsCumulativeDone(t *testing.T) {
	renderer := &sliceRenderer{}
	reporter := NewReporter(renderer, LevelInfo)

	phase := reporter.Phase("copy", 10, UnitBytes)
	phase.Advance(3)
	phase.Advance(4)

	advances := OfTypeEvents[PhaseAdvance](renderer.events)
	require.Len(t, advances, 2)
	assert.Equal(t, int64(3), advances[0].Done)
	assert.Equal(t, int64(7), advances[1].Done)
}

func TestPhaseEndDurationUsesClockSeam(t *testing.T) {
	fixClock(t)
	renderer := &sliceRenderer{}
	reporter := NewReporter(renderer, LevelInfo)

	phase := reporter.Phase("copy", 0, UnitItems)
	phase.End("done")

	ends := OfTypeEvents[PhaseEnd](renderer.events)
	require.Len(t, ends, 1)
	// Open captured tick 0, End captured tick 1: exactly one second apart.
	assert.Equal(t, time.Second, ends[0].Dur)
}

func TestDetailBelowActiveLevelIsDropped(t *testing.T) {
	renderer := &sliceRenderer{}
	reporter := NewReporter(renderer, LevelInfo)

	reporter.Detail(LevelInfo, "kept")
	reporter.Detail(LevelVerbose, "dropped")

	details := OfTypeEvents[Detail](renderer.events)
	require.Len(t, details, 1)
	assert.Equal(t, "kept", details[0].Text)
}

func TestPhaseHandleDetailHonorsSameFilter(t *testing.T) {
	renderer := &sliceRenderer{}
	reporter := NewReporter(renderer, LevelInfo)

	phase := reporter.Phase("copy", 0, UnitItems)
	phase.Detail(LevelInfo, "kept")
	phase.Detail(LevelDebug, "dropped")

	details := OfTypeEvents[Detail](renderer.events)
	require.Len(t, details, 1)
	assert.Equal(t, "kept", details[0].Text)
}

func TestQuietLevelStillPassesErrorDetail(t *testing.T) {
	renderer := &sliceRenderer{}
	reporter := NewReporter(renderer, LevelError)

	reporter.Detail(LevelError, "error line")
	reporter.Detail(LevelInfo, "info line")

	details := OfTypeEvents[Detail](renderer.events)
	require.Len(t, details, 1)
	assert.Equal(t, "error line", details[0].Text)
}

func TestDebugLevelPassesEveryDetail(t *testing.T) {
	renderer := &sliceRenderer{}
	reporter := NewReporter(renderer, LevelDebug)

	reporter.Detail(LevelError, "a")
	reporter.Detail(LevelInfo, "b")
	reporter.Detail(LevelVerbose, "c")
	reporter.Detail(LevelDebug, "d")

	assert.Len(t, OfTypeEvents[Detail](renderer.events), 4)
}

func TestPhaseAndTerminalEventsBypassLevelFilter(t *testing.T) {
	renderer := &sliceRenderer{}
	reporter := NewReporter(renderer, LevelError)

	phase := reporter.Phase("copy", 1, UnitItems)
	phase.Advance(1)
	phase.End("done")
	reporter.Warn(errors.New("warned"))
	reporter.Cancelled("stopped")
	reporter.Fail(errors.New("boom"))
	reporter.Done()

	assert.Len(t, OfTypeEvents[PhaseStart](renderer.events), 1)
	assert.Len(t, OfTypeEvents[PhaseAdvance](renderer.events), 1)
	assert.Len(t, OfTypeEvents[PhaseEnd](renderer.events), 1)
	assert.Len(t, OfTypeEvents[Warning](renderer.events), 1)
	assert.Len(t, OfTypeEvents[Cancelled](renderer.events), 1)
	assert.Len(t, OfTypeEvents[Failed](renderer.events), 1)
	assert.Len(t, OfTypeEvents[Done](renderer.events), 1)
}

func TestDetailUsesPrintfFormatting(t *testing.T) {
	renderer := &sliceRenderer{}
	reporter := NewReporter(renderer, LevelInfo)

	reporter.Detail(LevelInfo, "copied %d of %d", 3, 10)

	details := OfTypeEvents[Detail](renderer.events)
	require.Len(t, details, 1)
	assert.Equal(t, "copied 3 of 10", details[0].Text)
}

func TestTerminalMethodOnPhaseHandlePanics(t *testing.T) {
	renderer := &sliceRenderer{}
	reporter := NewReporter(renderer, LevelInfo)
	phase := reporter.Phase("copy", 0, UnitItems)

	require.Panics(t, func() { phase.Done() })
	require.Panics(t, func() { phase.Fail(errors.New("x")) })
	require.Panics(t, func() { phase.Cancelled("x") })
}

func TestNoopSwallowsEveryEvent(_ *testing.T) {
	reporter := Noop()

	phase := reporter.Phase("copy", 10, UnitItems)
	sub := phase.SubPhase("inner", 0, UnitItems)
	sub.Advance(5)
	sub.Detail(LevelError, "x")
	sub.End("done")
	reporter.Warn(errors.New("x"))
}

func TestNoopTerminalMethodsOnHandleDoNotPanic(t *testing.T) {
	phase := Noop().Phase("copy", 0, UnitItems)

	require.NotPanics(t, func() { phase.Done() })
	require.NotPanics(t, func() { phase.Fail(errors.New("x")) })
	require.NotPanics(t, func() { phase.Cancelled("x") })
}
