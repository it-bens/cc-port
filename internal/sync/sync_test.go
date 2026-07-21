package sync

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

func testHostname() (string, error) { return "test-host", nil }

func testGetenv(string) string { return "test-user" }

func testCurrentUser() (*user.User, error) { return &user.User{Username: "test-user"}, nil }

func TestSelfPusher_OnConfiguredMachineReturnsHostUser(t *testing.T) {
	got, err := selfPusher(os.Hostname, os.Getenv, user.Current)
	if err != nil {
		t.Fatalf("selfPusher: %v", err)
	}
	if got == "" {
		t.Fatal("selfPusher returned empty string")
	}
	if !strings.Contains(got, "-") {
		t.Fatalf("selfPusher = %q, want hyphen-separated host-user", got)
	}
}

func TestSelfPusher_EmptyUsernameReturnsError(t *testing.T) {
	_, err := selfPusher(
		func() (string, error) { return "test-host", nil },
		func(string) string { return "" },
		func() (*user.User, error) { return &user.User{}, nil },
	)

	require.Error(t, err)
	assert.ErrorContains(t, err, "username is empty")
}

func TestSelfPusher_UsesIdentitySeams(t *testing.T) {
	got, err := selfPusher(
		func() (string, error) { return "test-host", nil },
		func(string) string { return "test-user" },
		func() (*user.User, error) { return nil, errors.New("should not be called") },
	)

	require.NoError(t, err)
	assert.Equal(t, "test-host-test-user", got)
}

func TestPlanPush_NoPriorYieldsEmptyConflictFields(t *testing.T) {
	r := newFileRemote(t)
	targets, projectPath := buildTestTargets(t)

	prior := openPriorForTest(t, r, "fresh-name", "")
	plan, err := PlanPush(context.Background(), PushOptions{
		Targets:     targets,
		ProjectPath: projectPath,
		Name:        "fresh-name",
		Selected:    allSelection(),
		Hostname:    testHostname,
		Getenv:      testGetenv,
		CurrentUser: testCurrentUser,
	}, prior)
	if err != nil {
		t.Fatalf("PlanPush: %v", err)
	}
	if plan.PriorPushedBy != "" || plan.CrossMachine {
		t.Fatalf("expected no prior; got PriorPushedBy=%q CrossMachine=%v", plan.PriorPushedBy, plan.CrossMachine)
	}
	if plan.SelfPusher == "" {
		t.Fatal("expected non-empty SelfPusher")
	}
}

func TestPlanPush_PriorSameSelfNotCrossMachine(t *testing.T) {
	r := newFileRemote(t)
	targets, projectPath := buildTestTargets(t)

	priorA := openPriorForTest(t, r, "k", "")
	planA, err := PlanPush(context.Background(), PushOptions{
		Targets: targets, ProjectPath: projectPath, Name: "k",
		Selected: allSelection(),
		Hostname: testHostname, Getenv: testGetenv, CurrentUser: testCurrentUser,
	}, priorA)
	if err != nil {
		t.Fatalf("PlanPush A: %v", err)
	}
	writerA := openWriterForTest(t, r, "k", "")
	if _, err := ExecutePush(context.Background(), PushOptions{
		Targets: targets, ProjectPath: projectPath, Name: "k",
		Selected: allSelection(),
		Hostname: testHostname, Getenv: testGetenv, CurrentUser: testCurrentUser,
	}, planA, writerA); err != nil {
		t.Fatalf("ExecutePush: %v", err)
	}
	if err := writerA.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	priorB := openPriorForTest(t, r, "k", "")
	planB, err := PlanPush(context.Background(), PushOptions{
		Targets: targets, ProjectPath: projectPath, Name: "k",
		Selected: allSelection(),
		Hostname: testHostname, Getenv: testGetenv, CurrentUser: testCurrentUser,
	}, priorB)
	if err != nil {
		t.Fatalf("PlanPush B: %v", err)
	}
	if planB.PriorPushedBy != planB.SelfPusher {
		t.Fatalf("PriorPushedBy=%q SelfPusher=%q; expected match", planB.PriorPushedBy, planB.SelfPusher)
	}
	if planB.CrossMachine {
		t.Fatal("CrossMachine = true, want false (same self)")
	}
}

func TestPlanPush_PriorDifferentSelfFlagsCrossMachine(t *testing.T) {
	r := newFileRemote(t)
	injectArchiveWithPusher(t, r, "k", "different-host-different-user", time.Now().UTC().Add(-1*time.Hour))

	targets, projectPath := buildTestTargets(t)
	prior := openPriorForTest(t, r, "k", "")
	plan, err := PlanPush(context.Background(), PushOptions{
		Targets: targets, ProjectPath: projectPath, Name: "k",
		Selected: allSelection(),
		Hostname: testHostname, Getenv: testGetenv, CurrentUser: testCurrentUser,
	}, prior)
	if err != nil {
		t.Fatalf("PlanPush: %v", err)
	}
	if !plan.CrossMachine {
		t.Fatalf("CrossMachine = false, want true (different SelfPusher: %q vs %q)",
			plan.PriorPushedBy, plan.SelfPusher)
	}
}

