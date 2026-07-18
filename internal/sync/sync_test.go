package sync

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/manifest"
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
	if err := ExecutePush(context.Background(), PushOptions{
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
	if err := ExecutePush(context.Background(), PushOptions{
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

func TestPlanPull_PopulatesPlaceholdersFromManifest(t *testing.T) {
	r := newFileRemote(t)
	injectArchiveWithDeclaredPlaceholder(t, r, "k", "{{ORG}}", "/Users/sender", "host-user")
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
	if err := ExecutePush(context.Background(), PushOptions{
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
