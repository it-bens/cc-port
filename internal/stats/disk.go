package stats

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/manifest"
)

// computeDisk sizes the files LocateProject (or EnumerateProjects) attributes to
// a project, keyed by manifest.AllCategories name. Owned subtrees and
// project-specific files contribute; history and config are shared globals with
// no per-project disk footprint and stay at zero. file-history snapshot bytes
// are sized but never inspected, per the file-history opacity policy.
func computeDisk(ctx context.Context, locations *claude.ProjectLocations) (map[string]DiskUsage, error) {
	byCategory := make(map[string]DiskUsage, len(manifest.AllCategories))

	bodyFiles, err := claude.TranscriptFiles(ctx, locations.ProjectDir)
	if err != nil {
		return nil, err
	}
	sessions, err := sizeFiles(ctx, bodyFiles)
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

func (usage *DiskUsage) add(other DiskUsage) {
	usage.Files += other.Files
	usage.Bytes += other.Bytes
}

// sizeFiles stats each path and totals their count and size. The paths come
// from a ProjectLocations collector that already confirmed existence, so a stat
// error is unexpected and fails the command.
func sizeFiles(ctx context.Context, paths []string) (DiskUsage, error) {
	var usage DiskUsage
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return DiskUsage{}, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return DiskUsage{}, fmt.Errorf("stat %s: %w", path, err)
		}
		usage.Files++
		usage.Bytes += info.Size()
	}
	return usage, nil
}

// sizeDirs walks each directory and totals the count and size of every
// contained file. Snapshot and transcript bytes are sized, not read.
func sizeDirs(ctx context.Context, dirs []string) (DiskUsage, error) {
	var usage DiskUsage
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
			return DiskUsage{}, fmt.Errorf("walk %s: %w", dir, walkErr)
		}
	}
	return usage, nil
}
