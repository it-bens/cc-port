package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/it-bens/cc-port/internal/rewrite"
)

// ErrCompressedRolloutUnsupported is the sentinel discoverRolloutFiles
// returns when it finds a .jsonl.zst rollout with no plain .jsonl sibling.
// cc-port never reads compressed rollouts, so continuing past one would
// either silently skip that rollout's content or, during move, relocate the
// project while leaving a stale, unrewritten path trapped inside a rollout
// cc-port could not touch; refusing by name is the only honest outcome.
var ErrCompressedRolloutUnsupported = errors.New("compressed rollout unsupported")

// rolloutLineProbe reads just enough of a rollout JSONL line to classify
// it. Codex tags every RolloutItem line as {"type":…,"payload":…}
// (protocol/src/protocol.rs:3130-3145).
type rolloutLineProbe struct {
	Type string `json:"type"`
}

// rolloutTypeSessionMeta and rolloutTypeTurnContext are the two RolloutItem
// variants carrying the project's structured identity fields: session_meta
// (protocol/src/protocol.rs:3014-3062) and turn_context
// (protocol/src/protocol.rs:3208-3224).
const (
	rolloutTypeSessionMeta = "session_meta"
	rolloutTypeTurnContext = "turn_context"
)

// rolloutRoots lists the two physical roots a rollout can live under:
// sessions/YYYY/MM/DD/ and the flat archived_sessions/ (rollout/src/lib.rs:21-22).
// Archiving physically renames the file from one root to the other
// (thread-store/src/local/archive_thread.rs:41-53).
func rolloutRoots(home *Home) []string {
	return []string{
		filepath.Join(home.Dir, sessionsSubdir),
		filepath.Join(home.Dir, archivedSessionsSubdir),
	}
}

// discoverRolloutFiles walks both rollout roots and returns the LOGICAL
// rollout set, in sorted order: when both X.jsonl and its X.jsonl.zst
// sibling exist, only X.jsonl is kept. Codex's own compression worker can
// leave both on disk momentarily — it persists the compressed file before
// removing the plain one (rollout/src/compression.rs:632-651) — and never
// re-compresses once the plain file is gone, so a crash in that window
// strands the pair with no self-heal. Every data consumer (export, move,
// projectRollouts, knowsProject, stats) must see exactly one file per
// logical rollout or a duplicate archive entry corrupts the whole import;
// this mirrors Codex's own walker, which applies the identical suppression
// (rollout/src/compression.rs:141-163, should_skip_compressed_sibling at
// 941-943). A missing root is not an error: a fresh Codex home may not have
// written it yet.
func discoverRolloutFiles(home *Home) ([]string, error) {
	var files []string
	for _, root := range rolloutRoots(home) {
		found, err := listRolloutFiles(root)
		if err != nil {
			return nil, err
		}
		files = append(files, found...)
	}
	sort.Strings(files)
	files = suppressCompressedSiblings(files)
	var compressed []string
	for _, path := range files {
		if strings.HasSuffix(path, ".zst") {
			compressed = append(compressed, path)
		}
	}
	if len(compressed) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrCompressedRolloutUnsupported, strings.Join(compressed, ", "))
	}
	return files, nil
}

// suppressCompressedSiblings drops every .jsonl.zst entry whose plain
// .jsonl counterpart is also present in files.
func suppressCompressedSiblings(files []string) []string {
	plain := make(map[string]bool, len(files))
	for _, path := range files {
		if !strings.HasSuffix(path, ".zst") {
			plain[path] = true
		}
	}
	var kept []string
	for _, path := range files {
		if strings.HasSuffix(path, ".zst") && plain[strings.TrimSuffix(path, ".zst")] {
			continue
		}
		kept = append(kept, path)
	}
	return kept
}

func listRolloutFiles(root string) ([]string, error) {
	var files []string
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".jsonl.zst") {
			files = append(files, path)
		}
		return nil
	})
	if walkErr != nil {
		if errors.Is(walkErr, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("walk %s: %w", root, walkErr)
	}
	return files, nil
}

