# internal/credentials -- agent notes

## Before editing

- Every new error is a sentinel or typed error; assertion shape goes in the inventory (README §Errors).
- Prompt path obeys the cancellation contract (README §Quirks).
- Do not relax the 0600 file-mode ceiling on credentials files (README §Source layering and precedence).
- When adding a new credential source, extend the resolver's source list in source-precedence order; do not bypass `mergePreferLeft` (README §Source layering and precedence).

## Navigation

- Public surface: `credentials.go` (`Resolve`, `ResolveOptions`).
- Errors: `errors.go`.
- File parser: `file.go`.
- Env reader: `env.go`.
- TTY interface: `tty.go`. Real impl: `prompt.go` (`osTTYPrompter`).
- Resolver: `resolver.go` (`resolveWith`, the testable seam).
- Tests: `file_test.go`, `env_test.go`, `resolver_test.go`.
