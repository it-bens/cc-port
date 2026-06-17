package progress

import "io"

// CountingWriter wraps w so each successful write advances handle by the bytes
// actually written, not the bytes offered. A partial write that also returns
// an error still advances by what moved.
func CountingWriter(w io.Writer, handle PhaseHandle) io.Writer {
	return &countingWriter{inner: w, handle: handle}
}

type countingWriter struct {
	inner  io.Writer
	handle PhaseHandle
}

func (writer *countingWriter) Write(p []byte) (int, error) {
	n, err := writer.inner.Write(p)
	if n > 0 {
		writer.handle.Advance(int64(n))
	}
	return n, err
}

// CountingReader wraps r so each read advances handle by the bytes actually
// read, not the bytes requested. A short read that also returns an error still
// advances by what moved.
func CountingReader(r io.Reader, handle PhaseHandle) io.Reader {
	return &countingReader{inner: r, handle: handle}
}

type countingReader struct {
	inner  io.Reader
	handle PhaseHandle
}

func (reader *countingReader) Read(p []byte) (int, error) {
	n, err := reader.inner.Read(p)
	if n > 0 {
		reader.handle.Advance(int64(n))
	}
	return n, err
}
