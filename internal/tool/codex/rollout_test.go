package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rolloutFixturePath(home *Home, relative string) string {
	return filepath.Join(home.Dir, filepath.FromSlash(relative))
}

const (
	eraCPath = "sessions/2026/07/17/rollout-2026-07-17T10-00-00-00000000-0000-4000-8000-000000000001.jsonl"
	eraBPath = "sessions/2026/07/16/rollout-2026-07-16T08-00-00-00000000-0000-4000-8000-000000000003.jsonl"
	eraAPath = "sessions/2026/07/15/rollout-2026-07-15T07-00-00-00000000-0000-4000-8000-000000000004.jsonl"
)

func TestDiscoverRolloutFilesFindsBothRoots(t *testing.T) {
	home := SetupFixture(t)

	files, err := discoverRolloutFiles(home)

	require.NoError(t, err)
	assert.Contains(t, files, rolloutFixturePath(home, eraCPath))
	assert.Contains(t, files, rolloutFixturePath(home, "archived_sessions/rollout-2026-07-10T09-00-00-00000000-0000-4000-8000-000000000002.jsonl"))
}

// TestDiscoverRolloutFiles_SuppressesCompressedSibling guards finding H4:
// a crash mid-compression can leave both X.jsonl and X.jsonl.zst on disk
// (rollout/src/compression.rs:632-651 persists the compressed file before
// removing the plain one), and Codex never re-compresses once the plain
// file is gone, so the pair is durable with no self-heal. Every rollout
// consumer must see one logical file, mirroring Codex's own walker
// (should_skip_compressed_sibling); the freshness witness alone needs the
// raw, non-deduplicated enumeration for its mtime evidence.
func TestDiscoverRolloutFiles_SuppressesCompressedSibling(t *testing.T) {
	home := SetupFixture(t)
	plainPath := rolloutFixturePath(home, eraCPath)
	compressedPath := plainPath + zstSuffix
	plainData, err := os.ReadFile(plainPath) //nolint:gosec // G304: fixture path under t.TempDir()
	require.NoError(t, err)
	compressed, err := compressZstd(plainData)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(compressedPath, compressed, 0o600)) //nolint:gosec // G703: fixture path under t.TempDir()

	files, err := discoverRolloutFiles(home)
	require.NoError(t, err)
	assert.Contains(t, files, plainPath, "the plain file is always kept")
	assert.NotContains(t, files, compressedPath, "the crash-window .zst sibling is suppressed once the plain file exists")

	raw, err := discoverRolloutFilesRaw(home)
	require.NoError(t, err)
	assert.Contains(t, raw, compressedPath, "the witness's raw enumeration must still see the crash-window sibling")
	assert.Contains(t, raw, plainPath)
}

func TestPlanRolloutFileEraCCountsStructuredAndProseUnderDeep(t *testing.T) {
	home := SetupFixture(t)
	path := rolloutFixturePath(home, eraCPath)

	shallow, eraA, err := planRolloutFile(path, FixtureProjectPath(), "/Users/fixture/renamed-project", false, DefaultTranscodeCaps())
	require.NoError(t, err)
	assert.False(t, eraA)
	assert.Equal(t, 3, shallow, "session_meta.cwd, turn_context.cwd, and turn_context.workspace_roots[0], not the prose response_item")

	deep, eraA, err := planRolloutFile(path, FixtureProjectPath(), "/Users/fixture/renamed-project", true, DefaultTranscodeCaps())
	require.NoError(t, err)
	assert.False(t, eraA)
	assert.Equal(t, 5, deep, "structured fields, structured free text, and the prose response_item under --deep")
}

func TestPlanRolloutFileEraBHasNoTurnContext(t *testing.T) {
	home := SetupFixture(t)
	path := rolloutFixturePath(home, eraBPath)

	shallow, eraA, err := planRolloutFile(path, FixtureProjectPath(), "/Users/fixture/renamed-project", false, DefaultTranscodeCaps())
	require.NoError(t, err)
	assert.False(t, eraA)
	assert.Equal(t, 1, shallow, "only session_meta.cwd; era-B predates turn_context")
}

func TestPlanRolloutFileEraAIsSkippedEvenUnderDeep(t *testing.T) {
	home := SetupFixture(t)
	path := rolloutFixturePath(home, eraAPath)

	for _, deep := range []bool{false, true} {
		count, eraA, err := planRolloutFile(path, FixtureProjectPath(), "/Users/fixture/renamed-project", deep, DefaultTranscodeCaps())
		require.NoError(t, err)
		assert.True(t, eraA, "no session_meta or turn_context line: era-A")
		assert.Equal(t, 0, count)
	}
}

