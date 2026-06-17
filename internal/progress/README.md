# internal/progress

## Purpose

Progress indication for the long-running commands. A command holds a `Reporter` and emits an event stream as it works; the cmd layer composes one of four renderers to turn that stream into terminal output. The package models the event contract, the renderers, and the renderer-selection logic.

The Reporter observes quantities only: file counts, byte totals, phase names, durations. It never reads user-data content. Progress inherits the opacity that `docs/architecture.md` §File-history policy (cross-cutting) frames for snapshot bodies, so no new byte classification enters the codebase through this package.

## Public API

- **Reporter contract**
  - `Reporter`: the surface a command emits through. `Phase` opens a phase; `Detail`, `Warn` report mid-run; `Done`/`Fail`/`Cancelled` are the terminal methods the root owns.
  - `PhaseHandle`: a `Reporter` scoped to one open phase. `Phase` aliases `SubPhase`; both append one path segment. `Advance(n)` adds to the cumulative count and `End(summary)` closes the phase. Calling a terminal method on a handle panics.
  - `NewReporter(renderer Renderer, level Level) Reporter`: builds the root Reporter forwarding surviving events to `renderer` at the active `level`.
  - `Noop() Reporter`: a Reporter that swallows every event, the default a command carries when progress is off. Its handle's terminal methods swallow rather than panic.
- **Events**
  - `Event`: sealed interface; only the types below implement it, so a renderer's type switch is exhaustive.
  - `PhaseStart` (`Path`, `Total`, `Unit`, `At`), `PhaseAdvance` (`Path`, cumulative `Done`), `PhaseEnd` (`Path`, `Summary`, `Dur`). A phase's total is known when it opens, so there is no total-revision event.
  - `Detail` (`Level`, `Text`, `At`), `Warning` (`Err`, `At`).
  - `Cancelled` (`Reason`), `Failed` (`Err`), `Done`: the three terminal events.
  - `Unit`: `UnitItems`, `UnitFiles`, `UnitLines`, `UnitBytes`, `UnitEntries`. The zero value is `UnitItems`.
  - `Level`: `LevelError`, `LevelInfo`, `LevelVerbose`, `LevelDebug`, ascending. A `Detail` reaches the sink when its level is at or below the Reporter's active level.
- **Renderers and selection**
  - `Renderer`: `Consume(Event)` per surviving event in emission order, `Finalize() error` once after the stream closes.
  - `LedgerRenderer`, `StreamRenderer`, `JSONRenderer`, `NullRenderer`: the four sinks, built via `NewLedgerRenderer`, `NewStreamRenderer`, `NewJSONRenderer`, `NewNullRenderer`.
    - **Ledger** drives an interactive TTY through bubbletea: a live tree of phase nodes with spinner and progress bars. Selected when the sink is a TTY and neither `--json` nor `--quiet` is set.
    - **Stream** writes append-only, ANSI-free lines for CI logs and redirected stderr. `PhaseStart`/`PhaseEnd` always print; `PhaseAdvance` is rate-limited to one line per phase per 500 ms. The non-TTY default.
    - **JSON** emits one schema-stable, newline-delimited object per event under `--json`, regardless of TTY.
    - **Null** is the `--quiet` sink. It drops every event but warnings and a terminal summary: an `[ERROR]` line plus a `failed at <phase>` line on failure, a single line otherwise.
  - `Selection`: the flag-derived intent (`JSON`, `Quiet`, `Verbose`, `Debug`, `Output`).
  - `Pick(selection) (Renderer, Level)`: maps a `Selection` to a concrete renderer and active level. See §Contracts §Renderer selection.
- **Byte counters**
  - `CountingWriter`, `CountingReaderAt`: wrap an `io.Writer`/`io.ReaderAt` so each successful operation advances a `PhaseHandle` by the bytes actually moved, not the bytes offered.

## Contracts

### Quantity-only observation

The Reporter and every renderer observe quantities: counts, byte totals, phase names, durations, error values from `Warn`/`Fail`. They never read the body of a file being copied or rewritten. `docs/architecture.md` §File-history policy (cross-cutting) frames the opacity this package inherits.

Callers: every command path that holds a `Reporter` (`internal/move`, `internal/export`, `internal/importer`, `internal/sync`).

#### Handled

- Phase totals and advances carry integer counts and a `Unit`, never the content they count.
- `CountingWriter`/`CountingReaderAt` advance by byte length alone; the bytes flow through the wrapped stream untouched and unobserved.

#### Refused

None at runtime. The event types structurally cannot carry a file body: no `Event` field holds opaque payload bytes.

#### Not covered

