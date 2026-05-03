# Go Documentation Consultation

**CRITICAL**: Training memory of Go library APIs drifts. Consult `go doc <pkg>.<Symbol>` before writing a call you are not 100% certain about.

## Decision Test — run before writing a call to any Go library symbol (stdlib or third-party)

> **"Am I 100% certain of this symbol's signature, error behavior, and edge-case semantics — or am I pattern-matching from training?"**

- Certain (builtins like `len`/`append`) → proceed
- Not certain → `go doc <pkg>.<Symbol>` before writing the call

## Query Shapes — Pick the Narrowest That Answers the Question

| Need | Command | Typical size |
|---|---|---|
| One symbol (func / const / var / method / field) | `go doc <pkg>.<Symbol>` or `go doc <pkg>.<Type>.<Method>` | ~400–600 B |
| Discover what exists in a package | `go doc -short <pkg>` | ~1 KB (one-liners) |
| Package overview | `go doc <pkg>` | ~2–5 KB |
| Full package dump | `go doc -all <pkg>` | **10 KB+ — avoid** |

## Banned Patterns

```
WRONG:   (write the call, then fix compile errors iteratively)
CORRECT: go doc path/filepath.Join   # before the call

WRONG:   go doc -all encoding/json   # 10 KB+ for one function
CORRECT: go doc encoding/json.Marshal

WRONG:   go doc -src sync.Mutex.Lock # reflex, before reading the doc comment
CORRECT: go doc sync.Mutex.Lock      # escalate to -src only if the comment leaves behavior unclear
```

## Red Flags

| Thought | Reality |
|---|---|
| "I've written this a hundred times" | Training memory drifts and Go evolves; a symbol query costs ~500 B |
| "It's just a simple call" | Error return shape, nil handling, and OS-specific quirks live in the godoc |
| "I'll write it and let the compiler catch mistakes" | Error-recovery loops burn more tokens than the lookup would |
| "Let me grab `-all` to be safe" | `-all` is the token-burner path — start narrow and escalate |
| "The identifier name makes the behavior obvious" | `filepath.Clean`, `json.Unmarshal`, and `os.Create` all have non-obvious edge cases |

## When Consultation Is Not Required

- The call compiles and a test exercising it passes — don't retroactively look up working code
- Mechanically repeating an idiom already established elsewhere in the same file
