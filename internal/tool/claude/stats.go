package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/tool"
)

// referenceSurfaceNames is the display order ReferenceSurfaces reports in: a
// hard-coded base list (history, sessions, transcripts, memory, config,
// then the finer-grained "history entries" and "session files" counts),
// followed by every user-wide and session-keyed registry entry in
// registration order. Only the trailing two sections are registry-derived;
// a new registry entry shows up there automatically. file-history is absent
// on purpose: snapshot bytes are opaque and never scanned for references.
func referenceSurfaceNames() []string {
	surfaces := []string{categoryHistory, categorySessions, "transcripts", categoryMemory, categoryConfig, "history entries", "session files"}
	for target := range UserWideRewriteTargets() {
		surfaces = append(surfaces, target.Name)
	}
	for group := range SessionKeyedGroups() {
		surfaces = append(surfaces, group.Name)
	}
	return surfaces
}

// ReferenceSurfaces implements tool.Auditor. Each surface uses the count
// variant matching what an apply would rewrite there: the JSON-escape
// variant on surfaces routed through the typed JSON helpers (history,
// sessions, config), the raw variant elsewhere. Transcripts and memory
// additionally count the absolute encoded storage-dir form, mirroring
// move's two-pass rewrite of those surfaces.
func (workspace *Workspace) ReferenceSurfaces(ctx context.Context, project string) ([]tool.CountSurface, error) {
	locations, err := LocateProject(workspace.home, project)
	if err != nil {
		return nil, fmt.Errorf("locate project: %w", err)
	}

	counts, err := workspace.countReferences(ctx, locations)
	if err != nil {
		return nil, err
	}
	counts["history entries"] = locations.HistoryEntryCount
	counts["session files"] = len(locations.SessionFiles)

	surfaces := referenceSurfaceNames()
	ordered := make([]tool.CountSurface, 0, len(surfaces))
	for _, name := range surfaces {
		ordered = append(ordered, tool.CountSurface{Name: name, Count: counts[name]})
	}
	return ordered, nil
}

func (workspace *Workspace) countReferences(ctx context.Context, locations *ProjectLocations) (map[string]int, error) {
	projectPath := locations.ProjectPath
	encodedDir := locations.ProjectDir
	counts := make(map[string]int)

	historyCount, err := workspace.countHistoryReferences(ctx, projectPath)
	if err != nil {
		return nil, err
	}
	counts["history"] = historyCount

	sessionsCount, err := countAcrossFiles(ctx, locations.SessionFiles, projectPath, rewrite.CountPathInBytesWithJSONEscape)
	if err != nil {
		return nil, err
	}
	counts["sessions"] = sessionsCount

	transcriptFiles, err := TranscriptFiles(ctx, encodedDir)
	if err != nil {
		return nil, err
	}
	transcriptsCount, err := countWithEncodedDir(ctx, transcriptFiles, projectPath, encodedDir)
	if err != nil {
		return nil, err
	}
	counts["transcripts"] = transcriptsCount

	memoryCount, err := countWithEncodedDir(ctx, locations.MemoryFiles, projectPath, encodedDir)
	if err != nil {
		return nil, err
	}
	counts["memory"] = memoryCount

	configCount, err := workspace.countConfigReferences(ctx, projectPath)
	if err != nil {
		return nil, err
	}
	counts["config"] = configCount

	for target := range UserWideRewriteTargets() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		targetCount, err := countOptionalFile(target.RewritePath(workspace.home), projectPath, rewrite.CountPathInBytes)
		if err != nil {
			return nil, err
		}
		counts[target.Name] = targetCount
	}

	for group, path := range locations.AllFlatFiles() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path) //nolint:gosec // path from trusted ProjectLocations
		if err != nil {
			return nil, fmt.Errorf("read %s file %s: %w", group.Name, path, err)
		}
		counts[group.Name] += rewrite.CountPathInBytes(data, projectPath)
	}

	return counts, nil
}

type countVariant func(data []byte, path string) int

func countAcrossFiles(ctx context.Context, paths []string, path string, variant countVariant) (int, error) {
	total := 0
	for _, filePath := range paths {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		data, err := os.ReadFile(filePath) //nolint:gosec // path from trusted ProjectLocations
		if err != nil {
			return 0, fmt.Errorf("read %s: %w", filePath, err)
		}
		total += variant(data, path)
	}
	return total, nil
}

func countWithEncodedDir(ctx context.Context, paths []string, projectPath, encodedDir string) (int, error) {
	total := 0
	for _, filePath := range paths {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		data, err := os.ReadFile(filePath) //nolint:gosec // path from trusted ProjectLocations
		if err != nil {
			return 0, fmt.Errorf("read %s: %w", filePath, err)
		}
		total += rewrite.CountPathInBytes(data, projectPath)
		total += rewrite.CountPathInBytes(data, encodedDir)
	}
	return total, nil
}

func countOptionalFile(path, projectPath string, variant countVariant) (int, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path from trusted Home derivation
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	return variant(data, projectPath), nil
}

