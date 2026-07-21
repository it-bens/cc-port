# Test Data and Fixtures (reference)

## ISOLATION-003 — File dependency must be locatable from the test

External file dependencies (`os.ReadFile`, `os.Open`, `//go:embed`) point to a fixture the reader can locate from the test file.

### Acceptable

- `testdata/` (Go toolchain ignores this directory for builds)
- Test-local fixtures via `testutil.SetupFixture(t)` sourced from the repo `testdata/dotclaude/` tree
- Codex fixtures via `codex.SetupFixture(t)` sourced from `internal/tool/codex/testdata/dotcodex/` — the SQLite databases and the `memories/.git` baseline are built at runtime by the helper, because git cannot track a nested `.git`; never add on-disk copies of these to the fixture tree
- `testutil.WriteFixtureArchive(t)` for a known-good export archive; `testutil.FixtureProjectPath()` / `codex.FixtureProjectPath()` for the canonical project key the fixture trees are staged around
- `//go:embed testdata/foo.json` at the test package level

### Flag

- Absolute paths (`/home/user/data.json`, `/tmp/cc-port-fixture`)
- Source-tree access (`../../../internal/foo/bar.go` opened at test time)
- Cross-package fixture borrow (`../../internal/other/testdata/...`)
- Dynamic globs over an unbounded directory

```go
// WRONG: absolute path, flaky across machines
data, err := os.ReadFile("/home/me/samples/session.jsonl")

// WRONG: reaching into another package's fixtures
data, err := os.ReadFile("../../internal/move/testdata/old-layout.json")

// RIGHT: package-local testdata
data, err := os.ReadFile(filepath.Join("testdata", "session.jsonl"))

// RIGHT: staged fixture tree
home := testutil.SetupFixture(t)   // home.ConfigFile points into t.TempDir
```

## ISOLATION-004 — Descriptive identifiers in code

String literals used as identifiers in assertions are descriptive. Hex blobs make failure messages unreadable.

### Flag

- 32 consecutive hex characters used as a test-constructed identifier: `"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`
- Repeated-character placeholders: `"0000000000000001"`, `"ffffffffffffffff"`
- Placeholder UUIDs invented for the test body

### Do NOT flag

- UUIDs read from or written to `testdata/` (they mirror real on-disk shapes)
- Identifiers produced by the code under test (captured in a var named `generated`)
- Tests that specifically exercise UUID-format validation

```go
// WRONG: unreadable in a failure message
home.WriteSession(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", payload)
got, err := claude.LoadSession(home, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
require.NoError(t, err)
assert.Equal(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", got.ID)

// RIGHT: descriptive
home.WriteSession(t, "primary-session", payload)
got, err := claude.LoadSession(home, "primary-session")
require.NoError(t, err)
assert.Equal(t, "primary-session", got.ID)
```

### Descriptive-name conventions for cc-port

| Context | Good |
|---|---|
| Project paths | `/Users/test/Projects/myproject`, `/Users/test/Projects/newproject` |
| Session IDs in code | `"primary-session"`, `"orphaned-session"` |
| File-history keys | `"edited-file"`, `"deleted-file"` |
| Manifest entries | `"first-snapshot"`, `"later-snapshot"` |

## ISOLATION-006 — Real fixture files for parsers and complex I/O

Tests exercising file parsing or complex I/O read real fixture files from `testdata/` rather than build content inline.

### Applies when

- Test writes a multi-line blob to disk, then reads it back
- Test builds a file via heredoc-style string concatenation longer than ~10 lines
- Test exercises a parser, importer, exporter, or scanner against representative input

### Does not apply when

- Blob is a single line (`{"key":"value"}`)
- Test specifically exercises a malformed input shape (inline is clearer than a dedicated fixture per malformation)
- Content is not written to any file or stream

```go
// WRONG: 40-line heredoc builds a synthetic session file inline
func TestScanSession(t *testing.T) {
    content := `{"type":"session","id":"abc","entries":[` + ... + `]}` + "\n" +
               `{"type":"entry",...}` + "\n" +
               // ... 38 more lines
    path := filepath.Join(t.TempDir(), "session.jsonl")
    require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
    got, err := scan.Session(path)
    require.NoError(t, err)
    assert.Len(t, got.Entries, 5)
}

// RIGHT: the fixture lives in testdata/ where it can be inspected and reused
func TestScanSession(t *testing.T) {
    got, err := scan.Session(filepath.Join("testdata", "session.jsonl"))
    require.NoError(t, err)
    assert.Len(t, got.Entries, 5)
}
```

