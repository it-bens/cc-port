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

	"github.com/it-bens/cc-port/internal/fsutil"
	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/tool"
)

// ErrEncodedDirAmbiguous is returned when the old and new project paths
// both encode to the same on-disk storage directory. The encoder is lossy
// on '/', '.', and ' '.
var ErrEncodedDirAmbiguous = errors.New("refusing to move: old and new paths encode to the same directory")

// ErrEncodedDirCollision is returned when the new project's encoded
// directory already exists because another real path encodes to the same name.
var ErrEncodedDirCollision = errors.New("refusing to move: new project directory already exists")

// ErrResidualSourceDir is returned when the encoded project data is already
// removed but the on-disk source directory cannot be deleted.
var ErrResidualSourceDir = errors.New("on-disk source directory still present")

var removeAll = os.RemoveAll

// MoveSurfaces implements tool.Mover. Surfaces run in the returned order:
// each rewrite surface is individually atomic (a sibling-temp write via
// rewrite.SafeWriteFile) and registers its pre-image with the Restorer, so
// an in-process failure at any point restores every surface applied so
// far. The final "project-directory" surface performs the actual rename —
// copying the encoded storage directory (and, unless RefsOnly, the
// on-disk project directory) to the new path and removing the originals —
// and must run last so every reference surface has already been rewritten
// against the still-present old data.
func (workspace *Workspace) MoveSurfaces(req tool.MoveRequest) ([]tool.Surface, error) {
	if _, err := LocateProject(workspace.home, req.OldPath); err != nil {
		return nil, fmt.Errorf("locate project: %w", err)
	}
	if err := checkEncodedDirCollision(workspace.home, req.OldPath, req.NewPath); err != nil {
		return nil, err
	}
	if !req.RefsOnly {
		if err := checkPhysicalDestination(req.NewPath); err != nil {
			return nil, err
		}
	}
	workspace.clearMoveWarnings()
	locations, err := LocateProject(workspace.home, req.OldPath)
	if err != nil {
		return nil, fmt.Errorf("locate project for file-history warning: %w", err)
	}
	snapshots, err := snapshotPaths(context.Background(), locations)
	if err != nil {
		return nil, fmt.Errorf("inspect file-history snapshots: %w", err)
	}
	if len(snapshots) > 0 {
		workspace.addMoveWarning(fileHistoryWarning(len(snapshots)))
	}

	// Ordering is load-bearing: every surface but "sessions" and
	// "project-directory" calls LocateProject(OldPath), which re-verifies
	// project identity against the sessions/*.json witness. The "sessions"
	// surface is the one surface that rewrites that witness's cwd field,
	// so it must run last among the reference rewrites — otherwise a
	// later surface's identity check would see the witness already
	// pointing at NewPath and refuse to proceed. "project-directory" runs
	// last of all: it derives paths directly from Home.ProjectDir and
	// never calls LocateProject, so it does not depend on witness state.
	var surfaces []tool.Surface
	surfaces = append(surfaces, workspace.historySurface(req))
	surfaces = append(surfaces, workspace.userWideSurfaces(req)...)
	surfaces = append(surfaces, workspace.sessionKeyedSurfaces(req)...)
	surfaces = append(surfaces, workspace.configSurface(req))
	if req.DeepRewrite {
		surfaces = append(surfaces, workspace.transcriptsSurface(req))
	}
	surfaces = append(surfaces, workspace.memorySurface(req), workspace.sessionsSurface(req), workspace.projectDirectorySurface(req))
	return surfaces, nil
}

// ResidualWarnings implements tool.Mover: content a move preserves verbatim
// and cannot fully rewrite.
func (workspace *Workspace) ResidualWarnings(req tool.MoveRequest) ([]string, error) {
	warnings := workspace.moveWarningSnapshot()
	ctx := context.Background()
	locations, err := LocateProject(workspace.home, req.OldPath)
	if err != nil {
		if errors.Is(err, tool.ErrProjectAbsent) {
			return warnings, nil
		}
		return warnings, fmt.Errorf("locate project: %w", err)
	}
	paths, err := snapshotPaths(ctx, locations)
	if err != nil {
		return warnings, err
	}
	if len(paths) == 0 {
		return warnings, nil
	}
	warnings = appendUniqueMoveWarnings(warnings, fileHistoryWarning(len(paths)))
	return warnings, nil
}

