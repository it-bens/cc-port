package claude

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ProjectEnumeration describes one encoded project directory discovered under
// Home.ProjectsDir(): its encoded directory name, the real path a session
// witness attributes to it (empty when no witness exists), and the located
// data sized for the project's disk footprint.
type ProjectEnumeration struct {
	EncodedName  string
	ResolvedPath string
	Locations    *ProjectLocations
}

// EnumerateProjects lists every encoded project directory under
// Home.ProjectsDir() with the data needed to size each one's disk footprint.
// An absent or empty projects directory yields an empty slice, not an error.
//
// Unlike LocateProject it takes no caller-supplied path and runs no identity
// cross-check: the encoding is lossy, so each project's real path comes from a
// session witness when one exists and is left empty otherwise. Reference counts
// are out of scope here — they need a confirmed real path — so the global
// history and config surfaces are not consulted.
func EnumerateProjects(ctx context.Context, claudeHome *Home) ([]ProjectEnumeration, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	projectsDir := claudeHome.ProjectsDir()
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, nil
		}
		return nil, fmt.Errorf("read projects directory: %w", err)
	}

	var enumerations []ProjectEnumeration
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() {
			continue
		}
		encodedName := entry.Name()
		projectDir := filepath.Join(projectsDir, encodedName)

		locations := &ProjectLocations{ProjectDir: projectDir}
		sessionUUIDs, err := collectProjectDirEntries(ctx, locations, projectDir)
		if err != nil {
			return nil, err
		}

		resolvedPath, err := resolveWitnessPath(ctx, claudeHome, sessionUUIDs)
		if err != nil {
			return nil, err
		}
		locations.ProjectPath = resolvedPath

		if err := collectDiskFootprintLocations(ctx, locations, claudeHome, resolvedPath, sessionUUIDs); err != nil {
			return nil, err
		}

		enumerations = append(enumerations, ProjectEnumeration{
			EncodedName:  encodedName,
			ResolvedPath: resolvedPath,
			Locations:    locations,
		})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return enumerations, nil
}

// resolveWitnessPath returns the real project path a session witness attributes
// to the encoded directory with the given session UUID set, or "" when no
// witness exists. When witnesses disagree (a lossy-encoding collision) the
// first by session-file name wins; os.ReadDir's sorted order keeps the choice
// deterministic.
func resolveWitnessPath(ctx context.Context, claudeHome *Home, sessionUUIDs []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(sessionUUIDs) == 0 {
		return "", nil
	}
	uuidSet := make(map[string]struct{}, len(sessionUUIDs))
	for _, uuid := range sessionUUIDs {
		uuidSet[uuid] = struct{}{}
	}
	cwds, err := walkSessionWitnesses(ctx, claudeHome.SessionsDir(), uuidSet)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(cwds) == 0 {
		return "", nil
	}
	return cwds[0], nil
}

// collectDiskFootprintLocations fills the owned-data fields of locations by
// reusing LocateProject's per-collector helpers. The sessions/*.json collector
// is gated on a resolved real path — it attributes by cwd, which a witness-less
// project cannot supply — while the session-UUID-keyed collectors run
// unconditionally. The history and config surfaces carry no per-project disk
// footprint and are deliberately skipped.
func collectDiskFootprintLocations(
	ctx context.Context,
	locations *ProjectLocations,
	claudeHome *Home,
	resolvedPath string,
	sessionUUIDs []string,
) error {
	if err := collectMemoryFiles(ctx, locations, locations.ProjectDir); err != nil {
		return err
	}
	if err := collectFileHistoryDirs(ctx, locations, claudeHome, sessionUUIDs); err != nil {
		return err
	}
	if resolvedPath != "" {
		if err := collectSessionFiles(ctx, locations, claudeHome, resolvedPath); err != nil {
			return err
		}
	}
	if err := collectTodos(ctx, locations, claudeHome, sessionUUIDs); err != nil {
		return err
	}
	if err := collectUsageData(ctx, locations, claudeHome, sessionUUIDs); err != nil {
		return err
	}
	if err := collectPluginsData(ctx, locations, claudeHome, sessionUUIDs); err != nil {
		return err
	}
	return collectTaskFiles(ctx, locations, claudeHome, sessionUUIDs)
}
