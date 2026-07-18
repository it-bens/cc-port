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

// DefaultCaps returns the production decompression caps: 512 MiB per entry
// (two orders of magnitude above any real transcript, while still rejecting
// every known zip-bomb payload) and 4 GiB aggregate across one archive read
// (stops a many-entry archive from exhausting memory and disk before any
// single entry trips its own cap).
func DefaultCaps() Caps {
	return Caps{MaxEntryBytes: 512 << 20, MaxAggregateBytes: 4 << 30}
}
