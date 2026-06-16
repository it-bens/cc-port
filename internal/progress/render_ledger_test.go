package progress

import (
	"bytes"
	"errors"
	"testing"
	"time"

	teatest "github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainLedger runs model through teatest, feeding events over channel, and
// returns the final accumulated output. The caller closes channel to end the
// run (or sends a terminal event, which quits on its own).
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

func TestLedgerRendersInterruptedOnCancel(t *testing.T) {
	events := make(chan Event, ledgerChannelDepth)
	output := runLedger(t, events, func() {
		events <- PhaseStart{Path: []string{"copy"}, Total: 10, Unit: UnitFiles, At: time.Now()}
		events <- PhaseAdvance{Path: []string{"copy"}, Done: 4}
		events <- Cancelled{Reason: "user interrupt"}
	})

	assert.Contains(t, string(output), "interrupted at copy")
	assert.Contains(t, string(output), "(4/10 completed)")
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

func TestLedgerFinalizeReturnsRunError(t *testing.T) {
	var output bytes.Buffer
	renderer := NewLedgerRenderer(&output)
	renderer.Consume(PhaseStart{Path: []string{"copy"}, Total: 1, Unit: UnitFiles, At: time.Now()})
	renderer.Consume(Done{})

	assert.NoError(t, renderer.Finalize())
}
