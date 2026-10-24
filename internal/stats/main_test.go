package stats

import (
	"os"
	"testing"

	"github.com/it-bens/cc-port/internal/tool/codex/codextest"
)

func TestMain(m *testing.M) {
	os.Exit(codextest.RunWithTemplate(m))
}
