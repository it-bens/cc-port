package codex

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParsePSOutputRejectsLineBeyondScannerCap(t *testing.T) {
	_, err := parsePSOutput([]byte("1 " + strings.Repeat("x", 1024*1024) + "\n"))

	assert.Error(t, err)
}
