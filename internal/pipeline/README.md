# internal/pipeline

## Purpose

Composes write-side `io.Writer` chains and read-side `View` chains for cc-port's data flows. Stages live in their owning packages; this package owns the interfaces, the runners, and the close cascade.

## Public API

- `View`: stage-to-stage data carrier (`ReaderAt`, `Size`). No Close field; lifetime is reported separately.
- `Source`: read-side consumer-facing struct. Embeds `View` and adds an idempotent `Close` built by the runner.
- `WriterStage`, `ReaderStage`: stage interfaces. `Open` returns the data half plus an optional `io.Closer` (nil means passthrough).
- `RunWriter(ctx, []WriterStage) (io.WriteCloser, error)`: composes inside-out, returns an outer writer whose Close walks every accumulated closer with `errors.Join`.
- `RunReader(ctx, []ReaderStage) (Source, error)`: composes outside-in, returns a Source whose Close walks every accumulated closer in reverse with `errors.Join`.

## Contracts

### Stage composition

Used by `cmd/cc-port` to assemble per-command pipelines.

#### Handled

- Stage list with at least one element. Source/sink stages may sit at any position; the runner does not enforce ordering policy.
- Outer Close walks every non-nil closer once. Writer side closes outer-first, leaf-last; reader side closes latest-first, source-last.
- Outer Close is idempotent. Second and later calls return nil without re-closing stages.
- Close errors are joined with `errors.Join`. Every closer runs even if an earlier one returns an error.
- Error during stage Open: closes every closer accumulated so far with the same join semantics, returns the wrapped open error joined with any close errors.

#### Refused

- Empty stage list. RunWriter and RunReader return an error.

#### Not covered

- Ordering correctness. The runner accepts any list. Meaningful orderings (encrypt before sink, source before decrypt) are the responsibility of the cmd layer that builds the list.

### Close cascade

#### Handled

- Stages report only what they own. A stage with no resource returns `closer == nil`; the runner skips nil closers when building the chain.
- The runner is the single owner of close ordering, idempotency, and error joining. Stages do not chain to upstream or downstream Close.

#### Refused

- Stage Closers that themselves close upstream/downstream. Doing so would double-close once the runner reaches the chained closer.

#### Not covered

- Stage Closers that internally manage multiple resources (for example, `encrypt.decrypt` and `remote.drainToTempfile` close a tempfile and remove it). Those stages join their internal errors with `errors.Join`; the policy is the same shape as the runner's outer policy.

## Quirks

Leaf stages (sources, sinks) ignore their `upstream` / `downstream` parameter. The interface tolerates the zero `View` and `nil` `io.Writer` because every stage shares the same shape. Splitting leaf and filter into separate interfaces would split the runner loop for no real gain.

A stage may inspect its input at `Open` time and either transform it or return it unchanged. Self-skipping is the stage's prerogative; the runner imposes no act-vs-passthrough policy. Filters that depend on runtime detection (encryption, signing, compression-by-magic-byte) absorb the dispatch into their own `Open`; the cmd layer composes the same stage list regardless of whether the filter will fire on a given invocation.

## Tests

`pipeline_test.go` exercises:
- Single-stage round-trip on both read and write paths.
- Filter-then-sink and source-then-filter close ordering, with passthrough stages contributing no entry to the chain.
- Outer-Close idempotency on both paths.
- `errors.Join` of stage close errors on both paths.
- Open-error mid-composition: already-opened stage closers run before the error propagates.
- Stage-name and position-index appear in the wrapped error.
