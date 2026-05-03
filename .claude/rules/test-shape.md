---
paths:
  - "**/*_test.go"
---

# Tests: Shape on the Page

**CRITICAL**: A test MUST be readable top-to-bottom in one pass. Conditionals that select between assertions, assertions threaded through setup, and multi-behavior assertion blocks force the reader to reconstruct intent.

## Project baseline

- testify `require`/`assert` is the assertion library. `require` for preconditions, `assert` for behavioral claims after the act.
- Table-driven tests via `t.Run(tc.name, func(t *testing.T) {...})` are the idiom for parameterized coverage.
- `if tc.wantErr { require.Error(t, err); return }` is the accepted pattern for error/success dispatch in tables. Use it; do not invent new conditional shapes.
- No `t.Parallel()` today; this guide assumes sequential tests.

## Decision Test

Before finishing a test body:

> **"If the test name and signature were stripped, could a reviewer still identify the three phases (arrange, act, assert) and the one behavioral claim?"**

If no, restructure.

---

## CONV-014 — AAA Structure

Tests with 5+ statements MUST separate arrange, act, and assert phases. Assertions live after the final act, not interspersed.

### Skip

- Tests under 5 statements
- Table-driven subtest bodies of 2-3 statements
- Exception-only tests (arrange → expect → act is a fine two-phase shape)

```go
// WRONG: assertions scattered through the body
func TestProcessesHome(t *testing.T) {
    home := testutil.SetupFixture(t)
    assert.NotEmpty(t, home.Dir)                         // assertion in arrange
    plan, err := move.DryRun(home, opts)
    require.NoError(t, err)
    assert.Positive(t, plan.ReplacementsByCategory["history"])   // mid-act assertion
    err = move.Apply(home, opts)
    require.NoError(t, err)
    assert.DirExists(t, home.ProjectDir(newPath))
}

// RIGHT: arrange, act, assert
func TestApplyRelocatesProjectDir(t *testing.T) {
    // Arrange
    home := testutil.SetupFixture(t)

    // Act
    err := move.Apply(home, moveOpts)

    // Assert
    require.NoError(t, err)
    assert.DirExists(t, home.ProjectDir(newPath))
}
```

Comment banners are optional; blank lines between phases are enough when sections are short.

---

## DESIGN-001 — No Conditional Logic in Tests

Test bodies MUST NOT contain conditional logic that picks between assertions.

### Prohibited

- `if`/`else` selecting which assertion runs
- `switch`/`select` dispatching on test expectations
- Loops with per-iteration branching on expectations
- Ternary-style `(cond && a) || b` for control flow

### Carve-outs specific to Go

Not violations:

| Pattern | Why |
|---|---|
| `if tc.wantErr { require.Error(t, err); return } require.NoError(t, err)` in a table subtest | Idiomatic Go dispatch for error-vs-success. Bounded, single shape. |
| `for _, tc := range cases { t.Run(tc.name, ...) }` | Loop drives subtests, not assertion branching. |
| `if runtime.GOOS == "windows" { t.Skip(...) }` | Platform gate, not assertion logic. |
| `if testing.Short() { t.Skip(...) }` | Standard short-mode gate. |
| `require.NoError(t, err)` short-circuiting on precondition failure | Not an author-written conditional. |

The `wantErr` carve-out applies **only** when the success path's assertions are identical across rows. If rows need different positive assertions, split the table.

```go
// WRONG: branch picks between two different positive assertions
for _, tc := range cases {
    t.Run(tc.name, func(t *testing.T) {
        got, err := validate(tc.input)
        if tc.wantValid {
            require.NoError(t, err)
            assert.Equal(t, tc.wantLen, got.Len())
        } else {
            require.Error(t, err)
        }
    })
}

// RIGHT: two tables, two test functions
func TestValidateAcceptsInput(t *testing.T) {
    cases := []struct {
        name    string
        input   string
        wantLen int
    }{
        {"ascii", "john", 4},
        {"unicode", "jöhn", 4},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got, err := validate(tc.input)
            require.NoError(t, err)
            assert.Equal(t, tc.wantLen, got.Len())
        })
    }
}

func TestValidateRejectsInput(t *testing.T) {
    cases := []struct {
        name  string
        input string
    }{
        {"empty", ""},
        {"whitespace", "   "},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            _, err := validate(tc.input)
            require.Error(t, err)
        })
    }
}
```

