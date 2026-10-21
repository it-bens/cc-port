package progress

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runLedger runs a ledger model over events through teatest, calls feed to send
// events, and returns the final accumulated output. feed ends the run by closing
// events or by sending a terminal event.
func runLedger(t *testing.T, events chan Event, feed func()) []byte {
	t.Helper()
	model := newLedgerModel(events)
	test := teatest.NewTestModel(t, model, teatest.WithInitialTermSize(80, 24))

	feed()

	test.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	final := test.FinalModel(t)
	view := final.View()
	return []byte(view.Content)
}

func TestLedgerRendersCompletedPhaseAndDone(t *testing.T) {
	events := make(chan Event, ledgerChannelDepth)
	output := runLedger(t, events, func() {
		events <- PhaseStart{Path: []string{"copy"}, Total: 2, Unit: UnitFiles, At: time.Now()}
		events <- PhaseAdvance{Path: []string{"copy"}, Done: 2}
		events <- PhaseEnd{Path: []string{"copy"}, Summary: "2 files", Dur: time.Second}
		events <- Done{}
	})

	assert.Contains(t, string(output), "✓ copy")
	assert.Contains(t, string(output), "2 files")
	assert.Contains(t, string(output), "done")
}

func TestLedgerShowsLiveElapsedOnActivePhase(t *testing.T) {
	// Pin now to a constant 3s after the phase start so the live elapsed is
	// stable across the many View() calls the 10 Hz spinner triggers. A
	// per-call-advancing clock would jitter the rendered value.
	base := time.Date(2026, time.June, 16, 9, 0, 0, 0, time.UTC)
	original := now
	now = func() time.Time { return base.Add(3 * time.Second) }
	t.Cleanup(func() { now = original })

	events := make(chan Event, ledgerChannelDepth)
	output := runLedger(t, events, func() {
		events <- PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles, At: base}
		events <- PhaseAdvance{Path: []string{"copy"}, Done: 4}
		// No terminal event: closing the channel captures the still-active frame.
		close(events)
	})

	assert.Contains(t, string(output), "copy")
	assert.Contains(t, string(output), "4/10 files")
	assert.Contains(t, string(output), "3s")
}

func TestLedgerRendersInterruptedOnCancel(t *testing.T) {
	events := make(chan Event, ledgerChannelDepth)
	output := runLedger(t, events, func() {
		events <- PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles, At: time.Now()}
		events <- PhaseAdvance{Path: []string{"copy"}, Done: 4}
		events <- Cancelled{Reason: "user interrupt"}
	})

	assert.Contains(t, string(output), "interrupted at copy")
	assert.Contains(t, string(output), "(4/10 files completed)")
}

// TestLedgerCancelOmitsFractionForZeroTotalPhase pins the defect where a run
// cancelled inside a phase that opened with Total zero rendered a nonsense
// fraction. The live counter prints a bare count whenever the total is unknown,
// and the terminal line must agree rather than format its own done/total pair.
func TestLedgerCancelOmitsFractionForZeroTotalPhase(t *testing.T) {
	events := make(chan Event, ledgerChannelDepth)
	output := runLedger(t, events, func() {
		events <- PhaseStart{Path: []string{"archive.claude"}, Total: 0, Unit: UnitFiles, At: time.Now()}
		events <- PhaseAdvance{Path: []string{"archive.claude"}, Done: 5}
		events <- Cancelled{Reason: "user interrupt"}
	})

	assert.Contains(t, string(output), "interrupted at archive.claude")
	assert.NotContains(t, string(output), "/0 completed")
	assert.Contains(t, string(output), "5 files")
}

func TestLedgerRendersFailed(t *testing.T) {
	events := make(chan Event, ledgerChannelDepth)
	output := runLedger(t, events, func() {
		events <- PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles, At: time.Now()}
		events <- PhaseAdvance{Path: []string{"copy"}, Done: 4}
		events <- Failed{Err: errors.New("disk full")}
	})

	assert.Contains(t, string(output), "failed at copy")
	assert.Contains(t, string(output), "disk full")
}

func TestLedgerFailAppendsWarningSuffix(t *testing.T) {
	events := make(chan Event, ledgerChannelDepth)
	output := runLedger(t, events, func() {
		events <- PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles, At: time.Now()}
		events <- Warning{Err: errors.New("skipped one"), At: time.Now()}
		events <- Failed{Err: errors.New("disk full")}
	})

	assert.Contains(t, string(output), "failed at copy")
	assert.Contains(t, string(output), "(1 warning)")
}

func TestLedgerCancelAppendsWarningSuffix(t *testing.T) {
	events := make(chan Event, ledgerChannelDepth)
	output := runLedger(t, events, func() {
		events <- PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles, At: time.Now()}
		events <- Warning{Err: errors.New("skipped one"), At: time.Now()}
		events <- Warning{Err: errors.New("skipped two"), At: time.Now()}
		events <- Cancelled{Reason: "user interrupt"}
	})

	assert.Contains(t, string(output), "interrupted at copy")
	assert.Contains(t, string(output), "(2 warnings)")
}

