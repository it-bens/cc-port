package claude_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/it-bens/cc-port/internal/tool/claude/claudetest"
)

func TestMain(m *testing.M) {
	cleanup, err := claudetest.InstallSharedFixture()
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
