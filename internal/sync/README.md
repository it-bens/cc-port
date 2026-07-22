# internal/sync

## Purpose

Orchestrates `cc-port push` and `cc-port pull` across every selected tool.
Owns conflict-detection logic, plan-vs-execute split, and plan-summary rendering.
This package has no tool-specific knowledge; it drives `internal/export` and
`internal/importer`, both of which are themselves generic over `tool.Target`.

## Public API

- `PushOptions`, `PushPlan`, `PullOptions`, `PullPlan`: option and plan
  structs. Options carry no `Remote` or `Passphrase` field; cmd opens the
  pipelines. `PushOptions.Selected` and `PushOptions.Placeholders` are keyed
  by tool name, matching `export.Options`.
- `PriorRead`: bundles a pre-opened `pipeline.Source` plus the
  encrypted-or-not observation cmd reads off `Source.Meta.WasEncrypted`. nil
  means no prior.
- `PlanPush(ctx, opts, prior) (*PushPlan, error)`: reads prior remote (when
  prior is non-nil), populates plan with cross-machine state and encryption
  metadata.
- `ExecutePush(ctx, opts, plan, output io.Writer) (*export.Result, error)`: runs
  `export.Run(ctx, opts.Targets, ...)` against the writer cmd opened.
- `PlanPull(ctx, opts, source) (*PullPlan, error)`: reads the remote
  archive's manifest from the pre-opened source. Before rendering, it hard
  refuses through the import preflight gates: `VerifyManifestTools`,
  `VerifyEntryTools`, and per-block `PreflightBlock` category, anchor, and
  resolution validation.
- `ExecutePull(ctx, opts, plan, source) (*importer.Result, error)`: runs
  `importer.Run(ctx, opts.AllTools, opts.Targets, ...)` against the source.
- `(*PushPlan).Render(io.Writer, apply bool) error`,
  `(*PullPlan).Render(io.Writer, apply bool) error`: write the plan summary;
  `apply` selects the header ([dry-run] when false, the bare command line when
  true).
- Sentinel errors: `ErrCrossMachineConflict`, `ErrRemoteNotFound`,
  `ErrPassphraseRequired`, `ErrUnresolvedPlaceholder`. cmd translates raw
  `remote.ErrNotFound` and `encrypt.ErrPassphraseRequired` into the matching
  sentinel at pipeline-open time.

## Contracts

### Plan-and-execute split

Used by `cmd/cc-port` push and pull.

#### Handled

- Plan reads remote state (or notes its absence) and populates the plan
  struct without mutating remote or local data.
- Execute commits the upload (push) or import (pull). Failures during
  Execute leave partial state on the remote (push) or local (pull) per
  gocloud and importer semantics; sync surfaces the error.
- The cross-machine refusal triggers when
  `plan.PriorPushedBy != "" && != plan.SelfPusher`. cmd layer enforces; sync
  sets the field.
- `--force` overrides only the cmd layer's cross-machine-conflict refusal and
  the encrypted-prior passphrase requirement in `openPriorRead`. It never
  suppresses a failed `PlanPush` self-identity derivation.
- Conflict-detection metadata (`SyncPushedBy`, `SyncPushedAt`, encrypted
  flag, size) lives inside `metadata.xml` inside the archive. Bucket-level
  custom metadata is not used; the archive is the single source of truth.
- `PushPlan.Render` writes the plan summary ending after the
  cross-machine warning (or its absence); `apply` selects the header
  ([dry-run] when false, the bare command line when true). The trailing
  `(no changes; pass --apply to commit)` line (dry-run) and the `Pushed:`
  confirmation (apply) are the cmd layer's responsibility, not Render's. The
  Prior remote section omits when
  `PriorPushedBy` is empty; the cross-machine warning fires only when
  `CrossMachine` is true. Categories print via `selectionSummary`, which
  joins each tool's included categories as `"<tool>: cat1, cat2"` clauses in
  alphabetical tool-name order.
- `PullPlan.Render` writes the pull plan summary (header per `apply`, as
  above): the remote's pushed-by,
  pushed-at, size, and tool list, then one "Required resolutions" block per
  tool with declared placeholders, marking each as `(resolved)` or
  `MISSING`. The trailing `! N placeholder unresolved` warning fires only
  when at least one tool has an unresolved key.

#### Refused

- When the prior remote is encrypted and the passphrase is missing, cmd's
  `openPriorRead` returns `ErrPassphraseRequired` (or returns nil if
  `--force` is set). `PlanPush` itself sees only the dispatched outcome.
- `PlanPush` refuses a failed self-identity derivation even when `--force` is
  set, so `ExecutePush` cannot write an empty `SyncPushedBy` value.
- When the archive is missing on the remote, cmd's `openArchiveSource`
  returns `ErrRemoteNotFound`. `PlanPull` is not called.
- `openArchiveSource` translates encrypted-no-passphrase to
  `ErrPassphraseRequired`. The `encrypt.ErrUnencryptedInput` sentinel for
  plaintext-with-passphrase propagates wrapped without translation.
- `PlanPull` (dry-run) and `ExecutePull` refuse when an archive manifest
  names an unregistered tool.

#### Not covered

- Multi-archive push/pull. Each invocation handles one archive.
- Atomic push commit. The bucket writer commits on Close; mid-upload
  failures leave no archive on the remote, but a successful commit followed
  by a network failure on close is gocloud-driver-specific.
- Reverting a completed pull. Pull has no `--force` flag: its `ExecutePull`
  is the generic `importer.Run`, which stages and promotes over any existing
  destination directly (see `internal/importer/README.md` §Atomic staging).
  A successful pull's destination reflects the archive; recovering
  pre-pull local content is the operator's own backup, not a cc-port feature.

## Tests

`sync_test.go` covers `selfPusher` (hyphen-separated host-user on a
configured machine, refuse-or-platform-fall-back from an empty `$USER`), the
push-side Plan and Execute paths (no-prior, same-self, cross-machine prior,
round-trip with sync fields, export-warning propagation), the pull-side Plan paths (declared-placeholder
discovery across multiple tools, resolution coverage by sender `Resolve` and
by `--from-manifest`), a push-pull round-trip via `file://`, and the
sentinel errors. Pipeline-open dispatch tests (remote-not-found,
encrypted-no-passphrase, plaintext-with-passphrase) live in
`cmd/cc-port/pushcmd_test.go` and `cmd/cc-port/pullcmd_test.go` because cmd
owns the dispatch. `render_test.go` covers Render output via substring
assertions on push (no-prior plaintext, encrypted with prior and
cross-machine) and pull (with unresolved placeholders, encrypted clean),
plus the apply-run header drop and `humanizeBytes` boundary cases. File-backed remote (`file://` +
`t.TempDir()`) for unit tests; integration round-trips also use `file://`
and optionally S3.
