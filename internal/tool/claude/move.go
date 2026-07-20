package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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

// ErrEncodedDirStagingCollision is returned when the old project's encoded
// directory is identical to the new project's staging sibling (newEncodedDir
// + rewrite.StagingSuffix). Reconciling that staging path before promotion
// would delete the old project's real encoded data, not foreign debris —
// refused before any surface runs, mirroring internal/move's physical-path
// guard for the same pathological geometry.
var ErrEncodedDirStagingCollision = errors.New("refusing to move: old encoded project directory is new encoded directory's staging sibling")

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
	identity, err := workspace.resolveMoveIdentityState(req)
	if err != nil {
		return nil, fmt.Errorf("locate project: %w", err)
	}
	locatePath := identity.locatePath
	if err := checkEncodedDirCollision(workspace.home, req.OldPath, req.NewPath); err != nil {
		return nil, err
	}
	if !req.RefsOnly {
		if err := checkPhysicalDestination(req.OldPath, req.NewPath); err != nil {
			return nil, err
		}
	}
	workspace.clearMoveWarnings()
	locations, err := locateProjectForMove(workspace.home, locatePath)
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
	// "project-directory" locates the project via locatePath. The "sessions"
	// surface rewrites the sessions/*.json witness's cwd field, so it must run
	// last among the reference rewrites: the two-path witness acceptance depends
	// on later surfaces still seeing the old-path witness.
	var surfaces []tool.Surface
	surfaces = append(surfaces, workspace.historySurface(req))
	surfaces = append(surfaces, workspace.userWideSurfaces(req)...)
	surfaces = append(surfaces, workspace.sessionKeyedSurfaces(req, locatePath)...)
	surfaces = append(surfaces, workspace.configSurface(req))
	if req.DeepRewrite {
		surfaces = append(surfaces, workspace.transcriptsSurface(req, locatePath))
	}
	surfaces = append(surfaces,
		workspace.memorySurface(req, locatePath),
		workspace.sessionsSurface(req, locatePath),
		workspace.projectDirectorySurface(req, identity.identitySkipWarning),
	)
	return surfaces, nil
}

// moveIdentity preserves the old encoded directory as the locate path when
// witnesses have already flipped to NewPath before the directory move. A
// third-path witness remains a foreign-collision refusal.
type moveIdentity struct {
	locatePath          string
	identitySkipWarning string
}

func (workspace *Workspace) resolveMoveIdentity(req tool.MoveRequest) (string, error) {
	identity, err := workspace.resolveMoveIdentityState(req)
	if err != nil {
		return "", err
	}
	return identity.locatePath, nil
}

func (workspace *Workspace) resolveMoveIdentityState(req tool.MoveRequest) (moveIdentity, error) {
	claudeHome := workspace.home
	ctx := context.Background()

	oldDir := claudeHome.ProjectDir(req.OldPath)
	sessionUUIDs, err := collectProjectDirEntries(ctx, &ProjectLocations{}, oldDir)
	switch {
	case err == nil:
		skipWarning, err := verifyProjectMoveIdentity(claudeHome, req.OldPath, req.NewPath, sessionUUIDs)
		if err != nil {
			return moveIdentity{}, err
		}
		return moveIdentity{locatePath: req.OldPath, identitySkipWarning: skipWarning}, nil
	case !errors.Is(err, os.ErrNotExist):
		return moveIdentity{}, err
	}
	return moveIdentity{}, fmt.Errorf("%w: project directory not found: %s", tool.ErrProjectAbsent, oldDir)
}

