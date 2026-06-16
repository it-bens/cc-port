package progress

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// forceTTY pins isTTY to value under cleanup so Pick's TTY branch is
// deterministic.
func forceTTY(t *testing.T, value bool) {
	t.Helper()
	original := isTTY
	isTTY = func(*os.File) bool { return value }
	t.Cleanup(func() { isTTY = original })
}

func TestPickChoosesJSONRegardlessOfTTY(t *testing.T) {
	for _, tty := range []bool{true, false} {
		t.Run(map[bool]string{true: "tty", false: "no-tty"}[tty], func(t *testing.T) {
			forceTTY(t, tty)
			renderer, _ := Pick(Selection{JSON: true})
			assert.IsType(t, &JSONRenderer{}, renderer)
		})
	}
}

func TestPickChoosesNullWhenQuiet(t *testing.T) {
	forceTTY(t, true)
	renderer, _ := Pick(Selection{Quiet: true})
	assert.IsType(t, &NullRenderer{}, renderer)
}

func TestPickChoosesLedgerOnTTY(t *testing.T) {
	forceTTY(t, true)
	renderer, _ := Pick(Selection{})
	ledger, ok := renderer.(*LedgerRenderer)
	assert.True(t, ok, "expected *LedgerRenderer, got %T", renderer)
	if ok {
		// Tear down the program goroutine started at construction.
		assert.NoError(t, ledger.Finalize())
	}
}

func TestPickChoosesStreamOffTTY(t *testing.T) {
	forceTTY(t, false)
	renderer, _ := Pick(Selection{})
	assert.IsType(t, &StreamRenderer{}, renderer)
}

func TestPickLevelMapping(t *testing.T) {
	forceTTY(t, false)
	cases := []struct {
		name      string
		selection Selection
		want      Level
	}{
		{"quiet pins error", Selection{Quiet: true}, LevelError},
		{"quiet overrides debug", Selection{Quiet: true, Debug: true}, LevelError},
		{"debug", Selection{Debug: true}, LevelDebug},
		{"verbose", Selection{Verbose: true}, LevelVerbose},
		{"debug over verbose", Selection{Debug: true, Verbose: true}, LevelDebug},
		{"default info", Selection{}, LevelInfo},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, level := Pick(testCase.selection)
			assert.Equal(t, testCase.want, level)
		})
	}
}
