package codex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/sqlrewrite"
)

func TestSQLiteBusyCodeClassifiesPrimaryAndExtendedCodes(t *testing.T) {
	cases := []struct {
		name string
		code int
		want bool
	}{
		{name: "busy", code: 5, want: true},
		{name: "busy recovery", code: 261, want: true},
		{name: "busy snapshot", code: 517, want: true},
		{name: "constraint", code: 19, want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, isSQLiteBusyCode(testCase.code))
		})
	}
}

func TestProbeDatabaseBusyDetectsWriterOnPathContainingQuestionMark(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dot?codex")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	dbPath := filepath.Join(dir, "state_5.sqlite")

	// Seed the real file through a correctly escaped DSN; a raw sql.Open on
	// dbPath would itself be truncated at the '?' and create the wrong file.
	seed, err := sql.Open("sqlite", sqlrewrite.FileDSN(dbPath, nil))
	require.NoError(t, err)
	_, err = seed.ExecContext(context.Background(), "CREATE TABLE marker (value INTEGER)")
	require.NoError(t, err)
	require.NoError(t, seed.Close())

	blocker, err := sql.Open("sqlite", sqlrewrite.FileDSN(dbPath, nil))
	require.NoError(t, err)
	defer func() { _ = blocker.Close() }()
	_, err = blocker.ExecContext(context.Background(), "BEGIN IMMEDIATE")
	require.NoError(t, err)
	defer func() { _, _ = blocker.ExecContext(context.Background(), "ROLLBACK") }()

	busy, err := probeDatabaseBusy(dbPath)

	require.NoError(t, err)
	assert.True(t, busy, "probeDatabaseBusy must open the real file at a '?'-containing path, not a prefix truncated at the first '?'")
}
