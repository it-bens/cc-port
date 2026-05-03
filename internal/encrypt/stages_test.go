package encrypt_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/pipeline"
)

const stagePass = "stage-passphrase"

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

func TestWriterStage_Name(t *testing.T) {
	require.Equal(t, "encrypt", (&encrypt.WriterStage{Pass: "x"}).Name())
}

func TestWriterStage_RejectsNilDownstream(t *testing.T) {
	_, _, err := (&encrypt.WriterStage{Pass: stagePass}).Open(context.Background(), nil)
	require.Error(t, err)
}

func TestWriterStage_EncryptOpenSurfacesAgeEncryptError(t *testing.T) {
	_, _, err := (&encrypt.WriterStage{Pass: stagePass}).Open(context.Background(), failingWriter{})
	require.Error(t, err)
}

func TestEncryptingWriter_RejectsEmptyPassphrase(t *testing.T) {
	_, err := encrypt.EncryptingWriter(&bytes.Buffer{}, "")
	require.Error(t, err)
}

func TestDecryptingReader_RejectsEmptyPassphrase(t *testing.T) {
	_, err := encrypt.DecryptingReader(&bytes.Buffer{}, "")
	require.Error(t, err)
}

// bufferSinkStage is a test-only sink. The closed flag is the cascade
// witness: WriterStage's encrypt path must close the downstream sink
// when the caller closes the outermost writer; the round-trip test
// asserts this flag.
type bufferSinkStage struct {
	buf    *bytes.Buffer
	closed bool
}

func (s *bufferSinkStage) Open(_ context.Context, _ io.Writer) (io.Writer, io.Closer, error) {
	return s.buf, &bufferSinkCloser{stage: s}, nil
}
func (s *bufferSinkStage) Name() string { return "buffer-sink" }

type bufferSinkCloser struct{ stage *bufferSinkStage }

func (c *bufferSinkCloser) Close() error { c.stage.closed = true; return nil }

// failingWriter rejects every Write so age.Encrypt fails when writing
// its header, exercising the EncryptingWriter error fall-through in
// WriterStage.Open and the age.Encrypt error wrapper in EncryptingWriter.
type failingWriter struct{}

var errSyntheticHeaderWrite = errors.New("synthetic header write failure")

func (failingWriter) Write(_ []byte) (int, error) { return 0, errSyntheticHeaderWrite }

// guard against bytes.Reader/io.SectionReader interface drift.
var _ io.ReaderAt = (*bytes.Reader)(nil)

// encryptBytes runs body through encrypt.EncryptingWriter under passphrase
// and returns the cipher bytes. Centralizes the encrypt-and-buffer pattern
// so test bodies that need cipher bytes for a custom upstream View or
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

const dispatchPass = "dispatch-passphrase"

// streamingPlaintextView wraps body as both Reader and ReaderAt + Size, mirroring
// what file.Source produces. Used for plaintext branches whose contract
// propagates upstream ReaderAt/Size through.
func streamingPlaintextView(t *testing.T, body []byte) pipeline.View {
	t.Helper()
	r := bytes.NewReader(body)
	return pipeline.View{Reader: r, ReaderAt: r, Size: int64(len(body))}
}

// streamingEncryptedView wraps cipher bytes as Reader-only + ReaderAt for parity
// with file.Source. The encrypted+pass branch drops ReaderAt and Size from the
// returned View; this helper still populates them on the upstream side.
func streamingEncryptedView(t *testing.T, body []byte) pipeline.View {
	t.Helper()
	return streamingPlaintextView(t, encryptBytes(t, dispatchPass, body))
}

