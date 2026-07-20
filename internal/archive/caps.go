package archive

// Caps bounds decompression during an archive read: no single entry may
// decode past MaxEntryBytes, and the running total across every entry
// observed in one read may not pass MaxAggregateBytes. Per-entry caps alone
// do not stop a crafted archive with many entries at just-under-the-limit
// size from exhausting memory and disk in aggregate. MaxEntries bounds a
// third axis neither byte cap reaches: an archive of hundreds of thousands
// of zero-byte entries, each still allocating a RawEntry, a Staged record,
// and a temp inode, at a total archive size far under either byte cap. Zero
// disables the check.
type Caps struct {
	MaxEntryBytes     int64
	MaxAggregateBytes int64
	MaxEntries        int
}

// DefaultCaps returns the production decompression caps: 512 MiB per entry
// (two orders of magnitude above any real transcript, while still rejecting
// every known zip-bomb payload), 4 GiB aggregate across one archive read
// (stops a many-entry archive from exhausting memory and disk before any
// single entry trips its own cap), and 200,000 entries (two orders of
// magnitude above any real multi-project archive, turning a
// 500k-to-1M-zero-byte-entry archive into a bounded refusal).
func DefaultCaps() Caps {
	return Caps{MaxEntryBytes: 512 << 20, MaxAggregateBytes: 4 << 30, MaxEntries: 200_000}
}