func TestPlanPush_RejectsIdentityFailureRegardlessOfForce(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run("force="+strconv.FormatBool(force), func(t *testing.T) {
			_, err := PlanPush(context.Background(), PushOptions{
				Name:        "k",
				Hostname:    func() (string, error) { return "", nil },
				Getenv:      testGetenv,
				CurrentUser: testCurrentUser,
				Force:       force,
			}, nil)

			require.Error(t, err)
			assert.ErrorContains(t, err, "derive self identity")
		})
	}
}

func TestPlanPush_ForceAllowsCrossMachinePrior(t *testing.T) {
	r := newFileRemote(t)
	injectArchiveWithPusher(t, r, "k", "different-host-different-user", time.Now().UTC().Add(-time.Hour))
	prior := openPriorForTest(t, r, "k", "")

	plan, err := PlanPush(context.Background(), PushOptions{
		Name:        "k",
		Hostname:    testHostname,
		Getenv:      testGetenv,
		CurrentUser: testCurrentUser,
		Force:       true,
	}, prior)

	require.NoError(t, err)
	assert.True(t, plan.CrossMachine)
}

func TestExecutePush_RoundTripWritesArchiveWithSyncFields(t *testing.T) {
	r := newFileRemote(t)
	targets, projectPath := buildTestTargets(t)

	prior := openPriorForTest(t, r, "k", "")
	plan, err := PlanPush(context.Background(), PushOptions{
		Targets: targets, ProjectPath: projectPath, Name: "k",
		Selected: allSelection(),
		Hostname: testHostname, Getenv: testGetenv, CurrentUser: testCurrentUser,
	}, prior)
	if err != nil {
		t.Fatalf("PlanPush: %v", err)
	}
	fixed := time.Date(2021, 1, 2, 3, 4, 5, 0, time.UTC)
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = time.Now })

	writer := openWriterForTest(t, r, "k", "")
	if _, err := ExecutePush(context.Background(), PushOptions{
		Targets: targets, ProjectPath: projectPath, Name: "k",
		Selected: allSelection(),
	}, plan, writer); err != nil {
		t.Fatalf("ExecutePush: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	rc, err := r.Open(context.Background(), "k")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	metadata, err := manifest.ReadManifestFromZip(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("ReadManifestFromZip: %v", err)
	}
	if metadata.SyncPushedBy != plan.SelfPusher {
		t.Fatalf("SyncPushedBy = %q, want %q", metadata.SyncPushedBy, plan.SelfPusher)
	}
	if want := fixed.Format(time.RFC3339); metadata.SyncPushedAt != want {
		t.Fatalf("SyncPushedAt = %q, want %q", metadata.SyncPushedAt, want)
	}
}

func TestExecutePush_ReturnsExportWarnings(t *testing.T) {
	home, projectPath := buildTestHomeAndProject(t)
	require.NoError(t, os.MkdirAll(home.RulesDir(), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(home.RulesDir(), "push-rule.md"),
		[]byte("Applies to "+projectPath+" only.\n"),
		0o600,
	))
	targets := targetsFor(home)
	r := newFileRemote(t)
	plan, err := PlanPush(context.Background(), PushOptions{
		Targets: targets, ProjectPath: projectPath, Name: "k",
		Selected: allSelection(),
		Hostname: testHostname, Getenv: testGetenv, CurrentUser: testCurrentUser,
	}, openPriorForTest(t, r, "k", ""))
	require.NoError(t, err)

	writer := openWriterForTest(t, r, "k", "")
	result, err := ExecutePush(context.Background(), PushOptions{
		Targets: targets, ProjectPath: projectPath, Name: "k", Selected: allSelection(),
	}, plan, writer)

	require.NoError(t, err)
	require.NoError(t, writer.Close())
	assert.Contains(t, result.ByTool["claude"].Warnings, "rules file push-rule.md (line 1) references this project")
}

func TestPlanPull_PopulatesPlaceholdersFromManifest(t *testing.T) {
	r := newFileRemote(t)
	// Original must be a path the fixture's own export bodies actually
	// contain, so the placeholder token is embedded and the corrected
	// referenced-in-body classifier (finding FE3) still flags it as
	// unresolved. A never-referenced Original would legitimately no longer
	// be flagged, which is exactly the bug this classifier fixes.
	injectArchiveWithDeclaredPlaceholder(t, r, "k", "{{ORG}}", testutil.FixtureProjectPath(), "host-user")
	targets, _ := buildTestTargets(t)

	source := openSourceForTest(t, r, "k", "")
	plan, err := PlanPull(context.Background(), PullOptions{
		AllTools: toolSetForTest(), Targets: targets, Name: "k", TargetPath: t.TempDir(),
	}, source)
	if err != nil {
		t.Fatalf("PlanPull: %v", err)
	}
	unresolved := plan.UnresolvedPlaceholders["claude"]
	if len(unresolved) != 1 || unresolved[0] != "{{ORG}}" {
		t.Fatalf("UnresolvedPlaceholders[claude] = %v, want [{{ORG}}]", unresolved)
	}
}

