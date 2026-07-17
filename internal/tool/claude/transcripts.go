package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/it-bens/cc-port/internal/fsutil"
)

// TranscriptFiles returns every transcript body file under projectDir that a
// transcript rewrite touches: the top-level *.jsonl files plus every file under
// each subdirectory other than memory/ and sessions/, which are handled as their
// own categories. The walk visits every non-memory/sessions subdirectory, not
// just the UUID-named ones, so a non-UUID body directory is not silently
// dropped. ctx is checked per top-level entry and inside each recursive walk so
// a canceled context aborts within one iteration.
func TranscriptFiles(ctx context.Context, projectDir string) ([]string, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil, fmt.Errorf("read project directory: %w", err)
	}

	var transcripts []string
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		fullPath := filepath.Join(projectDir, name)
		if !entry.IsDir() {
			if strings.HasSuffix(name, ".jsonl") {
				transcripts = append(transcripts, fullPath)
			}
			continue
		}
		if name == categoryMemory || name == categorySessions {
			continue
		}
		subdirFiles, err := fsutil.ListFilesRecursive(ctx, fullPath)
		if err != nil {
			return nil, err
		}
		transcripts = append(transcripts, subdirFiles...)
	}
	return transcripts, nil
}
