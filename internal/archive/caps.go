package archive

// Caps bounds decompression during an archive read: no single entry may
// decode past MaxEntryBytes, and the running total across every entry
// observed in one read may not pass MaxAggregateBytes. Per-entry caps alone
// do not stop a crafted archive with many entries at just-under-the-limit
// size from exhausting memory and disk in aggregate.
type Caps struct {
	MaxEntryBytes     int64
	MaxAggregateBytes int64
}

// defaultCaps mirror the historical internal/importer limits: session
// transcripts can legitimately be large, so 512 MiB per entry is two orders
// of magnitude above any real transcript while still rejecting every known
// zip-bomb payload; 4 GiB aggregate stops a many-entry archive from
// exhausting memory and disk before any single entry trips its own cap.
var defaultCaps = Caps{MaxEntryBytes: 512 << 20, MaxAggregateBytes: 4 << 30}

// activeCaps is the cap set every reader in this package consults. Package
// state, not a parameter, because the caps are a process-wide safety
// backstop rather than a per-call tuning knob; SetCaps is the sanctioned
// override for tests that need to exercise rejection without materializing
// production-scale fixtures.
var activeCaps = defaultCaps

// SetCaps overrides the active decompression caps and returns a function
// that restores the previous value. Exported for cross-package tests
// (internal/importer's cap-rejection tests, in particular) that need to
// lower the limits cheaply; production code never calls it.
func SetCaps(caps Caps) (restore func()) {
	previous := activeCaps
	activeCaps = caps
	return func() { activeCaps = previous }
}
