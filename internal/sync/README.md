# internal/sync

## Purpose

Orchestrates `cc-port push` and `cc-port pull`. Owns conflict-detection logic, plan-vs-execute split, and dry-run rendering. Composes around the pipeline runner in `internal/pipeline` plus stages from `internal/remote`, `internal/encrypt`, and `internal/file`.

## Public API

- `PushOptions`, `PushPlan`, `PullOptions`, `PullPlan`: option and plan structs.
- `PlanPush(ctx, opts) (*PushPlan, error)`: reads prior remote (if any), populates plan with cross-machine state and encryption metadata.
- `ExecutePush(ctx, opts, plan) error`: runs the export-side pipeline and uploads to the remote.
- `PlanPull(ctx, opts) (*PullPlan, error)`: reads remote archive's manifest, populates plan with placeholder resolutions. Stub in this task; body lands in Task 7.
- `ExecutePull(ctx, opts, plan) error`: runs the import-side pipeline and applies the archive locally. Stub in this task; body lands in Task 7.
- Sentinel errors: `ErrCrossMachineConflict`, `ErrRemoteNotFound`, `ErrPassphraseRequired`, `ErrUnresolvedPlaceholder`.

## Contracts

### Plan-and-execute split

Used by `cmd/cc-port` push and pull.

#### Handled

- Plan reads remote state (or notes its absence) and populates the plan struct without mutating remote or local data.
- Execute commits the upload (push) or import (pull). Failures during Execute leave partial state on the remote (push) or local (pull) per gocloud and importer semantics; sync surfaces the error.
- The cross-machine refusal triggers when `plan.PriorPushedBy != "" && != plan.SelfPusher`. cmd layer enforces; sync sets the field.
- Conflict-detection metadata (`SyncPushedBy`, `SyncPushedAt`, encrypted flag, size) lives inside `metadata.xml` inside the archive. Bucket-level custom metadata is not used; the archive is the single source of truth.

#### Refused

- PlanPush returns `ErrPassphraseRequired` when the prior remote is encrypted and no passphrase is provided. `--force` overrides this at the cmd layer.
- PlanPull returns `ErrRemoteNotFound` when the archive is missing on the remote.
- PlanPull returns `ErrPassphraseRequired` for an encrypted remote without a passphrase, or `encrypt.ErrUnencryptedInput` for a plaintext remote with a passphrase.
- ExecutePull (via `importer.Run`'s `CheckConflict`) refuses when the encoded project directory already exists at TargetPath.

#### Not covered

- Multi-archive push/pull. Each invocation handles one archive.
- Atomic push commit. The bucket writer commits on Close; mid-upload failures leave no archive on the remote, but a successful commit followed by a network failure on close is gocloud-driver-specific.
- Pull `--force` overwrite of an existing local project. Operators delete the local project first.

## Tests

`sync_test.go` covers `selfPusher` (hyphen-separated host-user on a configured machine, refuse-or-platform-fall-back from an empty `$USER`), the push-side Plan and Execute paths (no-prior, same-self, cross-machine prior, encrypted-prior-no-passphrase refusal, round-trip with sync fields), the still-stub `PlanPull`, and the sentinel errors. `PlanPull`/`ExecutePull` coverage lands with Task 7. Mem-backed remote (gocloud `mem://`) for unit tests; integration round-trips against `file://` and optionally S3.
