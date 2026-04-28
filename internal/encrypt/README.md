# internal/encrypt

## Purpose

Streaming wrappers around `filippo.io/age` for cc-port archives plus self-skipping pipeline stage types. Symmetric (passphrase) mode only. Key-recipient mode is intentionally out of scope. Higher-level callers like `cmd/cc-port` and `internal/sync` consume this package, never the reverse.

## Public API

- `EncryptingWriter(dst, passphrase) (io.WriteCloser, error)`: encrypts plaintext into `dst`. Caller must `Close` to flush age's trailer.
- `DecryptingReader(src, passphrase) (io.Reader, error)`: yields plaintext from `src`. Auth failures surface on `Read` as `ErrPassphrase`.
- `IsEncrypted(header) bool`: reports whether `header` begins with the age v1 binary-format prefix. Buffers shorter than `MinPeekLen` return false.
- `MinPeekLen`: minimum buffer length `IsEncrypted` accepts.
- `WriterStage{Pass}`: `pipeline.WriterStage`. Encrypts plaintext into `downstream` when `Pass` is non-empty and returns a passthrough writer when `Pass` is empty. Both paths cascade `Close` to `downstream` so the leaf sink (typically `file.Sink`'s `*os.File`) closes when the caller closes the outermost writer. The cmd layer always includes this stage in its writer pipeline.
- `ReaderStage{Pass, Mode}`: `pipeline.ReaderStage` that owns the encrypted-vs-plaintext × pass-vs-no-pass dispatch matrix. Peeks the upstream's first 32 bytes and decrypts, passes through, or returns a sentinel error per the matrix below. `Mode` selects between `Strict` (default) and `Permissive`. Decryption materializes plaintext into a `0600` tempfile, and `Source.Close` removes it and chains to upstream.
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
| yes | non-empty | decrypt to 0600 tempfile | decrypt to 0600 tempfile |
| yes | empty     | `ErrPassphraseRequired`  | `ErrPassphraseRequired`  |
| no  | non-empty | `ErrUnencryptedInput`    | passthrough              |
| no  | empty     | passthrough              | passthrough              |

Mismatch cells (`ErrPassphraseRequired`, `ErrUnencryptedInput`) and decrypt-failure paths return the sentinel without closing upstream. `pipeline.RunReader` closes the upstream Source on stage error. Tempfile cleanup (close + remove) on the decrypt-failure path stays inside the stage because the tempfile is the stage's own resource.

#### Refused

Custom dispatch matrices in cmd-layer callers. The cmd layer sets `Pass` and `Mode` and lets the stage decide. It never peeks bytes itself.

#### Not covered

Bytes that match the age magic prefix but are not actual age archives. The decrypt drain fails with `ErrPassphrase`, so callers see the same unified error.

### Tempfile lifecycle

#### Handled

`ReaderStage.Open` materializes plaintext into `os.TempDir()` with mode `0600`. The returned `Source.Close` removes the tempfile and chains to upstream, and a second call returns nil.

#### Refused

Custom tempdir paths. Use `os.TempDir()` (overridable via `TMPDIR`).

#### Not covered

SIGKILL between `ReaderStage.Open` and `Source.Close` leaves a tempfile in `os.TempDir()`. The OS tempdir cleanup handles eventually. Decrypt size is bounded by the ciphertext byte count because the age plaintext-to-ciphertext ratio is roughly 1:1. No separate cap is enforced. There is no decompression-bomb amplification path to defend against.

## Tests

`encrypt_test.go` covers round-trip on sizes 0/1/1 MiB, wrong-passphrase rejection, tamper rejection, truncation rejection, and magic-byte detection. `stages_test.go` covers `WriterStage` round-trip via `RunWriter`, the empty-Pass passthrough, the full `ReaderStage` matrix under `Strict` and the differing cell under `Permissive`, tempfile cleanup with idempotent Close, mode `0600` verification, decrypt close cascade, and stage `Name()` reporting.

Coverage target: 90% or above.
