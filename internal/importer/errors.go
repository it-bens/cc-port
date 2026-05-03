// Package importer error values. Tests assert against these via
// errors.Is / errors.As rather than substring-matching Error() output.
package importer

import "errors"

// ErrEncodedDirCollision is returned by CheckConflict when the encoded
// project directory already exists. The wrapping error message names the
// path; callers test branch identity with errors.Is.
var ErrEncodedDirCollision = errors.New("encoded project directory already exists")
