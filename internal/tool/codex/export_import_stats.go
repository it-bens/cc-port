package codex

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/sqlrewrite"
	"github.com/it-bens/cc-port/internal/tool"
)

const (
	codexHomeKey        = "{{CODEX_HOME}}"
	codexProjectPathKey = "{{CODEX_PROJECT_PATH}}"
	codexHistoryFile    = "history.jsonl"
	sessionIndexFile    = "session_index.jsonl"
	maxCodexJSONLLine   = 16 << 20
	importFilePerm      = os.FileMode(0o600)
	archiveSessionRoot  = "sessions/"
	archiveArchivedRoot = "archived-sessions/"
	backfillStateTable  = "backfill_state"
)

// UnknownArchiveEntryError identifies a Codex-relative entry that the adapter
// does not own. Unknown input is never silently discarded.
type UnknownArchiveEntryError struct{ Name string }

func (err *UnknownArchiveEntryError) Error() string {
	return fmt.Sprintf("unknown codex archive entry: %q", err.Name)
}

type rolloutIdentity struct {
	ThreadID string
	CWDs     []string
	EraA     bool
}

type historyRecord struct {
	SessionID string `json:"session_id"`
	Timestamp any    `json:"ts"`
	Text      string `json:"text"`
}

type threadSidecar struct {
	ThreadID   string      `json:"thread_id"`
	ArchivedAt *int64      `json:"archived_at"`
	Title      *string     `json:"title"`
	Git        *sidecarGit `json:"git"`
}

type sidecarGit struct {
	SHA       *string `json:"sha"`
	Branch    *string `json:"branch"`
	OriginURL *string `json:"origin_url"`
}

// Placeholders declares Codex's machine-local home and project anchors.
func (workspace *Workspace) Placeholders(project string, _ map[string]bool) ([]manifest.Placeholder, error) {
	known, err := workspace.knowsProject(context.Background(), project)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, workspace.projectAbsenceError()
	}
	return []manifest.Placeholder{
		{Key: codexProjectPathKey, Original: project},
		{Key: codexHomeKey, Original: workspace.home.Dir},
	}, nil
}

// Export writes selected project state under the Codex archive namespace.
func (workspace *Workspace) Export(ctx context.Context, project string, selected map[string]bool, sink *archive.Sink) (tool.ExportResult, error) {
	result := tool.ExportResult{Categories: make(map[string][]tool.ArchiveEntry)}
	known, err := workspace.knowsProject(ctx, project)
	if err != nil {
		return result, err
	}
	if !known {
		return result, workspace.projectAbsenceError()
	}
	if err := workspace.recordProfileSQLiteHomeWarning(&result); err != nil {
		return result, err
	}
	rollouts, eraA, err := workspace.projectRollouts(ctx, project)
	if err != nil {
		return result, err
	}
	result.Skipped = append(result.Skipped, eraA...)
	if len(eraA) > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%d era-A rollout(s) could not be associated and were omitted: %s",
			len(eraA), strings.Join(eraA, ", "),
		))
	}

	rolloutLines, rolloutThreadIDs, err := workspace.readAndIdentifyRollouts(ctx, project, rollouts)
	if err != nil {
		return result, err
	}
	threadIDs, err := workspace.projectThreadIDSet(ctx, project, rolloutThreadIDs)
	if err != nil {
		return result, err
	}
	if selected[categorySessions] {
		for _, rollout := range rollouts {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			name, err := workspace.archiveRolloutName(rollout)
			if err != nil {
				return result, err
			}
			info, err := os.Stat(rollout)
			if err != nil {
				return result, fmt.Errorf("stat rollout %s: %w", rollout, err)
			}
			written, err := sink.WriteBytes(name, appendJSONLLines(rolloutLines[rollout]), info.ModTime())
			if err != nil {
				return result, fmt.Errorf("write %s: %w", name, err)
			}
			recordCodexEntry(&result, categorySessions, written)
		}
		if err := workspace.exportSessionIndex(ctx, sink, &result, threadIDs); err != nil {
			return result, err
		}
		if err := workspace.exportThreadSidecar(sink, &result, threadIDs); err != nil {
			return result, err
		}
	}
	if selected[categoryHistory] {
		if err := workspace.exportHistory(ctx, sink, &result, threadIDs); err != nil {
			return result, err
		}
	}
	return result, nil
}

func recordCodexEntry(result *tool.ExportResult, category string, written archive.WrittenEntry) {
	result.Categories[category] = append(result.Categories[category], tool.ArchiveEntry{ArchivePath: written.Name, Size: written.Size})
}

// recordProfileSQLiteHomeWarning appends profileSQLiteHomeWarning's result
// to result.Warnings when non-empty, mirroring the same check move's
// ResidualWarnings makes: Export has no separate residual-scan step, so it
// must surface a divergent profile overlay inline rather than silently
// archiving only what base config.toml's SQLiteDir resolves to.
func (workspace *Workspace) recordProfileSQLiteHomeWarning(result *tool.ExportResult) error {
	warning, err := profileSQLiteHomeWarning(workspace.home, workspace.getenv)
	if err != nil {
		return err
	}
	if warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}
	return nil
}