- A `Warning` or `Failed` carries an `error`, whose message a command may build from a path or filename. Sanitizing user-supplied error text is the emitting command's concern, not this package's.

### Renderer selection

`Pick` maps a `Selection` to a renderer and an active `Level`. Renderer choice and level are independent.

Callers: `cmd/cc-port` (`runWithProgress`).

#### Handled

- Renderer precedence: `--json` wins over everything including a TTY; then `--quiet` selects Null; then a TTY sink selects Ledger; otherwise Stream.
- Level mapping: `--quiet` pins `LevelError`; else `--debug` pins `LevelDebug`; else `--verbose` pins `LevelVerbose`; else `LevelInfo`.
- TTY detection runs through the `isTTY` seam against `Selection.Output`, so `--json` and `--quiet` never probe the terminal.

#### Refused

None at runtime. Every `Selection` resolves to exactly one renderer and one level.

#### Not covered

- Flag conflict validation (for example `--quiet` together with `--verbose`). `Pick` applies fixed precedence rather than rejecting the combination; the cmd layer owns any stricter policy.

### Drop policy

The Ledger owns the only event channel, so backpressure is possible there and nowhere else. `Detail` at `LevelVerbose` or `LevelDebug` is dropped when the channel is full; phase events and terminal events block until the model reads them, because they are load-bearing for the rendered tree and final frame. The blocking send also unblocks when the program goroutine has already exited, so a dead reader never freezes the work goroutine.

Callers: `cmd/cc-port` (`runWithProgress`), through `NewReporter`.

#### Handled

- Verbose and debug `Detail` events drop on a full Ledger channel so the work goroutine never blocks on cosmetic output.
- `PhaseStart`, `PhaseAdvance`, `PhaseEnd`, `Warning`, and all terminal events block on a full Ledger channel until consumed.
- Stream, JSON, and Null serialize every `Consume` through a mutex and never drop. They have no channel and no backpressure.

#### Refused

None at runtime. A dropped verbose Detail is the intended outcome, not an error.

#### Not covered

- The active-level filter in `eventStream.emitDetail` runs before any renderer sees the event. A `Detail` more verbose than the active level never reaches a renderer, so the Ledger drop policy applies only to Details that already survived the level filter.

### Reporter injection

A command receives its `Reporter` through its Options struct, never a package global. An unset `Reporter` field (nil) is replaced with `Noop()` by the command before use. The cmd layer is the sole renderer composer: `runWithProgress` in `cmd/cc-port` builds the renderer from the verbosity flags and constructs the Reporter, then hands it to the command.

Callers: `cmd/cc-port` (`runWithProgress`); every command's Options struct (`internal/move`, `internal/export`, `internal/importer`, `internal/sync`).

#### Handled

- Each command's Options struct carries a `Reporter` field defaulting to `Noop()`, so a command driven without progress wiring still runs.
- Renderer construction and the TTY probe live in `cmd/cc-port`, keeping the package free of terminal-detection policy at its call sites.

#### Refused

None at runtime. A nil `Reporter` on an Options struct is replaced with `Noop()` by the command, not rejected.

#### Not covered

- The package exposes no global Reporter and no registration entry point. A consumer that wants ambient progress must thread the Reporter through its own call graph.

## Quirks

The unexported package vars `now` and `isTTY` are the only package-level mutable state. They are seams a test reassigns under `t.Cleanup` to pin timestamps, durations, and terminal detection. Production never reassigns them.

Phases are determinate by file count where the iteration pre-enumerates its work list, so `PhaseStart.Total` carries the real count. The two streaming copy phases in `internal/move` are the documented exception: they open with `Total` zero and drive an indeterminate live counter through `CopyDir`'s `onEntry` callback, avoiding a second metadata walk just to learn the count up front. See `internal/move/README.md` §Source mtime preservation (move) for the copy phases themselves.

## Tests

The `progresstest` subpackage supplies `Recorder`, a recording `Renderer` for command-wiring tests. `Recorder.Reporter(level)` hands out a real `Reporter` built via `NewReporter`, so a test exercises the genuine level filter, path nesting, and `Phase`/`SubPhase` aliasing rather than a hand-rolled stand-in. `Recorder.Events()` returns the recorded events in emission order; `OfType[T](events)` filters them to one concrete event type.

Unit tests in `event`/`reporter`/`pick`/`counting`/`render_*` test files cover the level filter, path nesting, `Phase`/`SubPhase` aliasing, terminal-method panics on a handle, the `Pick` precedence and level matrix, the counting wrappers' advance-by-bytes-moved behavior, and each renderer's `Consume`/`Finalize` output. `render_golden_test.go` pins the Ledger frames. `progresstest/recorder_test.go` covers the recorder.
