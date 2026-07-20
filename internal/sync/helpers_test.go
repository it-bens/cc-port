package sync

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/remote"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

// newFileRemote opens a Remote against a per-test file:// directory.
func newFileRemote(t *testing.T) *remote.Remote {
	t.Helper()
	dir := t.TempDir()
	rawURL := "file://" + filepath.ToSlash(dir)
	r, err := remote.New(context.Background(), rawURL, remote.Deps{})
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

// buildTestTargets returns a single-claude-tool target slice bound to a
// staged fixture home, plus the fixture project path.
func buildTestTargets(t *testing.T) (targets []tool.Target, projectPath string) {
	t.Helper()
	home, projectPath := buildTestHomeAndProject(t)
	return targetsFor(home), projectPath
}

func targetsFor(home *claude.Home) []tool.Target {
	return []tool.Target{{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}}
}

// buildTestHomeBlank returns a Home rooted under a fresh t.TempDir() with
// Dir already created.
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

func allSelection() map[string]map[string]bool {
	claudeTool := claude.New()
	selected := make(map[string]bool)
	for _, category := range claudeTool.Categories() {
		selected[category.Name] = true
	}
	return map[string]map[string]bool{claudeTool.Name(): selected}
}

func toolSetForTest() *tool.Set { return tool.NewSet(claude.New()) }

// injectArchiveWithPusher writes a minimal valid cc-port archive to r at
// name with SyncPushedBy/SyncPushedAt set. The archive is plaintext.
func injectArchiveWithPusher(t *testing.T, r *remote.Remote, name, pusher string, at time.Time) {
	t.Helper()
	targets, projectPath := buildTestTargets(t)
	body := buildArchiveBytes(t, targets, projectPath, pusher, at, "", nil, "")
	uploadBytes(t, r, name, body)
}

// injectArchiveWithDeclaredPlaceholder writes a plaintext archive that
// declares one placeholder with the given Key and Original (no Resolve).
func injectArchiveWithDeclaredPlaceholder(t *testing.T, r *remote.Remote, name, key, original, pusher string) {
	t.Helper()
	targets, projectPath := buildTestTargets(t)
	placeholders := map[string][]manifest.Placeholder{"claude": {{Key: key, Original: original}}}
	body := buildArchiveBytes(t, targets, projectPath, pusher, time.Now().UTC(), "", placeholders, "")
	uploadBytes(t, r, name, body)
}

// injectArchiveWithSenderResolve writes an archive that declares the
// placeholder AND pre-fills its Resolve field, mirroring an export the
// sender ran with their own paths resolved before the push.
func injectArchiveWithSenderResolve(t *testing.T, r *remote.Remote, name, key, original, pusher string) {
	t.Helper()
	targets, projectPath := buildTestTargets(t)
	placeholders := map[string][]manifest.Placeholder{
		"claude": {{Key: key, Original: original, Resolve: "/Users/sender-resolved"}},
	}
	body := buildArchiveBytes(t, targets, projectPath, pusher, time.Now().UTC(), "", placeholders, "")
	uploadBytes(t, r, name, body)
}

// buildArchiveBytes runs export.Run through the optional encrypt stage
// into a bytes.Buffer and returns the resulting archive bytes. The
// trailing string parameter is reserved for future caller-supplied
// archive-naming context; current callers pass "".
func buildArchiveBytes(
	t *testing.T,
	targets []tool.Target,
	projectPath, pusher string,
	at time.Time,
	pass string,
	placeholders map[string][]manifest.Placeholder,
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
		Selected:     allSelection(),
		Placeholders: placeholders,
		SyncPushedBy: pusher,
		SyncPushedAt: at,
	}
	if _, err := export.Run(context.Background(), targets, &opts); err != nil {
		_ = w.Close()
		t.Fatalf("export.Run: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close pipeline: %v", err)
	}
	return buf.Bytes()
}

