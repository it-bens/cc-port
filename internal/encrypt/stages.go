package encrypt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/it-bens/cc-port/internal/pipeline"
)

// ErrPassphraseRequired is returned by ReaderStage.Open when the input is
// encrypted but the caller did not supply a passphrase.
var ErrPassphraseRequired = errors.New(
	"encrypt: archive is encrypted; passphrase required",
)

// ErrUnencryptedInput is returned by ReaderStage.Open in Strict mode when
// the input is plaintext but the caller supplied a passphrase. Surfacing
// this loudly catches the case where the operator pointed a command at
// the wrong file.
var ErrUnencryptedInput = errors.New(
	"encrypt: archive is not encrypted; passphrase flags expected encrypted input",
)

// peekBufLen is the read-ahead size ReaderStage uses to decide encrypted
// vs plaintext via Source.ReaderAt.ReadAt. Must be at least MinPeekLen.
const peekBufLen = 32

// Mode selects ReaderStage's behavior in the plaintext-with-passphrase
// cell of the dispatch matrix. Strict is the zero value, so an unset
// Mode field on ReaderStage means Strict; do not reorder the constants.
type Mode int

const (
	// Strict refuses plaintext-with-passphrase with ErrUnencryptedInput.
	// The default for read-side cmd paths (import, import manifest, pull):
	// the operator's passphrase is for THIS archive, so plaintext is a
	// mismatch worth flagging.
	Strict Mode = iota
	// Permissive accepts plaintext-with-passphrase silently. Used by
	// sync's cc-port push prior-read where the operator's passphrase
	// targets the new archive being written; the prior archive on the
	// remote may legitimately have been pushed plaintext.
	Permissive
)

// WriterStage is a pipeline.WriterStage that encrypts plaintext bytes
// written to it under Pass before forwarding them to downstream. When
// Pass is empty, the stage returns a passthrough writer that forwards
// to downstream unchanged. The cmd layer always includes this stage in
// its writer pipeline; the stage decides whether to act.
type WriterStage struct {
	Pass string
}

// Open returns a passthrough WriteCloser when Pass is empty and an
// age-encrypting WriteCloser otherwise. Both paths cascade Close to
// downstream so the leaf sink closes when the caller closes the
// outermost writer.
func (w *WriterStage) Open(_ context.Context, downstream io.Writer) (io.WriteCloser, error) {
	if downstream == nil {
		return nil, errors.New("encrypt.WriterStage: downstream is nil")
	}
	if w.Pass == "" {
		return &passthroughWriteCloser{inner: downstream}, nil
	}
	inner, err := EncryptingWriter(downstream, w.Pass)
	if err != nil {
		return nil, err
	}
	return &encryptingWriteCloser{inner: inner, downstream: downstream}, nil
}

// Name implements pipeline.WriterStage.
func (w *WriterStage) Name() string { return "encrypt" }

// passthroughWriteCloser forwards Write to inner and chains Close to
// inner's Close when inner implements io.Closer.
type passthroughWriteCloser struct{ inner io.Writer }

