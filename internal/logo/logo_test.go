package logo

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStringColoredOnLightBgUsesBrandNavy(t *testing.T) {
	got := String(false, true)

	assert.Contains(t, got, ansiNavyOnLightBg)
	assert.NotContains(t, got, ansiNavyOnDarkBg)
	assert.Contains(t, got, ansiOrange)
	assert.Contains(t, got, ansiReset)
}

func TestStringColoredOnDarkBgUsesLightenedNavy(t *testing.T) {
	got := String(true, true)

	assert.Contains(t, got, ansiNavyOnDarkBg)
	assert.NotContains(t, got, ansiNavyOnLightBg)
	assert.Contains(t, got, ansiOrange)
}

func TestStringPlainHasNoAnsi(t *testing.T) {
	got := String(false, false)

	assert.NotContains(t, got, "\x1b[")
}

func TestStringRowsHaveUniformRuneWidth(t *testing.T) {
	plain := String(false, false)
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")

	require.Len(t, lines, len(art))
	for index, line := range lines {
		assert.Equal(t, rowWidth, utf8.RuneCountInString(line), "row %d width", index)
	}
}

func TestRenderToNonTerminalWritesNothing(t *testing.T) {
	var buffer bytes.Buffer

	require.NoError(t, Render(&buffer))

	assert.Empty(t, buffer.Bytes())
}

func TestIsTerminalNonFileWriterIsFalse(t *testing.T) {
	var buffer bytes.Buffer

	assert.False(t, IsTerminal(&buffer))
}

func TestComposeBesidePairsLogoRowsWithText(t *testing.T) {
	got := composeBeside("cc-port 1.2.3\nsubtitle line\n", false, false)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")

	require.Len(t, lines, len(art))
	// Short text centers vertically; 14 logo rows, 2 text rows → offset 6.
	assert.Contains(t, lines[6], "cc-port 1.2.3")
	assert.Contains(t, lines[7], "subtitle line")
	assert.NotContains(t, lines[0], "cc-port")
	// Every line begins with either a logo row or the blank-logo padding.
	for index, line := range lines {
		assert.GreaterOrEqual(t, utf8.RuneCountInString(line), rowWidth+len(besidePad), "row %d", index)
	}
}

func TestComposeBesideCentersLogoInsideLongerText(t *testing.T) {
	var builder strings.Builder
	for range 20 {
		builder.WriteString("help text line\n")
	}

	got := composeBeside(builder.String(), false, false)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")

	// 20 text rows, 14 logo rows → logo starts at row 3, ends at row 16.
	require.Len(t, lines, 20)
	assert.NotContains(t, lines[0], "┌")
	assert.NotContains(t, lines[2], "┌")
	assert.Contains(t, lines[3], "┌")
	assert.Contains(t, lines[16], "└")
	assert.NotContains(t, lines[17], "└")
	for _, line := range lines {
		assert.Contains(t, line, "help text line")
	}
}

func TestRenderBesideToNonTerminalWritesTextOnly(t *testing.T) {
	var buffer bytes.Buffer

	require.NoError(t, RenderBeside(&buffer, "cc-port 1.2.3\n"))

	assert.Equal(t, "cc-port 1.2.3\n", buffer.String())
}