func fileHistoryWarning(count int) string {
	return fmt.Sprintf(
		"%d file-history snapshot(s) preserved verbatim; bodies may still contain the old project path "+
			"(Claude Code reads them by filename for in-session rewinds, not as path references)",
		count,
	)
}

func appendUniqueMoveWarnings(warnings []string, warning string) []string {
	for _, existing := range warnings {
		if existing == warning {
			return warnings
		}
	}
	return append(warnings, warning)
}

func checkPhysicalDestination(newPath string) error {
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("refusing to move: destination project directory already exists: %s", newPath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat destination project directory %s: %w", newPath, err)
	}
	return nil
}

func (workspace *Workspace) clearMoveWarnings() {
	workspace.moveWarningMutex.Lock()
	defer workspace.moveWarningMutex.Unlock()
	workspace.moveWarnings = nil
}

func (workspace *Workspace) addMoveWarning(warning string) {
	workspace.moveWarningMutex.Lock()
	defer workspace.moveWarningMutex.Unlock()
	workspace.moveWarnings = append(workspace.moveWarnings, warning)
}

func (workspace *Workspace) moveWarningSnapshot() []string {
	workspace.moveWarningMutex.Lock()
	defer workspace.moveWarningMutex.Unlock()
	return append([]string(nil), workspace.moveWarnings...)
}

func checkEncodedDirCollision(claudeHome *Home, oldPath, newPath string) error {
	oldEncodedDir := claudeHome.ProjectDir(oldPath)
	newEncodedDir := claudeHome.ProjectDir(newPath)

	if oldEncodedDir == newEncodedDir {
		return fmt.Errorf(
			"%w: %q and %q both encode to directory %s",
			ErrEncodedDirAmbiguous, oldPath, newPath, filepath.Base(newEncodedDir),
		)
	}
	if _, err := os.Stat(newEncodedDir); err == nil {
		return fmt.Errorf("%w: %s", ErrEncodedDirCollision, newEncodedDir)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat new project directory %s: %w", newEncodedDir, err)
	}
	return nil
}

// snapshotPaths returns every snapshot file path under locations.FileHistoryDirs.
func snapshotPaths(ctx context.Context, locations *ProjectLocations) ([]string, error) {
	var paths []string
	for _, fileHistoryDir := range locations.FileHistoryDirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		snapshots, err := fsutil.ListFilesRecursive(ctx, fileHistoryDir)
		if err != nil {
			return nil, fmt.Errorf("walk file-history dir %s: %w", fileHistoryDir, err)
		}
		paths = append(paths, snapshots...)
	}
	return paths, nil
}

// rewriteTracked performs the register -> byte-replace -> atomic-write
// sandwich used by every uniform plain-bytes rewrite surface.
func rewriteTracked(path, oldPath, newPath string, undo *tool.Restorer) (int, error) {
	if err := undo.RegisterFile(path); err != nil {
		return 0, err
	}
	original, err := os.ReadFile(path) //nolint:gosec // path constructed from trusted internal data
	if err != nil {
		return 0, err
	}
	rewritten, count := rewrite.ReplacePathInBytes(original, oldPath, newPath)
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if err := rewrite.SafeWriteFile(path, rewritten, info.Mode()); err != nil {
		return 0, err
	}
	return count, nil
}

func (workspace *Workspace) historySurface(req tool.MoveRequest) tool.Surface {
	return tool.Surface{
		Name: categoryHistory,
		Plan: func(ctx context.Context) (int, error) {
			count, _, err := workspace.scanHistoryFile(ctx, req.OldPath)
			return count, err
		},
		Apply: func(ctx context.Context, undo *tool.Restorer) (int, error) {
			return workspace.applyHistoryRewrite(ctx, req, undo)
		},
	}
}