## DESIGN-009 — Helper extraction for repeated arrange code

If two or more tests repeat 5+ consecutive lines of construction with identical types and arguments, extract a helper.

### Extraction patterns (Go)

| Pattern | Use when |
|---|---|
| Helper function with `t.Helper()`: `func newRewriter(t *testing.T, opts ...Option) *rewrite.Replacer` | Most common. Test-local, fails at the real call site, accepts overrides. |
| `t.Cleanup(func(){...})` inside a helper | Temp dirs, file handles, background goroutines. |
| Per-row `setup func(t *testing.T) *Service` field in a table | Table-driven tests where rows need distinct construction variants. |
| `testutil.SetupFixture(t)` / `codex.SetupFixture(t)` | Anything needing the full staged `~/.claude` or `~/.codex` fixture. Extend these helpers before inventing a new one. |
| `TestMain(m *testing.M)` | Process-global setup only (env, temp workspace). Do NOT use for per-test state; that crosses into ISOLATION-001. |

### Do NOT extract

- Helper would hide the single input that varies per test (the variation is the test)
- Fewer than 5 repeated lines
- Only two current occurrences; wait for the third before extracting

```go
// WRONG: same 6-line setup repeated across three tests
func TestRewriteReplacesExactPath(t *testing.T) {
    fsRoot := t.TempDir()
    require.NoError(t, os.WriteFile(filepath.Join(fsRoot, "config.json"), []byte(`{}`), 0o644))
    promoter := rewrite.NewSafeRenamePromoter(fsRoot)
    rewriter := rewrite.NewReplacer(promoter, rewrite.Options{})
    // ...
}
// (and again, and again)

// RIGHT: one helper placed below the tests that use it
func newRewriter(t *testing.T) (fsRoot string, r *rewrite.Replacer) {
    t.Helper()
    fsRoot = t.TempDir()
    require.NoError(t, os.WriteFile(filepath.Join(fsRoot, "config.json"), []byte(`{}`), 0o644))
    return fsRoot, rewrite.NewReplacer(rewrite.NewSafeRenamePromoter(fsRoot), rewrite.Options{})
}
```

### Placement

- Helpers go **below** the tests that use them (Go convention: callers above callees in test files).
- Helper names start lowercase (package-local).
- `*testing.T` is the first parameter; `t.Helper()` is line 1.
- Cleanup lives in `t.Cleanup` inside the helper, not in a returned `func()`.

## Production-scale gating with `//go:build large`

Tests that materialize hundreds of MiB or multi-GiB fixtures to exercise cap-rejection guards, aggregate-size limits, or stream-buffer overflow do not belong in CI. Two-test pattern:

- The production-scale test lives in a sibling file gated by `//go:build large`. The maintainer runs the tagged suite locally before merging changes to cap-guarded code: `go test -tags large ./internal/importer/...`.
- A small-cap variant in the untagged suite exercises the same branches at KiB scale by constructing a small `archive.Caps{...}` value and passing it straight into the function under test (`archive.OpenReader(src, size, caps)`). Caps are injected parameters, never package-global state to override.

Neither test replaces the other. The small-cap variant confirms on every run that the rejection branch fires. The large-tag variant confirms the threshold actually holds at production scale and that no hidden buffer (default `bufio.Scanner`, intermediate slice) breaks before the cap. CI runs the tagged suite in a dedicated step; `make test-large` covers it locally.

### Pattern

```go
// Small-cap variant: untagged, fast. Caps are a constructor argument, not
// package state, so the small value goes straight into the call under test.
func TestRejectsAggregateAtSmallCap(t *testing.T) {
    reader, err := archive.OpenReader(src, size, archive.Caps{MaxEntryBytes: 4096, MaxAggregateBytes: 3072})
    // build a ~4 KiB archive; assert rejection
}

// large-tag variant: production caps, slow.
//go:build large

func TestRejectsAggregateAtProductionCap(t *testing.T) {
    reader, err := archive.OpenReader(src, size, archive.DefaultCaps())
    // build a multi-GiB archive; assert rejection at the production cap
}
```

If the small-cap variant cannot exercise a branch the production-scale variant exercises (e.g. behavior that only manifests at the GiB scale of the real `bufio.Reader` chunking), document the gap in the production-scale test's leading comment so a maintainer reading only CI output knows what `large` adds.