// hasStructuredCwd reports whether lines contains at least one
// session_meta or turn_context line. A rollout with neither predates
// structured cwd tracking (era-A): move must not touch it, even under
// --deep, since there is nothing to anchor a safe rewrite to.
func hasStructuredCwd(lines [][]byte) bool {
	for _, line := range lines {
		var probe rolloutLineProbe
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		if probe.Type == rolloutTypeSessionMeta || probe.Type == rolloutTypeTurnContext {
			return true
		}
	}
	return false
}

// pathSubstitution pairs a literal byte sequence a rollout rewrite searches
// for with the value it replaces it with.
type pathSubstitution struct {
	old string
	new string
}

// rolloutSubstitutionSources returns oldPath plus every distinct cwd this
// rollout's session_meta/turn_context lines recorded that canonically
// matches oldPath but differs from it byte-for-byte: Codex records
// payload.cwd verbatim and uncanonicalized, so a rollout recorded through a
// symlink-aliased cwd never contains oldPath's literal bytes for the
// existing boundary-aware substring rewrite to find (spec §5.1, finding
// H1). rolloutFileSubstitutions is the sole caller, shared by
// planRolloutFile and captureMovePreflight (move.go), so a dry-run count and
// the plan rolloutsSurfaceWithPlans later applies always come from the same
// read of a rollout's bytes.
//
// The result is ordered longest-source-first (ties broken by source bytes,
// so the order never depends on map iteration), because callers apply
// sources sequentially against progressively mutated bytes: a source that
// is a boundary-prefix of another source (for example a self-referential
// symlink alias recorded as oldPath+"/alias") must be substituted before
// the shorter source, or the shorter source's own substitution pass
// consumes part of the longer source's match first and the longer source
// can never be found again.
func rolloutSubstitutionSources(lines [][]byte, oldPath string) ([]string, error) {
	identity, err := rolloutProjectIdentity(lines, oldPath)
	if err != nil {
		return nil, err
	}
	sources := []string{oldPath}
	seen := map[string]struct{}{oldPath: {}}
	for _, cwd := range identity.CWDs {
		if _, ok := seen[cwd]; ok {
			continue
		}
		seen[cwd] = struct{}{}
		matches, err := pathMatchesProject(cwd, oldPath)
		if err != nil {
			return nil, err
		}
		if matches {
			sources = append(sources, cwd)
		}
	}
	sort.Slice(sources, func(left, right int) bool {
		if len(sources[left]) != len(sources[right]) {
			return len(sources[left]) > len(sources[right])
		}
		return sources[left] < sources[right]
	})
	return sources, nil
}

// ErrSubstitutionWouldReintroduceSource is returned when a rollout has more
// than one substitution source and applying an earlier one, in sequence,
// causes a later (not-yet-applied) source to match text it did not match
// before that step ran. internal/move's nested-move guard only refuses
// newPath equaling oldPath or newPath being a /-boundary descendant of
// oldPath from the root; it does not catch oldPath (or another source)
// reappearing inside text a substitution just wrote, whether whole
// (contained entirely within one replacement value) or assembled from a
// replacement's output plus adjacent bytes the rewrite left unchanged
// (replacing "/longsource" with "/x/foo" inside "/longsource/bar" leaves
// "/x/foo/bar", completing a match for an unrelated recorded source
// "/foo/bar" that no single replacement value contains on its own). A
// containment check over replacement values alone cannot detect the second
// shape and can also over-refuse: a substring occurrence that would not
// actually fall on a path boundary is not a real hazard, since
// rewrite.ReplacePathInBytes(WithJSONEscape) would never match it. With a
// single source this is harmless: one such pass never re-scans its own
// output, so there is no later source left to check. A general fix needs a
// true single-pass multi-pattern substitution primitive with JSON-escape
// awareness in internal/rewrite, a much larger change than this narrow,
// doubly-conditional hazard warrants; cc-port refuses rather than risk
// silently corrupting a recorded path.
var ErrSubstitutionWouldReintroduceSource = errors.New("rollout substitution would re-introduce a later substitution source")

