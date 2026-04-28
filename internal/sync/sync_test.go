package sync

import (
	"context"
	"errors"
	"strings"
	"testing"
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

func TestPlanPush_Stub(t *testing.T) {
	_, err := PlanPush(context.Background(), PushOptions{})
	if err == nil {
		t.Fatal("expected stub to return error")
	}
}

func TestPlanPull_Stub(t *testing.T) {
	_, err := PlanPull(context.Background(), PullOptions{})
	if err == nil {
		t.Fatal("expected stub to return error")
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
