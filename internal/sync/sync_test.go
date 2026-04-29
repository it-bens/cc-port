package sync

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/manifest"
)

func TestSelfPusher_OnConfiguredMachineReturnsHostUser(t *testing.T) {
	got, err := selfPusher()
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
	// Force the empty-username branch by clearing $USER. On most CI
	// runners the user lookup succeeds via /etc/passwd; this test
	// exercises only the env-clearing path and accepts a pass when
	// the platform-level fallback fills the username.
	t.Setenv("USER", "")
	got, err := selfPusher()
	if err != nil {
		// Empty-username branch fired; correct.
		return
	}
	if got == "" {
		t.Fatal("selfPusher returned empty string with no error")
	}
	// Platform supplied a username via os/user.Current(); also correct.
}

func TestPlanPush_NoPriorYieldsEmptyConflictFields(t *testing.T) {
	r := newMemRemote(t)
	home, projectPath := buildTestHomeAndProject(t)

	plan, err := PlanPush(context.Background(), PushOptions{
		ClaudeHome:  home,
		ProjectPath: projectPath,
		Remote:      r,
		Name:        "fresh-name",
		Categories:  allCategoriesSet(),
	})
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
	r := newMemRemote(t)
	home, projectPath := buildTestHomeAndProject(t)

	planA, err := PlanPush(context.Background(), PushOptions{
		ClaudeHome: home, ProjectPath: projectPath, Remote: r, Name: "k",
		Categories: allCategoriesSet(),
	})
	if err != nil {
		t.Fatalf("PlanPush A: %v", err)
	}
	if err := ExecutePush(context.Background(), PushOptions{
		ClaudeHome: home, ProjectPath: projectPath, Remote: r, Name: "k",
		Categories: allCategoriesSet(),
	}, planA); err != nil {
		t.Fatalf("ExecutePush: %v", err)
	}

	planB, err := PlanPush(context.Background(), PushOptions{
		ClaudeHome: home, ProjectPath: projectPath, Remote: r, Name: "k",
		Categories: allCategoriesSet(),
	})
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
	r := newMemRemote(t)
	injectArchiveWithPusher(t, r, "k", "different-host-different-user", time.Now().UTC().Add(-1*time.Hour))

	home, projectPath := buildTestHomeAndProject(t)
	plan, err := PlanPush(context.Background(), PushOptions{
		ClaudeHome: home, ProjectPath: projectPath, Remote: r, Name: "k",
		Categories: allCategoriesSet(),
	})
	if err != nil {
		t.Fatalf("PlanPush: %v", err)
	}
	if !plan.CrossMachine {
		t.Fatalf("CrossMachine = false, want true (different SelfPusher: %q vs %q)",
			plan.PriorPushedBy, plan.SelfPusher)
	}
}

func TestPlanPush_PriorEncryptedNoPassphraseReturnsErrPassphraseRequired(t *testing.T) {
	r := newMemRemote(t)
	injectEncryptedArchive(t, r, "k", testPass, "other-host-other-user", time.Now().UTC())

	home, projectPath := buildTestHomeAndProject(t)
	_, err := PlanPush(context.Background(), PushOptions{
		ClaudeHome: home, ProjectPath: projectPath, Remote: r, Name: "k",
		Categories: allCategoriesSet(),
		// Passphrase deliberately empty
	})
	if !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("err = %v, want sync.ErrPassphraseRequired wrap", err)
	}
}

func TestExecutePush_RoundTripWritesArchiveWithSyncFields(t *testing.T) {
	r := newMemRemote(t)
	home, projectPath := buildTestHomeAndProject(t)

	plan, err := PlanPush(context.Background(), PushOptions{
		ClaudeHome: home, ProjectPath: projectPath, Remote: r, Name: "k",
		Categories: allCategoriesSet(),
	})
	if err != nil {
		t.Fatalf("PlanPush: %v", err)
	}
	before := time.Now().UTC()
	if err := ExecutePush(context.Background(), PushOptions{
		ClaudeHome: home, ProjectPath: projectPath, Remote: r, Name: "k",
		Categories: allCategoriesSet(),
	}, plan); err != nil {
		t.Fatalf("ExecutePush: %v", err)
	}
	after := time.Now().UTC()

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
	if metadata.SyncPushedAt == "" {
		t.Fatal("SyncPushedAt empty")
	}
	pushedAt, err := time.Parse(time.RFC3339, metadata.SyncPushedAt)
	if err != nil {
		t.Fatalf("Parse SyncPushedAt: %v", err)
	}
	if pushedAt.Before(before.Add(-1*time.Second)) || pushedAt.After(after.Add(1*time.Second)) {
		t.Fatalf("SyncPushedAt %v outside [%v, %v]", pushedAt, before, after)
	}
}

func TestPlanPull_NotFoundReturnsErrRemoteNotFound(t *testing.T) {
	r := newMemRemote(t)
	home, _ := buildTestHomeAndProject(t)
	_, err := PlanPull(context.Background(), PullOptions{
		ClaudeHome: home, Remote: r, Name: "missing", TargetPath: t.TempDir(),
	})
	if !errors.Is(err, ErrRemoteNotFound) {
		t.Fatalf("err = %v, want ErrRemoteNotFound", err)
	}
}

func TestPlanPull_EncryptedNoPassphraseReturnsErrPassphraseRequired(t *testing.T) {
	r := newMemRemote(t)
	injectEncryptedArchive(t, r, "k", testPass, "host-user", time.Now().UTC())
	home, _ := buildTestHomeAndProject(t)
	_, err := PlanPull(context.Background(), PullOptions{
		ClaudeHome: home, Remote: r, Name: "k", TargetPath: t.TempDir(),
		// Passphrase deliberately empty
	})
	if !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("err = %v, want ErrPassphraseRequired", err)
	}
}

