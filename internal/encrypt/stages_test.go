package encrypt_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/pipeline"
)

const (
	stagePass = "stage-passphrase"
	osWindows = "windows"
)

func TestWriterStage_RoundTripViaRunWriter(t *testing.T) {
	var buf bytes.Buffer
	sink := &bufferSinkStage{buf: &buf}
	w, err := pipeline.RunWriter(context.Background(), []pipeline.WriterStage{
		&encrypt.WriterStage{Pass: stagePass},
		sink,
	})
	require.NoError(t, err)

	plaintext := []byte("stage round trip plaintext")
	_, err = w.Write(plaintext)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	require.True(t, encrypt.IsEncrypted(buf.Bytes()), "WriterStage output should match age magic-byte prefix")
	require.True(t, sink.closed, "outer Close on the encrypt path must cascade to the downstream sink")

	reader, err := encrypt.DecryptingReader(bytes.NewReader(buf.Bytes()), stagePass)
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, plaintext, got)
}

func TestReaderStage_RoundTrip(t *testing.T) {
	plaintext := []byte("reader stage round trip")
	cipher := encryptBytes(t, stagePass, plaintext)
	upstream := pipeline.Source{
		ReaderAt: bytes.NewReader(cipher),
		Size:     int64(len(cipher)),
		Close:    func() error { return nil },
	}
	src, err := (&encrypt.ReaderStage{Pass: stagePass}).Open(context.Background(), upstream)
	require.NoError(t, err)
	t.Cleanup(func() { _ = src.Close() })

	require.Equal(t, int64(len(plaintext)), src.Size)
	got := make([]byte, src.Size)
	_, err = src.ReaderAt.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, plaintext, got)
}

func TestReaderStage_CloseRemovesTempfileIdempotent(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("tempdir semantics differ on Windows")
	}
	cipher := encryptBytes(t, stagePass, []byte("close idempotency test"))
	src, err := (&encrypt.ReaderStage{Pass: stagePass}).Open(context.Background(), pipeline.Source{
		ReaderAt: bytes.NewReader(cipher),
		Size:     int64(len(cipher)),
		Close:    func() error { return nil },
	})
	require.NoError(t, err)

	tempfile, ok := src.ReaderAt.(*os.File)
	require.True(t, ok)
	tempPath := tempfile.Name()

	require.NoError(t, src.Close())
	require.NoError(t, src.Close(), "second Close should return nil")

	_, statErr := os.Stat(tempPath)
	require.True(t, os.IsNotExist(statErr), "tempfile should be removed: %v", statErr)
}

