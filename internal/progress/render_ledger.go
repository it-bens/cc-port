package progress

import (
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

// ledgerChannelDepth is the buffer of the Consume->program channel. Verbose
// Detail events are dropped when this fills; all other events block.
const ledgerChannelDepth = 64

// spinnerInterval drives the ledger's spinner frames at ~10 Hz.
const spinnerInterval = 100 * time.Millisecond

// LedgerRenderer is the interactive-TTY sink. Consume applies the drop policy
// and feeds events into a bubbletea program; all rendering state lives in the
// model. It is selected when the sink is a TTY and neither --json nor --quiet
// is set.
type LedgerRenderer struct {
	events  chan Event
	program *tea.Program
	done    chan struct{}
	runErr  error
}

// NewLedgerRenderer builds a LedgerRenderer writing to output and starts its
// bubbletea program in a goroutine. SIGINT is left to cobra via
// WithoutSignalHandler; the Cancelled event drives the interrupted render.
func NewLedgerRenderer(output io.Writer) *LedgerRenderer {
	events := make(chan Event, ledgerChannelDepth)
	model := newLedgerModel(events)
	program := tea.NewProgram(
		model,
		tea.WithOutput(output),
		tea.WithInput(nil),
		tea.WithoutSignalHandler(),
	)
	renderer := &LedgerRenderer{
		events:  events,
		program: program,
		done:    make(chan struct{}),
	}
	go func() {
		defer close(renderer.done)
		_, err := program.Run()
		renderer.runErr = err
	}()
	return renderer
}

// Consume applies the drop policy: verbose/debug Detail events are dropped on
// backpressure so the work goroutine never blocks on cosmetic output; every
// other event blocks until the model reads it, because they are load-bearing
// for ledger correctness. The blocking send also unblocks if the program
// goroutine has already exited (early Run return or a panic in Update/View),
// so a dead reader never freezes the work goroutine on a full buffer.
func (renderer *LedgerRenderer) Consume(event Event) {
	if detail, ok := event.(Detail); ok && detail.Level >= LevelVerbose {
		select {
		case renderer.events <- event:
		default:
		}
		return
	}
	select {
	case renderer.events <- event:
	case <-renderer.done:
	}
}

// Finalize closes the event channel, waits for the program goroutine to exit,
// and returns the program's run error.
func (renderer *LedgerRenderer) Finalize() error {
	close(renderer.events)
	<-renderer.done
	return renderer.runErr
}

// ledgerModel is the bubbletea model: a tree of phase nodes plus the terminal
// outcome. It owns the event channel; a poll command drains it into Update.
type ledgerModel struct {
	events   <-chan Event
	roots    []*phaseNode
	index    map[string]*phaseNode
	spinner  spinner.Model
	warnings int
	outcome  terminalOutcome
}

// terminalOutcome records how the run ended so View can paint the final frame.
type terminalOutcome struct {
	kind   outcomeKind
	phase  string
	done   int64
	total  int64
	reason string
	err    error
}

type outcomeKind int

const (
	outcomeRunning outcomeKind = iota
	outcomeDone
	outcomeFailed
	outcomeCancelled
)

// phaseNode is one node in the rendered tree. terminalSymbol is empty for a
// normally-ended phase (rendered with ✓) and holds the ✗ or ⊘ glyph when the
// phase was the active one at a Failed or Cancelled event.
type phaseNode struct {
	path           []string
	total          int64
	done           int64
	unit           Unit
	children       []*phaseNode
	ended          bool
	summary        string
	duration       time.Duration
	terminalSymbol string
	bar            progress.Model
}

// channelClosedMsg is delivered by the poll command when the event channel
// closes, so the model can quit cleanly even without a terminal event.
type channelClosedMsg struct{}

// spinnerTickMsg drives a single spinner frame.
type spinnerTickMsg struct{}

func newLedgerModel(events <-chan Event) *ledgerModel {
	return &ledgerModel{
		events:  events,
		index:   make(map[string]*phaseNode),
		spinner: spinner.New(spinner.WithSpinner(spinner.Dot)),
	}
}

func (model *ledgerModel) Init() tea.Cmd {
	return tea.Batch(model.pollCmd(), model.spinnerTickCmd())
}

// pollCmd blocks on the event channel and delivers the next event as a Msg, or
// channelClosedMsg when the channel closes.
func (model *ledgerModel) pollCmd() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-model.events
		if !ok {
			return channelClosedMsg{}
		}
		return event
	}
}

// spinnerTickCmd re-arms the ~10 Hz spinner tick.
func (model *ledgerModel) spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

func (model *ledgerModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := message.(type) {
	case channelClosedMsg:
		return model, tea.Quit
	case spinnerTickMsg:
		// Advance one frame via the spinner's own tick, but discard the command
		// it returns: that command schedules a spinner.TickMsg this model never
		// routes. Frames are driven solely by the self-perpetuating spinnerTickCmd.
		nextTick := model.spinnerTickCmd()
		model.spinner, _ = model.spinner.Update(model.spinner.Tick())
		return model, nextTick
	case Event:
		return model.applyEvent(typed)
	default:
		return model, nil
	}
}

