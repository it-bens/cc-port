package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/stats"
	"github.com/it-bens/cc-port/internal/testutil"
)

// driveStats runs the stats subcommand through a fresh root command against
// claudeDir, returning the result stream alone (stderr is routed to its own
// buffer and discarded). Routing through newRootCmd wires the inherited --json
// persistent flag the cmd reads.
func driveStats(t *testing.T, claudeDir string, args ...string) (stdout string, err error) {
	t.Helper()
	var outBuffer, errBuffer bytes.Buffer
	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetOut(&outBuffer)
	rootCmd.SetErr(&errBuffer)
	rootCmd.SetArgs(append([]string{"stats", "--claude-home", claudeDir}, args...))
	err = rootCmd.Execute()
	return outBuffer.String(), err
}

func TestStatsCmd_ResultRoutedToStdout(t *testing.T) {
	home := testutil.SetupFixture(t)

	stdout, err := driveStats(t, home.Dir, testutil.FixtureProjectPath())
	require.NoError(t, err)

	// A bare fmt.Println would write to os.Stdout and leave this buffer empty.
	assert.Contains(t, stdout, "cc-port stats: "+testutil.FixtureProjectPath())
	assert.Contains(t, stdout, "[claude]")
	assert.Contains(t, stdout, "References")
	assert.Contains(t, stdout, "Disk footprint")
}

func TestStatsCmd_JSONFlagEmitsFootprintDTO(t *testing.T) {
	home := testutil.SetupFixture(t)

	stdout, err := driveStats(t, home.Dir, "--json", testutil.FixtureProjectPath())
	require.NoError(t, err)

	var footprint stats.Footprint
	require.NoError(t, json.Unmarshal([]byte(stdout), &footprint))

	assert.Equal(t, testutil.FixtureProjectPath(), footprint.ProjectPath)
	require.Len(t, footprint.ByTool, 1)
	claudeFootprint := footprint.ByTool[0]
	assert.Equal(t, "claude", claudeFootprint.Tool)
	assert.NotEmpty(t, claudeFootprint.Disk)
	assert.NotEmpty(t, claudeFootprint.References)
	assert.Positive(t, claudeFootprint.ReferenceTotal)
}

func TestStatsCmd_JSONFlagEmitsAllProjectsDTO(t *testing.T) {
	home := testutil.SetupFixture(t)

	stdout, err := driveStats(t, home.Dir, "--json")
	require.NoError(t, err)

	var footprints []stats.ProjectFootprint
	require.NoError(t, json.Unmarshal([]byte(stdout), &footprints))
	assert.Len(t, footprints, 4, "the fixture stages four encoded project directories")
}

// TestStatsCmd_RendersWitnessLessSuffixAndHumanizedBytes drives all-projects
// mode against a single witness-less project and asserts the renderer flags the
// missing witness and humanizes the byte total.
func TestStatsCmd_RendersWitnessLessSuffixAndHumanizedBytes(t *testing.T) {
	claudeDir := filepath.Join(t.TempDir(), "dotclaude")
	orphanDir := filepath.Join(claudeDir, "projects", "-tmp-orphan")
	require.NoError(t, os.MkdirAll(orphanDir, 0o750))
	transcript := filepath.Join(orphanDir, "aaaaaaaa-0000-0000-0000-000000000001.jsonl")
	require.NoError(t, os.WriteFile(transcript, bytes.Repeat([]byte("x"), 2048), 0o600))

	stdout, err := driveStats(t, claudeDir)
	require.NoError(t, err)

	assert.Contains(t, stdout, "(no session witness)",
		"a project with no session witness must render the suffix")
	assert.Contains(t, stdout, "2.0 KiB",
		"a 2048-byte footprint must render via humanizeBytes' KiB branch")
}

func TestStatsCmd_TooManyArgsIsUsageError(t *testing.T) {
	home := testutil.SetupFixture(t)

	_, err := driveStats(t, home.Dir, "/one", "/two")
	require.Error(t, err)

	var usageErr *usageError
	assert.ErrorAs(t, err, &usageErr, "a second positional argument must be a usage error (exit 2)")
}

func TestHumanizeBytes(t *testing.T) {
	cases := []struct {
		name  string
		bytes int64
		want  string
	}{
		{"below one KiB renders as bytes", 512, "512 B"},
		{"exact KiB boundary", 1 << 10, "1.0 KiB"},
		{"fractional KiB", 1536, "1.5 KiB"},
		{"MiB branch", 5 << 20, "5.0 MiB"},
		{"GiB branch", 3 << 30, "3.0 GiB"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, humanizeBytes(testCase.bytes))
		})
	}
}