func TestReaderStage_TempfileMode0600(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("0600 mode semantics differ on Windows")
	}
	cipher := encryptBytes(t, stagePass, []byte("mode test"))
	src, err := (&encrypt.ReaderStage{Pass: stagePass}).Open(context.Background(), pipeline.Source{
		ReaderAt: bytes.NewReader(cipher),
		Size:     int64(len(cipher)),
		Close:    func() error { return nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = src.Close() })

	tempfile, ok := src.ReaderAt.(*os.File)
	require.True(t, ok)
	info, err := os.Stat(tempfile.Name())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestWriterStage_PassthroughWhenPassEmpty(t *testing.T) {
	var buf bytes.Buffer
	sink := &bufferSinkStage{buf: &buf}
	w, err := pipeline.RunWriter(context.Background(), []pipeline.WriterStage{
		&encrypt.WriterStage{Pass: ""},
		sink,
	})
	require.NoError(t, err)

	plaintext := []byte("passthrough should not encrypt")
	_, err = w.Write(plaintext)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	require.Equal(t, plaintext, buf.Bytes(), "empty Pass must forward bytes unchanged")
	require.False(t, encrypt.IsEncrypted(buf.Bytes()), "passthrough output must not match age magic-byte prefix")
	require.True(t, sink.closed, "outer Close on the passthrough path must cascade to the downstream sink")
}

// ReaderStage owns the encrypted-vs-plaintext × pass-vs-no-pass dispatch
// matrix. Strict (default) refuses plaintext-with-pass; Permissive
// accepts it silently.

const dispatchPass = "dispatch-passphrase"

func makePlaintextSource(t *testing.T, body []byte) pipeline.Source {
	t.Helper()
	return pipeline.Source{
		ReaderAt: bytes.NewReader(body),
		Size:     int64(len(body)),
		Close:    func() error { return nil },
	}
}

func makeEncryptedSource(t *testing.T, body []byte) pipeline.Source {
	t.Helper()
	return makePlaintextSource(t, encryptBytes(t, dispatchPass, body))
}

// encryptBytes runs body through encrypt.EncryptingWriter under passphrase
// and returns the cipher bytes. Centralizes the encrypt-and-buffer pattern
// so test bodies that need cipher bytes for a custom upstream Source or
// for byte-mutation cases stay focused on the behavior they exercise.
func encryptBytes(t *testing.T, passphrase string, body []byte) []byte {
	t.Helper()
	var cipher bytes.Buffer
	w, err := encrypt.EncryptingWriter(&cipher, passphrase)
	require.NoError(t, err)
	if len(body) > 0 {
		_, err = w.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return cipher.Bytes()
}

func TestReaderStage_StrictPlaintextEmptyPassPassthrough(t *testing.T) {
	body := []byte("plaintext archive bytes")
	upstream := makePlaintextSource(t, body)
	src, err := (&encrypt.ReaderStage{Pass: "", Mode: encrypt.Strict}).Open(context.Background(), upstream)
	require.NoError(t, err)
	t.Cleanup(func() { _ = src.Close() })
	require.Equal(t, upstream.ReaderAt, src.ReaderAt, "plaintext+empty-pass must return upstream unchanged")
}

func TestReaderStage_StrictPlaintextWithPassReturnsErrUnencryptedInput(t *testing.T) {
	upstream := makePlaintextSource(t, []byte("plaintext bytes"))
	_, err := (&encrypt.ReaderStage{Pass: dispatchPass, Mode: encrypt.Strict}).Open(context.Background(), upstream)
	require.ErrorIs(t, err, encrypt.ErrUnencryptedInput)
}

func TestReaderStage_StrictEncryptedEmptyPassReturnsErrPassphraseRequired(t *testing.T) {
	upstream := makeEncryptedSource(t, []byte("body"))
	_, err := (&encrypt.ReaderStage{Pass: "", Mode: encrypt.Strict}).Open(context.Background(), upstream)
	require.ErrorIs(t, err, encrypt.ErrPassphraseRequired)
}

func TestReaderStage_StrictEncryptedWithPassDecrypts(t *testing.T) {
	body := []byte("decrypted body roundtrip")
	upstream := makeEncryptedSource(t, body)
	src, err := (&encrypt.ReaderStage{Pass: dispatchPass, Mode: encrypt.Strict}).Open(context.Background(), upstream)
	require.NoError(t, err)
	t.Cleanup(func() { _ = src.Close() })
	got := make([]byte, src.Size)
	_, err = src.ReaderAt.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, body, got)
}

func TestReaderStage_StrictEncryptedWrongPassReturnsErrPassphrase(t *testing.T) {
	upstream := makeEncryptedSource(t, []byte("body"))
	_, err := (&encrypt.ReaderStage{Pass: "wrong-passphrase", Mode: encrypt.Strict}).Open(context.Background(), upstream)
	require.Error(t, err)
	require.ErrorIs(t, err, encrypt.ErrPassphrase)
}

func TestReaderStage_PermissivePlaintextWithPassPassthrough(t *testing.T) {
	body := []byte("plaintext archive bytes")
	upstream := makePlaintextSource(t, body)
	src, err := (&encrypt.ReaderStage{Pass: dispatchPass, Mode: encrypt.Permissive}).Open(context.Background(), upstream)
	require.NoError(t, err, "Permissive must not refuse plaintext-with-pass")
	t.Cleanup(func() { _ = src.Close() })
	require.Equal(t, upstream.ReaderAt, src.ReaderAt, "plaintext-with-pass under Permissive must return upstream unchanged")
}

func TestReaderStage_PermissiveEncryptedEmptyPassReturnsErrPassphraseRequired(t *testing.T) {
	upstream := makeEncryptedSource(t, []byte("body"))
	_, err := (&encrypt.ReaderStage{Pass: "", Mode: encrypt.Permissive}).Open(context.Background(), upstream)
	require.ErrorIs(t, err, encrypt.ErrPassphraseRequired)
}

func TestReaderStage_DecryptCloseChainsToUpstream(t *testing.T) {
	cipher := encryptBytes(t, dispatchPass, []byte("close cascade body"))
	upstreamClosed := false
	upstream := pipeline.Source{
		ReaderAt: bytes.NewReader(cipher),
		Size:     int64(len(cipher)),
		Close:    func() error { upstreamClosed = true; return nil },
	}
	src, err := (&encrypt.ReaderStage{Pass: dispatchPass, Mode: encrypt.Strict}).Open(context.Background(), upstream)
	require.NoError(t, err)
	require.NoError(t, src.Close())
	require.True(t, upstreamClosed, "decrypt Source.Close should cascade to upstream.Close")
}

// TestReaderStage_RunReaderClosesUpstreamOnceOnError pins the contract
// that the pipeline runner is the sole closer on stage error: the stage
// returns the sentinel without closing upstream itself, and
// pipeline.RunReader closes upstream exactly once. One representative
// sentinel covers all three (ErrPassphraseRequired, ErrUnencryptedInput,
// decrypt failure); the runner's own tests cover the per-stage cascade
// shape.
func TestReaderStage_RunReaderClosesUpstreamOnceOnError(t *testing.T) {
	closeCount := 0
	source := &countingSourceStage{
		bytes:    encryptBytes(t, dispatchPass, nil),
		closeOut: &closeCount,
	}

	_, err := pipeline.RunReader(context.Background(), []pipeline.ReaderStage{
		source,
		&encrypt.ReaderStage{Pass: "", Mode: encrypt.Strict},
	})
	require.ErrorIs(t, err, encrypt.ErrPassphraseRequired)
	require.Equal(t, 1, closeCount, "runner must close upstream exactly once on stage error")
}

// countingSourceStage emits the supplied bytes as a pipeline.Source and
// increments closeOut each time Source.Close is called. Used by the
// close-once test above.
type countingSourceStage struct {
	bytes    []byte
	closeOut *int
}

func (s *countingSourceStage) Open(_ context.Context, _ pipeline.Source) (pipeline.Source, error) {
	return pipeline.Source{
		ReaderAt: bytes.NewReader(s.bytes),
		Size:     int64(len(s.bytes)),
		Close:    func() error { *s.closeOut++; return nil },
	}, nil
}
func (s *countingSourceStage) Name() string { return "counting-source" }

func TestWriterStage_NameAndReaderStageName(t *testing.T) {
	require.Equal(t, "encrypt", (&encrypt.WriterStage{Pass: "x"}).Name())
	require.Equal(t, "decrypt", (&encrypt.ReaderStage{Pass: "x"}).Name())
}

// bufferSinkStage is a test-only sink. The closed flag is the cascade
// witness: WriterStage's encrypt path must close the downstream sink
// when the caller closes the outermost writer; the round-trip test
// asserts this flag.
type bufferSinkStage struct {
	buf    *bytes.Buffer
	closed bool
}

func (s *bufferSinkStage) Open(_ context.Context, _ io.Writer) (io.WriteCloser, error) {
	return &bufferSinkWriter{stage: s}, nil
}
func (s *bufferSinkStage) Name() string { return "buffer-sink" }

type bufferSinkWriter struct{ stage *bufferSinkStage }

func (w *bufferSinkWriter) Write(p []byte) (int, error) { return w.stage.buf.Write(p) }
func (w *bufferSinkWriter) Close() error                { w.stage.closed = true; return nil }

// guard against bytes.Reader/io.SectionReader interface drift.
var _ io.ReaderAt = (*bytes.Reader)(nil)

func TestEncryptingWriter_RejectsEmptyPassphrase(t *testing.T) {
	_, err := encrypt.EncryptingWriter(&bytes.Buffer{}, "")
	require.Error(t, err)
}

func TestDecryptingReader_RejectsEmptyPassphrase(t *testing.T) {
	_, err := encrypt.DecryptingReader(&bytes.Buffer{}, "")
	require.Error(t, err)
}

func TestWriterStage_RejectsNilDownstream(t *testing.T) {
	_, err := (&encrypt.WriterStage{Pass: stagePass}).Open(context.Background(), nil)
	require.Error(t, err)
}

func TestReaderStage_RejectsEmptyUpstream(t *testing.T) {
	_, err := (&encrypt.ReaderStage{Pass: stagePass}).Open(context.Background(), pipeline.Source{})
	require.Error(t, err)
}

// failingReaderAt always returns an error from ReadAt that is not io.EOF.
type failingReaderAt struct{}

func (failingReaderAt) ReadAt(_ []byte, _ int64) (int, error) {
	return 0, errors.New("synthetic read failure")
}

func TestReaderStage_PeekErrorSurfaces(t *testing.T) {
	_, err := (&encrypt.ReaderStage{Pass: stagePass}).Open(context.Background(), pipeline.Source{
		ReaderAt: failingReaderAt{},
		Size:     1024,
		Close:    func() error { return nil },
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "peek archive header")
}

// passthroughCloseChainsToCloserWriter is a writer that doubles as an
// io.Closer; it lets the WriterStage passthrough path's downstream-is-
// Closer branch get exercised separately from the generic non-Closer
// io.Writer branch covered by bytes.Buffer.
type writerWithClose struct {
	buf    *bytes.Buffer
	closed bool
}

func (w *writerWithClose) Write(p []byte) (int, error) { return w.buf.Write(p) }
func (w *writerWithClose) Close() error                { w.closed = true; return nil }

func TestWriterStage_PassthroughCloseChainsToInnerCloser(t *testing.T) {
	var buf bytes.Buffer
	dst := &writerWithClose{buf: &buf}
	wc, err := (&encrypt.WriterStage{Pass: ""}).Open(context.Background(), dst)
	require.NoError(t, err)
	_, err = wc.Write([]byte("ok"))
	require.NoError(t, err)
	require.NoError(t, wc.Close())
	require.True(t, dst.closed, "passthrough Close must chain to inner Closer")
}

func TestWriterStage_PassthroughCloseNoCloser(t *testing.T) {
	var buf bytes.Buffer
	wc, err := (&encrypt.WriterStage{Pass: ""}).Open(context.Background(), &buf)
	require.NoError(t, err)
	_, err = wc.Write([]byte("ok"))
	require.NoError(t, err)
	require.NoError(t, wc.Close(), "passthrough Close on non-Closer downstream returns nil")
}

// errClosingWriter is a writer whose Close returns a sentinel error.
// Used to assert encryptingWriteCloser.Close joins the cascade error.
type errClosingWriter struct {
	buf bytes.Buffer
}

var errSinkClose = errors.New("synthetic sink close failure")

func (e *errClosingWriter) Write(p []byte) (int, error) { return e.buf.Write(p) }
func (e *errClosingWriter) Close() error                { return errSinkClose }

func TestWriterStage_EncryptCloseJoinsDownstreamCloseError(t *testing.T) {
	dst := &errClosingWriter{}
	wc, err := (&encrypt.WriterStage{Pass: stagePass}).Open(context.Background(), dst)
	require.NoError(t, err)
	_, err = wc.Write([]byte("payload"))
	require.NoError(t, err)
	closeErr := wc.Close()
	require.Error(t, closeErr)
	require.ErrorIs(t, closeErr, errSinkClose)
}

func TestWriterStage_EncryptCloseNoCloserDownstream(t *testing.T) {
	var buf bytes.Buffer
	wc, err := (&encrypt.WriterStage{Pass: stagePass}).Open(context.Background(), &buf)
	require.NoError(t, err)
	_, err = wc.Write([]byte("payload"))
	require.NoError(t, err)
	require.NoError(t, wc.Close())
}

// Tamper after the age header so the magic-byte peek still matches but
// the body decryption fails inside ReaderStage.decrypt's io.Copy. Keeps
// the io.Copy error branch covered in the dispatch path.
func TestReaderStage_DecryptIOCopyFailureSurfacesErrPassphrase(t *testing.T) {
	cipher := encryptBytes(t, dispatchPass, []byte("a payload that needs to be long enough to be split into a body chunk"))
	require.Greater(t, len(cipher), 200)
	cipher[len(cipher)-1] ^= 0xFF

	upstream := pipeline.Source{
		ReaderAt: bytes.NewReader(cipher),
		Size:     int64(len(cipher)),
		Close:    func() error { return nil },
	}
	_, err := (&encrypt.ReaderStage{Pass: dispatchPass, Mode: encrypt.Strict}).Open(context.Background(), upstream)
	require.ErrorIs(t, err, encrypt.ErrPassphrase)
}

// Close path: tempfile already removed by external code surfaces the
// IsNotExist branch (silently treated as nil), exercising that arm.
func TestReaderStage_CloseSilentOnExternallyRemovedTempfile(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("tempdir semantics differ on Windows")
	}
	cipher := encryptBytes(t, dispatchPass, []byte("external-remove body"))
	src, err := (&encrypt.ReaderStage{Pass: dispatchPass, Mode: encrypt.Strict}).Open(context.Background(), pipeline.Source{
		ReaderAt: bytes.NewReader(cipher),
		Size:     int64(len(cipher)),
		Close:    func() error { return nil },
	})
	require.NoError(t, err)

	tempfile, ok := src.ReaderAt.(*os.File)
	require.True(t, ok)
	require.NoError(t, os.Remove(tempfile.Name()), "external removal succeeds")

	require.NoError(t, src.Close(), "Close must treat IsNotExist remove as success")
}

// Close cascading upstream Close error: the decrypt Close should
// surface upstream.Close's error when temp close+remove succeed.
var errUpstreamCloseSentinel = errors.New("synthetic upstream close failure")

func TestReaderStage_DecryptTempfileCreateFailureSurfaces(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("TMPDIR semantics differ on Windows")
	}
	cipher := encryptBytes(t, dispatchPass, []byte("tempfile create body"))
	t.Setenv("TMPDIR", "/this/path/does/not/exist/cc-port-test")

	upstream := pipeline.Source{
		ReaderAt: bytes.NewReader(cipher),
		Size:     int64(len(cipher)),
		Close:    func() error { return nil },
	}
	_, err := (&encrypt.ReaderStage{Pass: dispatchPass, Mode: encrypt.Strict}).Open(context.Background(), upstream)
	require.Error(t, err)
	require.Contains(t, err.Error(), "create tempfile")
}

