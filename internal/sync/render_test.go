package sync

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/it-bens/cc-port/internal/manifest"
)

func TestPushPlan_RenderNoPriorPlaintext(t *testing.T) {
	plan := &PushPlan{
		Name:              "myproj",
		SelfPusher:        "laptop1-alice",
		Categories:        allCategoriesSet(),
		EncryptionEnabled: false,
	}
	var buf bytes.Buffer
	if err := plan.Render(&buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"cc-port push myproj",
		"Pipeline: export -> remote sink",
		"Categories: all",
		"Encryption: disabled",
		"Self pusher:  laptop1-alice",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Prior remote") {
		t.Fatalf("output should not include Prior remote section:\n%s", got)
	}
}

func TestPushPlan_RenderEncryptedWithPriorAndCrossMachine(t *testing.T) {
	plan := &PushPlan{
		Name:              "myproj",
		SelfPusher:        "laptop1-alice",
		Categories:        allCategoriesSet(),
		PriorPushedBy:     "laptop2-bob",
		PriorPushedAt:     time.Date(2026, 4, 25, 14, 32, 18, 0, time.UTC),
		PriorEncrypted:    true,
		PriorSize:         5_242_880, // 5 MiB
		EncryptionEnabled: true,
		CrossMachine:      true,
	}
	var buf bytes.Buffer
	if err := plan.Render(&buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"Pipeline: export -> encrypt -> remote sink",
		"Categories: all",
		"Encryption: enabled",
		"Pushed by:   laptop2-bob",
		"Pushed at:   2026-04-25T14:32:18Z",
		"Size:        5.0 MiB",
		"Encrypted:   yes",
		"Cross-machine conflict",
		"--force",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestPullPlan_RenderWithUnresolvedPlaceholders(t *testing.T) {
	plan := &PullPlan{
		Name:                   "myproj",
		RemotePushedBy:         "laptop1-alice",
		RemotePushedAt:         time.Date(2026, 4, 25, 14, 32, 18, 0, time.UTC),
		RemoteSize:             5_242_880,
		Categories:             allCategoriesSet(),
		DeclaredPlaceholders:   []manifest.Placeholder{{Key: "{{HOME}}"}, {Key: "{{CACHE}}"}},
		UnresolvedPlaceholders: []string{"{{CACHE}}"},
	}
	var buf bytes.Buffer
	if err := plan.Render(&buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"cc-port pull myproj",
		"Pipeline: remote source -> import core",
		"Categories:  all",
		"{{HOME}}",
		"{{CACHE}}",
		"MISSING; supply --from-manifest",
		"1 placeholder unresolved",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestPullPlan_RenderEncryptedClean(t *testing.T) {
	plan := &PullPlan{
		Name:            "x",
		RemoteEncrypted: true,
		RemoteSize:      1024,
		Categories:      allCategoriesSet(),
	}
	var buf bytes.Buffer
	if err := plan.Render(&buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"Pipeline: remote source -> decrypt -> import core",
		"Encryption: enabled",
		"Size:        1.0 KiB",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "placeholder unresolved") {
		t.Fatalf("output should not include unresolved warning when none:\n%s", got)
	}
}

func TestHumanizeBytesBoundaries(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{5 * 1024 * 1024, "5.0 MiB"},
		{int64(1.5 * 1024 * 1024 * 1024), "1.5 GiB"},
	}
	for _, c := range cases {
		if got := humanizeBytes(c.n); got != c.want {
			t.Errorf("humanizeBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