// ResidualWarnings implements tool.Mover: content a move preserves verbatim
// and cannot fully rewrite.
func (workspace *Workspace) ResidualWarnings(req tool.MoveRequest) ([]string, error) {
	warnings := workspace.moveWarningSnapshot()
	ctx := context.Background()
	locatePath, err := workspace.resolveMoveIdentity(req)
	if err != nil {
		if errors.Is(err, tool.ErrProjectAbsent) {
			return warnings, nil
		}
		return warnings, fmt.Errorf("locate project: %w", err)
	}
	locations, err := locateProjectForMove(workspace.home, locatePath)
	if err != nil {
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

func checkPhysicalDestination(oldPath, newPath string) error {
	state, err := classifyDestination(oldPath, newPath)
	if err != nil {
		return fmt.Errorf("stat destination project directory %s: %w", newPath, err)
	}
	if state == destinationRefused {
		return fmt.Errorf(
			"refusing to move: destination %s exists while source %s remains; deleting the destination and re-running is always safe",
			newPath, oldPath,
		)
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
	if oldEncodedDir == newEncodedDir+rewrite.StagingSuffix {
		return fmt.Errorf("%w: %s would be deleted as %s's staging sibling", ErrEncodedDirStagingCollision, oldEncodedDir, newEncodedDir)
	}
	state, err := classifyDestination(oldEncodedDir, newEncodedDir)
	if err != nil {
		return fmt.Errorf("stat new project directory %s: %w", newEncodedDir, err)
	}
	if state == destinationRefused {
		return fmt.Errorf(
			"%w: destination %s already exists while source %s remains; deleting the destination and re-running is always safe",
			ErrEncodedDirCollision, newEncodedDir, oldEncodedDir,
		)
	}
	return nil
}

type destinationState int

const (
	destinationPromote destinationState = iota
	destinationConverged
	destinationRefused
)

func classifyDestination(source, destination string) (destinationState, error) {
	if _, err := os.Lstat(destination); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return destinationPromote, nil
		}
		return 0, fmt.Errorf("stat %s: %w", destination, err)
	}
	if _, err := os.Stat(source); err == nil {
		return destinationRefused, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Errorf("stat %s: %w", source, err)
	}
	return destinationConverged, nil
}

func removeStagingDir(destination, source string) error {
	staging := destination + rewrite.StagingSuffix
	info, err := os.Lstat(staging)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat staging directory %s: %w", staging, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("refusing to remove staging path %s: not a directory", staging)
	}
	if staging == source {
		return fmt.Errorf("refusing to remove move source as staging: %s", staging)
	}
	empty, err := isEmptyDir(staging)
	if err != nil {
		return fmt.Errorf("inspect staging directory %s: %w", staging, err)
	}
	if !empty {
		return fmt.Errorf("refusing to remove non-empty staging directory %s", staging)
	}
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("remove staging directory %s: %w", staging, err)
	}
	return nil
}

// isEmptyDir reports whether dir contains no entries. It reads at most one
// directory entry rather than the full listing, so probing a potentially
// huge foreign directory stays cheap.
func isEmptyDir(dir string) (empty bool, err error) {
	handle, openErr := os.Open(dir) //nolint:gosec // G304: dir is a caller-supplied, already-validated staging path
	if openErr != nil {
		return false, fmt.Errorf("open %s: %w", dir, openErr)
	}
	defer func() {
		if closeErr := handle.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close %s: %w", dir, closeErr))
		}
	}()

	names, readErr := handle.Readdirnames(1)
	if readErr != nil {
		if errors.Is(readErr, io.EOF) {
			return true, nil
		}
		return false, fmt.Errorf("read %s: %w", dir, readErr)
	}
	return len(names) == 0, nil
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
		Plan: func(ctx context.Context) (tool.SurfaceResult, error) {
			count, malformed, err := workspace.scanHistoryFile(ctx, req.OldPath)
			if err != nil {
				return tool.SurfaceResult{}, err
			}
			return tool.SurfaceResult{Count: count, Warnings: historyMalformedWarnings(malformed)}, nil
		},
		Apply: func(ctx context.Context, undo *tool.Restorer) (tool.SurfaceResult, error) {
			count, malformed, err := workspace.applyHistoryRewrite(ctx, req, undo)
			if err != nil {
				return tool.SurfaceResult{}, err
			}
			return tool.SurfaceResult{Count: count, Warnings: historyMalformedWarnings(malformed)}, nil
		},
	}
}

