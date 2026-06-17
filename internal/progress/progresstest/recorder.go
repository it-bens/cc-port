// Package progresstest provides a recording Renderer for tests that observe
// the event stream a command produces. The Recorder hands out a real Reporter
// built via progress.NewReporter, so command-wiring tests exercise the genuine
// level filter, path nesting, and Phase/SubPhase aliasing rather than a
// parallel hand-rolled stand-in that could drift from production semantics.
package progresstest

import (
	"sync"

	"github.com/it-bens/cc-port/internal/progress"
)

// Recorder is a progress.Renderer that appends every consumed event to an
// ordered slice. It is safe for concurrent Consume calls so counting wrappers
// driven from multiple goroutines record without racing.
type Recorder struct {
	mutex  sync.Mutex
	events []progress.Event
}

// NewRecorder returns an empty Recorder.
func NewRecorder() *Recorder {
	return &Recorder{}
}

// Consume records event in emission order.
func (recorder *Recorder) Consume(event progress.Event) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.events = append(recorder.events, event)
}

// Finalize satisfies progress.Renderer. The recorder has nothing to flush.
func (recorder *Recorder) Finalize() error {
	return nil
}

// Reporter returns a real Reporter at the given active level that forwards into
// this recorder. Tests put the returned Reporter on a command's Options struct.
func (recorder *Recorder) Reporter(level progress.Level) progress.Reporter {
	return progress.NewReporter(recorder, level)
}

// Events returns the recorded events in emission order.
func (recorder *Recorder) Events() []progress.Event {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]progress.Event(nil), recorder.events...)
}

// OfType returns the events of concrete type T, preserving order.
func OfType[T progress.Event](events []progress.Event) []T {
	var matched []T
	for _, event := range events {
		if typed, ok := event.(T); ok {
			matched = append(matched, typed)
		}
	}
	return matched
}