### Acceptable wantErr shape

```go
cases := []struct {
    name    string
    input   string
    wantErr bool
}{
    {"well-formed", `{"ok":1}`, false},
    {"malformed", `{ bad`, true},
}
for _, tc := range cases {
    t.Run(tc.name, func(t *testing.T) {
        _, err := parse([]byte(tc.input))
        if tc.wantErr {
            require.Error(t, err)
            return
        }
        require.NoError(t, err)
    })
}
```

---

## DESIGN-005 — Assertion Scope

Multiple assertions in a test body are acceptable only when they verify a single logical behavior. Unrelated claims belong in separate tests.

Acceptable clusters:
- Multiple properties of one returned object
- Before/after state of one operation
- Related aspects of one behavior

Not acceptable:
- Create + persistence + logging + metric in one test

DESIGN-002 asks *what am I testing*; DESIGN-005 asks *given the behavior is fixed, how many assertions do I need and what do they cover*. Both can flag the same test for different reasons.

```go
// WRONG: four unrelated behaviors in one test
func TestImportRunsEndToEnd(t *testing.T) {
    home := testutil.SetupFixture(t)
    res, err := importer.Run(home, opts, reader)
    require.NoError(t, err)
    assert.Equal(t, "myproject", res.Project.Name)       // identity
    assert.True(t, res.ConfigRekey)                      // config handling
    assert.Len(t, res.Sessions, 3)                       // sessions imported
    assert.NotEmpty(t, res.Warnings)                     // warnings surface
    assert.FileExists(t, home.LogPath)                   // log side effect
}

// RIGHT: one behavior per test, related assertions grouped
func TestImportRestoresProjectIdentity(t *testing.T) {
    home := testutil.SetupFixture(t)
    res, err := importer.Run(home, opts, reader)
    require.NoError(t, err)
    assert.Equal(t, "myproject", res.Project.Name)
    assert.Equal(t, "/Users/test/Projects/myproject", res.Project.Path)
}

func TestImportRekeysConfigBlock(t *testing.T) { /* ... */ }
func TestImportRestoresAllSessionFiles(t *testing.T) { /* ... */ }
```

---

## Style notes

### Name tests in business language (absorbs CONV-010)

Name after what the code does, not how.

```go
// WRONG
func TestFilepathJoinUsageInSetup(t *testing.T) { }
func TestJSONUnmarshalError(t *testing.T)       { }

// RIGHT
func TestSetupFixtureStagesConfigUnderTempDir(t *testing.T) { }
func TestLoadManifestRejectsMalformedJSON(t *testing.T)     { }
```

### Order tests happy → edge → error (absorbs CONV-005)

Within a file, order functions and table rows happy → variation → config → edge → error. Soft convention; reorder only when adding new tests, not as a cleanup pass.

| Category | Indicators |
|---|---|
| Happy | No edge/error language in the name |
| Variation | `With`, `Using`, `For` modifiers |
| Config | `Mode`, `Option`, `Flag`, `Setting` |
| Edge | `Empty`, `Null`, `Zero`, `Max`, `Min`, `Boundary` |
| Error | `Rejects`, `Fails`, `Invalid`, `Error` |

### Execution time (absorbs ISOLATION-005)

If a test is noticeably slow, check for unintended external calls, oversized fixtures, or unbounded iteration. Tests here should complete in milliseconds; a test that takes seconds is a signal, not a feature.

---

## Related

- `test-behavior-under-test.md` — what the test claims
- `test-independence.md` — environmental independence
- `test-data-and-fixtures.md` — where arrange data comes from