func historyMalformedWarnings(lines []int) []string {
	if len(lines) == 0 {
		return nil
	}
	lineLabels := make([]string, 0, len(lines))
	for _, line := range lines {
		lineLabels = append(lineLabels, fmt.Sprintf("line %d", line))
	}
	return []string{fmt.Sprintf("history.jsonl: %d malformed line(s) skipped (%s)", len(lines), strings.Join(lineLabels, ", "))}
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
		if rewrite.CountPathInBytesWithJSONEscape(line, oldPath) > 0 {
			count++
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return 0, nil, fmt.Errorf("scan history.jsonl: %w", scanErr)
	}
	return count, malformed, nil
}

func (workspace *Workspace) applyHistoryRewrite(
	ctx context.Context, req tool.MoveRequest, undo *tool.Restorer,
) (count int, malformed []int, err error) {
	historyFile := workspace.home.HistoryFile()
	if _, err := os.Stat(historyFile); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil, nil
		}
		return 0, nil, fmt.Errorf("stat %s: %w", historyFile, err)
	}
	if err := undo.RegisterFile(historyFile); err != nil {
		return 0, nil, fmt.Errorf("back up history.jsonl: %w", err)
	}
	original, err := os.ReadFile(historyFile) //nolint:gosec // path constructed from trusted internal data
	if err != nil {
		return 0, nil, fmt.Errorf("read history.jsonl: %w", err)
	}
	var rewritten bytes.Buffer
	count, malformed, err = StreamHistoryJSONL(ctx, bytes.NewReader(original), &rewritten, req.OldPath, req.NewPath)
	if err != nil {
		return 0, nil, fmt.Errorf("rewrite history.jsonl: %w", err)
	}
	if err := rewrite.SafeWriteFile(historyFile, rewritten.Bytes(), 0o600); err != nil {
		return 0, nil, fmt.Errorf("write history.jsonl: %w", err)
	}
	return count, malformed, nil
}

func (workspace *Workspace) sessionsSurface(req tool.MoveRequest, locatePath string) tool.Surface {
	return tool.Surface{
		Name: categorySessions,
		Plan: func(ctx context.Context) (tool.SurfaceResult, error) {
			locations, err := locateProjectForMove(workspace.home, locatePath)
			if err != nil {
				return tool.SurfaceResult{}, fmt.Errorf("locate project: %w", err)
			}
			count := 0
			for _, sessionFilePath := range locations.SessionFiles {
				if err := ctx.Err(); err != nil {
					return tool.SurfaceResult{}, err
				}
				data, err := os.ReadFile(sessionFilePath) //nolint:gosec // path constructed from trusted internal data
				if err != nil {
					return tool.SurfaceResult{}, fmt.Errorf("read session file %s: %w", sessionFilePath, err)
				}
				_, changed, err := RewriteSessionFile(data, req.OldPath, req.NewPath)
				if err != nil {
					return tool.SurfaceResult{}, fmt.Errorf("analyze session file %s: %w", sessionFilePath, err)
				}
				if changed {
					count++
				}
			}
			return tool.SurfaceResult{Count: count, Warnings: malformedSessionWarnings(locations.MalformedSessionFiles)}, nil
		},
		Apply: func(ctx context.Context, undo *tool.Restorer) (tool.SurfaceResult, error) {
			locations, err := locateProjectForMove(workspace.home, locatePath)
			if err != nil {
				return tool.SurfaceResult{}, fmt.Errorf("locate project: %w", err)
			}
			count := 0
			for _, sessionFilePath := range locations.SessionFiles {
				if err := ctx.Err(); err != nil {
					return tool.SurfaceResult{}, err
				}
				if err := undo.RegisterFile(sessionFilePath); err != nil {
					return tool.SurfaceResult{}, err
				}
				original, err := os.ReadFile(sessionFilePath) //nolint:gosec // path constructed from trusted internal data
				if err != nil {
					return tool.SurfaceResult{}, err
				}
				rewritten, changed, err := RewriteSessionFile(original, req.OldPath, req.NewPath)
				if err != nil {
					return tool.SurfaceResult{}, fmt.Errorf("rewrite session file %s: %w", sessionFilePath, err)
				}
				info, err := os.Stat(sessionFilePath)
				if err != nil {
					return tool.SurfaceResult{}, err
				}
				if err := rewrite.SafeWriteFile(sessionFilePath, rewritten, info.Mode()); err != nil {
					return tool.SurfaceResult{}, fmt.Errorf("write session file %s: %w", sessionFilePath, err)
				}
				if changed {
					count++
				}
			}
			return tool.SurfaceResult{Count: count, Warnings: malformedSessionWarnings(locations.MalformedSessionFiles)}, nil
		},
	}
}

