package progress

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNullRendererDropsAllButWarningsAndSummary(t *testing.T) {
	var buffer bytes.Buffer
	renderer := NewNullRenderer(&buffer)

	renderer.Consume(PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles})
	renderer.Consume(PhaseAdvance{Path: []string{"copy"}, Done: 5})
	renderer.Consume(Detail{Level: LevelInfo, Text: "noise"})
	renderer.Consume(Warning{Err: errors.New("skipped a file")})
	renderer.Consume(PhaseEnd{Path: []string{"copy"}, Summary: "done"})
	renderer.Consume(Done{})
	require.NoError(t, renderer.Finalize())

	assert.Equal(t, "[WARN] skipped a file\ndone (1 warning)\n", buffer.String())
}

func TestNullRendererSummaryOmitsSuffixWithoutWarnings(t *testing.T) {
	var buffer bytes.Buffer
	renderer := NewNullRenderer(&buffer)

	renderer.Consume(Done{})
	require.NoError(t, renderer.Finalize())

	assert.Equal(t, "done\n", buffer.String())
}

func TestNullRendererPluralizesWarningCount(t *testing.T) {
	var buffer bytes.Buffer
	renderer := NewNullRenderer(&buffer)

	renderer.Consume(Warning{Err: errors.New("first")})
	renderer.Consume(Warning{Err: errors.New("second")})
	renderer.Consume(Done{})
	require.NoError(t, renderer.Finalize())

	assert.Equal(t, "[WARN] first\n[WARN] second\ndone (2 warnings)\n", buffer.String())
}

func TestNullRendererFailNamesActivePhase(t *testing.T) {
	var buffer bytes.Buffer
	renderer := NewNullRenderer(&buffer)

	renderer.Consume(PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles})
	renderer.Consume(Failed{Err: errors.New("disk full")})
	require.NoError(t, renderer.Finalize())

	assert.Equal(t, "[ERROR] disk full\nfailed at copy\n", buffer.String())
}

func TestNullRendererFailAppendsWarningSuffix(t *testing.T) {
	var buffer bytes.Buffer
	renderer := NewNullRenderer(&buffer)

	renderer.Consume(PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles})
	renderer.Consume(Warning{Err: errors.New("skipped one")})
	renderer.Consume(Failed{Err: errors.New("disk full")})
	require.NoError(t, renderer.Finalize())

	assert.Equal(t, "[WARN] skipped one\n[ERROR] disk full\nfailed at copy (1 warning)\n", buffer.String())
}

func TestNullRendererInterruptedLine(t *testing.T) {
	var buffer bytes.Buffer
	renderer := NewNullRenderer(&buffer)

	renderer.Consume(PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles})
	renderer.Consume(Cancelled{Reason: "user interrupt"})
	require.NoError(t, renderer.Finalize())

	assert.Equal(t, "[INTERRUPTED] user interrupt\n", buffer.String())
}

func TestNullRendererInterruptedAppendsWarningSuffix(t *testing.T) {
	var buffer bytes.Buffer
	renderer := NewNullRenderer(&buffer)

	renderer.Consume(PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles})
	renderer.Consume(Warning{Err: errors.New("skipped one")})
	renderer.Consume(Warning{Err: errors.New("skipped two")})
	renderer.Consume(Cancelled{Reason: "user interrupt"})
	require.NoError(t, renderer.Finalize())

	assert.Equal(t,
		"[WARN] skipped one\n[WARN] skipped two\n[INTERRUPTED] user interrupt (2 warnings)\n",
		buffer.String())
}
