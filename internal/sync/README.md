# internal/sync

## Purpose

Orchestrates `cc-port push` and `cc-port pull`. Owns conflict-detection logic, plan-vs-execute split, and dry-run rendering.

## Public API

- `PushOptions`, `PushPlan`, `PullOptions`, `PullPlan`: option and plan structs. Options carry no `Remote` or `Passphrase` field; cmd opens the pipelines.
- `PriorRead`: bundles a pre-opened `pipeline.Source` plus the encrypted-or-not observation cmd reads off the encrypt stage. nil means no prior.
- `PlanPush(ctx, opts, prior) (*PushPlan, error)`: reads prior remote (when prior is non-nil), populates plan with cross-machine state and encryption metadata.
- `ExecutePush(ctx, opts, plan, output io.Writer) error`: runs the export-side pipeline against the writer cmd opened.
- `PlanPull(ctx, opts, source) (*PullPlan, error)`: reads remote archive's manifest from the pre-opened source, populates plan with placeholder resolutions.
- `ExecutePull(ctx, opts, plan, source) error`: runs importer.Run against the source.
- `(*PushPlan).Render(io.Writer) error`, `(*PullPlan).Render(io.Writer) error`: write the dry-run preview.
- Sentinel errors: `ErrCrossMachineConflict`, `ErrRemoteNotFound`, `ErrPassphraseRequired`, `ErrUnresolvedPlaceholder`. cmd translates raw `remote.ErrNotFound` and `encrypt.ErrPassphraseRequired` into the matching sentinel at pipeline-open time.

## Contracts

### Plan-and-execute split

Used by `cmd/cc-port` push and pull.

#### Handled

- Plan reads remote state (or notes its absence) and populates the plan struct without mutating remote or local data.
- Execute commits the upload (push) or import (pull). Failures during Execute leave partial state on the remote (push) or local (pull) per gocloud and importer semantics; sync surfaces the error.
- The cross-machine refusal triggers when `plan.PriorPushedBy != "" && != plan.SelfPusher`. cmd layer enforces; sync sets the field.
- Conflict-detection metadata (`SyncPushedBy`, `SyncPushedAt`, encrypted flag, size) lives inside `metadata.xml` inside the archive. Bucket-level custom metadata is not used; the archive is the single source of truth.
- `PushPlan.Render` writes the dry-run preview ending after the cross-machine warning (or its absence). The trailing `(no changes; pass --apply to commit)` line is the cmd layer's responsibility, not Render's. The Prior remote section omits when `PriorPushedBy` is empty; the cross-machine warning fires only when `CrossMachine` is true.
- `PullPlan.Render` writes the pull dry-run preview. The Required resolutions block lists every declared placeholder, marking each as `(resolved)` or `MISSING`; the trailing `! N placeholder unresolved` warning fires only when `len(UnresolvedPlaceholders) > 0`. Categories print via `categoriesSummary`, which collapses to `all` when every `manifest.AllCategories` entry is set.

#### Refused

- When the prior remote is encrypted and the passphrase is missing, cmd's `openPriorRead` returns `ErrPassphraseRequired` (or returns nil if `--force` is set). PlanPush itself sees only the dispatched outcome.
- When the archive is missing on the remote, cmd's `openArchiveSource` returns `ErrRemoteNotFound`. PlanPull is not called.
- `openArchiveSource` translates encrypted-no-passphrase to `ErrPassphraseRequired`. The `encrypt.ErrUnencryptedInput` sentinel for plaintext-with-passphrase propagates wrapped without translation.
- ExecutePull (via `importer.Run`'s `CheckConflict`) refuses when the encoded project directory already exists at TargetPath.

#### Not covered

- Multi-archive push/pull. Each invocation handles one archive.
- Atomic push commit. The bucket writer commits on Close; mid-upload failures leave no archive on the remote, but a successful commit followed by a network failure on close is gocloud-driver-specific.
- Pull `--force` overwrite of an existing local project. Operators delete the local project first.

## Tests

`sync_test.go` covers `selfPusher` (hyphen-separated host-user on a configured machine, refuse-or-platform-fall-back from an empty `$USER`), the push-side Plan and Execute paths (no-prior, same-self, cross-machine prior, round-trip with sync fields), the pull-side Plan paths (declared-placeholder discovery, resolution coverage by `--resolution` and by sender Resolve), a push-pull round-trip via `mem://`, and the sentinel errors. Pipeline-open dispatch tests (remote-not-found, encrypted-no-passphrase, plaintext-with-passphrase) live in `cmd/cc-port/pushcmd_test.go` and `cmd/cc-port/pullcmd_test.go` because cmd owns the dispatch after the refactor. `render_test.go` covers Render output via substring assertions on push (no-prior plaintext, encrypted with prior and cross-machine), pull (with unresolved placeholders, encrypted clean), plus `humanizeBytes` boundary cases. Mem-backed remote (gocloud `mem://`) for unit tests; integration round-trips against `file://` and optionally S3.