func TestPlanPull_PlaintextWithPassphraseReturnsErrUnencryptedInput(t *testing.T) {
	r := newMemRemote(t)
	injectArchiveWithPusher(t, r, "k", "host-user", time.Now().UTC())
	home, _ := buildTestHomeAndProject(t)
	_, err := PlanPull(context.Background(), PullOptions{
		ClaudeHome: home, Remote: r, Name: "k", TargetPath: t.TempDir(),
		Passphrase: testPass,
	})
	if !errors.Is(err, encrypt.ErrUnencryptedInput) {
		t.Fatalf("err = %v, want encrypt.ErrUnencryptedInput", err)
	}
}

func TestPlanPull_PopulatesPlaceholdersFromManifest(t *testing.T) {
	r := newMemRemote(t)
	injectArchiveWithDeclaredPlaceholder(t, r, "k", "{{HOME}}", "/Users/sender", "host-user")
	home, _ := buildTestHomeAndProject(t)

	plan, err := PlanPull(context.Background(), PullOptions{
		ClaudeHome: home, Remote: r, Name: "k", TargetPath: t.TempDir(),
		// no Resolutions
	})
	if err != nil {
		t.Fatalf("PlanPull: %v", err)
	}
	if len(plan.UnresolvedPlaceholders) != 1 || plan.UnresolvedPlaceholders[0] != "{{HOME}}" {
		t.Fatalf("UnresolvedPlaceholders = %v, want [{{HOME}}]", plan.UnresolvedPlaceholders)
	}
}

func TestPlanPull_ResolutionMapClearsUnresolved(t *testing.T) {
	r := newMemRemote(t)
	injectArchiveWithDeclaredPlaceholder(t, r, "k", "{{HOME}}", "/Users/sender", "host-user")
	home, _ := buildTestHomeAndProject(t)
	plan, err := PlanPull(context.Background(), PullOptions{
		ClaudeHome: home, Remote: r, Name: "k", TargetPath: t.TempDir(),
		Resolutions: map[string]string{"{{HOME}}": "/Users/me"},
	})
	if err != nil {
		t.Fatalf("PlanPull: %v", err)
	}
	if len(plan.UnresolvedPlaceholders) != 0 {
		t.Fatalf("UnresolvedPlaceholders = %v, want empty", plan.UnresolvedPlaceholders)
	}
}

func TestPlanPull_SenderProvidedResolveClearsUnresolved(t *testing.T) {
	// A placeholder whose archive manifest carries a non-empty Resolve
	// counts as covered, matching cc-port import's
	// promptImportResolutions behavior. No --resolution or
	// --from-manifest is supplied here.
	r := newMemRemote(t)
	injectArchiveWithSenderResolve(t, r, "k", "{{HOME}}", "/Users/sender", "host-user")
	home, _ := buildTestHomeAndProject(t)
	plan, err := PlanPull(context.Background(), PullOptions{
		ClaudeHome: home, Remote: r, Name: "k", TargetPath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("PlanPull: %v", err)
	}
	if len(plan.UnresolvedPlaceholders) != 0 {
		t.Fatalf("UnresolvedPlaceholders = %v, want empty (sender Resolve covers)", plan.UnresolvedPlaceholders)
	}
}

func TestExecutePull_RoundTripFromMemRemote(t *testing.T) {
	r := newMemRemote(t)
	homeA, projectPathA := buildTestHomeAndProject(t)

	planA, err := PlanPush(context.Background(), PushOptions{
		ClaudeHome: homeA, ProjectPath: projectPathA, Remote: r, Name: "k",
		Categories: allCategoriesSet(),
	})
	if err != nil {
		t.Fatalf("PlanPush: %v", err)
	}
	if err := ExecutePush(context.Background(), PushOptions{
		ClaudeHome: homeA, ProjectPath: projectPathA, Remote: r, Name: "k",
		Categories: allCategoriesSet(),
	}, planA); err != nil {
		t.Fatalf("ExecutePush: %v", err)
	}

	homeB := buildTestHomeBlank(t)
	targetPath := filepath.Join(t.TempDir(), "pulled-project")

	planB, err := PlanPull(context.Background(), PullOptions{
		ClaudeHome: homeB, Remote: r, Name: "k", TargetPath: targetPath,
		Resolutions: defaultResolutionsForTest(t),
	})
	if err != nil {
		t.Fatalf("PlanPull: %v", err)
	}
	if len(planB.UnresolvedPlaceholders) != 0 {
		t.Fatalf("unresolved: %v", planB.UnresolvedPlaceholders)
	}
	if err := ExecutePull(context.Background(), PullOptions{
		ClaudeHome: homeB, Remote: r, Name: "k", TargetPath: targetPath,
		Resolutions: defaultResolutionsForTest(t),
	}, planB); err != nil {
		t.Fatalf("ExecutePull: %v", err)
	}

	encodedDir := claude.EncodePath(targetPath)
	if _, err := os.Stat(filepath.Join(homeB.Dir, "projects", encodedDir)); err != nil {
		t.Fatalf("encoded project dir missing after pull: %v", err)
	}
}

func TestSentinels_AreNonNil(t *testing.T) {
	for _, e := range []error{
		ErrCrossMachineConflict,
		ErrRemoteNotFound,
		ErrPassphraseRequired,
		ErrUnresolvedPlaceholder,
	} {
		if e == nil {
			t.Fatal("nil sentinel error")
		}
	}
	if !errors.Is(ErrRemoteNotFound, ErrRemoteNotFound) {
		t.Fatal("errors.Is identity broken")
	}
}
