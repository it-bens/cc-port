# Behavior Under Test (reference)

Loaded from `writing-tests` SKILL.md when the workflow needs the deep technical detail behind the *Identify the single behavior* step or the *Final quality gate*.

## UNIT-001 — Behavior, not implementation, trivial, or internal

Tests verify observable behavior of the exported API.

### Do test

- Return values and errors
- Observable state changes (files written, entries persisted, channels closed)
- Computed or derived values
- Validation logic

### Do NOT test

- Calls into dependencies. The return value already verifies the call happened.
- Unexported helpers via a `package foo` internal test when the exported API covers the behavior.
- Internal call order or algorithmic decomposition.
- Logic-free constructors (only `self.field = param`).
- Exported fields via round-trip (`s.Field = x; assert.Equal(x, s.Field)`).
- Accessor methods that return a field directly.
- Setter methods that only assign a field.
- Pure delegation: method forwards to a dependency without transforming input or output.

### Go-specific carve-outs

- `require.NoError(t, err)` before the act step is a **precondition**, not a trivial assertion on the act's return.
- `assert.IsType` or `assert.Implements` on a function with one concrete return type is trivially true. Delete it or replace with a behavior assertion.
- `package foo` internal tests are allowed when the invariant genuinely cannot be observed externally (file-history contract, lock handoffs). Prefer `package foo_test` when the exported surface suffices.

### When accessor / constructor tests ARE valid

- Constructor validates input and returns an error on violation
- Constructor transforms input (normalizes paths, computes a derived field)
- Accessor computes a derived value
- Setter has side effects or validation
- Delegation transforms input or output, or includes conditional logic

### Worked examples

```go
// WRONG: exported-field round trip
func TestPlanSetsFields(t *testing.T) {
    p := move.Plan{OldProjectDir: "/a", NewProjectDir: "/b"}
    assert.Equal(t, "/a", p.OldProjectDir)
    assert.Equal(t, "/b", p.NewProjectDir)
}

// WRONG: pure delegation
func TestServiceGetProducts(t *testing.T) {
    svc := &Service{repo: fakeRepo{items: xs}}
    got, err := svc.GetProducts(ctx)   // just forwards to repo.FindAll
    require.NoError(t, err)
    assert.Equal(t, xs, got)
}

// RIGHT: constructor with validation
func TestNewHomeRejectsRelativePath(t *testing.T) {
    _, err := claude.NewHome("./relative")
    require.Error(t, err)
}

// RIGHT: derived behavior
func TestEncodePathReplacesSlashesAndDots(t *testing.T) {
    got := claude.EncodePath("/tmp/cc-port.test/foo")
    assert.Equal(t, "-tmp-cc-port-test-foo", got)
}
```

## DESIGN-002 — Single behavior per test

Each test function exercises exactly one behavior. Violation signs:

- Name contains *And*
- Comment banners splitting the body into phases (`// create`, `// update`, `// delete`)
- Multiple unrelated assertions after distinct act steps

```go
// WRONG: TestProjectLifecycle exercises three behaviors
func TestProjectLifecycle(t *testing.T) {
    home := testutil.SetupFixture(t)

    // move
    require.NoError(t, move.Apply(home, moveOpts))
    assert.DirExists(t, home.ProjectDir(newPath))

    // export
    var buf bytes.Buffer
    require.NoError(t, export.Run(home, exportOpts, &buf))
    assert.NotEmpty(t, buf.Bytes())

    // import
    require.NoError(t, importer.Run(home, importOpts, bytes.NewReader(buf.Bytes())))
    assert.FileExists(t, manifestPath)
}
```

Split into `TestMoveApplyRelocatesProjectDir`, `TestExportRunProducesArchive`, `TestImportRunRestoresFromArchive`. Each test fails for one reason.

## DESIGN-004 — Test redundancy

Every test case (table row) and every top-level test covers a unique code path, boundary value, or regression. Key on *why* the case exists, not on *what* the input looks like.

A case earns its slot if at least one holds:

- **Unique code path**: triggers a branch no other case triggers
- **Boundary value**: exact threshold where behavior changes
- **Regression**: prevents a specific bug from returning; cite the issue or commit

If none hold, merge into an existing test with extra assertions, or delete.

### Preservation check

Before flagging a case as redundant, scan for preservation indicators:

| Indicator | Pattern |
|---|---|
| Regression marker in name | `Regression`, `Bug`, `Issue`, `#\d+` |
| Issue tracker reference in name | `GH-`, `PR-`, commit SHA |
| Comment at site | `// regression for #123`, `// prevents the ...` |
| Table row key | `"unicode fix (#123)"` |

If present, keep the case and add an explanatory comment. If absent, consolidate.

```go
// WRONG: all three rows exercise the a > b branch
cases := []struct {
    name string
    a, b int
}{
    {"a much greater than b", 100, 1},
    {"a greater than b", 10, 1},
    {"a slightly greater than b", 2, 1},
}

// RIGHT: each row justifies itself by the branch it triggers
cases := []struct {
    name string
    a, b int
}{
    {"greater refreshes", 10, 1},
    {"equal uses cache", 1, 1},
    {"less returns stale", 1, 10},
}
```

## DESIGN-010 — Guard clause isolation

When a test targets one early-return in a function with multiple sequential guards, the arrange section satisfies every other guard so the tested guard is the only possible exit. Otherwise the test may pass because a different guard fired first; the outcome looks right and the test proves nothing.

1. Read the public function the test exercises.
2. Enumerate its sequential guard clauses.
3. If the function has 2+ guards and the test targets one, verify the arrange satisfies all others.
4. If another guard would short-circuit with the current arrange, flag.

Does not apply when: function has one guard; test explicitly covers the all-preconditions-absent path; guards produce distinguishable outcomes that the assertion discriminates.

```go
// move.Apply has guards:
//   g1: if opts.OldPath == "" { return ErrOldPathRequired }
//   g2: if opts.NewPath == "" { return ErrNewPathRequired }
//   g3: if projectDir missing { return ErrProjectNotFound }

// WRONG: targets g3 but g1 fires first
func TestApplyMissingProject(t *testing.T) {
    home := testutil.SetupFixture(t)
    err := move.Apply(home, move.Options{NewPath: "/elsewhere"})  // OldPath empty
    require.ErrorIs(t, err, move.ErrProjectNotFound)               // never reached
}

// RIGHT: g1 and g2 satisfied, only g3 can fire
func TestApplyMissingProject(t *testing.T) {
    home := testutil.SetupFixture(t)
    err := move.Apply(home, move.Options{
        OldPath: "/Users/test/Projects/doesnotexist",
        NewPath: "/Users/test/Projects/newhome",
    })
    require.ErrorIs(t, err, move.ErrProjectNotFound)
}
```
