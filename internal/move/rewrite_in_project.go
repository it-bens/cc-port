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
		if err := rewritePathsPreservingMtime(transcriptPath, moveOptions); err != nil {
			return fmt.Errorf("rewrite transcript %s: %w", transcriptPath, err)
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
		if err := rewritePathsPreservingMtime(memoryFilePath, moveOptions); err != nil {
			return fmt.Errorf("rewrite memory file %s: %w", memoryFilePath, err)
		}
	}
	return nil
}

// rewritePathsPreservingMtime replaces the old project path with the new one
// inside the file at path, then restores its modification time. The mtime read
// here is the value CopyDir carried over from the source, so correctness
// depends on the copy running before this rewrite.
func rewritePathsPreservingMtime(path string, moveOptions Options) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // path constructed from trusted internal data
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	rewritten, _ := rewrite.ReplacePathInBytes(data, moveOptions.OldPath, moveOptions.NewPath)
	if err := rewrite.SafeWriteFile(path, rewritten, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		return fmt.Errorf("restore mtime %s: %w", path, err)
	}
	return nil
}
