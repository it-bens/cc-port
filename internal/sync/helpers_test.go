package sync

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/remote"
	"github.com/it-bens/cc-port/internal/testutil"

	// memblob registers the "mem" scheme so newMemRemote can open
	// "mem://" without touching disk. Test-only; production code paths
	// declare s3:// and file:// in internal/remote/remote.go.
	_ "gocloud.dev/blob/memblob"
)

const testPass = "correct horse battery staple"

func newMemRemote(t *testing.T) *remote.Remote {
	t.Helper()
	r, err := remote.New(context.Background(), "mem://")
	if err != nil {
		t.Fatalf("remote.New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func buildTestHomeAndProject(t *testing.T) (home *claude.Home, projectPath string) {
	t.Helper()
	home = testutil.SetupFixture(t)
	return home, "/Users/test/Projects/myproject"
}

// buildTestHomeBlank returns a Home rooted under a fresh t.TempDir() with
// Dir already created. ExecutePull's importer mkdir-then-rename steps
// require Dir to exist; the bare struct from the plan sketch fails the
// first os.Stat unless Dir is materialized here.
func buildTestHomeBlank(t *testing.T) *claude.Home {
	t.Helper()
	dir := t.TempDir()
	home := &claude.Home{
		Dir:        filepath.Join(dir, "dotclaude"),
		ConfigFile: filepath.Join(dir, "dotclaude.json"),
	}
	if err := os.MkdirAll(home.Dir, 0o700); err != nil {
		t.Fatalf("mkdir blank home: %v", err)
	}
	return home
}

func allCategoriesSet() manifest.CategorySet {
	var set manifest.CategorySet
	for _, spec := range manifest.AllCategories {
		spec.Apply(&set, true)
	}
	return set
}

func defaultResolutionsForTest(_ *testing.T) map[string]string {
	return map[string]string{"{{HOME}}": "/Users/me"}
}

// injectArchiveWithPusher writes a minimal valid cc-port archive to r at
// name with SyncPushedBy/SyncPushedAt set. The archive is plaintext.
func injectArchiveWithPusher(t *testing.T, r *remote.Remote, name, pusher string, at time.Time) {
	t.Helper()
	home, projectPath := buildTestHomeAndProject(t)
	body := buildArchiveBytes(t, home, projectPath, pusher, at, "", nil, "")
	uploadBytes(t, r, name, body)
}

// injectEncryptedArchive writes an age-encrypted archive under pass.
func injectEncryptedArchive(t *testing.T, r *remote.Remote, name, pass, pusher string, at time.Time) {
	t.Helper()
	home, projectPath := buildTestHomeAndProject(t)
	body := buildArchiveBytes(t, home, projectPath, pusher, at, pass, nil, "")
	uploadBytes(t, r, name, body)
}

// injectArchiveWithDeclaredPlaceholder writes a plaintext archive that
// declares one placeholder with the given Key and Original (no Resolve).
func injectArchiveWithDeclaredPlaceholder(t *testing.T, r *remote.Remote, name, key, original, pusher string) {
	t.Helper()
	home, projectPath := buildTestHomeAndProject(t)
	placeholders := []manifest.Placeholder{{Key: key, Original: original}}
	body := buildArchiveBytes(t, home, projectPath, pusher, time.Now().UTC(), "", placeholders, "")
	uploadBytes(t, r, name, body)
}

// injectArchiveWithSenderResolve writes an archive that declares the
// placeholder AND pre-fills its Resolve field, mirroring an export the
// sender ran with their own paths resolved before the push.
func injectArchiveWithSenderResolve(t *testing.T, r *remote.Remote, name, key, original, pusher string) {
	t.Helper()
	home, projectPath := buildTestHomeAndProject(t)
	placeholders := []manifest.Placeholder{
		{Key: key, Original: original, Resolve: "/Users/sender-resolved"},
	}
	body := buildArchiveBytes(t, home, projectPath, pusher, time.Now().UTC(), "", placeholders, "")
	uploadBytes(t, r, name, body)
}

// buildArchiveBytes runs export.Run through the optional encrypt stage
// into a bytes.Buffer and returns the resulting archive bytes. The
// trailing string parameter is reserved for future caller-supplied
// archive-naming context; current callers pass "".
func buildArchiveBytes(
	t *testing.T,
	home *claude.Home,
	projectPath, pusher string,
	at time.Time,
	pass string,
	placeholders []manifest.Placeholder,
	_ string,
) []byte {
	t.Helper()
	var buf bytes.Buffer
	stages := []pipeline.WriterStage{
		&encrypt.WriterStage{Pass: pass},
		&bufferSink{buf: &buf},
	}
	w, err := pipeline.RunWriter(context.Background(), stages)
	if err != nil {
		t.Fatalf("RunWriter: %v", err)
	}
	opts := export.Options{
		ProjectPath:  projectPath,
		Output:       w,
		Categories:   allCategoriesSet(),
		Placeholders: placeholders,
		SyncPushedBy: pusher,
		SyncPushedAt: at,
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

func uploadBytes(t *testing.T, r *remote.Remote, name string, body []byte) {
	t.Helper()
	w, err := r.Create(context.Background(), name)
	if err != nil {
		t.Fatalf("Remote.Create: %v", err)
	}
	if _, err := w.Write(body); err != nil {
		_ = w.Close()
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// bufferSink is a pipeline.WriterStage that writes into an in-memory
// buffer. Test-only; lives here rather than in internal/file so
// production code does not pick up an io.Writer-shaped sink with no
// flush guarantees.
type bufferSink struct{ buf *bytes.Buffer }

func (s *bufferSink) Open(_ context.Context, _ io.Writer) (io.WriteCloser, error) {
	return &bufferSinkCloser{buf: s.buf}, nil
}
func (s *bufferSink) Name() string { return "buffer sink" }

type bufferSinkCloser struct{ buf *bytes.Buffer }

func (b *bufferSinkCloser) Write(p []byte) (int, error) { return b.buf.Write(p) }
func (b *bufferSinkCloser) Close() error                { return nil }

// TestHelpersSmoke wires every helper at least once. Tasks 6 and 7 add
// the behavior-driven tests; this smoke test exists today so the unused
// linter does not flag landed-but-not-yet-called helpers and so a typo
// in a helper signature surfaces here rather than in Task 6.
func TestHelpersSmoke(t *testing.T) {
	r := newMemRemote(t)
	if r == nil {
		t.Fatal("newMemRemote returned nil")
	}
	if testPass == "" {
		t.Fatal("testPass is empty")
	}
	if got := defaultResolutionsForTest(t)["{{HOME}}"]; got == "" {
		t.Fatal("defaultResolutionsForTest missing {{HOME}}")
	}
	if blank := buildTestHomeBlank(t); blank.Dir == "" {
		t.Fatal("buildTestHomeBlank: empty Dir")
	}

	categories := allCategoriesSet()
	if !categories.Sessions || !categories.Tasks {
		t.Fatal("allCategoriesSet did not enable every category")
	}

	now := time.Now().UTC()
	injectArchiveWithPusher(t, r, "smoke-plain", "host-user", now)
	injectEncryptedArchive(t, r, "smoke-encrypted", testPass, "host-user", now)
	injectArchiveWithDeclaredPlaceholder(t, r, "smoke-declared", "{{HOME}}", "/Users/test", "host-user")
	injectArchiveWithSenderResolve(t, r, "smoke-resolved", "{{HOME}}", "/Users/test", "host-user")

	attrs, err := r.Stat(context.Background(), "smoke-plain")
	if err != nil {
		t.Fatalf("Stat smoke-plain: %v", err)
	}
	if attrs.Size == 0 {
		t.Fatal("smoke-plain archive has zero size")
	}
}
