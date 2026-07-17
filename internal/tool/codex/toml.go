package codex

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/tool"
)

// configProfileSuffix names a per-profile config overlay, which can carry
// its own [projects] table (core/src/config/mod.rs:273, 1757-1763).
const configProfileSuffix = ".config.toml"

// discoverConfigTOMLFiles returns config.toml (if present) followed by
// every <profile>.config.toml file, in sorted order.
func discoverConfigTOMLFiles(home *Home) ([]string, error) {
	var files []string
	configPath := filepath.Join(home.Dir, configTOMLFileName)
	if _, err := os.Stat(configPath); err == nil {
		files = append(files, configPath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("stat %s: %w", configPath, err)
	}

	pattern := "*" + configProfileSuffix
	matches, err := filepath.Glob(filepath.Join(home.Dir, pattern))
	if err != nil {
		return nil, fmt.Errorf("glob %s in %s: %w", pattern, home.Dir, err)
	}
	sort.Strings(matches)
	files = append(files, matches...)
	return files, nil
}

// planConfigTOMLFile reports how many key/value occurrences a move would
// rewrite in path. A missing file contributes zero.
func planConfigTOMLFile(path, oldPath, newPath string) (int, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path from adapter-controlled config discovery
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	_, count, err := rewrite.TOMLPathRewrite(data, oldPath, newPath)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	return count, nil
}

// applyConfigTOMLFile rewrites path in place via rewrite.TOMLPathRewrite,
// wrapping the primitive's validation errors (which report bytes only)
// with the file path.
func applyConfigTOMLFile(path, oldPath, newPath string, undo *tool.Restorer) (int, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	if err := undo.RegisterFile(path); err != nil {
		return 0, fmt.Errorf("back up %s: %w", path, err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path from adapter-controlled config discovery
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	rewritten, count, err := rewrite.TOMLPathRewrite(data, oldPath, newPath)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	if err := rewrite.SafeWriteFile(path, rewritten, info.Mode()); err != nil {
		return 0, fmt.Errorf("write %s: %w", path, err)
	}
	return count, nil
}