// Pre-close the inner *os.File before invoking Source.Close to surface
// the temp.Close() error branch in the decrypt close func.
func TestReaderStage_DecryptCloseSurfacesTempCloseError(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("tempdir semantics differ on Windows")
	}
	cipher := encryptBytes(t, dispatchPass, []byte("temp close err body"))
	src, err := (&encrypt.ReaderStage{Pass: dispatchPass, Mode: encrypt.Strict}).Open(context.Background(), pipeline.Source{
		ReaderAt: bytes.NewReader(cipher),
		Size:     int64(len(cipher)),
		Close:    func() error { return nil },
	})
	require.NoError(t, err)

	tempfile := src.ReaderAt.(*os.File)
	require.NoError(t, tempfile.Close(), "close tempfile out from under decrypt")
	t.Cleanup(func() { _ = os.Remove(tempfile.Name()) })

	closeErr := src.Close()
	require.Error(t, closeErr, "second temp.Close should surface as os.ErrClosed")
}

// failingWriter rejects every Write so age.Encrypt fails when writing
// its header, exercising the EncryptingWriter error fall-through in
// WriterStage.Open and the age.Encrypt error wrapper in EncryptingWriter.
type failingWriter struct{}

var errSyntheticHeaderWrite = errors.New("synthetic header write failure")