func addRolloutThreadID(ids map[string]struct{}, threadID string) {
	ids[threadID] = struct{}{}
}

// readAndIdentifyRollouts reads every rollout in rollouts (projectRollouts's
// matches; projectRollouts already read every discovered rollout once to
// classify eraA/matches, so this is the second read, of matches only) and
// caches its lines for Export's later archive-write pass so that read is
// not repeated a third time. It returns those cached lines alongside the
// union of thread IDs their session_meta/turn_context lines carry for project.
func (workspace *Workspace) readAndIdentifyRollouts(
	ctx context.Context, project string, rollouts []string,
) (rolloutLines map[string][][]byte, rolloutThreadIDs map[string]struct{}, err error) {
	rolloutLines = make(map[string][][]byte, len(rollouts))
	rolloutThreadIDs = make(map[string]struct{}, len(rollouts))
	for _, rollout := range rollouts {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		lines, err := readRolloutLines(rollout)
		if err != nil {
			return nil, nil, fmt.Errorf("read rollout %s: %w", rollout, err)
		}
		identity, err := rolloutProjectIdentity(lines, project)
		if err != nil {
			return nil, nil, fmt.Errorf("identify rollout %s: %w", rollout, err)
		}
		rolloutLines[rollout] = lines
		addRolloutThreadID(rolloutThreadIDs, identity.ThreadID)
	}
	return rolloutLines, rolloutThreadIDs, nil
}

func (workspace *Workspace) projectRollouts(ctx context.Context, project string) (matches, eraA []string, err error) {
	files, err := discoverRolloutFiles(workspace.home)
	if err != nil {
		return nil, nil, err
	}
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		lines, err := readRolloutLines(path)
		if err != nil {
			return nil, nil, fmt.Errorf("read rollout %s: %w", path, err)
		}
		identity, err := rolloutProjectIdentity(lines, project)
		if err != nil {
			return nil, nil, fmt.Errorf("identify rollout %s: %w", path, err)
		}
		if identity.EraA {
			eraA = append(eraA, path)
			continue
		}
		belongsToProject, err := identityMatchesProject(identity, project)
		if err != nil {
			return nil, nil, fmt.Errorf("match rollout %s: %w", path, err)
		}
		if belongsToProject {
			matches = append(matches, path)
		}
	}
	return matches, eraA, nil
}

func rolloutProjectIdentity(lines [][]byte, project string) (rolloutIdentity, error) {
	identity := rolloutIdentityFromLines(lines)
	if !identity.EraA && identity.ThreadID == "" {
		belongsToProject, err := identityMatchesProject(identity, project)
		if err != nil {
			return rolloutIdentity{}, err
		}
		if belongsToProject {
			return rolloutIdentity{}, fmt.Errorf("associated rollout has no session id")
		}
	}
	return identity, nil
}