// applyEvent mutates the tree for one progress event. Non-terminal events
// re-arm the poll command; terminal events quit after the final render.
func (model *ledgerModel) applyEvent(event Event) (tea.Model, tea.Cmd) {
	poll := model.pollCmd()
	switch typed := event.(type) {
	case PhaseStart:
		model.startPhase(typed)
		return model, poll
	case PhaseAdvance:
		model.advancePhase(typed)
		return model, poll
	case PhaseEnd:
		model.endPhase(typed)
		return model, poll
	case Detail:
		return model, tea.Batch(tea.Println(typed.Text), poll)
	case Warning:
		model.warnings++
		return model, tea.Batch(tea.Println(fmt.Sprintf("[WARN] %s", typed.Err)), poll)
	case Failed:
		open := model.openPhase()
		model.markActive(open, outcomeFailed)
		model.outcome = terminalOutcome{kind: outcomeFailed, phase: openName(open), err: typed.Err}
		return model, tea.Quit
	case Cancelled:
		open := model.openPhase()
		model.markActive(open, outcomeCancelled)
		done, total := openProgress(open)
		model.outcome = terminalOutcome{
			kind: outcomeCancelled, phase: openName(open),
			done: done, total: total, reason: typed.Reason,
		}
		return model, tea.Quit
	case Done:
		model.outcome = terminalOutcome{kind: outcomeDone}
		return model, tea.Quit
	default:
		return model, poll
	}
}

func (model *ledgerModel) startPhase(event PhaseStart) {
	node := &phaseNode{
		path:  event.Path,
		total: event.Total,
		unit:  event.Unit,
	}
	if event.Total > 0 {
		node.bar = progress.New()
	}
	model.index[joinedPath(event.Path)] = node
	if parent := model.parentOf(event.Path); parent != nil {
		parent.children = append(parent.children, node)
		return
	}
	model.roots = append(model.roots, node)
}

func (model *ledgerModel) advancePhase(event PhaseAdvance) {
	if node, ok := model.index[joinedPath(event.Path)]; ok {
		node.done = event.Done
	}
}

func (model *ledgerModel) endPhase(event PhaseEnd) {
	if node, ok := model.index[joinedPath(event.Path)]; ok {
		node.ended = true
		node.summary = event.Summary
		node.duration = event.Dur
	}
}

// parentOf returns the node owning the path one segment shorter, or nil when
// the path is a root.
func (model *ledgerModel) parentOf(path []string) *phaseNode {
	if len(path) <= 1 {
		return nil
	}
	return model.index[joinedPath(path[:len(path)-1])]
}

// openPhase returns the deepest not-yet-ended node, preferring later starts,
// or nil when every phase has ended.
func (model *ledgerModel) openPhase() *phaseNode {
	var deepest *phaseNode
	for _, node := range model.roots {
		if candidate, ok := deepestOpen(node); ok {
			deepest = candidate
		}
	}
	return deepest
}

// deepestOpen walks a subtree and returns its deepest open descendant.
func deepestOpen(node *phaseNode) (*phaseNode, bool) {
	for index := len(node.children) - 1; index >= 0; index-- {
		if candidate, ok := deepestOpen(node.children[index]); ok {
			return candidate, true
		}
	}
	if !node.ended {
		return node, true
	}
	return nil, false
}

// markActive flags the open node's terminal symbol kind for View.
func (model *ledgerModel) markActive(node *phaseNode, kind outcomeKind) {
	if node == nil {
		return
	}
	node.ended = true
	switch kind {
	case outcomeFailed:
		node.terminalSymbol = "✗"
	case outcomeCancelled:
		node.terminalSymbol = "⊘"
	case outcomeRunning, outcomeDone:
	}
}

func openName(node *phaseNode) string {
	if node == nil {
		return noPhase
	}
	return joinedPath(node.path)
}

func openProgress(node *phaseNode) (done, total int64) {
	if node == nil {
		return 0, 0
	}
	return node.done, node.total
}

func (model *ledgerModel) View() tea.View {
	var builder strings.Builder
	for _, root := range model.roots {
		model.writeNode(&builder, root, 0)
	}
	model.writeOutcome(&builder)
	return tea.NewView(builder.String())
}

// writeNode renders one node and its children at the given indent depth.
func (model *ledgerModel) writeNode(builder *strings.Builder, node *phaseNode, depth int) {
	indent := strings.Repeat("  ", depth)
	name := node.path[len(node.path)-1]
	switch {
	case node.terminalSymbol != "":
		fmt.Fprintf(builder, "%s%s %s\n", indent, node.terminalSymbol, name)
	case node.ended:
		fmt.Fprintf(builder, "%s✓ %s  %s  %s\n", indent, name, node.summary, node.duration)
	default:
		fmt.Fprintf(builder, "%s%s %s  %s\n", indent, model.spinner.View(), name, model.counter(node))
	}
	for _, child := range node.children {
		model.writeNode(builder, child, depth+1)
	}
}

// counter renders the in-progress leaf's bar (when total is known) and a
// unit-formatted count.
func (model *ledgerModel) counter(node *phaseNode) string {
	count := formatCount(node.done, node.total, node.unit)
	if node.total <= 0 {
		return count
	}
	fraction := float64(node.done) / float64(node.total)
	return node.bar.ViewAs(fraction) + "  " + count
}

// writeOutcome appends the terminal frame for a finished run.
func (model *ledgerModel) writeOutcome(builder *strings.Builder) {
	switch model.outcome.kind {
	case outcomeDone:
		fmt.Fprintf(builder, "done%s\n", warningSuffix(model.warnings))
	case outcomeFailed:
		fmt.Fprintf(builder, "failed at %s: %s%s\n",
			model.outcome.phase, model.outcome.err, warningSuffix(model.warnings))
	case outcomeCancelled:
		fmt.Fprintf(builder, "interrupted at %s (%d/%d completed)%s\n",
			model.outcome.phase, model.outcome.done, model.outcome.total, warningSuffix(model.warnings))
	case outcomeRunning:
	}
}

// formatCount renders a unit-aware "done/total" or bare "done" count.
func formatCount(done, total int64, unit Unit) string {
	if total <= 0 {
		return fmt.Sprintf("%d %s", done, unitName(unit))
	}
	return fmt.Sprintf("%d/%d %s", done, total, unitName(unit))
}