func (workspace *Workspace) scanHistoryFile(ctx context.Context, oldPath string) (count int, malformed []int, err error) {
	historyFile := workspace.home.HistoryFile()
	file, err := os.Open(historyFile) //nolint:gosec // path constructed from trusted internal data
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil, nil
		}
		return 0, nil, fmt.Errorf("open history.jsonl: %w", err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), MaxHistoryLine)

	lineNumber := 0
	for scanner.Scan() {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, nil, ctxErr
		}
		lineNumber++
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var probe HistoryEntry
		if jsonErr := json.Unmarshal(line, &probe); jsonErr != nil {
			malformed = append(malformed, lineNumber)
			continue
		}
		if rewrite.CountPathInBytes(line, oldPath) > 0 {
			count++
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return 0, nil, fmt.Errorf("scan history.jsonl: %w", scanErr)
	}
	return count, malformed, nil
}

func (workspace *Workspace) applyHistoryRewrite(ctx context.Context, req tool.MoveRequest, undo *tool.Restorer) (int, error) {
	historyFile := workspace.home.HistoryFile()
	if _, err := os.Stat(historyFile); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("stat %s: %w", historyFile, err)
	}
	if err := undo.RegisterFile(historyFile); err != nil {
		return 0, fmt.Errorf("back up history.jsonl: %w", err)
	}
	original, err := os.ReadFile(historyFile) //nolint:gosec // path constructed from trusted internal data
	if err != nil {
		return 0, fmt.Errorf("read history.jsonl: %w", err)
	}
	var rewritten bytes.Buffer
	count, _, err := StreamHistoryJSONL(ctx, bytes.NewReader(original), &rewritten, req.OldPath, req.NewPath)
	if err != nil {
		return 0, fmt.Errorf("rewrite history.jsonl: %w", err)
	}
	if err := rewrite.SafeWriteFile(historyFile, rewritten.Bytes(), 0o600); err != nil {
		return 0, fmt.Errorf("write history.jsonl: %w", err)
	}
	return count, nil
}

func (workspace *Workspace) sessionsSurface(req tool.MoveRequest) tool.Surface {
	return tool.Surface{
		Name: categorySessions,
		Plan: func(ctx context.Context) (int, error) {
			locations, err := LocateProject(workspace.home, req.OldPath)
			if err != nil {
				return 0, fmt.Errorf("locate project: %w", err)
			}
			count := 0
			for _, sessionFilePath := range locations.SessionFiles {
				if err := ctx.Err(); err != nil {
					return 0, err
				}
				data, err := os.ReadFile(sessionFilePath) //nolint:gosec // path constructed from trusted internal data
				if err != nil {
					return 0, fmt.Errorf("read session file %s: %w", sessionFilePath, err)
				}
				_, changed, err := RewriteSessionFile(data, req.OldPath, req.NewPath)
				if err != nil {
					return 0, fmt.Errorf("analyze session file %s: %w", sessionFilePath, err)
				}
				if changed {
					count++
				}
			}
			return count, nil
		},
		Apply: func(ctx context.Context, undo *tool.Restorer) (int, error) {
			locations, err := LocateProject(workspace.home, req.OldPath)
			if err != nil {
				return 0, fmt.Errorf("locate project: %w", err)
			}
			count := 0
			for _, sessionFilePath := range locations.SessionFiles {
				if err := ctx.Err(); err != nil {
					return 0, err
				}
				if err := undo.RegisterFile(sessionFilePath); err != nil {
					return 0, err
				}
				original, err := os.ReadFile(sessionFilePath) //nolint:gosec // path constructed from trusted internal data
				if err != nil {
					return 0, err
				}
				rewritten, changed, err := RewriteSessionFile(original, req.OldPath, req.NewPath)
				if err != nil {
					return 0, fmt.Errorf("rewrite session file %s: %w", sessionFilePath, err)
				}
				info, err := os.Stat(sessionFilePath)
				if err != nil {
					return 0, err
				}
				if err := rewrite.SafeWriteFile(sessionFilePath, rewritten, info.Mode()); err != nil {
					return 0, fmt.Errorf("write session file %s: %w", sessionFilePath, err)
				}
				if changed {
					count++
				}
			}
			return count, nil
		},
	}
}