func TestPlanPull_SenderProvidedResolveClearsUnresolved(t *testing.T) {
	r := newFileRemote(t)
	injectArchiveWithSenderResolve(t, r, "k", "{{ORG}}", "/Users/sender", "host-user")
	targets, _ := buildTestTargets(t)
	source := openSourceForTest(t, r, "k", "")
	plan, err := PlanPull(context.Background(), PullOptions{
		AllTools: toolSetForTest(), Targets: targets, Name: "k", TargetPath: t.TempDir(),
	}, source)
	if err != nil {
		t.Fatalf("PlanPull: %v", err)
	}
	if len(plan.UnresolvedPlaceholders["claude"]) != 0 {
		t.Fatalf("UnresolvedPlaceholders[claude] = %v, want empty (sender Resolve covers)", plan.UnresolvedPlaceholders["claude"])
	}
}

func TestPullPlanApplyGateParity(t *testing.T) {
	tests := []struct {
		name        string
		archive     func(*testing.T) []byte
		from        *manifest.Metadata
		assertError func(*testing.T, error)
	}{
		{
			name: "implicit-key override in from-manifest",
			archive: func(t *testing.T) []byte {
				targets, projectPath := buildTestTargets(t)
				return buildArchiveBytes(t, targets, projectPath, "host-user", time.Now().UTC(), map[string][]manifest.Placeholder{
					"claude": {{Key: "{{PROJECT_PATH}}", Original: projectPath}},
				}, "")
			},
			from: &manifest.Metadata{Tools: []manifest.Tool{{
				Name:         "claude",
				Placeholders: []manifest.Placeholder{{Key: "{{PROJECT_PATH}}", Resolve: "/sender/project"}},
			}}},
			assertError: func(t *testing.T, err error) {
				var typed *importer.ImplicitKeyOverrideError
				require.ErrorAs(t, err, &typed)
			},
		},
		{
			name: "undeclared from-manifest key",
			archive: func(t *testing.T) []byte {
				targets, projectPath := buildTestTargets(t)
				return buildArchiveBytes(t, targets, projectPath, "host-user", time.Now().UTC(), map[string][]manifest.Placeholder{
					"claude": {{Key: "{{DECLARED}}", Original: projectPath}},
				}, "")
			},
			from: &manifest.Metadata{Tools: []manifest.Tool{{
				Name:         "claude",
				Placeholders: []manifest.Placeholder{{Key: "{{UNDECLARED}}", Resolve: "/sender/project"}},
			}}},
			assertError: func(t *testing.T, err error) {
				var typed *importer.UndeclaredResolutionKeysError
				require.ErrorAs(t, err, &typed)
			},
		},
		{
			name: "non-absolute resolution value",
			archive: func(t *testing.T) []byte {
				targets, projectPath := buildTestTargets(t)
				return buildArchiveBytes(t, targets, projectPath, "host-user", time.Now().UTC(), map[string][]manifest.Placeholder{
					"claude": {{Key: "{{DECLARED}}", Original: projectPath, Resolve: "relative/path"}},
				}, "")
			},
			assertError: func(t *testing.T, err error) {
				var typed *archive.InvalidResolutionsError
				require.ErrorAs(t, err, &typed)
			},
		},
		{
			name: "archive manifest names an unregistered tool",
			archive: func(t *testing.T) []byte {
				return archiveWithManifest(t, &manifest.Metadata{Tools: []manifest.Tool{{Name: "unregistered"}}})
			},
			assertError: func(t *testing.T, err error) {
				var typed *manifest.UnregisteredToolError
				require.ErrorAs(t, err, &typed)
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			body := testCase.archive(t)
			home := buildTestHomeBlank(t)
			opts := PullOptions{
				AllTools:     toolSetForTest(),
				Targets:      targetsFor(home),
				Name:         "k",
				TargetPath:   filepath.Join(t.TempDir(), "pulled-project"),
				FromManifest: testCase.from,
			}
			source := pipeline.Source{View: pipeline.View{ReaderAt: bytes.NewReader(body), Size: int64(len(body))}}

			plan, planErr := PlanPull(context.Background(), opts, source)

			require.Nil(t, plan)
			require.Error(t, planErr)
			testCase.assertError(t, planErr)

			_, applyErr := importer.Run(context.Background(), opts.AllTools, opts.Targets, &importer.Options{
				Source:       source.ReaderAt,
				Size:         source.Size,
				TargetPath:   opts.TargetPath,
				Caps:         archive.DefaultCaps(),
				FromManifest: opts.FromManifest,
			})

			require.Error(t, applyErr)
			testCase.assertError(t, applyErr)
		})
	}
}

