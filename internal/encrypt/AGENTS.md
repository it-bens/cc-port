# internal/encrypt -- agent notes

## Before editing

- Refuse empty passphrases at the primitive entry points (`EncryptingWriter`, `DecryptingReader`); the stages handle empty `Pass` themselves (README §Passphrase mode only).
- Never type-switch on age's internal errors; surface `ErrPassphrase` only (README §Auth-failure unification).
- `ReaderStage` is the single owner of the encrypted-vs-plaintext × pass-vs-no-pass dispatch. Cmd-layer callers (`cmd/cc-port import`, `cmd/cc-port import manifest`, `cmd/cc-port pull`, sync push prior-read) compose the stage with the right `Mode` and let it decide; do not peek bytes or branch in the cmd layer (README §Stage dispatch).
- `Mode = Strict` is the default for read-side import paths; `Mode = Permissive` is reserved for sync push prior-read. Never use Permissive on import paths (README §Stage dispatch).
- `WriterStage` self-skips on empty `Pass` via a passthrough writer; the cmd layer always includes the stage. Never make inclusion conditional in callers (README §Passphrase mode only).
- `ReaderStage` materializes plaintext into a `0600` tempfile in `os.TempDir()`. Never widen perms or relocate (README §Tempfile lifecycle).
- `Source.Close` MUST stay idempotent and chain to upstream.Close. Cleanup failures return; never panic (README §Tempfile lifecycle).
- Add asymmetric (X25519) support as a separate spec, never as a flag on the existing functions (README §Passphrase mode only).

## Navigation

- Primitives: `encrypt.go` (`EncryptingWriter`, `DecryptingReader`, `IsEncrypted`, `MinPeekLen`, `ErrPassphrase`).
- Stages: `stages.go` (`WriterStage`, `ReaderStage`, `Mode`, `ErrPassphraseRequired`, `ErrUnencryptedInput`).
- Tests: `encrypt_test.go`, `stages_test.go`.
