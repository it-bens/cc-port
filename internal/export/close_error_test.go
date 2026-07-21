package export_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/export"
)

type centralDirectoryErrorWriter struct {
	output   bytes.Buffer
	writeErr error
}

func (writer *centralDirectoryErrorWriter) Write(data []byte) (int, error) {
	// The central-directory signature is emitted only when zip.Writer.Close
	// finalizes entries that archive.NewSink wrote during export.
	if bytes.Contains(data, []byte{'P', 'K', 0x01, 0x02}) {
		return 0, writer.writeErr
	}
	return writer.output.Write(data)
}

func TestRun_ArchiveWriterCloseError(t *testing.T) {
	targets, projectPath := fixtureTargets(t)
	sentinel := errors.New("synthetic archive-writer close failure")
	faultWriter := &centralDirectoryErrorWriter{writeErr: sentinel}

	_, err := export.Run(t.Context(), targets, &export.Options{
		ProjectPath: projectPath,
		Output:      faultWriter,
		Selected:    map[string]map[string]bool{"claude": {"sessions": true}},
	})

	require.ErrorIs(t, err, sentinel)
	require.ErrorContains(t, err, "finalize archive")
}