func (workspace *Workspace) countHistoryReferences(ctx context.Context, projectPath string) (int, error) {
	historyFile := workspace.home.HistoryFile()
	file, err := os.Open(historyFile) //nolint:gosec // path derived from trusted Home
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("open history.jsonl: %w", err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), MaxHistoryLine)

	total := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var probe HistoryEntry
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		total += rewrite.CountPathInBytesWithJSONEscape(line, projectPath)
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan history.jsonl: %w", err)
	}
	return total, nil
}

func (workspace *Workspace) countConfigReferences(ctx context.Context, projectPath string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	data, err := os.ReadFile(workspace.home.ConfigFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("read config file: %w", err)
	}
	return rewrite.CountPathInBytesWithJSONEscape(data, projectPath), nil
}

// DiskCategories implements tool.Auditor.
func (workspace *Workspace) DiskCategories(ctx context.Context, project string) ([]tool.SizeCategory, error) {
	locations, err := LocateProject(workspace.home, project)
	if err != nil {
		return nil, fmt.Errorf("locate project: %w", err)
	}
	byCategory, err := computeDisk(ctx, locations)
	if err != nil {
		return nil, err
	}
	return orderDisk(byCategory), nil
}

// diskUsage is the file count and total byte size of one category's owned data.
type diskUsage struct {
	Files int
	Bytes int64
}

func (usage *diskUsage) add(other diskUsage) {
	usage.Files += other.Files
	usage.Bytes += other.Bytes
}

// computeDisk sizes the files LocateProject (or EnumerateProjects)
// attributes to a project, keyed by category name. Owned subtrees and
// project-specific files contribute; history and config are shared globals
// with no per-project disk footprint and stay at zero. file-history
// snapshot bytes are sized but never inspected, per the file-history
// opacity policy.
func computeDisk(ctx context.Context, locations *ProjectLocations) (map[string]diskUsage, error) {
	byCategory := make(map[string]diskUsage, len(categories))

	transcriptFiles, err := TranscriptFiles(ctx, locations.ProjectDir)
	if err != nil {
		return nil, err
	}
	sessions, err := sizeFiles(ctx, transcriptFiles)
	if err != nil {
		return nil, err
	}
	sessionFileUsage, err := sizeFiles(ctx, locations.SessionFiles)
	if err != nil {
		return nil, err
	}
	sessions.add(sessionFileUsage)
	byCategory["sessions"] = sessions

	if byCategory["memory"], err = sizeFiles(ctx, locations.MemoryFiles); err != nil {
		return nil, err
	}
	if byCategory["file-history"], err = sizeDirs(ctx, locations.FileHistoryDirs); err != nil {
		return nil, err
	}

	for group, path := range locations.AllFlatFiles() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("stat %s file %s: %w", group.Name, path, err)
		}
		usage := byCategory[group.Category]
		usage.Files++
		usage.Bytes += info.Size()
		byCategory[group.Category] = usage
	}

	return byCategory, nil
}

func sizeFiles(ctx context.Context, paths []string) (diskUsage, error) {
	var usage diskUsage
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return diskUsage{}, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return diskUsage{}, fmt.Errorf("stat %s: %w", path, err)
		}
		usage.Files++
		usage.Bytes += info.Size()
	}
	return usage, nil
}

func sizeDirs(ctx context.Context, dirs []string) (diskUsage, error) {
	var usage diskUsage
	for _, dir := range dirs {
		walkErr := filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			usage.Files++
			usage.Bytes += info.Size()
			return nil
		})
		if walkErr != nil {
			return diskUsage{}, fmt.Errorf("walk %s: %w", dir, walkErr)
		}
	}
	return usage, nil
}

// orderDisk projects the per-category size map onto categories order,
// emitting every category (history, config, and config-grants included at
// zero) so the result always carries the full registry in canonical order.
func orderDisk(byCategory map[string]diskUsage) []tool.SizeCategory {
	ordered := make([]tool.SizeCategory, 0, len(categories))
	for _, category := range categories {
		usage := byCategory[category.Name]
		ordered = append(ordered, tool.SizeCategory{Name: category.Name, Files: usage.Files, Bytes: usage.Bytes})
	}
	return ordered
}

// EnumerateProjects implements tool.Auditor.
func (workspace *Workspace) EnumerateProjects(ctx context.Context) ([]tool.ProjectInfo, error) {
	enumerations, err := EnumerateProjects(ctx, workspace.home)
	if err != nil {
		return nil, fmt.Errorf("enumerate projects: %w", err)
	}

	infos := make([]tool.ProjectInfo, 0, len(enumerations))
	for _, enumeration := range enumerations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		byCategory, err := computeDisk(ctx, enumeration.Locations)
		if err != nil {
			return nil, err
		}
		disk := orderDisk(byCategory)

		label := enumeration.ResolvedPath
		if label == "" {
			label = enumeration.EncodedName
		}
		info := tool.ProjectInfo{
			Label:    label,
			Resolved: enumeration.ResolvedPath != "",
			Disk:     disk,
		}
		for _, usage := range disk {
			info.Files += usage.Files
			info.Bytes += usage.Bytes
		}
		infos = append(infos, info)
	}
	return infos, nil
}