func archiveWithManifest(t *testing.T, metadata *manifest.Metadata) []byte {
	t.Helper()
	data, err := xml.Marshal(metadata)
	require.NoError(t, err)

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	entry, err := writer.Create("metadata.xml")
	require.NoError(t, err)
	_, err = entry.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return buf.Bytes()
}

// TestPullImportGateAgree pins that PlanPull's unresolved-placeholder
// classification and the import preflight ExecutePull runs under the hood
// agree on the same archive (finding FE3): pull must no longer refuse an
// archive plain import accepts, and both must still refuse an archive that
// genuinely has an unresolved, referenced placeholder.
func TestPullImportGateAgree(t *testing.T) {
	const key = "{{SECRET}}"

	tests := []struct {
		name        string
		original    string
		wantRefused bool
	}{
		{
			name:        "declared, referenced, unresolved: both refuse",
			original:    testutil.FixtureProjectPath(),
			wantRefused: true,
		},
		{
			name:        "declared but never referenced: both accept",
			original:    "/Users/sender/never-referenced",
			wantRefused: false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			r := newFileRemote(t)
			injectArchiveWithDeclaredPlaceholder(t, r, "k", key, testCase.original, "host-user")
			homeB := buildTestHomeBlank(t)
			targetsB := targetsFor(homeB)
			targetPath := filepath.Join(t.TempDir(), "pulled-project")

			source := openSourceForTest(t, r, "k", "")
			pullOpts := PullOptions{AllTools: toolSetForTest(), Targets: targetsB, Name: "k", TargetPath: targetPath}
			plan, err := PlanPull(context.Background(), pullOpts, source)
			require.NoError(t, err)

			planRefuses := len(plan.UnresolvedPlaceholders["claude"]) > 0
			assert.Equal(t, testCase.wantRefused, planRefuses, "PlanPull's unresolved-placeholder verdict")

			_, err = ExecutePull(context.Background(), pullOpts, plan, source)

			if testCase.wantRefused {
				require.Error(t, err, "import preflight must refuse the same archive PlanPull flagged unresolved")
				var missingErr *importer.MissingResolutionsError
				assert.ErrorAs(t, err, &missingErr)
			} else {
				require.NoError(t, err, "import preflight must accept the same archive PlanPull cleared")
			}
		})
	}
}

func TestExecutePull_RoundTripFromFileRemote(t *testing.T) {
	r := newFileRemote(t)
	targetsA, projectPathA := buildTestTargets(t)

	priorA := openPriorForTest(t, r, "k", "")
	planA, err := PlanPush(context.Background(), PushOptions{
		Targets: targetsA, ProjectPath: projectPathA, Name: "k",
		Selected: allSelection(),
		Hostname: testHostname, Getenv: testGetenv, CurrentUser: testCurrentUser,
	}, priorA)
	if err != nil {
		t.Fatalf("PlanPush: %v", err)
	}
	writerA := openWriterForTest(t, r, "k", "")
	if _, err := ExecutePush(context.Background(), PushOptions{
		Targets: targetsA, ProjectPath: projectPathA, Name: "k",
		Selected: allSelection(),
	}, planA, writerA); err != nil {
		t.Fatalf("ExecutePush: %v", err)
	}
	if err := writerA.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	homeB := buildTestHomeBlank(t)
	targetsB := targetsFor(homeB)
	targetPath := filepath.Join(t.TempDir(), "pulled-project")

	source := openSourceForTest(t, r, "k", "")
	planB, err := PlanPull(context.Background(), PullOptions{
		AllTools: toolSetForTest(), Targets: targetsB, Name: "k", TargetPath: targetPath,
	}, source)
	if err != nil {
		t.Fatalf("PlanPull: %v", err)
	}
	if len(planB.UnresolvedPlaceholders["claude"]) != 0 {
		t.Fatalf("unresolved: %v", planB.UnresolvedPlaceholders["claude"])
	}
	if _, err := ExecutePull(context.Background(), PullOptions{
		AllTools: toolSetForTest(), Targets: targetsB, Name: "k", TargetPath: targetPath,
	}, planB, source); err != nil {
		t.Fatalf("ExecutePull: %v", err)
	}

	encodedDir := claude.EncodePath(targetPath)
	if _, err := os.Stat(filepath.Join(homeB.Dir, "projects", encodedDir)); err != nil {
		t.Fatalf("encoded project dir missing after pull: %v", err)
	}
}