func rolloutIdentityFromLines(lines [][]byte) rolloutIdentity {
	identity := rolloutIdentity{EraA: true}
	for _, line := range lines {
		var record struct {
			Type    string `json:"type"`
			Payload struct {
				ID        string `json:"id"`
				SessionID string `json:"session_id"`
				CWD       string `json:"cwd"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			continue
		}
		if record.Type != rolloutTypeSessionMeta && record.Type != rolloutTypeTurnContext {
			continue
		}
		identity.EraA = false
		if record.Payload.CWD != "" {
			identity.CWDs = append(identity.CWDs, record.Payload.CWD)
		}
		if identity.ThreadID == "" {
			identity.ThreadID = record.Payload.SessionID
			if identity.ThreadID == "" {
				identity.ThreadID = record.Payload.ID
			}
		}
	}
	return identity
}

func identityMatchesProject(identity rolloutIdentity, project string) (bool, error) {
	for _, cwd := range identity.CWDs {
		matched, err := pathMatchesProject(cwd, project)
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

// canonicalizePath resolves symlinks in path and falls back to a lexical
// filepath.Clean ONLY when path does not exist — the one degradation this
// function performs, since a recorded cwd for a since-deleted project can
// only be compared lexically. Every other EvalSymlinks failure (permission
// denied, a symlink loop, an I/O error) is a genuine failure, not absence,
// and is returned rather than silently degraded to a possibly-wrong lexical
// comparison.
func canonicalizePath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return filepath.Clean(path), nil
	}
	return "", fmt.Errorf("canonicalize %s: %w", path, err)
}

// pathMatchesProject compares cwd and project after canonicalizePath
// resolves both through any symlink, so a session recorded through a
// symlink-aliased cwd (Codex stores config.cwd() verbatim, uncanonicalized)
// still matches the project cc-port resolved through
// fsutil.ResolveExistingAncestor (spec §5.1). The equality-or-/-boundary-
// prefix breadth is unchanged; only the operands are canonicalized first.
func pathMatchesProject(cwd, project string) (bool, error) {
	canonicalCWD, err := canonicalizePath(cwd)
	if err != nil {
		return false, err
	}
	canonicalProject, err := canonicalizePath(project)
	if err != nil {
		return false, err
	}
	return canonicalCWD == canonicalProject || strings.HasPrefix(canonicalCWD, canonicalProject+"/"), nil
}

func appendJSONLLines(lines [][]byte) []byte {
	var data bytes.Buffer
	for _, line := range lines {
		data.Write(line)
		data.WriteByte('\n')
	}
	return data.Bytes()
}

func (workspace *Workspace) archiveRolloutName(path string) (string, error) {
	sessionsRoot := filepath.Join(workspace.home.Dir, sessionsSubdir)
	archivedRoot := filepath.Join(workspace.home.Dir, archivedSessionsSubdir)
	if relative, ok := relativeWithin(sessionsRoot, path); ok {
		return archiveSessionRoot + filepath.ToSlash(relative), nil
	}
	if relative, ok := relativeWithin(archivedRoot, path); ok {
		return archiveArchivedRoot + filepath.ToSlash(relative), nil
	}
	return "", fmt.Errorf("rollout %s is outside Codex rollout roots", path)
}

func relativeWithin(root, path string) (string, bool) {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return relative, true
}

func (workspace *Workspace) exportSessionIndex(
	ctx context.Context, sink *archive.Sink, result *tool.ExportResult, threadIDs map[string]struct{},
) error {
	path := filepath.Join(workspace.home.Dir, sessionIndexFile)
	file, err := os.Open(path) //nolint:gosec // G304: path derived from resolved Codex home
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open session index: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat session index: %w", err)
	}
	malformed := 0
	written, err := sink.WriteJSONL(ctx, "session-index/"+sessionIndexFile, file, maxCodexJSONLLine, func(line []byte) []byte {
		var record struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(line, &record) != nil {
			malformed++
			return nil
		}
		if _, ok := threadIDs[record.ID]; !ok {
			return nil
		}
		return sink.ApplyPlaceholders(line)
	}, info.ModTime())
	if err != nil {
		return fmt.Errorf("write session index: %w", err)
	}
	if malformed > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%d malformed line(s) in %s were omitted during export", malformed, sessionIndexFile,
		))
	}
	recordCodexEntry(result, categorySessions, written)
	return nil
}

func (workspace *Workspace) exportThreadSidecar(sink *archive.Sink, result *tool.ExportResult, threadIDs map[string]struct{}) error {
	if len(threadIDs) == 0 {
		return nil
	}
	var output bytes.Buffer
	databases, err := workspace.stateDatabasesNewestFirst()
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(threadIDs))
	for _, path := range databases {
		database, err := openReadOnlyDatabase(path)
		if err != nil {
			return err
		}
		rows, queryErr := database.QueryContext(
			context.Background(), `SELECT id, archived_at, title, git_sha, git_branch, git_origin_url FROM threads`,
		)
		if queryErr != nil {
			_ = database.Close()
			return fmt.Errorf("read threads sidecar from %s: %w", path, queryErr)
		}
		for rows.Next() {
			var id string
			var archivedAt sql.NullInt64
			var title, sha, branch, origin sql.NullString
			if err := rows.Scan(&id, &archivedAt, &title, &sha, &branch, &origin); err != nil {
				_ = rows.Close()
				_ = database.Close()
				return fmt.Errorf("read thread sidecar row: %w", err)
			}
			if _, ok := threadIDs[id]; !ok {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			line, err := json.Marshal(map[string]any{
				"thread_id": id, "archived_at": nullableInt64(archivedAt), "title": nullableString(title),
				"git": map[string]any{
					"sha": nullableString(sha), "branch": nullableString(branch), "origin_url": nullableString(origin),
				},
			})
			if err != nil {
				_ = rows.Close()
				_ = database.Close()
				return fmt.Errorf("marshal threads sidecar row: %w", err)
			}
			output.Write(line)
			output.WriteByte('\n')
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			_ = database.Close()
			return fmt.Errorf("iterate threads sidecar rows: %w", err)
		}
		_ = rows.Close()
		_ = database.Close()
	}
	written, err := sink.WriteBytes("threads-sidecar.jsonl", output.Bytes(), time.Time{})
	if err != nil {
		return fmt.Errorf("write threads sidecar: %w", err)
	}
	recordCodexEntry(result, categorySessions, written)
	return nil
}

func nullableString(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}

func nullableInt64(value sql.NullInt64) any {
	if value.Valid {
		return value.Int64
	}
	return nil
}

func (workspace *Workspace) exportHistory(ctx context.Context, sink *archive.Sink, result *tool.ExportResult, threadIDs map[string]struct{}) error {
	path := filepath.Join(workspace.home.Dir, codexHistoryFile)
	file, err := os.Open(path) //nolint:gosec // G304: path derived from resolved Codex home
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open history: %w", err)
	}
	defer func() { _ = file.Close() }()
	malformed := 0
	written, err := sink.WriteJSONL(ctx, "history/"+codexHistoryFile, file, maxCodexJSONLLine, func(line []byte) []byte {
		var record historyRecord
		if json.Unmarshal(line, &record) != nil {
			malformed++
			return nil
		}
		if _, ok := threadIDs[record.SessionID]; !ok {
			return nil
		}
		return sink.ApplyPlaceholders(line)
	}, time.Time{})
	if err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	if malformed > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%d malformed line(s) in %s were omitted during export", malformed, codexHistoryFile,
		))
	}
	recordCodexEntry(result, categoryHistory, written)
	return nil
}

// PreflightDirs returns every parent that import may mutate.
func (workspace *Workspace) PreflightDirs(string) []string {
	return []string{
		filepath.Join(workspace.home.Dir, sessionsSubdir),
		filepath.Join(workspace.home.Dir, archivedSessionsSubdir),
		workspace.home.Dir,
	}
}

// ImplicitAnchors resolves Codex's declared home and project anchors on the destination.
func (workspace *Workspace) ImplicitAnchors(project string) (map[string]string, error) {
	return map[string]string{codexHomeKey: workspace.home.Dir, codexProjectPathKey: project}, nil
}

// Stage routes regular rollout files for atomic promotion and retains merge
// inputs for Finalize. Imported rollouts remain decompressed .jsonl files:
// Codex reads both forms, and this preserves the archive's portable bytes.
func (workspace *Workspace) Stage(_ context.Context, project string, entry archive.Entry, resolutions map[string]string) ([]archive.Staged, error) {
	resolutions = codexImportResolutions(project, resolutions)
	switch {
	case strings.HasPrefix(entry.Name, archiveSessionRoot):
		return workspace.stageRollout(
			entry, strings.TrimPrefix(entry.Name, archiveSessionRoot), filepath.Join(workspace.home.Dir, sessionsSubdir), resolutions,
		)
	case strings.HasPrefix(entry.Name, archiveArchivedRoot):
		return workspace.stageRollout(
			entry, strings.TrimPrefix(entry.Name, archiveArchivedRoot), filepath.Join(workspace.home.Dir, archivedSessionsSubdir), resolutions,
		)
	case entry.Name == "session-index/"+sessionIndexFile:
		resolved, err := archive.ResolveEntryBytes(entry, resolutions)
		if err != nil {
			return nil, err
		}
		workspace.indexAppends = append(workspace.indexAppends, resolved)
		return nil, nil
	case entry.Name == "threads-sidecar.jsonl":
		resolved, err := archive.ResolveEntryBytes(entry, resolutions)
		if err != nil {
			return nil, err
		}
		workspace.sidecarAppends = append(workspace.sidecarAppends, resolved)
		return nil, nil
	case entry.Name == "history/"+codexHistoryFile:
		resolved, err := archive.ResolveEntryBytes(entry, resolutions)
		if err != nil {
			return nil, err
		}
		workspace.historyAppends = append(workspace.historyAppends, resolved)
		return nil, nil
	default:
		return nil, &UnknownArchiveEntryError{Name: entry.Name}
	}
}

func codexImportResolutions(project string, resolutions map[string]string) map[string]string {
	resolved := make(map[string]string, len(resolutions)+1)
	for key, value := range resolutions {
		resolved[key] = value
	}
	resolved[codexProjectPathKey] = project
	return resolved
}

func (workspace *Workspace) stageRollout(entry archive.Entry, relative, root string, resolutions map[string]string) ([]archive.Staged, error) {
	if !validArchiveRolloutName(relative, strings.HasPrefix(entry.Name, archiveArchivedRoot)) {
		return nil, &UnknownArchiveEntryError{Name: entry.Name}
	}
	staged, _, err := archive.StageSibling(root, filepath.FromSlash(relative), entry, resolutions, importFilePerm, entry.Modified)
	if err != nil {
		if staged.Temp != "" {
			return []archive.Staged{staged}, err
		}
		return nil, err
	}
	workspace.rolloutsStaged = true
	return []archive.Staged{staged}, nil
}

func validArchiveRolloutName(relative string, archived bool) bool {
	segments := strings.Split(relative, "/")
	if archived {
		if len(segments) != 1 {
			return false
		}
	} else if len(segments) != 4 {
		return false
	}
	if !archived {
		for index, width := range []int{4, 2, 2} {
			if len(segments[index]) != width {
				return false
			}
			for _, char := range segments[index] {
				if char < '0' || char > '9' {
					return false
				}
			}
		}
	}
	filename := segments[len(segments)-1]
	return strings.HasPrefix(filename, "rollout-") && strings.HasSuffix(filename, ".jsonl")
}

// Finalize appends deduplicated line stores without replacing their inodes,
// then applies sidecar rows only to existing threads through sqlrewrite.
func (workspace *Workspace) Finalize(ctx context.Context, project string, _ *archive.StagedSet) ([]string, error) {
	if project == "" {
		return nil, fmt.Errorf("finalize Codex import: target project is empty")
	}
	if err := appendUniqueHistory(ctx, filepath.Join(workspace.home.Dir, codexHistoryFile), workspace.historyAppends); err != nil {
		return nil, err
	}
	if err := appendUniqueExact(ctx, filepath.Join(workspace.home.Dir, sessionIndexFile), workspace.indexAppends); err != nil {
		return nil, err
	}
	sidecars, err := workspace.parseThreadSidecars()
	if err != nil {
		return nil, err
	}
	// Discover only when a consumer needs the list. An import with neither
	// sidecar rows to apply nor staged rollouts to re-arm must not start
	// depending on the state-DB directory being readable.
	var databases []string
	if len(sidecars) > 0 || workspace.rolloutsStaged {
		databases, err = discoverDatabases(workspace.home.SQLiteDir, stateDBGlob)
		if err != nil {
			return nil, err
		}
	}
	unapplied, err := applyThreadSidecars(sidecars, databases)
	if err != nil {
		return nil, err
	}
	if workspace.rolloutsStaged && len(databases) > 0 {
		if err := rearmBackfillState(databases); err != nil {
			return nil, err
		}
	}
	var warnings []string
	switch {
	case unapplied == 0:
	case len(databases) == 0:
		warnings = append(warnings, fmt.Sprintf(
			"%d threads sidecar row(s) could not be applied because no Codex state database exists yet; start Codex once (any directory), then rerun import",
			unapplied,
		))
	case workspace.rolloutsStaged:
		warnings = append(warnings, fmt.Sprintf(
			"%d threads sidecar row(s) could not be applied because Codex has not created their thread rows yet; "+
				"the session backfill was re-armed — start Codex once (any directory), then rerun import",
			unapplied,
		))
	default:
		warnings = append(warnings, fmt.Sprintf(
			"%d threads sidecar row(s) could not be applied because their thread rows do not exist and this "+
				"archive carries no rollout files to rebuild them from",
			unapplied,
		))
	}
	workspace.historyAppends, workspace.indexAppends, workspace.sidecarAppends, workspace.rolloutsStaged = nil, nil, nil, false
	return warnings, nil
}

func scanLines(ctx context.Context, path string) ([][]byte, error) {
	// Checked before opening anything: without this, a canceled context
	// scanning a missing or empty file falls straight through to the
	// (nil, nil) returns below, indistinguishable from a genuine "no
	// lines" result — the caller could never tell cancellation happened.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(path) //nolint:gosec // G304: resolved Codex home path
	if errors.Is(err, fs.ErrNotExist) {
		// Re-checked here too: the missing-file branch returns before the
		// scan loop even starts, so it must not report success on its own
		// if cancellation landed during the os.Open call above.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), maxCodexJSONLLine)
	var lines [][]byte
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(scanner.Bytes())) > 0 {
			lines = append(lines, append([]byte(nil), scanner.Bytes()...))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	// Checked again after the loop: cancellation observed right after the
	// final line (or on an empty file, where the loop never ran) must
	// still surface as an error rather than a successful-looking result.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func appendUniqueHistory(ctx context.Context, path string, chunks [][]byte) error {
	existing, err := scanLines(ctx, path)
	if err != nil {
		return fmt.Errorf("scan existing history: %w", err)
	}
	seen := make(map[string]struct{})
	for _, line := range existing {
		key, err := historyKey(line)
		if err != nil {
			return err
		}
		seen[key] = struct{}{}
	}
	var appendLines [][]byte
	for _, chunk := range chunks {
		lines, err := boundedChunkLines(chunk)
		if err != nil {
			return err
		}
		for _, line := range lines {
			key, err := historyKey(line)
			if err != nil {
				return err
			}
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				appendLines = append(appendLines, line)
			}
		}
	}
	return appendLinesToFile(path, appendLines)
}

func appendUniqueExact(ctx context.Context, path string, chunks [][]byte) error {
	existing, err := scanLines(ctx, path)
	if err != nil {
		return fmt.Errorf("scan existing session index: %w", err)
	}
	seen := make(map[string]struct{})
	for _, line := range existing {
		seen[string(line)] = struct{}{}
	}
	var appendLines [][]byte
	for _, chunk := range chunks {
		lines, err := boundedChunkLines(chunk)
		if err != nil {
			return err
		}
		for _, line := range lines {
			if _, ok := seen[string(line)]; !ok {
				seen[string(line)] = struct{}{}
				appendLines = append(appendLines, line)
			}
		}
	}
	return appendLinesToFile(path, appendLines)
}

func boundedChunkLines(chunk []byte) ([][]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(chunk))
	scanner.Buffer(make([]byte, 64<<10), maxCodexJSONLLine)
	var lines [][]byte
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) > 0 {
			lines = append(lines, append([]byte(nil), scanner.Bytes()...))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan incoming JSONL: %w", err)
	}
	return lines, nil
}

func historyKey(line []byte) (string, error) {
	var record historyRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return "", fmt.Errorf("parse history line for deduplication: %w", err)
	}
	// Codex timestamps history.jsonl at whole-second precision
	// (message-history/src/lib.rs:121-125, SystemTime::now()...as_secs()), so
	// two distinct prompts submitted to one thread within the same
	// wall-clock second share (session_id, ts). Text joins the key so those
	// two survive rather than collapsing into one on import, while an
	// identical re-imported record (same session, timestamp, and text)
	// still dedups.
	encoded, err := json.Marshal([]any{record.SessionID, record.Timestamp, record.Text})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func appendLinesToFile(path string, lines [][]byte) (err error) {
	if len(lines) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, importFilePerm) //nolint:gosec // G304: resolved Codex home path
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() > 0 {
		var lastByte [1]byte
		if _, err := file.ReadAt(lastByte[:], info.Size()-1); err != nil {
			return err
		}
		if lastByte[0] != '\n' {
			if _, err := file.Write([]byte{'\n'}); err != nil {
				return err
			}
		}
	}
	for _, line := range lines {
		if _, err := file.Write(append(append([]byte(nil), line...), '\n')); err != nil {
			return err
		}
	}
	return nil
}

func (workspace *Workspace) parseThreadSidecars() ([]threadSidecar, error) {
	var sidecars []threadSidecar
	for _, chunk := range workspace.sidecarAppends {
		lines, err := boundedChunkLines(chunk)
		if err != nil {
			return nil, err
		}
		for lineNumber, line := range lines {
			sidecar, err := parseThreadSidecar(line)
			if err != nil {
				return nil, fmt.Errorf("parse threads sidecar line %d: %w", lineNumber+1, err)
			}
			sidecars = append(sidecars, sidecar)
		}
	}
	return sidecars, nil
}

func applyThreadSidecars(sidecars []threadSidecar, databases []string) (int, error) {
	unapplied := 0
	for _, sidecar := range sidecars {
		applied := false
		for _, path := range databases {
			database, err := sqlrewrite.Open(path)
			if err != nil {
				return 0, fmt.Errorf("open state database %s: %w", path, err)
			}
			transaction, err := database.Begin()
			if err != nil {
				_ = database.Close()
				return 0, err
			}
			values := sidecarColumns(sidecar)
			count, err := database.UpdateColumnsByKey(transaction, threadsTable, "id", sidecar.ThreadID, values)
			if err == nil {
				err = transaction.Commit()
			} else {
				_ = transaction.Rollback()
			}
			if err == nil {
				err = database.CheckpointTruncate()
			}
			closeErr := database.Close()
			if err == nil {
				err = closeErr
			}
			if err != nil {
				return 0, fmt.Errorf("apply threads sidecar for %s: %w", sidecar.ThreadID, err)
			}
			applied = applied || count > 0
		}
		if !applied {
			unapplied++
		}
	}
	return unapplied, nil
}

func rearmBackfillState(databases []string) error {
	for _, path := range databases {
		database, err := sqlrewrite.Open(path)
		if err != nil {
			return fmt.Errorf("open state database %s: %w", path, err)
		}
		transaction, err := database.Begin()
		if err != nil {
			_ = database.Close()
			return fmt.Errorf("re-arm backfill state for %s: %w", path, err)
		}
		_, err = database.UpdateColumnsByKey(transaction, backfillStateTable, "id", 1, map[string]any{
			"status": "pending", "last_watermark": nil,
		})
		if err == nil {
			err = transaction.Commit()
		} else {
			_ = transaction.Rollback()
		}
		if err == nil {
			err = database.CheckpointTruncate()
		}
		closeErr := database.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return fmt.Errorf("re-arm backfill state for %s: %w", path, err)
		}
	}
	return nil
}

func sidecarColumns(sidecar threadSidecar) map[string]any {
	git := sidecarGit{}
	if sidecar.Git != nil {
		git = *sidecar.Git
	}
	return map[string]any{
		"archived_at":    sidecar.ArchivedAt,
		"title":          sidecar.Title,
		"git_sha":        git.SHA,
		"git_branch":     git.Branch,
		"git_origin_url": git.OriginURL,
	}
}

func parseThreadSidecar(line []byte) (threadSidecar, error) {
	var sidecar threadSidecar
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	if err := decoder.Decode(&sidecar); err != nil {
		return threadSidecar{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return threadSidecar{}, errors.New("contains multiple JSON values")
		}
		return threadSidecar{}, err
	}
	if sidecar.ThreadID == "" {
		return threadSidecar{}, errors.New("thread_id must be a non-empty string")
	}
	return sidecar, nil
}

// ReferenceSurfaces reports the native reference counts for one project.
func (workspace *Workspace) ReferenceSurfaces(ctx context.Context, project string) ([]tool.CountSurface, error) {
	known, err := workspace.knowsProject(ctx, project)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, workspace.projectAbsenceError()
	}
	rollouts, _, err := workspace.projectRollouts(ctx, project)
	if err != nil {
		return nil, err
	}
	threads, err := workspace.countThreadRows(ctx, project)
	if err != nil {
		return nil, err
	}
	rolloutThreadIDs := make(map[string]struct{}, len(rollouts))
	for _, path := range rollouts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lines, err := readRolloutLines(path)
		if err != nil {
			return nil, err
		}
		identity, err := rolloutProjectIdentity(lines, project)
		if err != nil {
			return nil, err
		}
		addRolloutThreadID(rolloutThreadIDs, identity.ThreadID)
	}
	ids, err := workspace.projectThreadIDSet(ctx, project, rolloutThreadIDs)
	if err != nil {
		return nil, err
	}
	history, err := countHistoryForIDs(ctx, filepath.Join(workspace.home.Dir, codexHistoryFile), ids)
	if err != nil {
		return nil, err
	}
	index, err := countIndexForIDs(ctx, filepath.Join(workspace.home.Dir, sessionIndexFile), ids)
	if err != nil {
		return nil, err
	}
	return []tool.CountSurface{
		{Name: "threads rows", Count: threads},
		{Name: "rollout files", Count: len(rollouts)},
		{Name: "history lines", Count: history},
		{Name: "session-index lines", Count: index},
	}, nil
}

func (workspace *Workspace) countThreadRows(ctx context.Context, project string) (int, error) {
	var total int
	paths, err := discoverDatabases(workspace.home.SQLiteDir, stateDBGlob)
	if err != nil {
		return 0, err
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		database, err := openReadOnlyDatabase(path)
		if err != nil {
			return 0, err
		}
		count, err := countMatchingThreadRows(ctx, database, project)
		_ = database.Close()
		if err != nil {
			return 0, fmt.Errorf("count thread rows in database %s: %w", path, err)
		}
		total += count
	}
	return total, nil
}
func countHistoryForIDs(ctx context.Context, path string, ids map[string]struct{}) (int, error) {
	lines, err := scanLines(ctx, path)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range lines {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		var record historyRecord
		if json.Unmarshal(line, &record) == nil {
			if _, ok := ids[record.SessionID]; ok {
				count++
			}
		}
	}
	return count, nil
}
func countIndexForIDs(ctx context.Context, path string, ids map[string]struct{}) (int, error) {
	lines, err := scanLines(ctx, path)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range lines {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		var record struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(line, &record) == nil {
			if _, ok := ids[record.ID]; ok {
				count++
			}
		}
	}
	return count, nil
}

// DiskCategories reports all on-disk files that Codex attributes to a project.
func (workspace *Workspace) DiskCategories(ctx context.Context, project string) ([]tool.SizeCategory, error) {
	known, err := workspace.knowsProject(ctx, project)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, workspace.projectAbsenceError()
	}
	rollouts, _, err := workspace.projectRollouts(ctx, project)
	if err != nil {
		return nil, err
	}
	active, archived := tool.SizeCategory{Name: "sessions"}, tool.SizeCategory{Name: "archived-sessions"}
	for _, path := range rollouts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(path, filepath.Join(workspace.home.Dir, archivedSessionsSubdir)+string(filepath.Separator)) {
			archived.Files++
			archived.Bytes += info.Size()
		} else {
			active.Files++
			active.Bytes += info.Size()
		}
	}
	history := tool.SizeCategory{Name: categoryHistory}
	return []tool.SizeCategory{active, archived, history}, nil
}

// EnumerateProjects unions database thread cwd values, config.toml/profile
// [projects] TOML keys, and rollout session_meta/turn_context cwd values.
func (workspace *Workspace) EnumerateProjects(ctx context.Context) ([]tool.ProjectInfo, error) {
	projects := make(map[string]struct{})
	paths, err := discoverDatabases(workspace.home.SQLiteDir, stateDBGlob)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		database, err := openReadOnlyDatabase(path)
		if err != nil {
			return nil, err
		}
		rows, err := database.QueryContext(ctx, `SELECT DISTINCT cwd FROM threads`)
		if err != nil {
			_ = database.Close()
			return nil, fmt.Errorf("query project directories from database %s: %w", path, err)
		}
		for rows.Next() {
			if err := ctx.Err(); err != nil {
				_ = rows.Close()
				_ = database.Close()
				return nil, err
			}
			var cwd string
			if err := rows.Scan(&cwd); err != nil {
				_ = rows.Close()
				_ = database.Close()
				return nil, fmt.Errorf("scan project directory from database %s: %w", path, err)
			}
			projects[cwd] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			_ = database.Close()
			return nil, fmt.Errorf("iterate project directories from database %s: %w", path, err)
		}
		_ = rows.Close()
		_ = database.Close()
	}
	configKeys, err := workspace.configProjectKeys(ctx)
	if err != nil {
		return nil, err
	}
	for _, key := range configKeys {
		projects[key] = struct{}{}
	}
	rolloutCWDs, err := workspace.rolloutProjectCWDs(ctx)
	if err != nil {
		return nil, err
	}
	for _, cwd := range rolloutCWDs {
		projects[cwd] = struct{}{}
	}
	names := make([]string, 0, len(projects))
	for project := range projects {
		names = append(names, project)
	}
	sort.Strings(names)
	result := make([]tool.ProjectInfo, 0, len(names))
	for _, project := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		disk, err := workspace.DiskCategories(ctx, project)
		if err != nil {
			return nil, err
		}
		info := tool.ProjectInfo{Label: project, Resolved: true, Disk: disk}
		for _, category := range disk {
			info.Files += category.Files
			info.Bytes += category.Bytes
		}
		result = append(result, info)
	}
	return result, nil
}

func (workspace *Workspace) configProjectKeys(ctx context.Context) ([]string, error) {
	configFiles, err := discoverConfigTOMLFiles(workspace.home)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, path := range configFiles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fileKeys, err := configTOMLProjectKeys(path)
		if err != nil {
			return nil, fmt.Errorf("read project keys from config %s: %w", path, err)
		}
		keys = append(keys, fileKeys...)
	}
	return keys, nil
}

func (workspace *Workspace) rolloutProjectCWDs(ctx context.Context) ([]string, error) {
	rollouts, err := discoverRolloutFiles(workspace.home)
	if err != nil {
		return nil, err
	}
	var cwds []string
	for _, path := range rollouts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lines, err := readRolloutLines(path)
		if err != nil {
			return nil, fmt.Errorf("read rollout %s: %w", path, err)
		}
		cwds = append(cwds, rolloutIdentityFromLines(lines).CWDs...)
	}
	return cwds, nil
}

// knowsProject reports whether Codex has any record of project: a rollout's
// structured cwd, a thread row, or a config.toml/profile projects key. A
// config-key-only project (a trust entry with no sessions yet) still
// counts, matching the three-way association projectKnown uses for move.
func (workspace *Workspace) knowsProject(ctx context.Context, project string) (bool, error) {
	rollouts, _, err := workspace.projectRollouts(ctx, project)
	if err != nil {
		return false, err
	}
	if len(rollouts) > 0 {
		return true, nil
	}
	count, err := workspace.countThreadRows(ctx, project)
	if err != nil {
		return false, err
	}
	if count > 0 {
		return true, nil
	}
	return configTOMLKnowsProject(workspace.home, project)
}

// projectThreadIDs accumulates matched thread IDs across every matched cwd
// value in every discovered database.
func (workspace *Workspace) projectThreadIDs(ctx context.Context, project string) (map[string]struct{}, error) {
	threadIDs := make(map[string]struct{})
	paths, err := discoverDatabases(workspace.home.SQLiteDir, stateDBGlob)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		database, err := openReadOnlyDatabase(path)
		if err != nil {
			return nil, err
		}
		matched, err := matchingThreadCWDs(ctx, database, project)
		if err != nil {
			_ = database.Close()
			return nil, fmt.Errorf("match thread cwd in database %s: %w", path, err)
		}
		for _, cwd := range matched {
			ids, err := threadIDsForCWD(ctx, database, cwd)
			if err != nil {
				_ = database.Close()
				return nil, fmt.Errorf("thread IDs for cwd %q in database %s: %w", cwd, path, err)
			}
			for _, id := range ids {
				if _, alreadyPresent := threadIDs[id]; alreadyPresent {
					continue
				}
				threadIDs[id] = struct{}{}
			}
		}
		_ = database.Close()
	}
	return threadIDs, nil
}

// projectThreadIDSet unions project's state-database thread IDs with
// rolloutThreadIDs, the caller's own rollout-derived set. Export and
// ReferenceSurfaces both need this exact union — a thread whose state-db
// row has no matching rollout file, or vice versa, must still count — so
// one function computes it for both callers instead of each re-deriving it
// and risking drift (finding FE2: ReferenceSurfaces used to build its id
// set from rollouts alone, so a state-db-only thread showed zero history
// and session-index counts even though Export and countThreadRows both
// included it).
func (workspace *Workspace) projectThreadIDSet(
	ctx context.Context, project string, rolloutThreadIDs map[string]struct{},
) (map[string]struct{}, error) {
	threadIDs, err := workspace.projectThreadIDs(ctx, project)
	if err != nil {
		return nil, err
	}
	for id := range rolloutThreadIDs {
		if _, alreadyPresent := threadIDs[id]; alreadyPresent {
			continue
		}
		threadIDs[id] = struct{}{}
	}
	return threadIDs, nil
}

func (workspace *Workspace) stateDatabasesNewestFirst() ([]string, error) {
	databases, err := discoverDatabases(workspace.home.SQLiteDir, stateDBGlob)
	if err != nil {
		return nil, err
	}
	type generationPath struct {
		path       string
		generation int
	}
	parsed := make([]generationPath, 0, len(databases))
	for _, path := range databases {
		name := filepath.Base(path)
		number := strings.TrimSuffix(strings.TrimPrefix(name, "state_"), ".sqlite")
		generation, parseErr := strconv.Atoi(number)
		if parseErr != nil || generation < 0 || "state_"+number+".sqlite" != name {
			return nil, fmt.Errorf("parse state database generation from %s", path)
		}
		parsed = append(parsed, generationPath{path: path, generation: generation})
	}
	sort.Slice(parsed, func(left, right int) bool { return parsed[left].generation > parsed[right].generation })
	ordered := make([]string, 0, len(parsed))
	for _, database := range parsed {
		ordered = append(ordered, database.path)
	}
	return ordered, nil
}