func TestApplyRolloutFileRewritesStructuredFieldsAlways(t *testing.T) {
	home := SetupFixture(t)
	path := rolloutFixturePath(home, eraCPath)
	newPath := "/Users/fixture/renamed-project"

	changed, eraA, err := applyRolloutFile(context.Background(), path, FixtureProjectPath(), newPath, false, DefaultTranscodeCaps())

	require.NoError(t, err)
	assert.False(t, eraA)
	assert.Equal(t, 3, changed)

	lines, _, err := readRolloutLines(path, DefaultTranscodeCaps())
	require.NoError(t, err)
	require.Len(t, lines, 3)
	assert.Contains(t, string(lines[0]), newPath, "session_meta.cwd rewritten")
	assert.Contains(t, string(lines[1]), newPath, "turn_context.cwd and workspace_roots rewritten")
	assert.Contains(t, string(lines[2]), FixtureProjectPath(), "prose left untouched without --deep")
}

func TestApplyRolloutFileRewritesProseUnderDeep(t *testing.T) {
	home := SetupFixture(t)
	path := rolloutFixturePath(home, eraCPath)
	newPath := "/Users/fixture/renamed-project"

	changed, eraA, err := applyRolloutFile(context.Background(), path, FixtureProjectPath(), newPath, true, DefaultTranscodeCaps())

	require.NoError(t, err)
	assert.False(t, eraA)
	assert.Equal(t, 5, changed)

	lines, _, err := readRolloutLines(path, DefaultTranscodeCaps())
	require.NoError(t, err)
	for _, line := range lines {
		assert.NotContains(t, string(line), FixtureProjectPath())
	}
}

func TestRewriteRolloutLineScopesShallowModeToStructuredFields(t *testing.T) {
	line := []byte(`{"type":"session_meta","payload":{"cwd":"/Users/fixture/codexproject","note":"keep /Users/fixture/codexproject verbatim"}}`)
	substitutions := []pathSubstitution{{old: FixtureProjectPath(), new: "/Users/fixture/renamed-project"}}

	shallow, shallowCount := rewriteRolloutLine(line, substitutions, false)
	deep, deepCount := rewriteRolloutLine(line, substitutions, true)

	assert.Equal(t, 1, shallowCount)
	assert.Contains(t, string(shallow), `"note":"keep /Users/fixture/codexproject verbatim"`)
	assert.Equal(t, 2, deepCount)
	assert.NotContains(t, string(deep), FixtureProjectPath())
}