func (p *passthroughWriteCloser) Write(b []byte) (int, error) { return p.inner.Write(b) }
func (p *passthroughWriteCloser) Close() error {
	if c, ok := p.inner.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// encryptingWriteCloser wraps the age writer so the pipeline contract
// holds: each filter's Close cascades to its downstream's Close. age's
// Close flushes the trailer but does not close dst, so without this
// wrapper the leaf writer (typically file.Sink's *os.File) would leak
// when Pass is non-empty. Mirrors passthroughWriteCloser so skip and
// act paths honor the same contract.
type encryptingWriteCloser struct {
	inner      io.WriteCloser
	downstream io.Writer
}

func (e *encryptingWriteCloser) Write(p []byte) (int, error) { return e.inner.Write(p) }
func (e *encryptingWriteCloser) Close() error {
	err := e.inner.Close()
	if c, ok := e.downstream.(io.Closer); ok {
		if cerr := c.Close(); cerr != nil {
			return errors.Join(err, cerr)
		}
	}
	return err
}

// ReaderStage is a pipeline.ReaderStage that owns the
// encrypted-vs-plaintext × pass-vs-no-pass dispatch matrix. Open peeks
// the upstream's first 32 bytes via Source.ReaderAt.ReadAt (position-
// independent; later consumers re-read from byte 0 unaffected) and:
//
//   - encrypted + non-empty Pass: decrypts upstream into a 0600 tempfile,
//     returns a new Source whose ReaderAt is that tempfile.
//   - encrypted + empty Pass: returns ErrPassphraseRequired.
//   - plaintext + non-empty Pass + Mode==Strict: returns ErrUnencryptedInput.
//   - plaintext + non-empty Pass + Mode==Permissive: returns upstream
//     unchanged.
//   - plaintext + empty Pass: returns upstream unchanged.
//
// Mismatch and decrypt-failure cells return the sentinel; the pipeline
// runner closes the upstream Source on stage error
// (pipeline.RunReader). The stage does not close upstream on the error
// path, which keeps the close-once invariant in one place.
//
// Cmd-layer read paths (import, import manifest, sync pull) compose this
// stage with Mode=Strict. Sync push prior-read composes with
// Mode=Permissive. The cmd layer never peeks bytes itself.
type ReaderStage struct {
	Pass string
	Mode Mode

	// wasEncrypted records the IsEncrypted peek decision from the most
	// recent successful Open. Stale after a failed Open; callers must
	// not read it on the error path.
	wasEncrypted bool
}

// WasEncrypted reports whether the most recent Open dispatched the
// encrypted branch (encrypted upstream + non-empty Pass). Read after a
// successful Open only.
func (r *ReaderStage) WasEncrypted() bool { return r.wasEncrypted }

// Open peeks the upstream's first 32 bytes and dispatches the
// encrypted-vs-plaintext × pass-vs-no-pass matrix per the package
// docs. Mismatch and decrypt-failure cells return their sentinel
// without closing upstream; pipeline.RunReader is the sole closer on
// stage error.
func (r *ReaderStage) Open(ctx context.Context, upstream pipeline.Source) (pipeline.Source, error) {
	if upstream.ReaderAt == nil {
		return pipeline.Source{}, errors.New("encrypt.ReaderStage: upstream is empty")
	}
	header := make([]byte, peekBufLen)
	n, err := upstream.ReaderAt.ReadAt(header, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return pipeline.Source{}, fmt.Errorf("peek archive header: %w", err)
	}
	encrypted := IsEncrypted(header[:n])
	r.wasEncrypted = encrypted

	switch {
	case encrypted && r.Pass == "":
		return pipeline.Source{}, ErrPassphraseRequired
	case !encrypted && r.Pass != "" && r.Mode == Strict:
		return pipeline.Source{}, ErrUnencryptedInput
	case encrypted && r.Pass != "":
		return r.decrypt(ctx, upstream)
	default:
		// plaintext + empty pass, or plaintext + pass + Permissive
		return upstream, nil
	}
}

// Name implements pipeline.ReaderStage.
func (r *ReaderStage) Name() string { return "decrypt" }

// decrypt materializes plaintext into a 0600 tempfile and returns it as
// the new Source.ReaderAt. Source.Close removes the tempfile
// (idempotent) and chains to upstream.Close. On error, the tempfile is
// cleaned up here; the pipeline runner closes the upstream Source.
func (r *ReaderStage) decrypt(_ context.Context, upstream pipeline.Source) (pipeline.Source, error) {
	temp, err := os.CreateTemp("", "cc-port-decrypt-*.zip")
	if err != nil {
		return pipeline.Source{}, fmt.Errorf("create tempfile: %w", err)
	}
	tempPath := temp.Name()
	if err := os.Chmod(tempPath, 0o600); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return pipeline.Source{}, fmt.Errorf("chmod tempfile %s: %w", tempPath, err)
	}

	section := io.NewSectionReader(upstream.ReaderAt, 0, upstream.Size)
	decryptor, err := DecryptingReader(section, r.Pass)
	if err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return pipeline.Source{}, fmt.Errorf("decrypt archive: %w", err)
	}
	if _, err := io.Copy(temp, decryptor); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return pipeline.Source{}, fmt.Errorf("decrypt archive: %w", err)
	}

	info, err := temp.Stat()
	if err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return pipeline.Source{}, fmt.Errorf("stat tempfile %s: %w", tempPath, err)
	}

	upstreamClose := upstream.Close
	closed := false
	return pipeline.Source{
		ReaderAt: temp,
		Size:     info.Size(),
		Close: func() error {
			if closed {
				return nil
			}
			closed = true
			closeErr := temp.Close()
			removeErr := os.Remove(tempPath)
			var upstreamErr error
			if upstreamClose != nil {
				upstreamErr = upstreamClose()
			}
			switch {
			case closeErr != nil:
				return closeErr
			case removeErr != nil && !os.IsNotExist(removeErr):
				return removeErr
			default:
				return upstreamErr
			}
		},
	}, nil
}
