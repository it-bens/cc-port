package progress

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingHandle is a PhaseHandle stub that captures Advance deltas. Only
// Advance is exercised by the counting wrappers; the remaining methods satisfy
// the interface and must never be called.
type recordingHandle struct {
	advances []int64
}

func (handle *recordingHandle) Advance(n int64) { handle.advances = append(handle.advances, n) }

func (*recordingHandle) Phase(string, int64, Unit) PhaseHandle {
	panic("unexpected Phase call")
}

func (*recordingHandle) SubPhase(string, int64, Unit) PhaseHandle {
	panic("unexpected SubPhase call")
}
func (*recordingHandle) Detail(Level, string, ...any) { panic("unexpected Detail call") }
func (*recordingHandle) Warn(error)                   { panic("unexpected Warn call") }
func (*recordingHandle) End(string)                   { panic("unexpected End call") }
func (*recordingHandle) Done()                        { panic("unexpected Done call") }
func (*recordingHandle) Fail(error)                   { panic("unexpected Fail call") }
func (*recordingHandle) Cancelled(string)             { panic("unexpected Cancelled call") }

// shortWriter writes only the first accept bytes and reports an error, to
// exercise the partial-progress path.
type shortWriter struct {
	accept int
	err    error
}

func (writer *shortWriter) Write(p []byte) (int, error) {
	written := writer.accept
	if written > len(p) {
		written = len(p)
	}
	return written, writer.err
}

// shortReader reads only the first deliver bytes and reports an error.
type shortReader struct {
	deliver int
	err     error
}

func (reader *shortReader) Read(p []byte) (int, error) {
	read := reader.deliver
	if read > len(p) {
		read = len(p)
	}
	return read, reader.err
}

// shortReaderAt delivers only the first deliver bytes for any ReadAt.
type shortReaderAt struct {
	deliver int
	err     error
}

func (reader *shortReaderAt) ReadAt(p []byte, _ int64) (int, error) {
	read := reader.deliver
	if read > len(p) {
		read = len(p)
	}
	return read, reader.err
}

func TestCountingWriterAdvancesByBytesWritten(t *testing.T) {
	handle := &recordingHandle{}
	writer := CountingWriter(&shortWriter{accept: 8}, handle)

	n, err := writer.Write(make([]byte, 8))

	require.NoError(t, err)
	assert.Equal(t, 8, n)
	assert.Equal(t, []int64{8}, handle.advances)
}

func TestCountingWriterAdvancesByShortWriteEvenWithError(t *testing.T) {
	handle := &recordingHandle{}
	writeErr := errors.New("disk full")
	writer := CountingWriter(&shortWriter{accept: 3, err: writeErr}, handle)

	n, err := writer.Write(make([]byte, 10))

	require.ErrorIs(t, err, writeErr)
	assert.Equal(t, 3, n)
	// Advances by the 3 bytes that moved, not the 10 offered.
	assert.Equal(t, []int64{3}, handle.advances)
}

func TestCountingWriterDoesNotAdvanceOnZeroBytes(t *testing.T) {
	handle := &recordingHandle{}
	writer := CountingWriter(&shortWriter{accept: 0, err: errors.New("closed")}, handle)

	_, err := writer.Write(make([]byte, 4))

	require.Error(t, err)
	assert.Empty(t, handle.advances)
}

func TestCountingReaderAdvancesByBytesRead(t *testing.T) {
	handle := &recordingHandle{}
	reader := CountingReader(&shortReader{deliver: 5}, handle)

	n, err := reader.Read(make([]byte, 16))

	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, []int64{5}, handle.advances)
}

func TestCountingReaderAdvancesOnShortReadWithEOF(t *testing.T) {
	handle := &recordingHandle{}
	reader := CountingReader(&shortReader{deliver: 2, err: io.EOF}, handle)

	n, err := reader.Read(make([]byte, 16))

	require.ErrorIs(t, err, io.EOF)
	assert.Equal(t, 2, n)
	assert.Equal(t, []int64{2}, handle.advances)
}

func TestCountingReaderAtAdvancesByBytesRead(t *testing.T) {
	handle := &recordingHandle{}
	reader := CountingReaderAt(&shortReaderAt{deliver: 4}, handle)

	n, err := reader.ReadAt(make([]byte, 16), 100)

	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, []int64{4}, handle.advances)
}