// guardSubstitutionOrder refuses when applying substitutions sequentially,
// exactly as rewriteRolloutLine will, causes a later (not-yet-applied)
// source to match more occurrences than it matched immediately before that
// step ran. It reuses rewriteRolloutLine itself, called with a growing
// PREFIX of the ordered substitution list against the untouched original
// line, so "the state after step k" and "the state after step k-1" are
// exactly what an apply would actually produce at each point, not a
// simulation of it; the only new primitive is the counting comparison,
// reusing rewrite.CountPathInBytesWithJSONEscape (already used by
// rewriteRolloutLine's own deep branch) over the whole line both times.
// Comparing the WHOLE line rather than isolating the touched field is
// still exact: content no substitution reached is identical on both sides
// of the comparison and contributes nothing to the delta, whether the
// rewrite is running in --deep or structured mode.
//
// A first, simpler design ran the full substitution sequence once, then
// ran it again over its own output, refusing if the second full pass found
// anything. That design has a real gap: a corrupted result can itself be a
// fixed point under further whole-sequence re-application. Concretely
// (TestPlanAndApplyRolloutFileRefuseWhenSuffixReintroducesSource), sourceA
// and sourceB have UNEQUAL suffixes past oldPath ("/nested" and
// "/other/nested"), and sourceA sorts first (longer raw bytes). newPath
// plus sourceA's suffix happens to spell out sourceB's raw stored bytes
// exactly; rewriteStructuredRolloutFields' own per-field loop applies every
// substitution to one field's value in sequence, so within that single,
// otherwise-unguarded pass sourceB's substitution immediately fires again
// on the text sourceA's step just produced, settling into a doubled path
// segment (".../other/other/nested") that no longer contains either
// source's original literal bytes — so a second full pass over that output
// finds nothing further, even though the value is already wrong. Had the
// two suffixes been identical instead, their replacement values would be
// byte-identical too, and the later step would just rewrite already-correct
// text rather than produce a doubled segment; the danger here is the
// unequal-suffix containment, not aliasing as such. Checking after each
// individual step, while the moment of reintroduction is still observable,
// is what catches it; this was verified against exactly that case, not
// assumed. A single source never triggers this: there is no later, pending
// source left to check.
func guardSubstitutionOrder(line []byte, substitutions []pathSubstitution, deep bool) error {
	if len(substitutions) <= 1 {
		return nil
	}
	before, _ := rewriteRolloutLine(line, nil, deep)
	for step := range substitutions {
		after, _ := rewriteRolloutLine(line, substitutions[:step+1], deep)
		for later := step + 1; later < len(substitutions); later++ {
			source := substitutions[later].old
			if rewrite.CountPathInBytesWithJSONEscape(after, source) > rewrite.CountPathInBytesWithJSONEscape(before, source) {
				return fmt.Errorf(
					"%w: substituting %q for %q introduces a new match for %q that was not present before this step",
					ErrSubstitutionWouldReintroduceSource, substitutions[step].old, substitutions[step].new, source,
				)
			}
		}
		before = after
	}
	return nil
}

// rolloutSubstitutions pairs each source rolloutSubstitutionSources reported
// with the value Apply must write: newPath for the literal oldPath source,
// or newPath with whatever suffix the source's canonicalized form carried
// past oldPath's canonical form for a symlink-aliased source — the same
// suffix-preservation matchingThreadRewrites uses for threads.cwd.
func rolloutSubstitutions(sources []string, oldPath, newPath string) ([]pathSubstitution, error) {
	canonicalOldPath, err := canonicalizePath(oldPath)
	if err != nil {
		return nil, err
	}
	substitutions := make([]pathSubstitution, 0, len(sources))
	for _, source := range sources {
		if source == oldPath {
			substitutions = append(substitutions, pathSubstitution{old: source, new: newPath})
			continue
		}
		canonicalSource, err := canonicalizePath(source)
		if err != nil {
			return nil, err
		}
		suffix := strings.TrimPrefix(canonicalSource, canonicalOldPath)
		substitutions = append(substitutions, pathSubstitution{old: source, new: newPath + suffix})
	}
	return substitutions, nil
}