func malformedSessionWarnings(paths []string) []string {
	warnings := make([]string, 0, len(paths))
	for _, path := range paths {
		warnings = append(warnings, fmt.Sprintf("sessions/%s: malformed JSON preserved unchanged", filepath.Base(path)))
	}
	return warnings
}

func (workspace *Workspace) userWideSurfaces(req tool.MoveRequest) []tool.Surface {
	var surfaces []tool.Surface
	for target := range UserWideRewriteTargets() {
		path := target.RewritePath(workspace.home)
		surfaces = append(surfaces, tool.Surface{
			Name: target.Name,
			Plan: func(ctx context.Context) (tool.SurfaceResult, error) {
				if err := ctx.Err(); err != nil {
					return tool.SurfaceResult{}, err
				}
				data, err := os.ReadFile(path) //nolint:gosec // path constructed from trusted internal data
				if err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						return tool.SurfaceResult{}, nil
					}
					return tool.SurfaceResult{}, fmt.Errorf("read %s: %w", path, err)
				}
				_, count := rewrite.ReplacePathInBytes(data, req.OldPath, req.NewPath)
				return tool.SurfaceResult{Count: count}, nil
			},
			Apply: func(_ context.Context, undo *tool.Restorer) (tool.SurfaceResult, error) {
				if _, err := os.Stat(path); err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						return tool.SurfaceResult{}, nil
					}
					return tool.SurfaceResult{}, fmt.Errorf("stat %s: %w", path, err)
				}
				count, err := rewriteTracked(path, req.OldPath, req.NewPath, undo)
				return tool.SurfaceResult{Count: count}, err
			},
		})
	}
	return surfaces
}

func (workspace *Workspace) sessionKeyedSurfaces(req tool.MoveRequest, locatePath string) []tool.Surface {
	var surfaces []tool.Surface
	for group := range SessionKeyedGroups() {
		surfaces = append(surfaces, tool.Surface{
			Name: group.Name,
			Plan: func(ctx context.Context) (tool.SurfaceResult, error) {
				locations, err := locateProjectForMove(workspace.home, locatePath)
				if err != nil {
					return tool.SurfaceResult{}, fmt.Errorf("locate project: %w", err)
				}
				count := 0
				for _, path := range group.Files(locations) {
					if err := ctx.Err(); err != nil {
						return tool.SurfaceResult{}, err
					}
					if group.SidecarFilter != nil && group.SidecarFilter(filepath.Base(path)) {
						continue
					}
					data, err := os.ReadFile(path) //nolint:gosec // path from trusted ProjectLocations
					if err != nil {
						return tool.SurfaceResult{}, fmt.Errorf("read %s file %s: %w", group.Name, path, err)
					}
					_, n := rewrite.ReplacePathInBytes(data, req.OldPath, req.NewPath)
					count += n
				}
				return tool.SurfaceResult{Count: count}, nil
			},
			Apply: func(ctx context.Context, undo *tool.Restorer) (tool.SurfaceResult, error) {
				locations, err := locateProjectForMove(workspace.home, locatePath)
				if err != nil {
					return tool.SurfaceResult{}, fmt.Errorf("locate project: %w", err)
				}
				count := 0
				for _, path := range group.Files(locations) {
					if err := ctx.Err(); err != nil {
						return tool.SurfaceResult{}, err
					}
					if group.SidecarFilter != nil && group.SidecarFilter(filepath.Base(path)) {
						continue
					}
					info, err := os.Stat(path)
					if err != nil {
						return tool.SurfaceResult{}, err
					}
					n, err := rewriteTracked(path, req.OldPath, req.NewPath, undo)
					if err != nil {
						return tool.SurfaceResult{}, fmt.Errorf("rewrite %s %s: %w", group.Name, path, err)
					}
					if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
						return tool.SurfaceResult{}, fmt.Errorf("restore mtime %s: %w", path, err)
					}
					count += n
				}
				return tool.SurfaceResult{Count: count}, nil
			},
		})
	}
	return surfaces
}

