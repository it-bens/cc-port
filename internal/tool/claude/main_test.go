package claude_test

import (
	"os"
	"testing"

	"github.com/it-bens/cc-port/internal/tool/claude/claudetest"
)

func TestMain(m *testing.M) {
	os.Exit(claudetest.RunWithSharedFixture(m.Run))
}
