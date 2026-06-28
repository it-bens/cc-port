package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// errOutputExists is returned by requireOutputAbsent when the --output path
// already exists. Same-package callers and tests discriminate via errors.Is.
var errOutputExists = errors.New("output file already exists")

// requireOutputAbsent rejects writing a manifest over an existing --output path
// so an export or import never clobbers a file the user already has.
func requireOutputAbsent(output string) error {
	if _, err := os.Stat(output); err == nil {
		return fmt.Errorf("%w: %s; remove it or pass --output", errOutputExists, output)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat output %s: %w", output, err)
	}
	return nil
}
