# internal/progress — agent notes

## Before editing

- Progress observes quantities only: counts, bytes, names, durations. Never read or carry user-data bytes through an Event (README §Quantity-only observation).
- A command receives its Reporter through its Options struct, never a package global; a nil Reporter is replaced with `Noop()` by the command (README §Reporter injection).
- Verbose and debug `Detail` events drop on Ledger backpressure; phase and terminal events block (README §Drop policy).
- Select renderers and detect the terminal only through `Pick` and the `isTTY` seam; add no second selection path or ad-hoc terminal check (README §Renderer selection).
- The `now`, `isTTY`, and `ledgerInput` package vars are the only package-level mutable state, reassigned only by tests under `t.Cleanup` (README §Quirks).

## Navigation

- Reporter and event stream: `reporter.go`, `noop.go`.
- Events, units, levels: `event.go`.
- Renderer selection: `pick.go`.
- Renderers: `render_ledger.go`, `render_stream.go`, `render_json.go`, `render_null.go`.
- Byte counters: `counting.go`.
- Test recorder: `progresstest/recorder.go`.
