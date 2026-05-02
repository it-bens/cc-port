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
// vs plaintext via View.ReaderAt.ReadAt. Must be at least MinPeekLen.
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
// Pass is empty, the stage returns downstream unchanged with a nil
// closer (passthrough). The cmd layer always includes this stage in
// its writer pipeline; the stage decides whether to act.
type WriterStage struct {
	Pass string
}

// Open returns downstream unchanged with a nil closer when Pass is
// empty; otherwise returns the age-encrypting writer as both writer and
// closer. The age writer's Close flushes the trailer; the runner
// cascades to downstream.
func (w *WriterStage) Open(_ context.Context, downstream io.Writer) (io.Writer, io.Closer, error) {
	if downstream == nil {
		return nil, nil, errors.New("encrypt.WriterStage: downstream is nil")
	}
	if w.Pass == "" {
		return downstream, nil, nil
	}
	inner, err := EncryptingWriter(downstream, w.Pass)
	if err != nil {
		return nil, nil, err
	}
	return inner, inner, nil
}

// Name implements pipeline.WriterStage.
func (w *WriterStage) Name() string { return "encrypt" }

// ReaderStage is a pipeline.ReaderStage that owns the
// encrypted-vs-plaintext × pass-vs-no-pass dispatch matrix. Open peeks
// the upstream's first 32 bytes via View.ReaderAt.ReadAt (position-
// independent; later consumers re-read from byte 0 unaffected) and:
//
//   - encrypted + non-empty Pass: decrypts upstream into a 0600 tempfile,
//     returns the tempfile as the new View plus an io.Closer that closes
//     the file and removes it.
//   - encrypted + empty Pass: returns ErrPassphraseRequired.
//   - plaintext + non-empty Pass + Mode==Strict: returns ErrUnencryptedInput.
//   - plaintext + non-empty Pass + Mode==Permissive: returns upstream
//     unchanged with a nil closer.
//   - plaintext + empty Pass: returns upstream unchanged with a nil closer.
//
// Mismatch and decrypt-failure cells return the sentinel; the pipeline
// runner closes any upstream closer it has accumulated so far. The
// stage does not call upstream.Close itself; the runner owns the
// cascade.
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
// docs. Mismatch and decrypt-failure cells return their sentinel.
func (r *ReaderStage) Open(ctx context.Context, upstream pipeline.View) (pipeline.View, io.Closer, error) {
	if upstream.ReaderAt == nil {
		return pipeline.View{}, nil, errors.New("encrypt.ReaderStage: upstream is empty")
	}
	header := make([]byte, peekBufLen)
	n, err := upstream.ReaderAt.ReadAt(header, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return pipeline.View{}, nil, fmt.Errorf("peek archive header: %w", err)
	}
	encrypted := IsEncrypted(header[:n])
	r.wasEncrypted = encrypted

	switch {
	case encrypted && r.Pass == "":
		return pipeline.View{}, nil, ErrPassphraseRequired
	case !encrypted && r.Pass != "" && r.Mode == Strict:
		return pipeline.View{}, nil, ErrUnencryptedInput
	case encrypted && r.Pass != "":
		return r.decrypt(ctx, upstream)
	default:
		// plaintext + empty pass, or plaintext + pass + Permissive
		return upstream, nil, nil
	}
}

// Name implements pipeline.ReaderStage.
func (r *ReaderStage) Name() string { return "decrypt" }

// decrypt materializes plaintext into a 0600 tempfile and returns it as
// the new View. The returned io.Closer closes the file and removes it,
// joining errors via errors.Join. On error, the tempfile is cleaned up
// here.
func (r *ReaderStage) decrypt(_ context.Context, upstream pipeline.View) (pipeline.View, io.Closer, error) {
	temp, err := os.CreateTemp("", "cc-port-decrypt-*.zip")
	if err != nil {
		return pipeline.View{}, nil, fmt.Errorf("create tempfile: %w", err)
	}
	tempPath := temp.Name()
	if err := os.Chmod(tempPath, 0o600); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return pipeline.View{}, nil, fmt.Errorf("chmod tempfile %s: %w", tempPath, err)
	}

	section := io.NewSectionReader(upstream.ReaderAt, 0, upstream.Size)
	decryptor, err := DecryptingReader(section, r.Pass)
	if err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return pipeline.View{}, nil, fmt.Errorf("decrypt archive: %w", err)
	}
	if _, err := io.Copy(temp, decryptor); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return pipeline.View{}, nil, fmt.Errorf("decrypt archive: %w", err)
	}

	info, err := temp.Stat()
	if err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return pipeline.View{}, nil, fmt.Errorf("stat tempfile %s: %w", tempPath, err)
	}

	return pipeline.View{ReaderAt: temp, Size: info.Size()},
		&tempfileCloser{file: temp, path: tempPath},
		nil
}

// tempfileCloser closes the tempfile and removes it. errors.Join
// surfaces both failures; os.IsNotExist on Remove is filtered to nil.
type tempfileCloser struct {
	file *os.File
	path string
}

func (c *tempfileCloser) Close() error {
	closeErr := c.file.Close()
	removeErr := os.Remove(c.path)
	if removeErr != nil && os.IsNotExist(removeErr) {
		removeErr = nil
	}
	return errors.Join(closeErr, removeErr)
}
