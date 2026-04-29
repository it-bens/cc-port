package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/testutil"
)

// setupCmdFixture stages the dotclaude fixture and returns the home dir
// path (suitable for $HOME) plus the canonical project path inside the
// fixture. testutil.SetupFixture lays out home.Dir at <tempdir>/dotclaude
// and home.ConfigFile at <tempdir>/dotclaude.json, so filepath.Dir(home.Dir)
// is the parent that maps to $HOME for cmd-layer tests.
func setupCmdFixture(t *testing.T) (homeParent, projectPath string) {
	t.Helper()
	home := testutil.SetupFixture(t)
	homeParent = filepath.Dir(home.Dir)
	return homeParent, "/Users/test/Projects/myproject"
}

// injectArchiveWithPusherAtURL writes a plaintext archive to the file://
// URL at the given name with SyncPushedBy = pusher.
func injectArchiveWithPusherAtURL(t *testing.T, url, name, pusher string) {
	t.Helper()
	body := buildCmdArchiveBytes(t, pusher, "", nil)
	writeAtURL(t, url, name, body)
}

// injectArchiveWithDeclaredPlaceholderAtURL writes an archive that
// declares one placeholder to the file:// URL.
func injectArchiveWithDeclaredPlaceholderAtURL(t *testing.T, url, name, key, original, pusher string) {
	t.Helper()
	placeholders := []manifest.Placeholder{{Key: key, Original: original}}
	body := buildCmdArchiveBytes(t, pusher, "", placeholders)
	writeAtURL(t, url, name, body)
}

func defaultResolutionsForCmd(_ *testing.T) map[string]string {
	return map[string]string{"{{HOME}}": "/Users/me"}
}

func buildCmdArchiveBytes(t *testing.T, pusher, pass string, placeholders []manifest.Placeholder) []byte {
	t.Helper()
	home := testutil.SetupFixture(t)
	var buf bytes.Buffer
	stages := []pipeline.WriterStage{
		&encrypt.WriterStage{Pass: pass},
		&cmdBytesSink{buf: &buf},
	}
	w, err := pipeline.RunWriter(context.Background(), stages)
	if err != nil {
		t.Fatalf("RunWriter: %v", err)
	}
	opts := export.Options{
		ProjectPath:  "/Users/test/Projects/myproject",
		Output:       w,
		Categories:   allCategoriesCmdSet(),
		Placeholders: placeholders,
		SyncPushedBy: pusher,
		SyncPushedAt: time.Now().UTC(),
	}
	if _, err := export.Run(context.Background(), home, &opts); err != nil {
		_ = w.Close()
		t.Fatalf("export.Run: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close pipeline: %v", err)
	}
	return buf.Bytes()
}

// writeAtURL writes body to <dir>/<name> for file:// URLs only. Non-file
// schemes are rejected loudly so a test cannot silently write to the
// wrong destination if the URL gets mistyped.
func writeAtURL(t *testing.T, url, name string, body []byte) {
	t.Helper()
	const prefix = "file://"
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("writeAtURL: only file:// supported, got %q", url)
	}
	dest := filepath.Join(url[len(prefix):], name)
	if err := os.WriteFile(dest, body, 0o600); err != nil {
		t.Fatalf("write %s: %v", dest, err)
	}
}

func allCategoriesCmdSet() manifest.CategorySet {
	var set manifest.CategorySet
	for _, spec := range manifest.AllCategories {
		spec.Apply(&set, true)
	}
	return set
}

type cmdBytesSink struct{ buf *bytes.Buffer }

func (s *cmdBytesSink) Open(_ context.Context, _ io.Writer) (io.WriteCloser, error) {
	return &cmdBytesSinkCloser{buf: s.buf}, nil
}
func (s *cmdBytesSink) Name() string { return "bytes sink" }

type cmdBytesSinkCloser struct{ buf *bytes.Buffer }

func (b *cmdBytesSinkCloser) Write(p []byte) (int, error) { return b.buf.Write(p) }
func (b *cmdBytesSinkCloser) Close() error                { return nil }

// TestSyncCmdHelpersSmoke wires every helper at least once. Tasks 9 and
// 10 add the behavior-driven tests; this smoke test exists today so the
// unused linter does not flag landed-but-not-yet-called helpers and so a
// typo in a helper signature surfaces here rather than in Task 9.
func TestSyncCmdHelpersSmoke(t *testing.T) {
	tempHome, projectPath := setupCmdFixture(t)
	if tempHome == "" || projectPath == "" {
		t.Fatal("setupCmdFixture returned empty values")
	}
	if got := defaultResolutionsForCmd(t)["{{HOME}}"]; got == "" {
		t.Fatal("defaultResolutionsForCmd missing {{HOME}}")
	}
	categories := allCategoriesCmdSet()
	if !categories.Sessions || !categories.Tasks {
		t.Fatal("allCategoriesCmdSet did not enable every category")
	}

	dir := t.TempDir()
	url := "file://" + dir
	injectArchiveWithPusherAtURL(t, url, "smoke-plain", "host-user")
	injectArchiveWithDeclaredPlaceholderAtURL(t, url, "smoke-declared", "{{HOME}}", "/Users/test", "host-user")

	for _, name := range []string{"smoke-plain", "smoke-declared"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("Stat %s: %v", name, err)
		}
		if info.Size() == 0 {
			t.Fatalf("archive %s has zero size", name)
		}
	}
}