// TestLedgerDropsVerboseDetailUnderBackpressure exercises the drop policy
// directly: with the channel full and no program draining it, verbose Detail
// Consumes return promptly while a load-bearing event blocks until a reader
// frees a slot. This asserts the contract that cosmetic output never blocks the
// work goroutine.
func TestLedgerDropsVerboseDetailUnderBackpressure(t *testing.T) {
	// Depth 1, pre-filled, so every further send hits backpressure.
	renderer := &LedgerRenderer{events: make(chan Event, 1)}
	renderer.events <- PhaseStart{Path: []string{"copy"}}

	// Verbose Details must drop, not block: this returns even though the
	// channel is full.
	verboseReturned := make(chan struct{})
	go func() {
		for range 1000 {
			renderer.Consume(Detail{Level: LevelVerbose, Text: "noise"})
			renderer.Consume(Detail{Level: LevelDebug, Text: "trace"})
		}
		close(verboseReturned)
	}()
	select {
	case <-verboseReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("verbose Detail Consume blocked under backpressure; drop policy violated")
	}

	// A load-bearing event blocks until a slot frees: it must NOT be in the
	// channel yet (channel still full from the PhaseStart).
	phaseDelivered := make(chan struct{})
	go func() {
		renderer.Consume(PhaseAdvance{Path: []string{"copy"}, Done: 1})
		close(phaseDelivered)
	}()
	select {
	case <-phaseDelivered:
		t.Fatal("load-bearing event delivered while channel full; it must block")
	case <-time.After(100 * time.Millisecond):
		// Expected: still blocked.
	}

	// Drain the pre-filled PhaseStart; the blocked PhaseAdvance now proceeds.
	<-renderer.events
	select {
	case <-phaseDelivered:
		// Expected: the blocking send completed once a slot opened.
	case <-time.After(2 * time.Second):
		t.Fatal("load-bearing event never delivered after channel drained")
	}

	delivered := <-renderer.events
	advance, ok := delivered.(PhaseAdvance)
	require.True(t, ok, "expected PhaseAdvance, got %T", delivered)
	assert.Equal(t, int64(1), advance.Done)
}

func TestLedgerFinalizeSucceedsAfterCleanRun(t *testing.T) {
	var output bytes.Buffer
	renderer := NewLedgerRenderer(&output)
	renderer.Consume(PhaseStart{Path: []string{"copy"}, Total: 1, Unit: UnitFiles, At: time.Now()})
	renderer.Consume(Done{})

	assert.NoError(t, renderer.Finalize())
}

// frameWatcher captures everything the renderer writes and closes painted the
// first time the captured bytes contain marker.
type frameWatcher struct {
	mutex   sync.Mutex
	buffer  bytes.Buffer
	marker  string
	painted chan struct{}
	closed  bool
}

func newFrameWatcher(marker string) *frameWatcher {
	return &frameWatcher{marker: marker, painted: make(chan struct{})}
}

func (watcher *frameWatcher) Write(data []byte) (int, error) {
	watcher.mutex.Lock()
	defer watcher.mutex.Unlock()
	written, err := watcher.buffer.Write(data)
	if !watcher.closed && strings.Contains(watcher.buffer.String(), watcher.marker) {
		watcher.closed = true
		close(watcher.painted)
	}
	return written, err
}

func (watcher *frameWatcher) captured() string {
	watcher.mutex.Lock()
	defer watcher.mutex.Unlock()
	return watcher.buffer.String()
}

// TestLedgerRendererSendsNoQueries pins the property NewLedgerRenderer relies
// on: with input disabled, bubbletea writes neither the synchronized-output
// mode probe nor the Kitty keyboard query, so no reply can land on the shell
// prompt after exit. TERM names kitty so the mode probe is armed and only
// disabled input holds it back. The keyboard query is written only on a live
// flush, never the closing one, so the test waits until a frame carrying the
// phase label has been painted before it finishes the run.
func TestLedgerRendererSendsNoQueries(t *testing.T) {
	t.Setenv("TERM", "xterm-kitty")

	output := newFrameWatcher("copy")
	renderer := newLedgerRenderer(output, tea.WithWindowSize(80, 24))
	renderer.Consume(PhaseStart{Path: []string{"copy"}, Total: 2, Unit: UnitFiles, At: time.Now()})
	renderer.Consume(PhaseAdvance{Path: []string{"copy"}, Done: 2})
	renderer.Consume(PhaseEnd{Path: []string{"copy"}, Summary: "2 files", Dur: time.Second})

	select {
	case <-output.painted:
	case <-renderer.done:
		require.FailNow(t, "ledger program exited before painting a frame",
			"run error: %v; captured output: %q", renderer.runErr, output.captured())
	case <-time.After(5 * time.Second):
		renderer.Consume(Done{})
		_ = renderer.Finalize()
		require.FailNow(t, "no ledger frame was painted", "captured output: %q", output.captured())
	}

	renderer.Consume(Done{})
	require.NoError(t, renderer.Finalize())

	assert.NotContains(t, output.captured(), ansi.RequestModeSynchronizedOutput)
	assert.NotContains(t, output.captured(), ansi.RequestKittyKeyboard)
}
