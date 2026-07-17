package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/tool"
)

// agentsPluginsMarketplaceFile is the one shared-home artifact this
// adapter knows how to rewrite; the populated shape of ~/.agents is
// otherwise unverified (spec §6.2), so every other path hit under it is
// reported, never rewritten.
const agentsPluginsMarketplaceFile = "plugins/marketplace.json"

// marketplacePathBearingStringPaths walks the documented marketplace source
// shapes. A source is either a string, or an object whose path-bearing fields
// are path, url, and package. Every key is escaped for gjson/sjson paths.
func marketplacePathBearingStringPaths(value any, prefix string) []string {
	var paths []string
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			childPrefix := gjson.Escape(key)
			if prefix != "" {
				childPrefix = prefix + "." + childPrefix
			}
			if key == "source" {
				switch source := child.(type) {
				case string:
					paths = append(paths, childPrefix)
				case map[string]any:
					for sourceKey, sourceValue := range source {
						if sourceKey == "path" || sourceKey == "url" || sourceKey == "package" {
							if _, isString := sourceValue.(string); isString {
								paths = append(paths, childPrefix+"."+gjson.Escape(sourceKey))
							}
						}
					}
				}
			}
			paths = append(paths, marketplacePathBearingStringPaths(child, childPrefix)...)
		}
	case []any:
		for index, child := range typed {
			childPrefix := fmt.Sprintf("%s.%d", prefix, index)
			paths = append(paths, marketplacePathBearingStringPaths(child, childPrefix)...)
		}
	}
	return paths
}

// planAgentsMarketplace reports how many "source" values a move would
// rewrite in $HOME/.agents/plugins/marketplace.json. An absent
// ~/.agents, an absent marketplace.json, or a file that fails to parse
// all contribute zero (the last is surfaced via ResidualWarnings, not an
// error: an unparseable shared-home file must not block every other
// tool's move).
func planAgentsMarketplace(agentsDir, oldPath string) (int, error) {
	data, ok, err := readAgentsMarketplace(agentsDir)
	if err != nil || !ok {
		return 0, err
	}
	var document any
	if json.Unmarshal(data, &document) != nil {
		return 0, nil
	}
	total := 0
	for _, path := range marketplacePathBearingStringPaths(document, "") {
		value := gjson.GetBytes(data, path).String()
		total += rewrite.CountPathInBytesWithJSONEscape([]byte(value), oldPath)
	}
	return total, nil
}

// applyAgentsMarketplace rewrites every "source" value in
// $HOME/.agents/plugins/marketplace.json in place.
func applyAgentsMarketplace(agentsDir, oldPath, newPath string, undo *tool.Restorer) (int, error) {
	data, ok, err := readAgentsMarketplace(agentsDir)
	if err != nil || !ok {
		return 0, err
	}
	var document any
	if json.Unmarshal(data, &document) != nil {
		return 0, nil
	}
	sourcePaths := marketplacePathBearingStringPaths(document, "")
	if len(sourcePaths) == 0 {
		return 0, nil
	}

	path := filepath.Join(agentsDir, agentsPluginsMarketplaceFile)
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	if err := undo.RegisterFile(path); err != nil {
		return 0, fmt.Errorf("back up %s: %w", path, err)
	}

	total := 0
	updated := data
	for _, sourcePath := range sourcePaths {
		original := gjson.GetBytes(updated, sourcePath).String()
		rewrittenValue, count := rewrite.ReplacePathInBytesWithJSONEscape([]byte(original), oldPath, newPath)
		if count == 0 {
			continue
		}
		total += count
		updated, err = sjson.SetBytes(updated, sourcePath, string(rewrittenValue))
		if err != nil {
			return 0, fmt.Errorf("rewrite %s in %s: %w", sourcePath, path, err)
		}
	}
	if total == 0 {
		return 0, nil
	}
	if err := rewrite.SafeWriteFile(path, updated, info.Mode()); err != nil {
		return 0, fmt.Errorf("write %s: %w", path, err)
	}
	return total, nil
}

func readAgentsMarketplace(agentsDir string) (data []byte, ok bool, err error) {
	if agentsDir == "" {
		return nil, false, nil
	}
	path := filepath.Join(agentsDir, agentsPluginsMarketplaceFile)
	data, statErr := os.ReadFile(path) //nolint:gosec // G304: path constructed from resolved home directory
	if statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read %s: %w", path, statErr)
	}
	return data, true, nil
}

// residualAgentsWarning reports whether ~/.agents contains any file that
// still references oldPath. marketplace.json is included so a schema-shaped
// value the targeted rewrite intentionally leaves alone cannot be silent.
func residualAgentsWarning(agentsDir, oldPath string) (string, error) {
	if agentsDir == "" {
		return "", nil
	}
	count := 0
	walkErr := filepath.WalkDir(agentsDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // G304: path from adapter-controlled ~/.agents walk
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}
		if rewrite.ContainsBoundedPath(data, oldPath) {
			count++
		}
		return nil
	})
	if walkErr != nil {
		if errors.Is(walkErr, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("walk %s: %w", agentsDir, walkErr)
	}
	if count == 0 {
		return "", nil
	}
	return fmt.Sprintf(
		"%d file(s) under ~/.agents still reference the old project path; cc-port only rewrites plugins/marketplace.json in this shared directory",
		count,
	), nil
}
