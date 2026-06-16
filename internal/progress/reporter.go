package progress

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/x/term"
)

// now and isTTY are seams reassigned under t.Cleanup so tests can pin
// timestamps, durations, and terminal detection. isTTY is consumed by renderer
// selection in a later task; it lives here beside now so both seams sit in one
// place.
var (
	now   = time.Now
	isTTY = func(file *os.File) bool { return term.IsTerminal(file.Fd()) }
)

// Reporter is the surface a command emits progress through. The root Reporter
// owns the terminal methods (Done/Fail/Cancelled); a PhaseHandle is a Reporter
// scoped to one phase and must not invoke them.
type Reporter interface {
	Phase(name string, total int64, unit Unit) PhaseHandle
	Detail(level Level, format string, args ...any)
	Warn(err error)
	Done()
	Fail(err error)
	Cancelled(reason string)
}

// PhaseHandle is a Reporter scoped to an open phase. Phase aliases SubPhase:
// both append one segment to this handle's path. Advance reports cumulative
// progress; End closes the phase.
type PhaseHandle interface {
	Reporter
	SubPhase(name string, total int64, unit Unit) PhaseHandle
	Advance(n int64)
	End(summary string)
}

// eventStream is the single sink and filter shared by the root reporter and
// every phase handle. Holding one instance behind a pointer keeps the
// active-level filter at exactly one site (emitDetail) so a sub-phase cannot
// bypass it. The mutex serializes emission and Advance bookkeeping because
// counting wrappers may drive one handle from concurrent goroutines.
type eventStream struct {
	mutex    sync.Mutex
	renderer Renderer
	level    Level
}

// emit forwards an unfiltered event: phase, warning, and terminal events
// always reach the sink.
func (stream *eventStream) emit(event Event) {
	stream.mutex.Lock()
	defer stream.mutex.Unlock()
	stream.renderer.Consume(event)
}

// emitDetail is the only level-filter site. A Detail more verbose than the
// active level is dropped before reaching the sink.
func (stream *eventStream) emitDetail(level Level, text string) {
	if level > stream.level {
		return
	}
	stream.mutex.Lock()
	defer stream.mutex.Unlock()
	stream.renderer.Consume(Detail{Level: level, Text: text, At: now()})
}

// NewReporter builds the root Reporter forwarding surviving events to renderer
// at the given active level.
func NewReporter(renderer Renderer, level Level) Reporter {
	return &rootReporter{stream: &eventStream{renderer: renderer, level: level}}
}

type rootReporter struct {
	stream *eventStream
}

func (reporter *rootReporter) Phase(name string, total int64, unit Unit) PhaseHandle {
	return openPhase(reporter.stream, nil, name, total, unit)
}

//nolint:goprintffuncname // contract-mandated method name; Detail, not Detailf
func (reporter *rootReporter) Detail(level Level, format string, args ...any) {
	reporter.stream.emitDetail(level, formatDetail(format, args))
}

func (reporter *rootReporter) Warn(err error) {
	reporter.stream.emit(Warning{Err: err, At: now()})
}

func (reporter *rootReporter) Done() {
	reporter.stream.emit(Done{})
}

func (reporter *rootReporter) Fail(err error) {
	reporter.stream.emit(Failed{Err: err})
}

func (reporter *rootReporter) Cancelled(reason string) {
	reporter.stream.emit(Cancelled{Reason: reason})
}

type phaseHandle struct {
	stream *eventStream
	path   []string
	start  time.Time
	done   int64
}

// openPhase emits PhaseStart and returns the handle. The open time is captured
// once and reused for both PhaseStart.At and the PhaseEnd duration so a fake
// clock that advances per call cannot skew the two apart.
func openPhase(stream *eventStream, parent []string, name string, total int64, unit Unit) *phaseHandle {
	path := append(append([]string(nil), parent...), name)
	start := now()
	stream.emit(PhaseStart{Path: path, Total: total, Unit: unit, At: start})
	return &phaseHandle{stream: stream, path: path, start: start}
}

func (handle *phaseHandle) Phase(name string, total int64, unit Unit) PhaseHandle {
	return handle.SubPhase(name, total, unit)
}

func (handle *phaseHandle) SubPhase(name string, total int64, unit Unit) PhaseHandle {
	return openPhase(handle.stream, handle.path, name, total, unit)
}

//nolint:goprintffuncname // contract-mandated method name; Detail, not Detailf
func (handle *phaseHandle) Detail(level Level, format string, args ...any) {
	handle.stream.emitDetail(level, formatDetail(format, args))
}

func (handle *phaseHandle) Warn(err error) {
	handle.stream.emit(Warning{Err: err, At: now()})
}

func (handle *phaseHandle) Advance(n int64) {
	handle.stream.mutex.Lock()
	defer handle.stream.mutex.Unlock()
	handle.done += n
	handle.stream.renderer.Consume(PhaseAdvance{Path: handle.path, Done: handle.done})
}

func (handle *phaseHandle) End(summary string) {
	handle.stream.emit(PhaseEnd{Path: handle.path, Summary: summary, Dur: now().Sub(handle.start)})
}

// Done, Fail, and Cancelled on a phase handle are programmer misuse: terminal
// events belong to the root Reporter only. Panic rather than silently emit a
// stray terminal event mid-run.
func (handle *phaseHandle) Done() {
	panic("progress: Done called on a PhaseHandle; terminal methods belong to the root Reporter")
}

func (handle *phaseHandle) Fail(error) {
	panic("progress: Fail called on a PhaseHandle; terminal methods belong to the root Reporter")
}

func (handle *phaseHandle) Cancelled(string) {
	panic("progress: Cancelled called on a PhaseHandle; terminal methods belong to the root Reporter")
}

// formatDetail renders a Detail's text. With no args the format string is the
// literal text, so a caller passing a value containing % verbs as the sole
// argument is not misread as a format string.
func formatDetail(format string, args []any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}