// TestApplyRolloutFileRewritesSymlinkAliasedCWD guards finding H1 (spec
// §5.1): Codex stores payload.cwd verbatim and uncanonicalized, so a
// rollout recorded through a symlink-aliased cwd never literally contains
// the resolved oldPath bytes the plain substring rewrite searches for.
// rolloutSubstitutionSources must still identify it and applyRolloutFile
// must rewrite the stored value to the new path, not silently leave it
// stale (a real move rename would leave it dangling).
func TestApplyRolloutFileRewritesSymlinkAliasedCWD(t *testing.T) {
	tempRoot := t.TempDir()
	realProject := filepath.Join(tempRoot, "real", "project")
	require.NoError(t, os.MkdirAll(realProject, 0o750))
	require.NoError(t, os.Symlink(filepath.Join(tempRoot, "real"), filepath.Join(tempRoot, "link")))
	aliasedCWD := filepath.Join(tempRoot, "link", "project")
	newPath := filepath.Join(tempRoot, "real", "renamed-project")

	path := filepath.Join(tempRoot, "rollout.jsonl")
	line := `{"type":"session_meta","payload":{"id":"thread-1","session_id":"thread-1","cwd":"` + aliasedCWD + `"}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(line), 0o600))

	planCount, eraA, err := planRolloutFile(path, realProject, newPath, false, DefaultTranscodeCaps())
	require.NoError(t, err)
	require.False(t, eraA)
	require.Positive(t, planCount, "sanity: the symlink-aliased rollout must be counted")

	applyCount, eraA, err := applyRolloutFile(context.Background(), path, realProject, newPath, false, DefaultTranscodeCaps())
	require.NoError(t, err)
	require.False(t, eraA)
	assert.Equal(t, planCount, applyCount, "dry-run count and apply must consume the same substitution sources (spec §5.1)")

	lines, _, err := readRolloutLines(path, DefaultTranscodeCaps())
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Contains(t, string(lines[0]), `"cwd":"`+newPath+`"`,
		"the stored payload.cwd must be rewritten to the literal new path, not left as the symlink alias")
	assert.NotContains(t, string(lines[0]), aliasedCWD, "the symlink alias must not remain in the rewritten rollout")
}

// TestApplyRolloutFileRewritesOverlappingSubstitutionSourcesInOrder guards
// against source-order-dependent corruption. When one substitution source is
// a boundary-prefix of another — here a symlink recorded INSIDE the project
// (oldPath+"/alias") that resolves back to the project root itself, so both
// oldPath and the alias are valid sources — a sequential rewrite that
// processes the shorter source (oldPath) first consumes part of the longer
// source's match before the longer source's own substitution runs, leaving
// the value wrong ("newPath/alias" instead of "newPath") and making the
// count depend on which order the sources happened to iterate in.
// rolloutSubstitutionSources' longest-first ordering must substitute the
// more specific (longer) spelling first so the shorter source only ever
// matches what is genuinely left over.
func TestApplyRolloutFileRewritesOverlappingSubstitutionSourcesInOrder(t *testing.T) {
	tempRoot := t.TempDir()
	realProject := filepath.Join(tempRoot, "real", "project")
	require.NoError(t, os.MkdirAll(realProject, 0o750))
	recordedCWD := filepath.Join(realProject, "alias")
	require.NoError(t, os.Symlink(realProject, recordedCWD))
	newPath := filepath.Join(tempRoot, "renamed-project")

	path := filepath.Join(tempRoot, "rollout.jsonl")
	line := `{"type":"session_meta","payload":{"id":"thread-1","session_id":"thread-1","cwd":"` + recordedCWD + `"}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(line), 0o600))

	planCount, eraA, err := planRolloutFile(path, realProject, newPath, false, DefaultTranscodeCaps())
	require.NoError(t, err)
	require.False(t, eraA)
	require.Positive(t, planCount, "sanity: the overlapping-source rollout must be counted")

	applyCount, eraA, err := applyRolloutFile(context.Background(), path, realProject, newPath, false, DefaultTranscodeCaps())
	require.NoError(t, err)
	require.False(t, eraA)
	assert.Equal(t, planCount, applyCount, "dry-run count and apply must agree even when substitution sources overlap")

	lines, _, err := readRolloutLines(path, DefaultTranscodeCaps())
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Contains(t, string(lines[0]), `"cwd":"`+newPath+`"`,
		"the longer, more specific source must be substituted whole, not partially consumed by the shorter oldPath first")
	assert.NotContains(t, string(lines[0]), "/alias\"", "no fragment of the consumed longer source may survive in the rewritten value")
}

