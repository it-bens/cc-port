---
paths:
  - "**/*_test.go"
---

# Tests: Data and Fixtures

**CRITICAL**: Arrange data MUST come from sources the reader can see. External files pulled from unknown paths, opaque hex identifiers, and inline blobs duplicated across tests all obscure what the test is exercising.

## Project baseline

- `testutil.SetupFixture(t)` stages `testdata/dotclaude/` under `t.TempDir` and returns a `*claude.Home`. This is the canonical fixture entry point for integration-flavored tests.
- `testdata/` fixture **file** names may be UUID-style hex (`a1b2c3d4-…-000001.jsonl`) because Claude Code's real on-disk shapes are that format. Test **identifiers in code** (variables, assertion arguments) must be descriptive.
- Inline fixtures (byte literals, short heredoc-style strings) are preferred for narrow unit tests exercising one parser path. Real fixture files are preferred for end-to-end flows.
- Per-test helpers already wire up repeated construction (`testutil.SetupFixture`, local `newRewriter`-style functions). Extend those before inventing new patterns.

## Decision Test

Before writing arrange code:

> **"Can a reader of only this test tell what the inputs are and where they came from?"**

If the answer requires opening another file outside `testdata/`, a helper in a sibling package, or a cross-test fixture borrow, simplify.

---

## ISOLATION-003 — Mystery Guest File Dependency

External file dependencies (`os.ReadFile`, `os.Open`, `//go:embed`) MUST point to a fixture the reader can locate from the test file.

### Acceptable

- `testdata/` (Go toolchain ignores this directory for builds)
- Test-local fixtures via `testutil.SetupFixture(t)` sourced from the repo `testdata/` tree
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

---

## ISOLATION-004 — Opaque Test Data Identifiers

String literals used as identifiers in assertions MUST be descriptive. Hex blobs make failure messages unreadable.

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

---

## ISOLATION-006 — Real Fixture Files

Tests exercising file parsing or complex I/O SHOULD read real fixture files from `testdata/` rather than build content inline.

### Applies when

- Test writes a multi-line blob to disk, then reads it back
- Test builds a file via heredoc-style string concatenation longer than ~10 lines
- Test exercises a parser/importer/exporter/scanner against representative input

### Does not apply when

- Blob is a single line (`{"key":"value"}`)
- Test specifically exercises a malformed input shape (inline is clearer than a dedicated fixture per malformation)
- Content isn't written to any file or stream

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

---

## DESIGN-009 — Duplicated Inline Arrange Code

If two or more tests repeat 5+ consecutive lines of construction with identical types and arguments, extract a helper.

### Extraction patterns (Go)

| Pattern | Use when |
|---|---|
| Helper function with `t.Helper()`: `func newRewriter(t *testing.T, opts ...Option) *rewrite.Replacer` | Most common. Test-local, fails at the real call site, accepts overrides. |
| `t.Cleanup(func(){...})` inside a helper | Temp dirs, file handles, background goroutines. |
| Per-row `setup func(t *testing.T) *Service` field in a table | Table-driven tests where rows need distinct construction variants. |
| `testutil.SetupFixture(t)` | Anything needing the full staged `~/.claude` fixture. Extend this helper before inventing a new one. |
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

---

## Related

- `test-independence.md` — why external files and globals leak across tests
- `test-behavior-under-test.md` — what the fixture is actually there to verify
- `test-shape.md` — structure of the test body
