package importer

import (
	"testing"

	"github.com/it-bens/cc-port/internal/tool/claude"
)

func TestFileHistoryLiteralsMatchClaudeAdapter(t *testing.T) {
	adapter := claude.New()
	if fileHistoryTool != adapter.Name() {
		t.Fatalf("fileHistoryTool = %q, want %q", fileHistoryTool, adapter.Name())
	}

	for _, category := range adapter.Categories() {
		if category.Name == "file-history" {
			if want := category.Name + "/"; fileHistoryEntryPrefix != want {
				t.Fatalf("fileHistoryEntryPrefix = %q, want %q", fileHistoryEntryPrefix, want)
			}
			return
		}
	}
	t.Fatal("Claude adapter has no file-history category")
}