func (failingWriter) Write(_ []byte) (int, error) { return 0, errSyntheticHeaderWrite }

func TestWriterStage_EncryptOpenSurfacesAgeEncryptError(t *testing.T) {
	_, err := (&encrypt.WriterStage{Pass: stagePass}).Open(context.Background(), failingWriter{})
	require.Error(t, err)
}

// Make the tempdir non-writable after the tempfile is created so that
// os.Remove inside the decrypt Close func returns a non-IsNotExist
// error, exercising that switch arm.
func TestReaderStage_DecryptCloseSurfacesRemoveError(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("posix permission semantics required")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions; skip when running as root")
	}
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	cipher := encryptBytes(t, dispatchPass, []byte("remove err body"))
	src, err := (&encrypt.ReaderStage{Pass: dispatchPass, Mode: encrypt.Strict}).Open(context.Background(), pipeline.Source{
		ReaderAt: bytes.NewReader(cipher),
		Size:     int64(len(cipher)),
		Close:    func() error { return nil },
	})
	require.NoError(t, err)

	tempfile := src.ReaderAt.(*os.File)
	tempPath := tempfile.Name()

	require.NoError(t, os.Chmod(dir, 0o500), "drop write perm on tempdir") //nolint:gosec // G302: directory permissions, test-only
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })                         //nolint:gosec // G302: directory permissions, test-only

	closeErr := src.Close()
	require.Error(t, closeErr, "remove on read-only parent dir should surface")

	require.NoError(t, os.Chmod(dir, 0o700)) //nolint:gosec // G302: directory permissions, test-only
	_ = os.Remove(tempPath)
}

func TestReaderStage_DecryptCloseSurfacesUpstreamCloseError(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("tempdir semantics differ on Windows")
	}
	cipher := encryptBytes(t, dispatchPass, []byte("close upstream err"))
	upstream := pipeline.Source{
		ReaderAt: bytes.NewReader(cipher),
		Size:     int64(len(cipher)),
		Close:    func() error { return errUpstreamCloseSentinel },
	}
	src, err := (&encrypt.ReaderStage{Pass: dispatchPass, Mode: encrypt.Strict}).Open(context.Background(), upstream)
	require.NoError(t, err)

	closeErr := src.Close()
	require.ErrorIs(t, closeErr, errUpstreamCloseSentinel)
}
