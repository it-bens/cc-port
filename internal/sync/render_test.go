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
		Selected:          allSelection(),
		EncryptionEnabled: false,
	}
	var buf bytes.Buffer
	if err := plan.Render(&buf, false); err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"cc-port push myproj",
		"Pipeline: export -> remote sink",
		"Categories: claude:",
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
		Selected:          allSelection(),
		PriorPushedBy:     "laptop2-bob",
		PriorPushedAt:     time.Date(2026, 4, 25, 14, 32, 18, 0, time.UTC),
		PriorEncrypted:    true,
		PriorSize:         5_242_880, // 5 MiB
		EncryptionEnabled: true,
		CrossMachine:      true,
	}
	var buf bytes.Buffer
	if err := plan.Render(&buf, false); err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"Pipeline: export -> encrypt -> remote sink",
		"Categories: claude:",
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

func TestPushPlan_RenderApplyDropsDryRunPrefix(t *testing.T) {
	plan := &PushPlan{
		Name:       "myproj",
		SelfPusher: "laptop1-alice",
		Selected:   allSelection(),
	}
	var buf bytes.Buffer
	if err := plan.Render(&buf, true); err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "[dry-run]") {
		t.Fatalf("apply-run header must not carry the [dry-run] prefix:\n%s", got)
	}
	if !strings.Contains(got, "cc-port push myproj") {
		t.Fatalf("output missing command line:\n%s", got)
	}
}

func TestPullPlan_RenderWithUnresolvedPlaceholders(t *testing.T) {
	plan := &PullPlan{
		Name:           "myproj",
		RemotePushedBy: "laptop1-alice",
		RemotePushedAt: time.Date(2026, 4, 25, 14, 32, 18, 0, time.UTC),
		RemoteSize:     5_242_880,
		Tools:          []string{"claude"},
		DeclaredPlaceholders: map[string][]manifest.Placeholder{
			"claude": {{Key: "{{HOME}}"}, {Key: "{{CACHE}}"}},
		},
		UnresolvedPlaceholders: map[string][]string{"claude": {"{{CACHE}}"}},
	}
	var buf bytes.Buffer
	if err := plan.Render(&buf, false); err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"cc-port pull myproj",
		"Pipeline: remote source -> import core",
		"Tools:       claude",
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
	}
	var buf bytes.Buffer
	if err := plan.Render(&buf, false); err != nil {
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

func TestPullPlan_RenderApplyDropsDryRunPrefix(t *testing.T) {
	plan := &PullPlan{
		Name:  "myproj",
		Tools: []string{"claude"},
	}
	var buf bytes.Buffer
	if err := plan.Render(&buf, true); err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "[dry-run]") {
		t.Fatalf("apply-run header must not carry the [dry-run] prefix:\n%s", got)
	}
	if !strings.Contains(got, "cc-port pull myproj") {
		t.Fatalf("output missing command line:\n%s", got)
	}
}

// TestPullPlan_RenderHumanizesByteMagnitudes drives the two humanizeBytes
// branches the other render tests in this file don't reach: KiB is covered
// by TestPullPlan_RenderEncryptedClean and MiB by
// TestPushPlan_RenderEncryptedWithPriorAndCrossMachine.
func TestPullPlan_RenderHumanizesByteMagnitudes(t *testing.T) {
	cases := []struct {
		name string
		size int64
		want string
	}{
		{"sub-KiB value renders as bytes", 512, "512 B"},
		{"GiB value renders with one decimal", 1_610_612_736, "1.5 GiB"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			plan := &PullPlan{Name: "x", RemoteSize: testCase.size}
			var buf bytes.Buffer
			if err := plan.Render(&buf, false); err != nil {
				t.Fatalf("Render: %v", err)
			}
			got := buf.String()
			if !strings.Contains(got, testCase.want) {
				t.Fatalf("output missing %q:\n%s", testCase.want, got)
			}
		})
	}
}