func (workspace *Workspace) configSurface(req tool.MoveRequest) tool.Surface {
	return tool.Surface{
		Name: categoryConfig,
		Plan: func(ctx context.Context) (tool.SurfaceResult, error) {
			if err := ctx.Err(); err != nil {
				return tool.SurfaceResult{}, err
			}
			data, err := os.ReadFile(workspace.home.ConfigFile)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return tool.SurfaceResult{}, nil
				}
				return tool.SurfaceResult{}, fmt.Errorf("read config file: %w", err)
			}
			_, rekeyed, err := RewriteUserConfig(data, req.OldPath, req.NewPath)
			if err != nil {
				return tool.SurfaceResult{}, fmt.Errorf("analyze config file: %w", err)
			}
			if rekeyed {
				return tool.SurfaceResult{Count: 1}, nil
			}
			return tool.SurfaceResult{}, nil
		},
		Apply: func(_ context.Context, undo *tool.Restorer) (tool.SurfaceResult, error) {
			configFile := workspace.home.ConfigFile
			if _, err := os.Stat(configFile); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return tool.SurfaceResult{}, nil
				}
				return tool.SurfaceResult{}, fmt.Errorf("stat %s: %w", configFile, err)
			}
			if err := undo.RegisterFile(configFile); err != nil {
				return tool.SurfaceResult{}, fmt.Errorf("read config file for backup: %w", err)
			}
			original, err := os.ReadFile(configFile) //nolint:gosec // path constructed from trusted internal data
			if err != nil {
				return tool.SurfaceResult{}, fmt.Errorf("read config file: %w", err)
			}
			rewritten, rekeyed, err := RewriteUserConfig(original, req.OldPath, req.NewPath)
			if err != nil {
				return tool.SurfaceResult{}, fmt.Errorf("rewrite config file: %w", err)
			}
			if err := rewrite.SafeWriteFile(configFile, rewritten, 0o600); err != nil {
				return tool.SurfaceResult{}, fmt.Errorf("write config file: %w", err)
			}
			if rekeyed {
				return tool.SurfaceResult{Count: 1}, nil
			}
			return tool.SurfaceResult{}, nil
		},
	}
}

