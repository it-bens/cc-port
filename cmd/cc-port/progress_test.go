package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/progress"
)

func TestRunWithProgress_EmitsDoneOnSuccess(t *testing.T) {
	cmd := newProgressTestCmd(t)
	require.NoError(t, cmd.Flags().Set("json", "true"))
	sinkPath := captureJSONSink(t)

	err := runWithProgress(cmd, func(_ context.Context, _ progress.Reporter) error {
		return nil
	})
	require.NoError(t, err)

	rendered := readSink(t, sinkPath)
	assert.Contains(t, rendered, `"event":"done"`)
	assert.NotContains(t, rendered, `"event":"failed"`)
}

func TestRunWithProgress_EmitsFailedOnWorkError(t *testing.T) {
	cmd := newProgressTestCmd(t)
	require.NoError(t, cmd.Flags().Set("json", "true"))
	sinkPath := captureJSONSink(t)

	workErr := errors.New("work blew up")
	err := runWithProgress(cmd, func(_ context.Context, _ progress.Reporter) error {
		return workErr
	})
	require.ErrorIs(t, err, workErr)

	rendered := readSink(t, sinkPath)
	assert.Contains(t, rendered, `"event":"failed"`)
}

func TestRunWithProgress_RoutesCanceledWorkErrorToCancelled(t *testing.T) {
	cmd := newProgressTestCmd(t)
	require.NoError(t, cmd.Flags().Set("json", "true"))
	sinkPath := captureJSONSink(t)

	err := runWithProgress(cmd, func(_ context.Context, _ progress.Reporter) error {
		return context.Canceled
	})
	require.ErrorIs(t, err, context.Canceled)

	rendered := readSink(t, sinkPath)
	assert.Contains(t, rendered, `"event":"cancelled"`)
	assert.NotContains(t, rendered, `"event":"failed"`)
}

func TestRunWithProgress_JSONModeEmitsEventObjectsThroughSink(t *testing.T) {
	cmd := newProgressTestCmd(t)
	require.NoError(t, cmd.Flags().Set("json", "true"))
	sinkPath := captureJSONSink(t)

	err := runWithProgress(cmd, func(_ context.Context, reporter progress.Reporter) error {
		phase := reporter.Phase("copy", 1, progress.UnitFiles)
		phase.End("done")
		return nil
	})
	require.NoError(t, err)

	rendered := readSink(t, sinkPath)
	assert.Contains(t, rendered, `{"event":`)
	assert.Contains(t, rendered, `"event":"phase_start"`)
	assert.Contains(t, rendered, `"event":"phase_end"`)
}

type fakeInterruptibleRenderer struct {
	interrupt chan struct{}
}

func (fakeInterruptibleRenderer) Consume(progress.Event) {}
func (fakeInterruptibleRenderer) Finalize() error        { return nil }
func (renderer fakeInterruptibleRenderer) Interrupted() <-chan struct{} {
	return renderer.interrupt
}

type fakePlainRenderer struct{}

func (fakePlainRenderer) Consume(progress.Event) {}
func (fakePlainRenderer) Finalize() error        { return nil }

func TestWireInterrupt_CancelsContextOnInterrupt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	renderer := fakeInterruptibleRenderer{interrupt: make(chan struct{})}

	wireInterrupt(ctx, cancel, renderer)
	close(renderer.interrupt)

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context was not cancelled after interrupt")
	}
}

func TestWireInterrupt_SkipsNonInterruptibleRenderer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wireInterrupt(ctx, cancel, fakePlainRenderer{})

	select {
	case <-ctx.Done():
		t.Fatal("context cancelled for a non-interruptible renderer")
	case <-time.After(50 * time.Millisecond):
	}
}

func readSink(t *testing.T, sinkPath string) string {
	t.Helper()
	data, err := os.ReadFile(sinkPath) //nolint:gosec // G304: path under t.TempDir()
	require.NoError(t, err)
	return string(data)
}

// newProgressTestCmd builds a command carrying the four verbosity flags
// runWithProgress reads, with a non-nil context so context.WithCancel has a
// real parent. Production seeds the context via cobra's ExecuteContext; tests
// that call runWithProgress directly must seed it here.
func newProgressTestCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().BoolP("quiet", "q", false, "")
	cmd.Flags().Bool("verbose", false, "")
	cmd.Flags().Bool("debug", false, "")
	cmd.Flags().Bool("json", false, "")
	cmd.SetContext(context.Background())
	return cmd
}

// captureJSONSink redirects stderrSink to a temp file for the duration of the
// test and returns the file's path so the caller can read the rendered JSON
// back after runWithProgress returns.
func captureJSONSink(t *testing.T) string {
	t.Helper()
	sinkPath := filepath.Join(t.TempDir(), "progress.jsonl")
	file, err := os.Create(sinkPath) //nolint:gosec // G304: path under t.TempDir()
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })

	original := stderrSink
	stderrSink = file
	t.Cleanup(func() { stderrSink = original })
	return sinkPath
}