// buildBareZip64EOCDArchive returns a synthetic ZIP trailer -- a zip64 End
// Of Central Directory record, the zip64 locator pointing at it, and a
// plain EOCD record carrying the zip64 sentinel -- declaring entryCount
// entries. It is not a parseable ZIP; it exists only to prove PlanPull
// refuses an archive whose trailer alone declares more entries than
// archive.DefaultCaps allows, without constructing that many real entries.
// A plain (non-zip64) EOCD's 16-bit count field tops out at 65,535, well
// under DefaultCaps' 200,000-entry MaxEntries, so only a zip64 trailer can
// exercise this refusal at production scale.
func buildBareZip64EOCDArchive(t *testing.T, entryCount uint64) []byte {
	t.Helper()
	zip64End := make([]byte, 56)
	binary.LittleEndian.PutUint32(zip64End[0:4], 0x06064b50)   // zip64 EOCD signature
	binary.LittleEndian.PutUint64(zip64End[32:40], entryCount) // total entries

	locator := make([]byte, 20)
	binary.LittleEndian.PutUint32(locator[0:4], 0x07064b50) // zip64 locator signature
	binary.LittleEndian.PutUint64(locator[8:16], 0)         // zip64 EOCD record starts at offset 0

	eocd := make([]byte, 22)
	binary.LittleEndian.PutUint32(eocd[0:4], 0x06054b50) // EOCD signature
	binary.LittleEndian.PutUint16(eocd[10:12], 0xffff)   // zip64 sentinel

	return append(append(zip64End, locator...), eocd...)
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

func (s *bufferSink) Open(_ context.Context, _ io.Writer) (io.Writer, io.Closer, error) {
	return s.buf, &bufferSinkCloser{}, nil
}
func (s *bufferSink) Name() string { return "buffer sink" }

type bufferSinkCloser struct{}

func (b *bufferSinkCloser) Close() error { return nil }

// openPriorForTest opens the prior reader pipeline for sync_test.go.
//
//nolint:unparam // pass mirrors openPriorRead; reserved for a future encrypted-prior test.
func openPriorForTest(t *testing.T, r *remote.Remote, name, pass string) *PriorRead {
	t.Helper()
	src, err := pipeline.RunReader(context.Background(), []pipeline.ReaderStage{
		&remote.Source{Remote: r, Key: name},
		&encrypt.ReaderStage{Pass: pass, Mode: encrypt.Permissive},
		&pipeline.MaterializeStage{},
	})
	if errors.Is(err, remote.ErrNotFound) {
		return nil
	}
	if err != nil {
		t.Fatalf("openPriorForTest: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })
	return &PriorRead{Source: src, WasEncrypted: src.Meta.WasEncrypted}
}

// openSourceForTest opens the strict reader pipeline for pull tests.
//
//nolint:unparam // name mirrors the production archive key; current callers share one key.
func openSourceForTest(t *testing.T, r *remote.Remote, name, pass string) pipeline.Source {
	t.Helper()
	src, err := pipeline.RunReader(context.Background(), []pipeline.ReaderStage{
		&remote.Source{Remote: r, Key: name},
		&encrypt.ReaderStage{Pass: pass, Mode: encrypt.Strict},
		&pipeline.MaterializeStage{},
	})
	if err != nil {
		t.Fatalf("openSourceForTest: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })
	return src
}

// openWriterForTest opens the writer pipeline for ExecutePush tests.
//
//nolint:unparam // name mirrors the production archive key; current callers share one key.
func openWriterForTest(t *testing.T, r *remote.Remote, name, pass string) io.WriteCloser {
	t.Helper()
	w, err := pipeline.RunWriter(context.Background(), []pipeline.WriterStage{
		&encrypt.WriterStage{Pass: pass},
		&remote.Sink{Remote: r, Key: name},
	})
	if err != nil {
		t.Fatalf("openWriterForTest: %v", err)
	}
	return w
}