func (workspace *Workspace) transcriptsSurface(req tool.MoveRequest, locatePath string) tool.Surface {
	return tool.Surface{
		Name: "transcripts",
		Plan: func(ctx context.Context) (tool.SurfaceResult, error) {
			locations, err := locateProjectForMove(workspace.home, locatePath)
			if err != nil {
				return tool.SurfaceResult{}, fmt.Errorf("locate project: %w", err)
			}
			transcriptPaths, err := TranscriptFiles(ctx, locations.ProjectDir)
			if err != nil {
				return tool.SurfaceResult{}, err
			}
			oldEncodedDir := workspace.home.ProjectDir(req.OldPath)
			newEncodedDir := workspace.home.ProjectDir(req.NewPath)
			total := 0
			for _, transcriptPath := range transcriptPaths {
				if err := ctx.Err(); err != nil {
					return tool.SurfaceResult{}, err
				}
				data, err := os.ReadFile(transcriptPath) //nolint:gosec // path constructed from trusted internal data
				if err != nil {
					return tool.SurfaceResult{}, fmt.Errorf("read transcript %s: %w", transcriptPath, err)
				}
				_, count := rewrite.ReplacePathInBytes(data, req.OldPath, req.NewPath)
				total += count
				_, encodedCount := rewrite.ReplacePathInBytes(data, oldEncodedDir, newEncodedDir)
				total += encodedCount
			}
			return tool.SurfaceResult{Count: total}, nil
		},
		Apply: func(ctx context.Context, undo *tool.Restorer) (tool.SurfaceResult, error) {
			// Project-directory runs last, so transcripts are rewritten in
			// place under the old encoded directory before its later copy.
			locations, err := locateProjectForMove(workspace.home, locatePath)
			if err != nil {
				return tool.SurfaceResult{}, fmt.Errorf("locate project: %w", err)
			}
			transcriptPaths, err := TranscriptFiles(ctx, locations.ProjectDir)
			if err != nil {
				return tool.SurfaceResult{}, err
			}
			oldEncodedDir := workspace.home.ProjectDir(req.OldPath)
			newEncodedDir := workspace.home.ProjectDir(req.NewPath)
			total := 0
			for _, transcriptPath := range transcriptPaths {
				if err := ctx.Err(); err != nil {
					return tool.SurfaceResult{}, err
				}
				n, err := rewriteTwicePreservingMtime(transcriptPath, req.OldPath, req.NewPath, oldEncodedDir, newEncodedDir, undo)
				if err != nil {
					return tool.SurfaceResult{}, fmt.Errorf("rewrite transcript %s: %w", transcriptPath, err)
				}
				total += n
			}
			return tool.SurfaceResult{Count: total}, nil
		},
	}
}