func (workspace *Workspace) userWideSurfaces(req tool.MoveRequest) []tool.Surface {
	var surfaces []tool.Surface
	for target := range UserWideRewriteTargets() {
		path := target.RewritePath(workspace.home)
		surfaces = append(surfaces, tool.Surface{
			Name: target.Name,
			Plan: func(ctx context.Context) (int, error) {
				if err := ctx.Err(); err != nil {
					return 0, err
				}
				data, err := os.ReadFile(path) //nolint:gosec // path constructed from trusted internal data
				if err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						return 0, nil
					}
					return 0, fmt.Errorf("read %s: %w", path, err)
				}
				_, count := rewrite.ReplacePathInBytes(data, req.OldPath, req.NewPath)
				return count, nil
			},
			Apply: func(_ context.Context, undo *tool.Restorer) (int, error) {
				if _, err := os.Stat(path); err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						return 0, nil
					}
					return 0, fmt.Errorf("stat %s: %w", path, err)
				}
				return rewriteTracked(path, req.OldPath, req.NewPath, undo)
			},
		})
	}
	return surfaces
}

func (workspace *Workspace) sessionKeyedSurfaces(req tool.MoveRequest) []tool.Surface {
	var surfaces []tool.Surface
	for group := range SessionKeyedGroups() {
		surfaces = append(surfaces, tool.Surface{
			Name: group.Name,
			Plan: func(ctx context.Context) (int, error) {
				locations, err := LocateProject(workspace.home, req.OldPath)
				if err != nil {
					return 0, fmt.Errorf("locate project: %w", err)
				}
				count := 0
				for _, path := range group.Files(locations) {
					if err := ctx.Err(); err != nil {
						return 0, err
					}
					if group.SidecarFilter != nil && group.SidecarFilter(filepath.Base(path)) {
						continue
					}
					data, err := os.ReadFile(path) //nolint:gosec // path from trusted ProjectLocations
					if err != nil {
						return 0, fmt.Errorf("read %s file %s: %w", group.Name, path, err)
					}
					_, n := rewrite.ReplacePathInBytes(data, req.OldPath, req.NewPath)
					count += n
				}
				return count, nil
			},
			Apply: func(ctx context.Context, undo *tool.Restorer) (int, error) {
				locations, err := LocateProject(workspace.home, req.OldPath)
				if err != nil {
					return 0, fmt.Errorf("locate project: %w", err)
				}
				count := 0
				for _, path := range group.Files(locations) {
					if err := ctx.Err(); err != nil {
						return 0, err
					}
					if group.SidecarFilter != nil && group.SidecarFilter(filepath.Base(path)) {
						continue
					}
					info, err := os.Stat(path)
					if err != nil {
						return 0, err
					}
					n, err := rewriteTracked(path, req.OldPath, req.NewPath, undo)
					if err != nil {
						return 0, fmt.Errorf("rewrite %s %s: %w", group.Name, path, err)
					}
					if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
						return 0, fmt.Errorf("restore mtime %s: %w", path, err)
					}
					count += n
				}
				return count, nil
			},
		})
	}
	return surfaces
}

func (workspace *Workspace) configSurface(req tool.MoveRequest) tool.Surface {
	return tool.Surface{
		Name: categoryConfig,
		Plan: func(ctx context.Context) (int, error) {
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
			_, rekeyed, err := RewriteUserConfig(data, req.OldPath, req.NewPath)
			if err != nil {
				return 0, fmt.Errorf("analyze config file: %w", err)
			}
			if rekeyed {
				return 1, nil
			}
			return 0, nil
		},
		Apply: func(_ context.Context, undo *tool.Restorer) (int, error) {
			configFile := workspace.home.ConfigFile
			if _, err := os.Stat(configFile); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return 0, nil
				}
				return 0, fmt.Errorf("stat %s: %w", configFile, err)
			}
			if err := undo.RegisterFile(configFile); err != nil {
				return 0, fmt.Errorf("read config file for backup: %w", err)
			}
			original, err := os.ReadFile(configFile) //nolint:gosec // path constructed from trusted internal data
			if err != nil {
				return 0, fmt.Errorf("read config file: %w", err)
			}
			rewritten, rekeyed, err := RewriteUserConfig(original, req.OldPath, req.NewPath)
			if err != nil {
				return 0, fmt.Errorf("rewrite config file: %w", err)
			}
			if err := rewrite.SafeWriteFile(configFile, rewritten, 0o600); err != nil {
				return 0, fmt.Errorf("write config file: %w", err)
			}
			if rekeyed {
				return 1, nil
			}
			return 0, nil
		},
	}
}

