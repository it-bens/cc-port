package move

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/it-bens/cc-port/internal/rewrite"
)

// rewriteNewProjectDir rewrites the copied project dir: transcripts and memory files.
func rewriteNewProjectDir(ctx context.Context, newProjectDir string, moveOptions Options) error {
	if moveOptions.RewriteTranscripts {
		if err := rewriteTranscriptsInDir(ctx, newProjectDir, moveOptions); err != nil {
			return err
		}
	}

	if err := rewriteMemoryFilesInDir(ctx, newProjectDir, moveOptions); err != nil {
		return err
	}

	return nil
}

func rewriteTranscriptsInDir(ctx context.Context, newProjectDir string, moveOptions Options) error {
	newTranscripts, err := listTranscriptFiles(ctx, newProjectDir)
	if err != nil {
		return fmt.Errorf("collect transcripts in new dir: %w", err)
	}
	for _, transcriptPath := range newTranscripts {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := os.ReadFile(transcriptPath) //nolint:gosec // path constructed from trusted internal data
		if err != nil {
			return fmt.Errorf("read transcript %s: %w", transcriptPath, err)
		}
		rewritten, _ := rewrite.ReplacePathInBytes(data, moveOptions.OldPath, moveOptions.NewPath)
		if err := rewrite.SafeWriteFile(transcriptPath, rewritten, 0o644); err != nil {
			return fmt.Errorf("write transcript %s: %w", transcriptPath, err)
		}
	}
	return nil
}

func rewriteMemoryFilesInDir(ctx context.Context, newProjectDir string, moveOptions Options) error {
	newMemoryDir := filepath.Join(newProjectDir, "memory")
	if _, err := os.Stat(newMemoryDir); err != nil {
		return nil
	}

	memoryEntries, err := os.ReadDir(newMemoryDir)
	if err != nil {
		return fmt.Errorf("read new memory directory: %w", err)
	}
	for _, entry := range memoryEntries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			continue
		}
		memoryFilePath := filepath.Join(newMemoryDir, entry.Name())
		data, err := os.ReadFile(memoryFilePath) //nolint:gosec // path constructed from trusted internal data
		if err != nil {
			return fmt.Errorf("read memory file %s: %w", memoryFilePath, err)
		}
		rewritten, _ := rewrite.ReplacePathInBytes(data, moveOptions.OldPath, moveOptions.NewPath)
		if err := rewrite.SafeWriteFile(memoryFilePath, rewritten, 0o644); err != nil {
			return fmt.Errorf("write memory file %s: %w", memoryFilePath, err)
		}
	}
	return nil
}
