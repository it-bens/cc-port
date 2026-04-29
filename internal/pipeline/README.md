# internal/pipeline

## Purpose

Composes write-side `io.Writer` chains and read-side `Source` chains for cc-port's data flows. Stages live in their owning packages; this package owns the interfaces and the runners.

## Public API

- `Source`: read-side carrier struct (`ReaderAt`, `Size`, `Close`). Each stage's Source.Close cleans its own resources and chains to upstream.Close.
- `WriterStage`, `ReaderStage`: stage interfaces.
- `RunWriter(ctx, []WriterStage) (io.WriteCloser, error)`: composes inside-out, returns outermost writer.
- `RunReader(ctx, []ReaderStage) (Source, error)`: composes outside-in, returns final Source.

## Contracts

### Stage composition

Used by `cmd/cc-port` to assemble per-command pipelines.

#### Handled

- Stage list with at least one element. Source/sink stages may sit at any position; runner does not enforce ordering policy.
- Close-chain unwinds: closing the outermost writer or the returned Source walks every stage's close in chain order.
- Error during stage Open: closes any already-opened earlier stages, returns wrapped error naming the failing stage.

#### Refused

- Empty stage list. RunWriter and RunReader return an error.

#### Not covered

- Ordering correctness. The runner accepts any list. Meaningful orderings (encrypt before sink, source before decrypt) are the responsibility of the cmd layer that builds the list.

## Quirks

The leaf stages (sources, sinks) ignore their `upstream` / `downstream` parameter. The interface tolerates the zero `Source` and `nil` `io.Writer` because every stage shares the same shape. Splitting leaf and filter into separate interfaces would split the runner loop for no real gain.

A stage may inspect its input at `Open` time and either transform it or return it unchanged. Self-skipping is the stage's prerogative; the runner imposes no act-vs-passthrough policy. Filters that depend on runtime detection (encryption, signing, compression-by-magic-byte) absorb the dispatch into their own `Open`; the cmd layer composes the same stage list regardless of whether the filter will fire on a given invocation.

## Tests

`pipeline_test.go` exercises:
- Single-stage round-trip on both read and write paths.
- Filter-then-sink and source-then-filter close ordering.
- Error propagation at each position with stage-name and position-index in the wrapped error.
