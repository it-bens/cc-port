package manifest_test

import (
	"archive/zip"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/it-bens/cc-port/internal/manifest"
)

func boolPtr(value bool) *bool { return &value }

func TestMetadata_MarshalUnmarshal(t *testing.T) {
	created := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)

	original := &manifest.Metadata{
		Export: manifest.Info{
			Created: created,
			Categories: []manifest.Category{
				{Name: "sessions", Included: true},
				{Name: "settings", Included: false},
			},
		},
		Placeholders: []manifest.Placeholder{
			{Key: "HOME", Original: "/home/user"},
			{Key: "PROJECT", Original: "/home/user/project", Resolvable: boolPtr(true)},
		},
	}

	temporaryDirectory := t.TempDir()
	path := filepath.Join(temporaryDirectory, "metadata.xml")

	if err := manifest.WriteManifest(path, original); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	roundTripped, err := manifest.ReadManifest(path)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}

	opts := cmp.Options{
		cmpopts.IgnoreFields(manifest.Metadata{}, "XMLName"),
		cmpopts.EquateApproxTime(time.Second),
	}

	if diff := cmp.Diff(original, roundTripped, opts); diff != "" {
		t.Errorf("round-trip mismatch (-want +got):\n%s", diff)
	}
}

func TestMetadata_XMLFormat(t *testing.T) {
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	metadata := &manifest.Metadata{
		Export: manifest.Info{
			Created: created,
			Categories: []manifest.Category{
				{Name: "sessions", Included: true},
			},
		},
		Placeholders: []manifest.Placeholder{
			{Key: "HOME", Original: "/home/user"},
		},
	}

	temporaryDirectory := t.TempDir()
	path := filepath.Join(temporaryDirectory, "metadata.xml")

	if err := manifest.WriteManifest(path, metadata); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	data, err := os.ReadFile(path) //nolint:gosec // G304: test-controlled temp path
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(data)

	checks := []struct {
		description string
		substring   string
	}{
		{"XML declaration", xml.Header[:5]},
		{"root element cc-port", "<cc-port"},
		{"export element", "<export>"},
		{"categories wrapper", "<categories>"},
		{"category element with name attribute", `name="sessions"`},
		{"included attribute true", `included="true"`},
		{"placeholders wrapper", "<placeholders>"},
		{"placeholder element", "<placeholder"},
		{"key attribute", `key="HOME"`},
		{"original attribute", `original="/home/user"`},
	}

	for _, check := range checks {
		if !strings.Contains(content, check.substring) {
			t.Errorf("%s: expected %q in output:\n%s", check.description, check.substring, content)
		}
	}
}

func TestManifest_WithResolve(t *testing.T) {
	created := time.Date(2024, 3, 20, 12, 0, 0, 0, time.UTC)

	original := &manifest.Metadata{
		Export: manifest.Info{
			Created:    created,
			Categories: []manifest.Category{},
		},
		Placeholders: []manifest.Placeholder{
			{
				Key:        "HOME",
				Original:   "/home/olduser",
				Resolvable: boolPtr(true),
				Resolve:    "/home/newuser",
			},
			{
				Key:        "UNRESOLVABLE",
				Original:   "/some/path",
				Resolvable: boolPtr(false),
			},
			{
				Key:      "PLAIN",
				Original: "/plain/path",
			},
		},
	}

	temporaryDirectory := t.TempDir()
	path := filepath.Join(temporaryDirectory, "metadata.xml")

	if err := manifest.WriteManifest(path, original); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	roundTripped, err := manifest.ReadManifest(path)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}

	opts := cmp.Options{
		cmpopts.IgnoreFields(manifest.Metadata{}, "XMLName"),
		cmpopts.EquateApproxTime(time.Second),
		cmpopts.EquateEmpty(),
	}

	if diff := cmp.Diff(original, roundTripped, opts); diff != "" {
		t.Errorf("Resolve round-trip mismatch (-want +got):\n%s", diff)
	}

	assertPlaceholderFields(t, roundTripped)
	assertZIPRoundTrip(t, original, temporaryDirectory, path, opts)
}

// assertPlaceholderFields verifies the Resolvable and Resolve fields on each
// placeholder in the round-tripped metadata.
func assertPlaceholderFields(t *testing.T, roundTripped *manifest.Metadata) {
	t.Helper()

	// Verify Resolvable and Resolve are present for the first placeholder.
	first := roundTripped.Placeholders[0]
	if first.Resolvable == nil || !*first.Resolvable {
		t.Errorf("expected Resolvable=true for HOME placeholder, got %v", first.Resolvable)
	}
	if first.Resolve != "/home/newuser" {
		t.Errorf("expected Resolve=/home/newuser, got %q", first.Resolve)
	}

	// Verify Resolve is empty and Resolvable is false for the second placeholder.
	second := roundTripped.Placeholders[1]
	if second.Resolvable == nil || *second.Resolvable {
		t.Errorf("expected Resolvable=false for UNRESOLVABLE placeholder, got %v", second.Resolvable)
	}
	if second.Resolve != "" {
		t.Errorf("expected empty Resolve for UNRESOLVABLE placeholder, got %q", second.Resolve)
	}

	// Verify Resolvable and Resolve are absent for the third placeholder.
	third := roundTripped.Placeholders[2]
	if third.Resolvable != nil {
		t.Errorf("expected nil Resolvable for PLAIN placeholder, got %v", third.Resolvable)
	}
	if third.Resolve != "" {
		t.Errorf("expected empty Resolve for PLAIN placeholder, got %q", third.Resolve)
	}
}

// assertZIPRoundTrip verifies the metadata survives a ZIP round-trip.
func assertZIPRoundTrip(t *testing.T, original *manifest.Metadata, temporaryDirectory, path string, opts cmp.Options) {
	t.Helper()

	zipPath := filepath.Join(temporaryDirectory, "export.zip")
	if err := createTestZip(zipPath, path); err != nil {
		t.Fatalf("createTestZip: %v", err)
	}

	fromZip, err := manifest.ReadManifestFromZip(zipPath)
	if err != nil {
		t.Fatalf("ReadManifestFromZip: %v", err)
	}

	if diff := cmp.Diff(original, fromZip, opts); diff != "" {
		t.Errorf("ZIP round-trip mismatch (-want +got):\n%s", diff)
	}
}

// createTestZip creates a ZIP archive at zipPath containing the file at
// sourcePath stored as metadata.xml.
func createTestZip(zipPath, sourcePath string) error {
	zipFile, err := os.Create(zipPath) //nolint:gosec // G304: test-controlled temp path
	if err != nil {
		return err
	}
	defer func() { _ = zipFile.Close() }()

	writer := zip.NewWriter(zipFile)
	defer func() { _ = writer.Close() }()

	entry, err := writer.Create("metadata.xml")
	if err != nil {
		return err
	}

	data, err := os.ReadFile(sourcePath) //nolint:gosec // G304: test-controlled path
	if err != nil {
		return err
	}

	_, err = entry.Write(data)
	return err
}