// TestPlanAndApplyRolloutFileRefuseWhenNewPathContainsASubstitutionSource
// guards the residual hazard longest-first ordering alone does not close:
// with two or more substitution sources, if newPath's own bytes contain one
// of those sources as a substring, substituting the longer source first
// would write text that then contains the shorter source, and the shorter
// source's own pass would match inside that freshly written text and
// corrupt it — the same failure class as processing sources out of order,
// just triggered by the destination instead of the sources overlapping each
// other. internal/move's nested-move guard (validateNotNested) only refuses
// newPath == oldPath or newPath nested directly under oldPath from the
// root; it does not catch oldPath reappearing as a middle path segment of
// an otherwise-unrelated newPath, so this rollout-level guard must catch it
// independently. Both plan and apply must refuse identically, since both
// derive their substitutions from the same rolloutFileSubstitutions. This
// case has an empty suffix (the offending source resolves to the project
// root exactly), so guardSubstitutionOrder's check on the replacement value
// reduces to checking newPath alone, same as before the guard generalized
// to inspect full replacement values.
func TestPlanAndApplyRolloutFileRefuseWhenNewPathContainsASubstitutionSource(t *testing.T) {
	tempRoot := t.TempDir()
	realProject := filepath.Join(tempRoot, "real", "project")
	require.NoError(t, os.MkdirAll(realProject, 0o750))
	require.NoError(t, os.Symlink(filepath.Join(tempRoot, "real"), filepath.Join(tempRoot, "link")))
	aliasedCWD := filepath.Join(tempRoot, "link", "project")
	// newPath deliberately contains realProject's own bytes as a middle
	// segment, unrelated to nesting from the root — validateNotNested would
	// not refuse this destination.
	newPath := filepath.Join(tempRoot, "elsewhere") + realProject + "/thing"

	path := filepath.Join(tempRoot, "rollout.jsonl")
	line := `{"type":"session_meta","payload":{"id":"thread-1","session_id":"thread-1","cwd":"` + aliasedCWD + `"}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(line), 0o600))

	_, _, err := planRolloutFile(path, realProject, newPath, false, DefaultTranscodeCaps())
	require.ErrorIs(t, err, ErrSubstitutionWouldReintroduceSource, "plan must refuse for the same reason apply would")

	_, _, err = applyRolloutFile(context.Background(), path, realProject, newPath, false, DefaultTranscodeCaps())
	require.ErrorIs(t, err, ErrSubstitutionWouldReintroduceSource, "apply must refuse rather than silently corrupt the stored value")
}

// TestPlanAndApplyRolloutFileRefuseWhenSuffixReintroducesSource closes the
// gap the newPath-only check left open: newPath alone can be shorter than a
// later source, so it can never CONTAIN that source as a substring, yet an
// earlier source's full replacement value (newPath plus its own suffix, the
// part of its canonical form past the project boundary) can still complete
// it. sourceA (via linkA, a symlink to the project's parent) resolves to
// project/nested, suffix "/nested"; sourceB (via linkC, a DIFFERENT symlink
// pointing directly at the project itself) resolves to project/other/nested,
// a different suffix "/other/nested" — chosen deliberately unequal so the
// two sources' correct replacement values differ and any cross-substitution
// is visibly wrong, not a coincidental no-op. newPath is chosen so that
// newPath+suffixA equals sourceB's literal bytes exactly, even though
// newPath alone (missing the "/nested" tail) is shorter than sourceB and
// could never contain it: the detection can only come from inspecting the
// replacement value. Manually verified with the guard disabled (not
// checked in): sourceA's line came back as ".../other/other/nested"
// (newPath+suffixB, the wrong suffix, doubling "other") instead of the
// correct ".../other/nested" (newPath+suffixA).
func TestPlanAndApplyRolloutFileRefuseWhenSuffixReintroducesSource(t *testing.T) {
	tempRoot := t.TempDir()
	realProject := filepath.Join(tempRoot, "real", "project")
	require.NoError(t, os.MkdirAll(filepath.Join(realProject, "nested"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(realProject, "other", "nested"), 0o750))
	require.NoError(t, os.Symlink(filepath.Join(tempRoot, "real"), filepath.Join(tempRoot, "linkA")))
	require.NoError(t, os.Symlink(realProject, filepath.Join(tempRoot, "linkC")))
	sourceA := filepath.Join(tempRoot, "linkA", "project", "nested")
	sourceB := filepath.Join(tempRoot, "linkC", "other", "nested")
	newPath := filepath.Join(tempRoot, "linkC", "other")
	require.Less(t, len(newPath), len(sourceB), "sanity: newPath alone must be shorter than sourceB and so can never contain it")
	require.NotContains(t, newPath, sourceB)

	path := filepath.Join(tempRoot, "rollout.jsonl")
	lines := `{"type":"session_meta","payload":{"id":"thread-1","session_id":"thread-1","cwd":"` + sourceA + `"}}` + "\n" +
		`{"type":"turn_context","payload":{"cwd":"` + sourceB + `"}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(lines), 0o600))

	_, _, err := planRolloutFile(path, realProject, newPath, false, DefaultTranscodeCaps())
	require.ErrorIs(t, err, ErrSubstitutionWouldReintroduceSource, "plan must refuse for the same reason apply would")

	_, _, err = applyRolloutFile(context.Background(), path, realProject, newPath, false, DefaultTranscodeCaps())
	require.ErrorIs(t, err, ErrSubstitutionWouldReintroduceSource, "apply must refuse rather than silently corrupt the stored value")
}

