package move

// Bindings exposed for black-box tests in move_test.go. Test files are not
// compiled into the production binary, so these aliases stay out of the
// package's public API while giving external tests access to internal
// enumeration helpers for count/containment assertions.
var (
	SnapshotPaths = snapshotPaths
)