func (workspace *Workspace) memorySurface(req tool.MoveRequest, locatePath string) tool.Surface {
	return tool.Surface{
		Name: "memory",
		Plan: func(ctx context.Context) (tool.SurfaceResult, error) {
			locations, err := locateProjectForMove(workspace.home, locatePath)
			if err != nil {
				return tool.SurfaceResult{}, fmt.Errorf("locate project: %w", err)
			}
			oldEncodedDir := workspace.home.ProjectDir(req.OldPath)
			newEncodedDir := workspace.home.ProjectDir(req.NewPath)
			total := 0
			for _, memoryFilePath := range locations.MemoryFiles {
				if err := ctx.Err(); err != nil {
					return tool.SurfaceResult{}, err
				}
				data, err := os.ReadFile(memoryFilePath) //nolint:gosec // path constructed from trusted internal data
				if err != nil {
					return tool.SurfaceResult{}, fmt.Errorf("read memory file %s: %w", memoryFilePath, err)
				}
				_, count := rewrite.ReplacePathInBytes(data, req.OldPath, req.NewPath)
				total += count
				_, encodedCount := rewrite.ReplacePathInBytes(data, oldEncodedDir, newEncodedDir)
				total += encodedCount
			}
			return tool.SurfaceResult{Count: total}, nil
		},
		Apply: func(ctx context.Context, undo *tool.Restorer) (tool.SurfaceResult, error) {
			locations, err := locateProjectForMove(workspace.home, locatePath)
			if err != nil {
				return tool.SurfaceResult{}, fmt.Errorf("locate project: %w", err)
			}
			oldEncodedDir := workspace.home.ProjectDir(req.OldPath)
			newEncodedDir := workspace.home.ProjectDir(req.NewPath)
			total := 0
			for _, memoryFilePath := range locations.MemoryFiles {
				if err := ctx.Err(); err != nil {
					return tool.SurfaceResult{}, err
				}
				n, err := rewriteTwicePreservingMtime(memoryFilePath, req.OldPath, req.NewPath, oldEncodedDir, newEncodedDir, undo)
				if err != nil {
					return tool.SurfaceResult{}, fmt.Errorf("rewrite memory file %s: %w", memoryFilePath, err)
				}
				total += n
			}
			return tool.SurfaceResult{Count: total}, nil
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
func (workspace *Workspace) projectDirectorySurface(req tool.MoveRequest, identitySkipWarning string) tool.Surface {
	return tool.Surface{
		Name: tool.SurfaceProjectDirectory,
		Plan: func(_ context.Context) (tool.SurfaceResult, error) {
			destinations := []string{workspace.home.ProjectDir(req.NewPath)}
			if !req.RefsOnly {
				destinations = append(destinations, req.NewPath)
			}
			warnings, err := strandedStagingWarnings(destinations)
			if err != nil {
				return tool.SurfaceResult{}, err
			}
			if identitySkipWarning != "" {
				warnings = append(warnings, identitySkipWarning)
			}
			return tool.SurfaceResult{Count: 1, Warnings: warnings}, nil
		},
		Apply: func(ctx context.Context, undo *tool.Restorer) (tool.SurfaceResult, error) {
			err := workspace.applyProjectDirectoryMove(ctx, req, undo)
			// The move runs either the plan or the apply path, never both, so
			// carrying the skip here mirrors the plan and keeps a witnessless
			// --apply from proceeding silently.
			var warnings []string
			if identitySkipWarning != "" {
				warnings = append(warnings, identitySkipWarning)
			}
			return tool.SurfaceResult{Count: 1, Warnings: warnings}, err
		},
	}
}

func strandedStagingWarnings(destinations []string) ([]string, error) {
	var warnings []string
	for _, destination := range destinations {
		staging := destination + rewrite.StagingSuffix
		// Lstat, matching removeStagingDir's apply-time probe, so the plan and
		// the apply agree on what sits at the staging path: a symlink there is
		// the entry itself, not whatever it points at.
		if _, err := os.Lstat(staging); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("stat staging path %s: %w", staging, err)
		}
		warnings = append(warnings, "stranded staging path requires apply handling: "+staging)
	}
	return warnings, nil
}

func (workspace *Workspace) applyProjectDirectoryMove(ctx context.Context, req tool.MoveRequest, undo *tool.Restorer) error {
	oldProjectDir := workspace.home.ProjectDir(req.OldPath)
	newProjectDir := workspace.home.ProjectDir(req.NewPath)

	if err := removeStagingDir(newProjectDir, oldProjectDir); err != nil {
		return fmt.Errorf("reconcile project data staging: %w", err)
	}
	if err := promoteIfNeeded(ctx, oldProjectDir, newProjectDir, undo); err != nil {
		return fmt.Errorf("copy project directory: %w", err)
	}

	if !req.RefsOnly {
		if err := removeStagingDir(req.NewPath, req.OldPath); err != nil {
			return fmt.Errorf("reconcile on-disk project staging: %w", err)
		}
		if err := promoteIfNeeded(ctx, req.OldPath, req.NewPath, undo); err != nil {
			return fmt.Errorf("copy project on disk: %w", err)
		}
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

func promoteIfNeeded(ctx context.Context, source, destination string, undo *tool.Restorer) error {
	state, err := classifyDestination(source, destination)
	if err != nil {
		return err
	}
	switch state {
	case destinationConverged:
		return nil
	case destinationRefused:
		return fmt.Errorf(
			"destination %s already exists while source %s remains; deleting the destination and re-running is always safe",
			destination, source,
		)
	default:
		return rewrite.PromoteDir(ctx, source, destination, undo, fsutil.CopyDir)
	}
}
