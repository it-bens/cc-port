package progress

import (
	"encoding/json"
	"io"
	"strings"
	"sync"
)

// jsonSchemaVersion is the "v" field stamped on every emitted object. Bump it
// only on a breaking schema change.
const jsonSchemaVersion = 1

// JSONRenderer emits one schema-stable JSON object per event, newline
// delimited. It is selected by --json regardless of TTY.
type JSONRenderer struct {
	mutex    sync.Mutex
	encoder  *json.Encoder
	warnings int
	phase    activePhase
}

// jsonEvent is the wire shape. Fields are omitempty so each event carries only
// its relevant keys; "v" and "event" are always present.
type jsonEvent struct {
	Version  int      `json:"v"`
	Event    string   `json:"event"`
	Path     []string `json:"path,omitempty"`
	Total    int64    `json:"total,omitempty"`
	Done     int64    `json:"done,omitempty"`
	Unit     string   `json:"unit,omitempty"`
	Summary  string   `json:"summary,omitempty"`
	Duration string   `json:"duration,omitempty"`
	Level    string   `json:"level,omitempty"`
	Text     string   `json:"text,omitempty"`
	Error    string   `json:"error,omitempty"`
	Reason   string   `json:"reason,omitempty"`
	Warnings int      `json:"warnings,omitempty"`
}

// NewJSONRenderer builds a JSONRenderer over writer.
func NewJSONRenderer(writer io.Writer) *JSONRenderer {
	return &JSONRenderer{encoder: json.NewEncoder(writer)}
}

// Consume emits one schema-stable JSON object for the event.
func (renderer *JSONRenderer) Consume(event Event) {
	renderer.mutex.Lock()
	defer renderer.mutex.Unlock()

	record := jsonEvent{Version: jsonSchemaVersion}
	switch typed := event.(type) {
	case PhaseStart:
		renderer.phase.start(typed)
		record.Event = "phase_start"
		record.Path = typed.Path
		record.Total = typed.Total
		record.Unit = unitName(typed.Unit)
	case PhaseAdvance:
		renderer.phase.advance(typed)
		record.Event = "phase_advance"
		record.Path = typed.Path
		record.Done = typed.Done
	case PhaseEnd:
		renderer.phase.end(typed)
		record.Event = "phase_end"
		record.Path = typed.Path
		record.Summary = typed.Summary
		record.Duration = typed.Dur.String()
	case Detail:
		record.Event = "detail"
		record.Level = levelName(typed.Level)
		record.Text = typed.Text
	case Warning:
		renderer.warnings++
		record.Event = "warning"
		record.Error = typed.Err.Error()
	case Failed:
		record.Event = "failed"
		record.Error = typed.Err.Error()
		record.Path = renderer.phase.pathOrNil()
		record.Warnings = renderer.warnings
	case Cancelled:
		record.Event = "cancelled"
		record.Reason = typed.Reason
		record.Path = renderer.phase.pathOrNil()
		record.Done, record.Total = renderer.phase.doneTotal()
		record.Warnings = renderer.warnings
	case Done:
		record.Event = "done"
		record.Warnings = renderer.warnings
	}
	renderer.encode(&record)
}

// Finalize reports no error: the renderer holds nothing to tear down.
func (renderer *JSONRenderer) Finalize() error { return nil }

// encode writes one record best-effort. The fixed-shape struct never fails
// marshaling, so the only error is a sink-write failure, which corrupts no
// downstream state and has nowhere useful to surface; see NullRenderer.printf.
func (renderer *JSONRenderer) encode(record *jsonEvent) {
	_ = renderer.encoder.Encode(record)
}

func unitName(unit Unit) string {
	switch unit {
	case UnitItems:
		return "items"
	case UnitFiles:
		return "files"
	case UnitLines:
		return "lines"
	case UnitBytes:
		return "bytes"
	case UnitEntries:
		return "entries"
	default:
		return "items"
	}
}

func levelName(level Level) string {
	switch level {
	case LevelError:
		return "error"
	case LevelInfo:
		return "info"
	case LevelVerbose:
		return "verbose"
	case LevelDebug:
		return "debug"
	default:
		return "info"
	}
}

// pathOrNil returns the deepest open phase's path, or nil when none is open.
func (active *activePhase) pathOrNil() []string {
	state, ok := active.current()
	if !ok {
		return nil
	}
	return state.path
}

// doneTotal returns the deepest open phase's progress, or zero when none.
func (active *activePhase) doneTotal() (done, total int64) {
	state, ok := active.current()
	if !ok {
		return 0, 0
	}
	return state.done, state.total
}

// joinedPath renders a phase path as a dotted string, "(none)" when empty.
func joinedPath(path []string) string {
	if len(path) == 0 {
		return noPhase
	}
	return strings.Join(path, ".")
}
