# internal/pipeline -- agent notes

## Before editing

- Keep the runner free of policy. Ordering correctness is the cmd-layer's responsibility (README §Stage composition).
- Source.Close is a function field, not an interface method. Stages chain their close to upstream's close inside the closure (README §Public API).
- Leaf stages receive zero `Source` or nil `io.Writer`. Do not add a separate leaf interface (README §Quirks).

## Navigation

- Entry: `pipeline.go` (`RunWriter`, `RunReader`, `Source`, `WriterStage`, `ReaderStage`).
- Tests: `pipeline_test.go`.
