package progress

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// advanceInterval is the minimum wall time between two printed PhaseAdvance
// lines for one phase. PhaseStart and PhaseEnd are never throttled.
const advanceInterval = 500 * time.Millisecond

// StreamRenderer writes append-only, ANSI-free lines for CI logs and
// redirected stderr. PhaseStart/PhaseEnd always print; PhaseAdvance is rate
// limited per phase. It is the non-TTY default.
type StreamRenderer struct {
	mutex      sync.Mutex
	writer     io.Writer
	warnings   int
	phase      activePhase
	lastPrintf map[string]time.Time
}

// NewStreamRenderer builds a StreamRenderer over writer.
func NewStreamRenderer(writer io.Writer) *StreamRenderer {
	return &StreamRenderer{writer: writer, lastPrintf: make(map[string]time.Time)}
}

// Consume prints append-only lines: phase open/close and terminal events
// always; PhaseAdvance throttled per phase.
func (renderer *StreamRenderer) Consume(event Event) {
	renderer.mutex.Lock()
	defer renderer.mutex.Unlock()

	switch typed := event.(type) {
	case PhaseStart:
		renderer.phase.start(typed)
		renderer.printf("[START] phase=%s total=%d unit=%s\n",
			joinedPath(typed.Path), typed.Total, unitName(typed.Unit))
	case PhaseAdvance:
		renderer.phase.advance(typed)
		renderer.printAdvance(typed)
	case PhaseEnd:
		renderer.phase.end(typed)
		renderer.printf("[END] phase=%s summary=%q dur=%s\n",
			joinedPath(typed.Path), typed.Summary, typed.Dur)
		delete(renderer.lastPrintf, joinedPath(typed.Path))
	case Detail:
		renderer.printf("[%s] %s\n", levelName(typed.Level), typed.Text)
	case Warning:
		renderer.warnings++
		renderer.printf("[WARN] %s\n", typed.Err)
	case Failed:
		renderer.printf("[FAIL] phase=%s err=%s%s\n",
			renderer.phase.name(), typed.Err, renderer.warningSuffix())
	case Cancelled:
		done, total := renderer.phase.doneTotal()
		renderer.printf("[CANCELLED] phase=%s done=%d/%d%s\n",
			renderer.phase.name(), done, total, renderer.warningSuffix())
	case Done:
		renderer.printf("[DONE]%s\n", renderer.warningSuffix())
	}
}

// Finalize reports no error: the renderer holds nothing to tear down.
func (renderer *StreamRenderer) Finalize() error { return nil }

func (renderer *StreamRenderer) warningSuffix() string {
	return warningSuffix(renderer.warnings)
}

// printf writes one display line best-effort; see NullRenderer.printf.
func (renderer *StreamRenderer) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(renderer.writer, format, args...)
}

// printAdvance prints a throttled progress line: at most one per advanceInterval
// per phase. The first advance for a phase always prints.
func (renderer *StreamRenderer) printAdvance(event PhaseAdvance) {
	key := joinedPath(event.Path)
	current := now()
	last, seen := renderer.lastPrintf[key]
	if seen && current.Sub(last) < advanceInterval {
		return
	}
	renderer.lastPrintf[key] = current

	total := renderer.phase.totalOf(event.Path)
	renderer.printf("[PROGRESS] phase=%s %s\n", key, formatProgress(event.Done, total))
}

// formatProgress renders "done/total (pct%)"; when total is unknown (<= 0) it
// renders just the done count so the line never divides by zero.
func formatProgress(done, total int64) string {
	if total <= 0 {
		return fmt.Sprintf("%d", done)
	}
	percent := float64(done) / float64(total) * 100
	return fmt.Sprintf("%d/%d (%.0f%%)", done, total, percent)
}
