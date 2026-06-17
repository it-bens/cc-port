package move

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/rewrite"
)

// rewriteNewProjectDir rewrites the copied project dir: transcripts and memory
// files. oldEncodedDir and newEncodedDir are the absolute encoded storage dirs;
// every rewritten body also has old→new encoded-dir references swapped.
func rewriteNewProjectDir(
	ctx context.Context, oldEncodedDir, newEncodedDir string, moveOptions Options, phase progress.PhaseHandle,
) error {
	if moveOptions.RewriteTranscripts {
		if err := rewriteTranscriptsInDir(ctx, oldEncodedDir, newEncodedDir, moveOptions, phase); err != nil {
			return err
		}
	}

	if err := rewriteMemoryFilesInDir(ctx, oldEncodedDir, newEncodedDir, moveOptions, phase); err != nil {
		return err
	}

	return nil
}

func rewriteTranscriptsInDir(
	ctx context.Context, oldEncodedDir, newEncodedDir string, moveOptions Options, phase progress.PhaseHandle,
) error {
	newTranscripts, err := listTranscriptFiles(ctx, newEncodedDir)
	if err != nil {
		return fmt.Errorf("collect transcripts in new dir: %w", err)
	}
	transcriptsPhase := phase.SubPhase("transcripts", int64(len(newTranscripts)), progress.UnitFiles)
	for _, transcriptPath := range newTranscripts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := rewritePathsPreservingMtime(transcriptPath, oldEncodedDir, newEncodedDir, moveOptions); err != nil {
			return fmt.Errorf("rewrite transcript %s: %w", transcriptPath, err)
		}
		transcriptsPhase.Advance(1)
	}
	transcriptsPhase.End(fmt.Sprintf("%d files", len(newTranscripts)))
	return nil
}

func rewriteMemoryFilesInDir(
	ctx context.Context, oldEncodedDir, newEncodedDir string, moveOptions Options, phase progress.PhaseHandle,
) error {
	newMemoryDir := filepath.Join(newEncodedDir, "memory")
	if _, err := os.Stat(newMemoryDir); err != nil {
		return nil
	}

	memoryEntries, err := os.ReadDir(newMemoryDir)
	if err != nil {
		return fmt.Errorf("read new memory directory: %w", err)
	}

	memoryFiles := make([]string, 0, len(memoryEntries))
	for _, entry := range memoryEntries {
		if entry.IsDir() {
			continue
		}
		memoryFiles = append(memoryFiles, filepath.Join(newMemoryDir, entry.Name()))
	}

	memoryPhase := phase.SubPhase("memory", int64(len(memoryFiles)), progress.UnitFiles)
	for _, memoryFilePath := range memoryFiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := rewritePathsPreservingMtime(memoryFilePath, oldEncodedDir, newEncodedDir, moveOptions); err != nil {
			return fmt.Errorf("rewrite memory file %s: %w", memoryFilePath, err)
		}
		memoryPhase.Advance(1)
	}
	memoryPhase.End(fmt.Sprintf("%d files", len(memoryFiles)))
	return nil
}

// rewritePathsPreservingMtime replaces the old project path with the new one,
// and the old encoded storage dir with the new one, inside the file at path,
// then restores its modification time. The mtime read here is the value
// CopyDir carried over from the source, so correctness depends on the copy
// running before this rewrite.
func rewritePathsPreservingMtime(path, oldEncodedDir, newEncodedDir string, moveOptions Options) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // path constructed from trusted internal data
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	rewritten, _ := rewrite.ReplacePathInBytes(data, moveOptions.OldPath, moveOptions.NewPath)
	rewritten, _ = rewrite.ReplacePathInBytes(rewritten, oldEncodedDir, newEncodedDir)
	if err := rewrite.SafeWriteFile(path, rewritten, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		return fmt.Errorf("restore mtime %s: %w", path, err)
	}
	return nil
}
