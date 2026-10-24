package stats

import (
	"fmt"
	"os"
	"testing"

	"github.com/it-bens/cc-port/internal/tool/codex/codextest"
)

// TestMain installs the Codex fixture template once: every codextest
// SetupFixture call in this package copies it.
func TestMain(m *testing.M) {
	cleanup, err := codextest.InstallTemplate()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	exitCode := m.Run()
	if err := cleanup(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		exitCode = 1
	}
	os.Exit(exitCode)
}
