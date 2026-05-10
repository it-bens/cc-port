// Command encode-path prints the ~/.claude/projects/<encoded> directory name
// for a given user-supplied project path, using the same symlink-resolve-then-
// encode transformation that cc-port itself applies. Used by demo-fixture seed
// scripts so the fixture's encoded directory matches what cc-port will compute
// at demo runtime.
package main

import (
	"fmt"
	"os"

	"github.com/it-bens/cc-port/internal/claude"
)

func encodeArg(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path argument is required")
	}
	resolved, err := claude.ResolveProjectPath(path)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", path, err)
	}
	return claude.EncodePath(resolved), nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: encode-path <absolute-path>")
		os.Exit(2)
	}
	encoded, err := encodeArg(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	fmt.Println(encoded)
}
