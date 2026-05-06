package credentials

import "context"

// ttyPrompter abstracts an interactive secret prompt so tests can
// inject a fake without opening /dev/tty. The interface is unexported
// because exporting it would widen the public API to support tests
// only; tests live in package credentials and construct fakes
// directly.
type ttyPrompter interface {
	// Prompt reads a single secret value from the TTY using label as
	// the prompt text. Echo is suppressed. Implementations honor
	// ctx cancellation by aborting within one read cycle and
	// returning a context-canceled error wrapped via fmt.Errorf.
	Prompt(ctx context.Context, label string) (string, error)
}
