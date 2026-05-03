# internal/pipeline — agent notes

## Before editing

- Keep the runner free of policy. Ordering correctness is the cmd-layer's responsibility (README §Stage composition).
- The runner owns the close cascade. Stages return their own io.Closer, or nil for passthrough; never call upstream's or downstream's Close from a stage (README §Close cascade).
- Source.Close is built by the runner. Do not reintroduce per-stage `closed bool` flags or close-cascade wrappers (README §Close cascade).
- Leaf stages receive a zero `View` or nil `io.Writer`. Do not add a separate leaf interface (README §Quirks).
- Add MaterializeStage as the trailing reader stage when the consumer needs io.ReaderAt. Never re-introduce per-stage tempfile drains (README §Public API).

## Navigation

- Entry: `pipeline.go` (`View`, `Meta`, `Source`, `WriterStage`, `ReaderStage`, `RunWriter`, `RunReader`).
- Materialization: `materialize.go` (`MaterializeStage`, `tempfileCloser`).
- Tests: `pipeline_test.go`, `materialize_test.go`.
