package progress

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// noPhase is the placeholder a terminal line shows when no phase is open.
const noPhase = "(none)"

// NullRenderer is the --quiet sink. It drops every event except warnings,
// which print inline, and tracks just enough state to print one final summary
// line on the terminal event.
type NullRenderer struct {
	mutex    sync.Mutex
	writer   io.Writer
	warnings int
	phase    activePhase
}

// NewNullRenderer builds a NullRenderer over writer. Pick passes the selected
// sink; tests pass a *bytes.Buffer directly.
func NewNullRenderer(writer io.Writer) *NullRenderer {
	return &NullRenderer{writer: writer}
}

// Consume drops every event but warnings and the terminal summary.
func (renderer *NullRenderer) Consume(event Event) {
	renderer.mutex.Lock()
	defer renderer.mutex.Unlock()

	switch typed := event.(type) {
	case PhaseStart:
		renderer.phase.start(typed)
	case PhaseAdvance:
		renderer.phase.advance(typed)
	case PhaseEnd:
		renderer.phase.end(typed)
	case Warning:
		renderer.warnings++
		renderer.printf("[WARN] %s\n", typed.Err)
	case Failed:
		renderer.printf("[ERROR] %s\n", typed.Err)
		renderer.printf("failed at %s%s\n", renderer.phase.name(), renderer.warningSuffix())
	case Cancelled:
		renderer.printf("[INTERRUPTED] %s%s\n", typed.Reason, renderer.warningSuffix())
	case Done:
		renderer.printf("done%s\n", renderer.warningSuffix())
	}
}

// Finalize reports no error: the renderer holds nothing to tear down.
func (renderer *NullRenderer) Finalize() error { return nil }

// printf writes one display line best-effort. A failed write to the progress
// sink corrupts no downstream state and has nowhere useful to surface, so the
// error is dropped rather than aborting the command.
func (renderer *NullRenderer) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(renderer.writer, format, args...)
}

func (renderer *NullRenderer) warningSuffix() string {
	return warningSuffix(renderer.warnings)
}

// warningSuffix formats the shared " (N warnings)" tail appended to a terminal
// summary. It is empty when no warning occurred.
func warningSuffix(count int) string {
	if count == 0 {
		return ""
	}
	noun := "warnings"
	if count == 1 {
		noun = "warning"
	}
	return fmt.Sprintf(" (%d %s)", count, noun)
}

// activePhase tracks the last opened phase whose PhaseEnd has not arrived, so
// terminal lines can name it with accurate done/total. Phases nest, so it keeps
// a stack and reports the deepest open phase.
type activePhase struct {
	stack []openPhaseState
}

type openPhaseState struct {
	path  []string
	done  int64
	total int64
}

func (active *activePhase) start(event PhaseStart) {
	active.stack = append(active.stack, openPhaseState{
		path:  event.Path,
		total: event.Total,
	})
}

func (active *activePhase) advance(event PhaseAdvance) {
	for index := len(active.stack) - 1; index >= 0; index-- {
		if pathEqual(active.stack[index].path, event.Path) {
			active.stack[index].done = event.Done
			return
		}
	}
}

func (active *activePhase) end(event PhaseEnd) {
	for index := len(active.stack) - 1; index >= 0; index-- {
		if pathEqual(active.stack[index].path, event.Path) {
			active.stack = append(active.stack[:index], active.stack[index+1:]...)
			return
		}
	}
}

// current returns the deepest open phase and whether one is open.
func (active *activePhase) current() (openPhaseState, bool) {
	if len(active.stack) == 0 {
		return openPhaseState{}, false
	}
	return active.stack[len(active.stack)-1], true
}

// name returns the deepest open phase's dotted name, or "(none)" when no phase
// is open.
func (active *activePhase) name() string {
	state, ok := active.current()
	if !ok {
		return noPhase
	}
	return strings.Join(state.path, ".")
}

// totalOf returns the total of the open phase at path, or 0 when not found.
func (active *activePhase) totalOf(path []string) int64 {
	for index := len(active.stack) - 1; index >= 0; index-- {
		if pathEqual(active.stack[index].path, path) {
			return active.stack[index].total
		}
	}
	return 0
}

func pathEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
