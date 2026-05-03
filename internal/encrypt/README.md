# internal/encrypt

## Purpose

Streaming wrappers around `filippo.io/age` for cc-port archives plus self-skipping pipeline stage types. Symmetric (passphrase) mode only. Key-recipient mode is intentionally out of scope. Higher-level callers like `cmd/cc-port` and `internal/sync` consume this package, never the reverse.

## Public API

- `EncryptingWriter(dst, passphrase) (io.WriteCloser, error)`: encrypts plaintext into `dst`. Caller must `Close` to flush age's trailer.
- `DecryptingReader(src, passphrase) (io.Reader, error)`: yields plaintext from `src`. Auth failures surface on `Read` as `ErrPassphrase`.
- `IsEncrypted(header) bool`: reports whether `header` begins with the age v1 binary-format prefix. Buffers shorter than `MinPeekLen` return false.
- `MinPeekLen`: minimum buffer length `IsEncrypted` accepts.
- `WriterStage{Pass}`: `pipeline.WriterStage`. Encrypts plaintext into `downstream` when `Pass` is non-empty (returns the age writer as both writer and closer); returns `downstream` unchanged with a nil closer when `Pass` is empty (passthrough). The cmd layer always includes this stage in its writer pipeline.
- `ReaderStage{Pass, Mode}`: `pipeline.ReaderStage` that owns the encrypted-vs-plaintext × pass-vs-no-pass dispatch matrix. Peeks the upstream's first 32 bytes via `bufio.Reader.Peek` and either wraps the buffered upstream in `DecryptingReader` (encrypted-with-pass branch; returned `View.Reader` is the plaintext stream, no closer), passes through with the bufio-wrapped Reader and propagated `ReaderAt`/`Size` (plaintext branches), or returns the matrix's sentinel error. No tempfile is created in any branch; consumers that need `io.ReaderAt` compose `pipeline.MaterializeStage` downstream. Contributes `Meta.WasEncrypted = true` on the encrypted-with-pass branch and `false` on plaintext branches. No contribution on error paths.
- `Mode`: enum with `Strict` and `Permissive`. `Strict` refuses plaintext-with-pass with `ErrUnencryptedInput`. `Permissive` accepts plaintext-with-pass silently. Cmd-layer read paths (`import`, `import manifest`, sync `pull`) compose with `Strict`. Sync `push` prior-read composes with `Permissive`. `Strict` is the zero value, so an unset `Mode` field means `Strict`.
- `ErrPassphrase`: wrong passphrase, tamper, or truncation. age conflates these by design.
- `ErrPassphraseRequired`: encrypted input + empty passphrase.
- `ErrUnencryptedInput`: plaintext input + non-empty passphrase under `Strict`.

## Contracts

### Passphrase mode only

#### Handled

`EncryptingWriter` and `DecryptingReader` take a non-empty `passphrase` and use age's scrypt recipient/identity as the only key derivation path. `WriterStage` accepts an empty `Pass` and passes through without invoking age. `ReaderStage` accepts an empty `Pass` and consults `Mode` plus the magic-byte peek to decide between passthrough and `ErrPassphraseRequired`.

#### Refused

An empty `passphrase` to `EncryptingWriter` or `DecryptingReader` is refused with an error. Stage callers route through `WriterStage` / `ReaderStage` instead.

#### Not covered

Asymmetric (X25519) recipients. Adding them would change the public shape, so do it as a separate spec, not as a flag.

### Auth-failure unification

#### Handled

Wrong passphrase, header corruption, body tamper, and truncation all surface as `ErrPassphrase`. The wrapping `errors.Join` chain preserves age's error for diagnostic logging without inviting branching.

#### Refused

Type-switching on age's internal error types. Callers see `ErrPassphrase` only.

#### Not covered

Distinguishing wrong-passphrase from tampered-file at the API surface. age conflates the cases on purpose, and this package follows.

### Stage dispatch

`ReaderStage.Open` owns the matrix. `Mode = Strict` is the read-side cmd default. `Mode = Permissive` exists for sync `push` prior-read where the operator's passphrase targets the new archive being written, not the prior remote one.

#### Handled

| Encrypted | Pass | Strict | Permissive |
|---|---|---|---|
| yes | non-empty | decrypt streaming via `DecryptingReader` | decrypt streaming via `DecryptingReader` |
| yes | empty     | `ErrPassphraseRequired`                  | `ErrPassphraseRequired`                  |
| no  | non-empty | `ErrUnencryptedInput`                    | passthrough                              |
| no  | empty     | passthrough                              | passthrough                              |

Dispatch is streaming. `bufio.Peek` reads the magic-byte window without consuming. The encrypted-with-pass branch wraps the buffered reader in `DecryptingReader`; plaintext branches return the bufio-wrapped reader and propagate any `ReaderAt`/`Size` the upstream exposed. No tempfile is created in any branch. Mismatch cells (`ErrPassphraseRequired`, `ErrUnencryptedInput`) and decrypt-failure paths return `(pipeline.View{}, pipeline.Meta{}, nil, sentinel)`. `pipeline.RunReader` closes any closers accumulated so far. The stage contributes `Meta.WasEncrypted = true` on the encrypted-with-pass branch and `false` on plaintext branches; no contribution on error paths.

#### Refused

Custom dispatch matrices in cmd-layer callers. The cmd layer sets `Pass` and `Mode` and lets the stage decide. It never peeks bytes itself.

#### Not covered

Bytes that match the age magic prefix but are not actual age archives. The decrypt drain fails with `ErrPassphrase`, so callers see the same unified error.

## Tests

`encrypt_test.go` covers round-trip on sizes 0/1/1 MiB, wrong-passphrase rejection, tamper rejection, truncation rejection, and magic-byte detection. `stages_test.go` covers `WriterStage` round-trip via `RunWriter`, the empty-Pass passthrough, the streaming `ReaderStage` matrix under `Strict` and the differing cell under `Permissive`, the encrypted-with-pass branch returning a streaming View, the plaintext branches propagating `ReaderAt`/`Size`, the runner-closes-upstream-once-on-error contract, and stage `Name()` reporting. The runner-level cascade and idempotency contracts live in `internal/pipeline/pipeline_test.go`. Tempfile-lifecycle tests live in `internal/pipeline/materialize_test.go`.

Coverage target: 90% or above.
