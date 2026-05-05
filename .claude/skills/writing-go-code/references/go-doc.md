# go doc Consultation (reference)

Loaded from `writing-go-code` SKILL.md when the workflow needs the deep technical detail behind the *Confirm the API call* step.

## Query shapes — pick the narrowest that answers the question

| Need | Command | Typical size |
|---|---|---|
| One symbol (func / const / var / method / field) | `go doc <pkg>.<Symbol>` or `go doc <pkg>.<Type>.<Method>` | ~400–600 B |
| Discover what exists in a package | `go doc -short <pkg>` | ~1 KB (one-liners) |
| Package overview | `go doc <pkg>` | ~2–5 KB |
| Full package dump | `go doc -all <pkg>` | **10 KB+ — avoid** |

## Banned patterns

```
WRONG:   (write the call, then fix compile errors iteratively)
CORRECT: go doc path/filepath.Join   # before the call

WRONG:   go doc -all encoding/json   # 10 KB+ for one function
CORRECT: go doc encoding/json.Marshal

WRONG:   go doc -src sync.Mutex.Lock # reflex, before reading the doc comment
CORRECT: go doc sync.Mutex.Lock      # escalate to -src only if the comment leaves behavior unclear
```

## When consultation is not required

- The call compiles and a test exercising it passes — don't retroactively look up working code
- Mechanically repeating an idiom already established elsewhere in the same file
- Builtins (`len`, `append`, `cap`, `make`, `delete`, `copy`)
