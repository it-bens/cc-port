package codex

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/pelletier/go-toml/v2"

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

// configTOMLProjectKeys parses path's [projects] table and returns its
// top-level keys. A missing file contributes no keys. This parses the TOML
// structure rather than scanning raw bytes, so a path occurring only in a
// comment or an unrelated value never surfaces as a key.
func configTOMLProjectKeys(path string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path from adapter-controlled config discovery
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var config struct {
		Projects map[string]any `toml:"projects"`
	}
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse %s projects: %w", path, err)
	}
	keys := make([]string, 0, len(config.Projects))
	for key := range config.Projects {
		keys = append(keys, key)
	}
	return keys, nil
}

// configTOMLKnowsProject reports whether any config.toml/profile file has a
// [projects] key matching project, using the same equality-or-/-boundary
// predicate pathMatchesProject applies to thread and rollout cwds. This
// holds even when Codex has recorded no thread rows or rollouts for project
// yet (a trust entry created before the first session), so callers use it
// as a third, independent association alongside state-database and rollout
// evidence.
func configTOMLKnowsProject(home *Home, project string) (bool, error) {
	files, err := discoverConfigTOMLFiles(home)
	if err != nil {
		return false, err
	}
	for _, path := range files {
		keys, err := configTOMLProjectKeys(path)
		if err != nil {
			return false, err
		}
		for _, key := range keys {
			if pathMatchesProject(key, project) {
				return true, nil
			}
		}
	}
	return false, nil
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
