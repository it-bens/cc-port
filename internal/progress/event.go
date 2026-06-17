// Package progress models the progress-indication event stream cc-port
// commands emit while they run. It defines the Reporter/PhaseHandle contract
// commands wire against, the immutable Event values that contract produces,
// and the Renderer sink those events flow into. Renderers themselves live
// outside this file.
package progress

import "time"

// Unit names what a phase counts so a renderer can format totals and
// throughput. The zero value is UnitItems, a generic count.
type Unit int

// The known units a phase can count.
const (
	UnitItems Unit = iota
	UnitFiles
	UnitLines
	UnitBytes
	UnitEntries
)

// Level is a Detail's verbosity, ascending: LevelError is the least verbose
// (always shown), LevelDebug the most. A Reporter has an active level; a
// Detail reaches the sink iff its level is at or below the active level.
type Level int

// The verbosity levels, ascending from least to most verbose.
const (
	LevelError Level = iota
	LevelInfo
	LevelVerbose
	LevelDebug
)

// Event is a single progress occurrence. The interface is sealed: only the
// types in this file implement it, so a renderer's type switch is exhaustive.
type Event interface {
	isEvent()
}

// PhaseStart opens a phase. Path is the dotted phase address, one segment per
// nesting level. Total is the unit count the phase expects to complete; a
// total is always known when the phase opens, so there is no separate
// total-revision event.
type PhaseStart struct {
	Path  []string
	Total int64
	Unit  Unit
	At    time.Time
}

// PhaseAdvance reports forward progress within a phase. Done is the cumulative
// units completed so far, not the delta of the triggering Advance call.
type PhaseAdvance struct {
	Path []string
	Done int64
}

// PhaseEnd closes a phase. Dur is the wall time from the matching PhaseStart.
type PhaseEnd struct {
	Path    []string
	Summary string
	Dur     time.Duration
}

// Detail is a free-text diagnostic line at a verbosity level. Only Details
// surviving the active-level filter become events.
type Detail struct {
	Level Level
	Text  string
	At    time.Time
}

// Warning carries a non-fatal error. Warnings are never level-filtered.
type Warning struct {
	Err error
	At  time.Time
}

// Cancelled is a terminal event: the run stopped on request.
type Cancelled struct {
	Reason string
}

// Failed is a terminal event: the run stopped on an error.
type Failed struct {
	Err error
}

// Done is a terminal event: the run completed successfully.
type Done struct{}

func (PhaseStart) isEvent()   {}
func (PhaseAdvance) isEvent() {}
func (PhaseEnd) isEvent()     {}
func (Detail) isEvent()       {}
func (Warning) isEvent()      {}
func (Cancelled) isEvent()    {}
func (Failed) isEvent()       {}
func (Done) isEvent()         {}

// Renderer consumes the event stream and produces the user-visible output.
// Consume receives every surviving event in emission order; Finalize runs once
// after the stream closes. Implementations live in renderer-specific files.
type Renderer interface {
	Consume(Event)
	Finalize() error
}

// Interruptible is implemented by renderers that own interactive terminal input
// and can observe a user interrupt (Ctrl-C). The cmd layer type-asserts a
// renderer to this interface and routes Interrupted to context cancellation;
// renderers without interactive input do not implement it.
type Interruptible interface {
	Interrupted() <-chan struct{}
}
