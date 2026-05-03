---
paths:
  - "**/*_test.go"
---

# Tests: Independence

**CRITICAL**: A test's outcome MUST depend only on its own body and the values it constructs. Package globals, process singletons, the clock, and the PRNG are shared resources that silently break test ordering, `-run` filters, `-shuffle`, and any future `t.Parallel()` adoption.

## Project baseline

- Tests use `t.TempDir` per test for filesystem state. No `t.Parallel()` today.
- No package-level mutable vars in test files. `TestMain` is not used.
- `time.Now()` appears only inside `manifest.Info{Created: time.Now()}` fixture constructors, never as an asserted value.
- No UUID generation in test assertions; session UUIDs in `testdata/` are fixed strings.

These rules are insurance policies against drifting away from that baseline, not flags for current code.

## Decision Test

Before merging a test:

> **"If I ran this test with `-shuffle=on`, `-run=TheOtherTest`, or after `t.Parallel()` adoption, would the outcome change?"**

If yes, the test depends on state outside its own body.

---

## ISOLATION-001 — Shared Mutable State

Go tests MUST NOT share mutable state across test functions or subtests. Four real leak vectors:

1. **Package-level `var`** written by one test, read by another
2. **`TestMain` initialized values** subsequently mutated by tests
3. **Subtest closure captures** where the outer function mutates a variable later subtests read
4. **Global singletons** (`http.DefaultClient`, process env, working directory) mutated without restoration

### Do NOT flag

- Read-only values loaded once in `TestMain` (compiled regex, golden file)
- Values produced by a helper called per test (`t.TempDir`, `testutil.SetupFixture(t)`)
- `sync.Once`-gated lazy init of immutable data
- Per-test locals passed explicitly into subtests

```go
// WRONG: subtest 2 depends on subtest 1
func TestCache(t *testing.T) {
    var cache *Cache
    t.Run("init", func(t *testing.T) {
        cache = NewCache(10)
    })
    t.Run("stores", func(t *testing.T) {
        cache.Put("k", "v")    // panics under -run=TestCache/stores
    })
}

// RIGHT: each subtest owns its state
func TestCacheStoresValue(t *testing.T) {
    cache := NewCache(10)
    cache.Put("k", "v")
    assert.Equal(t, "v", cache.Get("k"))
}

// WRONG: mutating http.DefaultClient without restoration
func TestCustom(t *testing.T) {
    http.DefaultClient = &http.Client{Timeout: time.Second}   // leaks to later tests
}

// RIGHT: restore via t.Cleanup
func TestCustom(t *testing.T) {
    original := http.DefaultClient
    t.Cleanup(func() { http.DefaultClient = original })
    http.DefaultClient = &http.Client{Timeout: time.Second}
}
```

`t.Parallel()` amplifies the risk: two parallel tests racing on the same global produce nondeterministic outcomes. Prefer injection over global mutation.

---

## ISOLATION-002 — Non-Deterministic Inputs

Values that change each run MUST NOT feed into assertions.

### Flag

| Call | Context |
|---|---|
| `time.Now()`, `time.Since(...)` | as an asserted value, or encoded into one |
| `math/rand.Int()`, `math/rand.Read(...)` unseeded | any test use |
| `crypto/rand` readers | any test use |
| `uuid.New()`, `uuid.NewRandom()` | asserted |
| `os.Hostname()` | asserted |
| `os.Getpid()` | asserted |

### Skip

| Call | Context |
|---|---|
| `time.Now()` | only in fixture constructors whose value is never asserted (`manifest.Info{Created: time.Now()}`) |
| `time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)` | fixed values always OK |
| `rand.New(rand.NewSource(seed))` | deterministic seed, repeatable |

```go
// WRONG: asserts against a generated UUID
func TestExportProducesID(t *testing.T) {
    got := export.NewID()
    assert.Regexp(t, `^[0-9a-f-]{36}$`, got)   // tautological; what new bug does this catch?
}

// RIGHT: inject the generator, assert on deterministic output
func TestExportUsesProvidedID(t *testing.T) {
    got := export.NewIDWith(func() string { return "fixed-id" })
    assert.Equal(t, "fixed-id", got)
}

// WRONG: asserts on a wall-clock value
func TestManifestTimestamp(t *testing.T) {
    info := manifest.New()
    assert.InDelta(t, time.Now().Unix(), info.Created.Unix(), 5)   // flaky
}

// RIGHT: fix the clock or compare structure
func TestManifestCreatedIsPopulated(t *testing.T) {
    info := manifest.New()
    assert.False(t, info.Created.IsZero())
}
```

---

## Related

- `test-behavior-under-test.md` — what the test claims to verify
- `test-data-and-fixtures.md` — deterministic arrange data
- `test-shape.md` — structural rules