func (workspace *Workspace) transcriptsSurface(req tool.MoveRequest) tool.Surface {
	return tool.Surface{
		Name: "transcripts",
		Plan: func(ctx context.Context) (int, error) {
			locations, err := LocateProject(workspace.home, req.OldPath)
			if err != nil {
				return 0, fmt.Errorf("locate project: %w", err)
			}
			transcriptPaths, err := TranscriptFiles(ctx, locations.ProjectDir)
			if err != nil {
				return 0, err
			}
			oldEncodedDir := workspace.home.ProjectDir(req.OldPath)
			newEncodedDir := workspace.home.ProjectDir(req.NewPath)
			total := 0
			for _, transcriptPath := range transcriptPaths {
				if err := ctx.Err(); err != nil {
					return 0, err
				}
				data, err := os.ReadFile(transcriptPath) //nolint:gosec // path constructed from trusted internal data
				if err != nil {
					return 0, fmt.Errorf("read transcript %s: %w", transcriptPath, err)
				}
				_, count := rewrite.ReplacePathInBytes(data, req.OldPath, req.NewPath)
				total += count
				_, encodedCount := rewrite.ReplacePathInBytes(data, oldEncodedDir, newEncodedDir)
				total += encodedCount
			}
			return total, nil
		},
		Apply: func(ctx context.Context, undo *tool.Restorer) (int, error) {
			// Transcripts live under the project directory, which the
			// project-directory surface has already copied to NewPath by
			// the time surfaces run in MoveSurfaces order — but that
			// surface runs LAST, so at this point transcripts are rewritten
			// in place under the OLD encoded directory, and the
			// project-directory surface's copy carries the rewritten bytes
			// forward.
			locations, err := LocateProject(workspace.home, req.OldPath)
			if err != nil {
				return 0, fmt.Errorf("locate project: %w", err)
			}
			transcriptPaths, err := TranscriptFiles(ctx, locations.ProjectDir)
			if err != nil {
				return 0, err
			}
			oldEncodedDir := workspace.home.ProjectDir(req.OldPath)
			newEncodedDir := workspace.home.ProjectDir(req.NewPath)
			total := 0
			for _, transcriptPath := range transcriptPaths {
				if err := ctx.Err(); err != nil {
					return 0, err
				}
				n, err := rewriteTwicePreservingMtime(transcriptPath, req.OldPath, req.NewPath, oldEncodedDir, newEncodedDir, undo)
				if err != nil {
					return 0, fmt.Errorf("rewrite transcript %s: %w", transcriptPath, err)
				}
				total += n
			}
			return total, nil
		},
	}
}

func (workspace *Workspace) memorySurface(req tool.MoveRequest) tool.Surface {
	return tool.Surface{
		Name: "memory",
		Plan: func(ctx context.Context) (int, error) {
			locations, err := LocateProject(workspace.home, req.OldPath)
			if err != nil {
				return 0, fmt.Errorf("locate project: %w", err)
			}
			oldEncodedDir := workspace.home.ProjectDir(req.OldPath)
			newEncodedDir := workspace.home.ProjectDir(req.NewPath)
			total := 0
			for _, memoryFilePath := range locations.MemoryFiles {
				if err := ctx.Err(); err != nil {
					return 0, err
				}
				data, err := os.ReadFile(memoryFilePath) //nolint:gosec // path constructed from trusted internal data
				if err != nil {
					return 0, fmt.Errorf("read memory file %s: %w", memoryFilePath, err)
				}
				_, count := rewrite.ReplacePathInBytes(data, req.OldPath, req.NewPath)
				total += count
				_, encodedCount := rewrite.ReplacePathInBytes(data, oldEncodedDir, newEncodedDir)
				total += encodedCount
			}
			return total, nil
		},
		Apply: func(ctx context.Context, undo *tool.Restorer) (int, error) {
			locations, err := LocateProject(workspace.home, req.OldPath)
			if err != nil {
				return 0, fmt.Errorf("locate project: %w", err)
			}
			oldEncodedDir := workspace.home.ProjectDir(req.OldPath)
			newEncodedDir := workspace.home.ProjectDir(req.NewPath)
			total := 0
			for _, memoryFilePath := range locations.MemoryFiles {
				if err := ctx.Err(); err != nil {
					return 0, err
				}
				n, err := rewriteTwicePreservingMtime(memoryFilePath, req.OldPath, req.NewPath, oldEncodedDir, newEncodedDir, undo)
				if err != nil {
					return 0, fmt.Errorf("rewrite memory file %s: %w", memoryFilePath, err)
				}
				total += n
			}
			return total, nil
		},
	}
}