// rewriteRolloutLine rewrites one rollout JSONL line, applying substitutions
// in order (session_meta and turn_context lines always; every other line —
// response items, world-state blobs, compacted summaries — only under
// deep). Substitutions must already be ordered longest-source-first
// (rolloutSubstitutionSources): planRolloutFile calls this same function to
// count, over a throwaway copy, rather than counting each source
// independently against the original line, so a dry-run count can never
// diverge from what an apply actually replaces — a source-order-dependent
// outcome is not something two different code paths could disagree about,
// since only one path exists. A malformed or non-JSON line is left verbatim;
// rolloutMalformedWarnings reports that preservation to the operator, matching
// the Claude adapter's preserve-verbatim-and-warn convention.
func rewriteRolloutLine(line []byte, substitutions []pathSubstitution, deep bool) (rewritten []byte, count int) {
	var probe rolloutLineProbe
	if err := json.Unmarshal(line, &probe); err != nil {
		return line, 0
	}
	structured := probe.Type == rolloutTypeSessionMeta || probe.Type == rolloutTypeTurnContext
	if deep {
		updated := line
		total := 0
		for _, substitution := range substitutions {
			var replaced int
			updated, replaced = rewrite.ReplacePathInBytesWithJSONEscape(updated, substitution.old, substitution.new)
			total += replaced
		}
		return updated, total
	}
	if !structured {
		return line, 0
	}
	return rewriteStructuredRolloutFields(line, probe.Type, substitutions)
}

//nolint:gocritic // Named results would be shadowed by the per-field rewrite values.
func rewriteStructuredRolloutFields(line []byte, rolloutType string, substitutions []pathSubstitution) ([]byte, int) {
	updated := line
	total := 0
	for _, path := range structuredRolloutFieldPaths(line, rolloutType) {
		value := gjson.GetBytes(updated, path)
		if value.Type != gjson.String {
			continue
		}
		rewritten := []byte(value.String())
		fieldCount := 0
		for _, substitution := range substitutions {
			var replaced int
			rewritten, replaced = rewrite.ReplacePathInBytes(rewritten, substitution.old, substitution.new)
			fieldCount += replaced
		}
		if fieldCount == 0 {
			continue
		}
		var err error
		updated, err = sjson.SetBytes(updated, path, string(rewritten))
		if err != nil {
			return line, 0
		}
		total += fieldCount
	}
	return updated, total
}

func structuredRolloutFieldPaths(line []byte, rolloutType string) []string {
	paths := []string{"payload.cwd"}
	if rolloutType != rolloutTypeTurnContext {
		return paths
	}
	roots := gjson.GetBytes(line, "payload.workspace_roots")
	if !roots.IsArray() {
		return paths
	}
	for index, root := range roots.Array() {
		if root.Type == gjson.String {
			paths = append(paths, "payload.workspace_roots."+strconv.Itoa(index))
		}
	}
	return paths
}