// TestPlanAndApplyRolloutFileRefuseWhenReplacementAndUnchangedTextStraddleASource
// guards the shape a containment-over-replacement-values check cannot
// detect at all: a later source assembled from an earlier substitution's
// output PLUS adjacent bytes the rewrite left unchanged, not from the
// replacement value alone. sourceX (exact alias, suffix "") appears in a
// response_item's free-form prose text, immediately followed by "/bar" —
// ordinary trailing text, not itself a recorded cwd. Substituting sourceX
// with newPath (deep mode reaches this prose) leaves "newPath/bar" in the
// text. newPath is chosen so "newPath/bar" exactly equals sourceY's own
// literal bytes: sourceY is recorded (via a second alias resolving through
// a symlink literally named "bar") as a genuine, independent substitution
// source with its OWN suffix ("/elsewhere", via bar -> elsewhere), so its
// later pass matches the just-assembled text and corrupts it to
// "newPath/elsewhere" — prose that was never supposed to be touched beyond
// the sourceX swap. newPath alone (shorter than sourceY, missing the
// "/bar" tail) could never contain sourceY, and no single substitution's
// replacement VALUE (newPath, or newPath+suffixX="") contains sourceY's
// bytes either, since "/bar" comes from the untouched prose, not from any
// substitution's own output — a containment-over-replacement-values check
// has nothing to inspect that would catch this. Manually verified with the
// guard disabled (not checked in): the response_item line came back
// reading ".../linkY/elsewhere && ls" instead of the correct
// ".../linkY/bar && ls".
func TestPlanAndApplyRolloutFileRefuseWhenReplacementAndUnchangedTextStraddleASource(t *testing.T) {
	tempRoot := t.TempDir()
	realProject := filepath.Join(tempRoot, "real", "project")
	require.NoError(t, os.MkdirAll(filepath.Join(realProject, "elsewhere"), 0o750))
	require.NoError(t, os.Symlink("elsewhere", filepath.Join(realProject, "bar")))
	require.NoError(t, os.Symlink(filepath.Join(tempRoot, "real"), filepath.Join(tempRoot, "linkX")))
	require.NoError(t, os.Symlink(realProject, filepath.Join(tempRoot, "linkY")))
	sourceX := filepath.Join(tempRoot, "linkX", "project")
	sourceY := filepath.Join(tempRoot, "linkY", "bar")
	newPath := filepath.Join(tempRoot, "linkY")
	require.NotContains(t, newPath, sourceY, "sanity: newPath alone must be shorter than sourceY and so can never contain it")

	path := filepath.Join(tempRoot, "rollout.jsonl")
	lines := `{"type":"session_meta","payload":{"id":"thread-1","session_id":"thread-1","cwd":"` + sourceX + `"}}` + "\n" +
		`{"type":"turn_context","payload":{"cwd":"` + sourceY + `"}}` + "\n" +
		`{"type":"response_item","payload":{"text":"ran: cd ` + sourceX + `/bar && ls"}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(lines), 0o600))

	_, _, err := planRolloutFile(path, realProject, newPath, true, DefaultTranscodeCaps())
	require.ErrorIs(t, err, ErrSubstitutionWouldReintroduceSource, "plan must refuse for the same reason apply would")

	_, _, err = applyRolloutFile(context.Background(), path, realProject, newPath, true, DefaultTranscodeCaps())
	require.ErrorIs(t, err, ErrSubstitutionWouldReintroduceSource, "apply must refuse rather than silently corrupt the stored value")
}

// TestApplyRolloutFileSingleSourceSucceedsWhenNewPathContainsOldPath is the
// over-refusal regression guard: with exactly one substitution source, a
// newPath that contains oldPath as a literal prefix is completely safe (one
// rewrite.ReplacePathInBytes pass never re-scans its own output), so
// guardSubstitutionOrder must not fire here even though the same substring
// relationship triggers the multi-source guard above.
func TestApplyRolloutFileSingleSourceSucceedsWhenNewPathContainsOldPath(t *testing.T) {
	tempRoot := t.TempDir()
	project := filepath.Join(tempRoot, "project")
	newPath := project + "-renamed"

	path := filepath.Join(tempRoot, "rollout.jsonl")
	line := `{"type":"session_meta","payload":{"id":"thread-1","session_id":"thread-1","cwd":"` + project + `"}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(line), 0o600))

	planCount, eraA, err := planRolloutFile(path, project, newPath, false, DefaultTranscodeCaps())
	require.NoError(t, err)
	require.False(t, eraA)
	require.Positive(t, planCount, "sanity: the recorded cwd must be counted")

	applyCount, eraA, err := applyRolloutFile(context.Background(), path, project, newPath, false, DefaultTranscodeCaps())
	require.NoError(t, err)
	require.False(t, eraA)
	assert.Equal(t, planCount, applyCount)

	lines, _, err := readRolloutLines(path, DefaultTranscodeCaps())
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Contains(t, string(lines[0]), `"cwd":"`+newPath+`"`, "a single source must rewrite correctly even when newPath contains oldPath as a prefix")
}

func TestApplyRolloutFileLeavesEraAFileByteIdentical(t *testing.T) {
	home := SetupFixture(t)
	path := rolloutFixturePath(home, eraAPath)
	before, _, err := readRolloutLines(path, DefaultTranscodeCaps())
	require.NoError(t, err)

	_, eraA, err := applyRolloutFile(context.Background(), path, FixtureProjectPath(), "/Users/fixture/renamed-project", true, DefaultTranscodeCaps())
	require.NoError(t, err)
	require.True(t, eraA)

	after, _, err := readRolloutLines(path, DefaultTranscodeCaps())
	require.NoError(t, err)
	assert.Equal(t, before, after)
}
