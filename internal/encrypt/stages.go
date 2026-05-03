package encrypt

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"

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

// peekBufLen is the inspection window ReaderStage hands to bufio.Peek to
// decide encrypted vs plaintext. Must be at least MinPeekLen.
const peekBufLen = 32

// readBufLen is the bufio buffer size. Sized larger than peekBufLen so
// age.Decrypt's header parse can refill past the peek window without the
// buffer doubling as a one-shot peek slot.
const readBufLen = 4096

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
// the upstream's first peekBufLen bytes via bufio.Reader.Peek (does not
// consume) and:
//
//   - encrypted + non-empty Pass: wraps the buffered upstream in
//     DecryptingReader and returns it as the new View. Meta carries
//     WasEncrypted = true.
//   - encrypted + empty Pass: returns ErrPassphraseRequired.
//   - plaintext + non-empty Pass + Mode==Strict: returns ErrUnencryptedInput.
//   - plaintext + non-empty Pass + Mode==Permissive: returns the
//     buffered upstream as the new View, propagating any ReaderAt and
//     Size the upstream already exposed. Meta.WasEncrypted = false.
//   - plaintext + empty Pass: same as the Permissive plaintext case.
//
// No tempfile is created in any branch. The bufio buffer is internal to
// the streaming Reader path; consumers that read via View.ReaderAt
// observe position-independent ReadAt semantics on the original
// upstream and are unaffected by the bufio peek.
//
// Cmd-layer read paths (import, import manifest, sync pull) compose this
// stage with Mode=Strict. Sync push prior-read composes with
// Mode=Permissive. The cmd layer never peeks bytes itself.
type ReaderStage struct {
	Pass string
	Mode Mode
}

// Open performs the dispatch. Mismatch and decrypt-failure cells return
// their sentinel; the pipeline runner closes any upstream closer it has
// accumulated so far. The stage does not call upstream.Close itself;
// the runner owns the cascade.
func (r *ReaderStage) Open(_ context.Context, upstream pipeline.View) (pipeline.View, pipeline.Meta, io.Closer, error) {
	if upstream.Reader == nil {
		return pipeline.View{}, pipeline.Meta{}, nil, errors.New("encrypt.ReaderStage: upstream is empty")
	}
	buffered := bufio.NewReaderSize(upstream.Reader, readBufLen)
	header, err := buffered.Peek(peekBufLen)
	if err != nil && !errors.Is(err, io.EOF) {
		return pipeline.View{}, pipeline.Meta{}, nil, fmt.Errorf("peek archive header: %w", err)
	}
	encrypted := IsEncrypted(header)

	switch {
	case encrypted && r.Pass == "":
		return pipeline.View{}, pipeline.Meta{}, nil, ErrPassphraseRequired
	case !encrypted && r.Pass != "" && r.Mode == Strict:
		return pipeline.View{}, pipeline.Meta{}, nil, ErrUnencryptedInput
	case encrypted && r.Pass != "":
		plain, err := DecryptingReader(buffered, r.Pass)
		if err != nil {
			return pipeline.View{}, pipeline.Meta{}, nil, fmt.Errorf("decrypt archive: %w", err)
		}
		return pipeline.View{Reader: plain}, pipeline.Meta{WasEncrypted: true}, nil, nil
	default:
		return pipeline.View{
				Reader:   buffered,
				ReaderAt: upstream.ReaderAt,
				Size:     upstream.Size,
			},
			pipeline.Meta{WasEncrypted: false},
			nil,
			nil
	}
}

// Name implements pipeline.ReaderStage.
func (r *ReaderStage) Name() string { return "decrypt" }