// rolloutFileSubstitutions reads path and computes its ordered substitution
// list. planRolloutFile and captureMovePreflight (move.go) are its only
// callers, so a dry-run count and the plan rolloutsSurfaceWithPlans later
// applies can never derive a different source order or a different set of
// replacement values from the same on-disk bytes.
func rolloutFileSubstitutions(path, oldPath, newPath string, deep bool) (lines [][]byte, substitutions []pathSubstitution, eraA bool, err error) {
	lines, err = readRolloutLines(path)
	if err != nil {
		return nil, nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	if !hasStructuredCwd(lines) {
		return lines, nil, true, nil
	}
	sources, err := rolloutSubstitutionSources(lines, oldPath)
	if err != nil {
		return nil, nil, false, fmt.Errorf("determine substitution sources for %s: %w", path, err)
	}
	substitutions, err = rolloutSubstitutions(sources, oldPath, newPath)
	if err != nil {
		return nil, nil, false, fmt.Errorf("determine substitution values for %s: %w", path, err)
	}
	for _, line := range lines {
		if err := guardSubstitutionOrder(line, substitutions, deep); err != nil {
			return nil, nil, false, fmt.Errorf("%s: %w", path, err)
		}
	}
	return lines, substitutions, false, nil
}

// planRolloutFile reports how many occurrences a move would rewrite in
// path, and whether path is era-A (no structured cwd, therefore skipped
// entirely regardless of deep). It counts by running rewriteRolloutLine —
// the identical sequential substitution pipeline applyRolloutSubstitutions
// uses — over a throwaway copy of each line and summing the reported counts,
// rather than counting each substitution source independently against the
// original bytes: sources can overlap (a symlink alias recorded as
// oldPath+"/alias" is a boundary-prefix of oldPath itself), and independent
// per-source counting double-counts an overlapping occurrence that the
// real, order-dependent rewrite only ever touches once (spec §5.1).
func planRolloutFile(path, oldPath, newPath string, deep bool) (count int, eraA bool, err error) {
	lines, substitutions, eraA, err := rolloutFileSubstitutions(path, oldPath, newPath, deep)
	if err != nil {
		return 0, false, err
	}
	if eraA {
		return 0, true, nil
	}
	for _, line := range lines {
		_, lineCount := rewriteRolloutLine(line, substitutions, deep)
		count += lineCount
	}
	return count, false, nil
}

// applyRolloutSubstitutions applies substitutions captured during preflight.
// It deliberately performs no path canonicalization, so another tool's apply
// cannot make a previously canonical match disappear by removing oldPath.
func applyRolloutSubstitutions(path string, substitutions []pathSubstitution, deep bool) (int, error) {
	changedCount, err := rewriteRolloutLines(path, func(line []byte) ([]byte, int) {
		return rewriteRolloutLine(line, substitutions, deep)
	})
	if err != nil {
		return 0, fmt.Errorf("rewrite %s: %w", path, err)
	}
	return changedCount, nil
}

func readRolloutLines(path string) (lines [][]byte, err error) {
	file, err := os.Open(path) //nolint:gosec // G304: path from adapter-controlled rollout discovery
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), maxCodexJSONLLine)
	for scanner.Scan() {
		lines = append(lines, append([]byte(nil), scanner.Bytes()...))
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, fmt.Errorf("scan %s: %w", path, scanErr)
	}
	return lines, nil
}

func rewriteRolloutLines(path string, transform func(line []byte) (rewritten []byte, count int)) (int, error) {
	lines, err := readRolloutLines(path)
	if err != nil {
		return 0, err
	}

	var output bytes.Buffer
	count := 0
	for _, line := range lines {
		rewrittenLine, lineCount := transform(line)
		count += lineCount
		output.Write(rewrittenLine)
		output.WriteByte('\n')
	}

	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	if err := rewrite.SafeWriteFile(path, output.Bytes(), info.Mode()); err != nil {
		return 0, fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		return 0, fmt.Errorf("restore mtime %s: %w", path, err)
	}
	return count, nil
}

func rolloutMalformedWarnings(path string, lines [][]byte) []string {
	warnings := make([]string, 0)
	for number, line := range lines {
		var probe rolloutLineProbe
		if json.Unmarshal(line, &probe) != nil {
			warnings = append(warnings, fmt.Sprintf("%s: malformed JSON line %d preserved unchanged", path, number+1))
		}
	}
	return warnings
}
