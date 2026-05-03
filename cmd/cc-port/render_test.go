package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/scan"
)

func TestRenderRulesReport_EmptyReportProducesNoOutput(t *testing.T) {
	var buf bytes.Buffer

	renderRulesReport(&buf, "", scan.Report{})

	assert.Empty(t, buf.String())
}

func TestRenderRulesReport_WarningsListed(t *testing.T) {
	var buf bytes.Buffer

	renderRulesReport(&buf, "", scan.Report{
		Warnings: []scan.Warning{
			{File: "rule.md", Line: 3, Path: "/old"},
			{File: "other.md", Line: 12, Path: "/old"},
		},
	})

	output := buf.String()
	assert.Contains(t, output, "Warning: Rules files with matching paths:")
	assert.Contains(t, output, "rule.md (line 3)")
	assert.Contains(t, output, "other.md (line 12)")
}

func TestRenderRulesReport_ScanErrorReported(t *testing.T) {
	var buf bytes.Buffer

	renderRulesReport(&buf, "", scan.Report{Err: errors.New("permission denied")})

	output := buf.String()
	assert.Contains(t, output, "could not scan rules files")
	assert.Contains(t, output, "permission denied")
}

func TestRenderRulesReport_PrefixAppliedToHeadingAndIndentedBody(t *testing.T) {
	var buf bytes.Buffer

	renderRulesReport(&buf, "  └ ", scan.Report{
		Warnings: []scan.Warning{{File: "rule.md", Line: 3}},
	})

	// Pin exact alignment: the body indent is computed from the prefix's
	// rune count, not its byte length, so "  └ " (4 runes / 6 bytes) yields
	// 4 + 2 = 6 spaces of body indent. assert.Contains would let an 8-space
	// regression slip through (it still contains 6 spaces).
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "  └ Warning: Rules files with matching paths:", lines[0])
	assert.Equal(t, "      rule.md (line 3)", lines[1])
}