// rewriteTwicePreservingMtime replaces both the real project path and the
// encoded storage directory form inside path, then restores path's
// pre-rewrite modification time.
func rewriteTwicePreservingMtime(
	path, oldPath, newPath, oldEncodedDir, newEncodedDir string, undo *tool.Restorer,
) (int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	if err := undo.RegisterFile(path); err != nil {
		return 0, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // path constructed from trusted internal data
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	rewritten, count := rewrite.ReplacePathInBytes(data, oldPath, newPath)
	rewritten, encodedCount := rewrite.ReplacePathInBytes(rewritten, oldEncodedDir, newEncodedDir)
	if err := rewrite.SafeWriteFile(path, rewritten, info.Mode()); err != nil {
		return 0, fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		return 0, fmt.Errorf("restore mtime %s: %w", path, err)
	}
	return count + encodedCount, nil
}

// projectDirectorySurface performs the actual rename: copying the encoded
// storage directory (and, unless RefsOnly, the on-disk project directory)
// to the new path and removing the originals. Its Plan reports one because
// Claude always relocates its encoded project directory; RefsOnly suppresses
// only the physical project-directory copy.
func (workspace *Workspace) projectDirectorySurface(req tool.MoveRequest) tool.Surface {
	return tool.Surface{
		Name: tool.SurfaceProjectDirectory,
		Plan: func(_ context.Context) (int, error) {
			return 1, nil
		},
		Apply: func(ctx context.Context, undo *tool.Restorer) (int, error) {
			return 1, workspace.applyProjectDirectoryMove(ctx, req, undo)
		},
	}
}

func (workspace *Workspace) applyProjectDirectoryMove(ctx context.Context, req tool.MoveRequest, undo *tool.Restorer) error {
	oldProjectDir := workspace.home.ProjectDir(req.OldPath)
	newProjectDir := workspace.home.ProjectDir(req.NewPath)

	if err := fsutil.CopyDir(ctx, oldProjectDir, newProjectDir, nil); err != nil {
		return fmt.Errorf("copy project directory: %w", err)
	}
	undo.RegisterUndo(func() error { return os.RemoveAll(newProjectDir) })

	if !req.RefsOnly {
		if err := fsutil.CopyDir(ctx, req.OldPath, req.NewPath, nil); err != nil {
			return fmt.Errorf("copy project on disk: %w", err)
		}
		undo.RegisterUndo(func() error { return os.RemoveAll(req.NewPath) })
	}

	if _, err := os.Stat(newProjectDir); err != nil {
		return fmt.Errorf("verify new project data dir: %w", err)
	}
	if !req.RefsOnly {
		if _, err := os.Stat(req.NewPath); err != nil {
			return fmt.Errorf("verify new project dir on disk: %w", err)
		}
	}

	if !req.RefsOnly {
		if err := removeAll(req.OldPath); err != nil {
			workspace.addMoveWarning(fmt.Sprintf("%v: %s: %v", ErrResidualSourceDir, req.OldPath, err))
		}
	}
	if err := removeAll(oldProjectDir); err != nil {
		workspace.addMoveWarning(fmt.Sprintf("old encoded project data directory still present: %s: %v", oldProjectDir, err))
	}
	return nil
}
