package tool

import (
	"context"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/manifest"
)

// Tool is the compile-time contract every supported coding tool implements.
// The registry is a literal constructor call in cmd/cc-port; there is no
// init() and no plugin surface.
type Tool interface {
	// Name is the tool's wire identity: archive prefix, manifest <tool
	// name=…> attribute, and generated --<name>-home flag.
	Name() string
	// DisplayName is the human-readable label ("Claude Code", "OpenAI Codex").
	DisplayName() string
	// Categories returns the tool's export categories in canonical,
	// registration-stable order.
	Categories() []Category

	// Detect reports whether default-location state exists on this
	// machine. Absence is not an error.
	Detect() (bool, error)

	// Open resolves the tool's state roots per the tool's own resolution
	// rules and returns a Workspace bound to them. override is the
	// generated --<name>-home flag value; empty means "use the default
	// location."
	Open(override string) (Workspace, error)

	// ImplicitAnchorKeys returns this tool's placeholder key names that its
	// Importer resolves without user input (see Importer.ImplicitAnchors).
	// Static per tool — no project or Workspace required — so NewSet can
	// check cross-tool placeholder-key uniqueness at construction.
	ImplicitAnchorKeys() []string
}

// Workspace is a Tool bound to resolved state roots for one command
// invocation. It composes the four command-facing capabilities so a single
// Open() result serves move, export, import, and stats alike.
type Workspace interface {
	Root() string     // primary state dir, display only
	LockPath() string // cc-port's advisory flock for this tool

	// ActiveWriters gathers liveness evidence. Any non-empty result blocks
	// every mutating command. An evidence source that cannot be read
	// returns an error wrapping ErrNoWitness, which also blocks.
	ActiveWriters() ([]ActiveWriter, error)

	Mover
	Exporter
	Importer
	Auditor
}

// Surface is one named, independently plannable and applicable unit of a
// move. Plan reports how many occurrences it would touch; Apply performs
// the rewrite and registers whatever the Restorer needs to reverse it.
type Surface struct {
	Name  string
	Plan  func(ctx context.Context) (count int, err error)
	Apply func(ctx context.Context, undo *Restorer) (count int, err error)
}

// Mover produces the ordered list of Surfaces a move touches, plus any
// warnings about content a move cannot fully rewrite (e.g. opaque snapshot
// bytes that may still reference the old path).
type Mover interface {
	MoveSurfaces(req MoveRequest) ([]Surface, error)
	ResidualWarnings(req MoveRequest) ([]string, error)
}

// ArchiveEntry names one file an Exporter wrote into the archive, relative
// to the tool's own namespace.
type ArchiveEntry struct {
	ArchivePath string
	Size        int64
}

// ExportResult summarizes one tool's Export call: for each of the tool's
// category names, the archive entries written under it. Skipped names
// entries that exist but could not be exported (e.g. an era of file format
// the tool cannot read) — absence from an archive is always reported here,
// never silent. Warnings carries other human-readable notices the export
// surfaced (e.g. a rules-file path match, or opaque bytes preserved
// verbatim) for the generic command layer to print without knowing the
// tool's internal shape.
type ExportResult struct {
	Categories map[string][]ArchiveEntry
	Skipped    []string
	Warnings   []string
}

// Exporter produces the placeholder set for one project and streams that
// project's selected categories into an archive.Sink.
type Exporter interface {
	Placeholders(project string, selected map[string]bool) ([]manifest.Placeholder, error)
	Export(ctx context.Context, project string, selected map[string]bool, sink *archive.Sink) (ExportResult, error)
}

// Importer stages one tool's share of an import archive and finalizes any
// merge step that plain file promotion cannot express.
type Importer interface {
	// PreflightDirs returns every directory the importer will write under,
	// so the generic import command can resolve symlink parents for all of
	// them before touching the archive.
	PreflightDirs(project string) []string

	// ImplicitAnchors returns this tool's placeholder keys that the import
	// flow resolves itself (e.g. {{PROJECT_PATH}}, {{HOME}}), pre-resolved
	// to their values for this project and machine. Caller-supplied
	// resolutions for these keys are refused.
	ImplicitAnchors(project string) (map[string]string, error)

	// Stage routes one archive entry (already stripped of its tool-prefix
	// path segment) to its destination, streaming it through resolutions.
	// Returns every artifact staged for atomic promotion; an entry whose
	// merge is deferred to Finalize (e.g. a history append) returns an
	// empty slice and no error.
	Stage(ctx context.Context, project string, entry archive.Entry, resolutions map[string]string) ([]archive.Staged, error)

	// Finalize runs merge steps that are not plain file promotion, after
	// every tool's staged files have promoted. Every Finalize append
	// deduplicates against existing content, so import is re-runnable and
	// a re-run never duplicates history or index lines.
	Finalize(ctx context.Context, project string, staged *archive.StagedSet) error
}

// CountSurface is one named path-reference surface stats reports on: how
// many bounded occurrences of the project's path it holds.
type CountSurface struct {
	Name  string
	Count int
}

// SizeCategory is one category's disk footprint: file count and total bytes.
type SizeCategory struct {
	Name  string
	Files int
	Bytes int64
}

// ProjectInfo is one project this tool knows about, for all-projects
// enumeration. Resolved is false when the tool could only report an opaque
// on-disk label (e.g. a lossily-encoded directory name) with no confirmed
// real path — references are out of scope for such a project (they need a
// confirmed real path to scan shared files against), so ProjectInfo carries
// disk usage only, computed as part of the same enumeration pass rather
// than a per-project follow-up call.
type ProjectInfo struct {
	Label    string
	Resolved bool
	Disk     []SizeCategory
	Files    int
	Bytes    int64
}

// Auditor is the read-only, lock-free surface stats renders per tool.
type Auditor interface {
	ReferenceSurfaces(project string) ([]CountSurface, error)
	DiskCategories(project string) ([]SizeCategory, error)
	EnumerateProjects() ([]ProjectInfo, error)
}
