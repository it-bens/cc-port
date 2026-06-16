package progress

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var updateGolden = flag.Bool("update-golden", false, "update golden files")

// ansiPattern matches ANSI escape sequences, stripped before comparing stream
// goldens so a stray control byte fails loudly rather than hiding in the file.
var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;?]*[a-zA-Z]")

// pinClock advances now by one millisecond per call from a fixed base, so
// rate-limit tests that need sub-500ms gaps can drive time precisely; callers
// that need larger gaps emit explicit advances by reassigning the step.
func pinClock(t *testing.T, step time.Duration) {
	t.Helper()
	base := time.Date(2026, time.June, 16, 9, 0, 0, 0, time.UTC)
	var ticks int64
	original := now
	now = func() time.Time {
		instant := base.Add(time.Duration(ticks) * step)
		ticks++
		return instant
	}
	t.Cleanup(func() { now = original })
}

// goldenCompare writes got to the golden file under -update, else asserts got
// equals the stored golden.
func goldenCompare(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "render", name+".golden")
	if *updateGolden {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755)) //nolint:gosec // G301: test-controlled golden dir
		require.NoError(t, os.WriteFile(path, got, 0o644))         //nolint:gosec // G306: test-controlled golden file
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // G304: test-controlled golden path
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))
}

// lifecycleEvents is a fixed two-phase happy path: start/advance/advance/end,
// a sibling phase, then done.
func lifecycleEvents() []Event {
	return []Event{
		PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles, At: time.Time{}},
		PhaseAdvance{Path: []string{"copy"}, Done: 4},
		PhaseAdvance{Path: []string{"copy"}, Done: 10},
		PhaseEnd{Path: []string{"copy"}, Summary: "10 files", Dur: 2 * time.Second},
		PhaseStart{Path: []string{"rewrite"}, Total: 3, Unit: UnitItems, At: time.Time{}},
		PhaseAdvance{Path: []string{"rewrite"}, Done: 3},
		PhaseEnd{Path: []string{"rewrite"}, Summary: "3 items", Dur: time.Second},
		Done{},
	}
}

func consumeAll(renderer Renderer, events []Event) {
	for _, event := range events {
		renderer.Consume(event)
	}
}

func TestStreamRendererLifecycleGolden(t *testing.T) {
	pinClock(t, time.Second)
	var buffer bytes.Buffer
	renderer := NewStreamRenderer(&buffer)

	consumeAll(renderer, lifecycleEvents())
	require.NoError(t, renderer.Finalize())

	stripped := ansiPattern.ReplaceAll(buffer.Bytes(), nil)
	require.Equal(t, buffer.Bytes(), stripped, "stream output must contain no ANSI")
	goldenCompare(t, "stream_lifecycle", stripped)
}

func TestStreamRendererRateLimitsAdvance(t *testing.T) {
	// 200ms per tick: two rapid advances fall inside one 500ms window (one
	// printed line), and a later advance crosses it (a second line).
	pinClock(t, 200*time.Millisecond)
	var buffer bytes.Buffer
	renderer := NewStreamRenderer(&buffer)

	renderer.Consume(PhaseStart{Path: []string{"copy"}, Total: 100, Unit: UnitBytes})
	renderer.Consume(PhaseAdvance{Path: []string{"copy"}, Done: 10}) // t=200ms, first: prints
	renderer.Consume(PhaseAdvance{Path: []string{"copy"}, Done: 20}) // t=400ms, <500ms: dropped
	renderer.Consume(PhaseAdvance{Path: []string{"copy"}, Done: 30}) // t=600ms, <500ms since 200: dropped
	renderer.Consume(PhaseAdvance{Path: []string{"copy"}, Done: 90}) // t=800ms, >=500ms since 200: prints
	require.NoError(t, renderer.Finalize())

	goldenCompare(t, "stream_ratelimit", buffer.Bytes())
}

func TestStreamRendererWarningGolden(t *testing.T) {
	pinClock(t, time.Second)
	var buffer bytes.Buffer
	renderer := NewStreamRenderer(&buffer)

	renderer.Consume(PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles})
	renderer.Consume(Warning{Err: errors.New("skipped a file")})
	renderer.Consume(Done{})
	require.NoError(t, renderer.Finalize())

	goldenCompare(t, "stream_warning", buffer.Bytes())
}

func TestStreamRendererFailGolden(t *testing.T) {
	pinClock(t, time.Second)
	var buffer bytes.Buffer
	renderer := NewStreamRenderer(&buffer)

	renderer.Consume(PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles})
	renderer.Consume(PhaseAdvance{Path: []string{"copy"}, Done: 4})
	renderer.Consume(Failed{Err: errors.New("disk full")})
	require.NoError(t, renderer.Finalize())

	goldenCompare(t, "stream_fail", buffer.Bytes())
}

func TestStreamRendererCancelGolden(t *testing.T) {
	pinClock(t, time.Second)
	var buffer bytes.Buffer
	renderer := NewStreamRenderer(&buffer)

	renderer.Consume(PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles})
	renderer.Consume(PhaseAdvance{Path: []string{"copy"}, Done: 4})
	renderer.Consume(Cancelled{Reason: "user interrupt"})
	require.NoError(t, renderer.Finalize())

	goldenCompare(t, "stream_cancel", buffer.Bytes())
}

func TestJSONRendererLifecycleGolden(t *testing.T) {
	pinClock(t, time.Second)
	var buffer bytes.Buffer
	renderer := NewJSONRenderer(&buffer)

	consumeAll(renderer, lifecycleEvents())
	require.NoError(t, renderer.Finalize())

	goldenCompare(t, "json_lifecycle", buffer.Bytes())
}

func TestJSONRendererWarningGolden(t *testing.T) {
	var buffer bytes.Buffer
	renderer := NewJSONRenderer(&buffer)

	renderer.Consume(PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles})
	renderer.Consume(Warning{Err: errors.New("skipped a file")})
	renderer.Consume(Done{})
	require.NoError(t, renderer.Finalize())

	goldenCompare(t, "json_warning", buffer.Bytes())
}

func TestJSONRendererFailGolden(t *testing.T) {
	var buffer bytes.Buffer
	renderer := NewJSONRenderer(&buffer)

	renderer.Consume(PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles})
	renderer.Consume(PhaseAdvance{Path: []string{"copy"}, Done: 4})
	renderer.Consume(Failed{Err: errors.New("disk full")})
	require.NoError(t, renderer.Finalize())

	goldenCompare(t, "json_fail", buffer.Bytes())
}

func TestJSONRendererCancelGolden(t *testing.T) {
	var buffer bytes.Buffer
	renderer := NewJSONRenderer(&buffer)

	renderer.Consume(PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles})
	renderer.Consume(PhaseAdvance{Path: []string{"copy"}, Done: 4})
	renderer.Consume(Cancelled{Reason: "user interrupt"})
	require.NoError(t, renderer.Finalize())

	goldenCompare(t, "json_cancel", buffer.Bytes())
}