func TestReaderStage_EncryptedWithPassDecryptsToStreamingView(t *testing.T) {
	plaintext := []byte("reader stage decrypt round trip")
	upstream := streamingEncryptedView(t, plaintext)

	view, meta, closer, err := (&encrypt.ReaderStage{Pass: dispatchPass, Mode: encrypt.Strict}).Open(context.Background(), upstream)

	require.NoError(t, err)
	require.Nil(t, closer, "decrypt branch contributes no closer; upstream's closer is the runner's")
	assert.True(t, meta.WasEncrypted, "encrypted+pass branch must report WasEncrypted")
	require.NotNil(t, view.Reader)
	require.Nil(t, view.ReaderAt, "decrypt branch produces a streaming View; ReaderAt must be nil")
	assert.Equal(t, int64(0), view.Size, "decrypt branch produces an unknown plaintext size; Size must be 0")
	got, err := io.ReadAll(view.Reader)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

func TestReaderStage_StrictPlaintextEmptyPassPassthroughPropagatesRandomAccess(t *testing.T) {
	body := []byte("plaintext archive bytes")
	upstream := streamingPlaintextView(t, body)

	view, meta, closer, err := (&encrypt.ReaderStage{Pass: "", Mode: encrypt.Strict}).Open(context.Background(), upstream)

	require.NoError(t, err)
	require.Nil(t, closer, "plaintext+empty-pass passthrough must contribute no closer")
	assert.False(t, meta.WasEncrypted)
	require.NotNil(t, view.Reader, "plaintext branch returns the bufio-wrapped Reader")
	require.Equal(t, upstream.ReaderAt, view.ReaderAt, "plaintext branch must propagate upstream ReaderAt")
	assert.Equal(t, upstream.Size, view.Size, "plaintext branch must propagate upstream Size")
}

func TestReaderStage_StrictPlaintextWithPassReturnsErrUnencryptedInput(t *testing.T) {
	upstream := streamingPlaintextView(t, []byte("plaintext bytes"))

	_, _, _, err := (&encrypt.ReaderStage{Pass: dispatchPass, Mode: encrypt.Strict}).Open(context.Background(), upstream)

	require.ErrorIs(t, err, encrypt.ErrUnencryptedInput)
}

func TestReaderStage_StrictEncryptedEmptyPassReturnsErrPassphraseRequired(t *testing.T) {
	upstream := streamingEncryptedView(t, []byte("body"))

	_, _, _, err := (&encrypt.ReaderStage{Pass: "", Mode: encrypt.Strict}).Open(context.Background(), upstream)

	require.ErrorIs(t, err, encrypt.ErrPassphraseRequired)
}

func TestReaderStage_StrictEncryptedWrongPassReturnsErrPassphraseOnOpen(t *testing.T) {
	// age scrypt MAC-verifies the file-key envelope during age.Decrypt.
	// A wrong passphrase therefore fails on Open, before any Read.
	// DecryptingReader wraps the failure with ErrPassphrase via errors.Join.
	upstream := streamingEncryptedView(t, []byte("body"))

	_, _, _, err := (&encrypt.ReaderStage{Pass: "wrong-passphrase", Mode: encrypt.Strict}).Open(context.Background(), upstream)

	require.Error(t, err)
	require.ErrorIs(t, err, encrypt.ErrPassphrase)
}

func TestReaderStage_PermissivePlaintextWithPassPassthroughPropagatesRandomAccess(t *testing.T) {
	body := []byte("permissive plain")
	upstream := streamingPlaintextView(t, body)

	view, meta, closer, err := (&encrypt.ReaderStage{Pass: dispatchPass, Mode: encrypt.Permissive}).Open(context.Background(), upstream)

	require.NoError(t, err, "Permissive must not refuse plaintext-with-pass")
	require.Nil(t, closer)
	assert.False(t, meta.WasEncrypted)
	require.Equal(t, upstream.ReaderAt, view.ReaderAt)
	assert.Equal(t, upstream.Size, view.Size)
}

func TestReaderStage_PermissiveEncryptedEmptyPassReturnsErrPassphraseRequired(t *testing.T) {
	upstream := streamingEncryptedView(t, []byte("body"))

	_, _, _, err := (&encrypt.ReaderStage{Pass: "", Mode: encrypt.Permissive}).Open(context.Background(), upstream)

	require.ErrorIs(t, err, encrypt.ErrPassphraseRequired)
}

func TestReaderStage_RejectsEmptyUpstream(t *testing.T) {
	_, _, _, err := (&encrypt.ReaderStage{Pass: stagePass}).Open(context.Background(), pipeline.View{})

	require.Error(t, err)
}

func TestReaderStage_PeekErrorSurfaces(t *testing.T) {
	_, _, _, err := (&encrypt.ReaderStage{Pass: stagePass}).Open(context.Background(), pipeline.View{
		Reader: failingReader{},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "peek archive header")
}

// failingReader returns a non-EOF error on every Read, exercising
// ReaderStage's bufio.Peek error branch.
type failingReader struct{}

func (failingReader) Read(_ []byte) (int, error) {
	return 0, errors.New("synthetic read failure")
}

// streamingCountingSource is a ReaderStage that emits the supplied bytes as
// a streaming pipeline.View and counts how many times the runner closes it.
// Replaces the old ReaderAt-shaped countingSourceStage.
type streamingCountingSource struct {
	bytes    []byte
	closeOut *int
}

func (s *streamingCountingSource) Open(_ context.Context, _ pipeline.View) (pipeline.View, pipeline.Meta, io.Closer, error) {
	return pipeline.View{Reader: bytes.NewReader(s.bytes)},
		pipeline.Meta{},
		&streamingCountingCloser{count: s.closeOut},
		nil
}
func (s *streamingCountingSource) Name() string { return "streaming-counting-source" }

type streamingCountingCloser struct{ count *int }

func (c *streamingCountingCloser) Close() error { *c.count++; return nil }

// TestReaderStage_RunReaderClosesUpstreamOnceOnError pins the runner-as-sole-
// closer contract on a streaming source: ReaderStage returns the
// ErrPassphraseRequired sentinel without closing upstream itself, and
// pipeline.RunReader closes upstream exactly once.
func TestReaderStage_RunReaderClosesUpstreamOnceOnError(t *testing.T) {
	closeCount := 0
	source := &streamingCountingSource{
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

func TestReaderStage_Name(t *testing.T) {
	require.Equal(t, "decrypt", (&encrypt.ReaderStage{Pass: "x"}).Name())
}
