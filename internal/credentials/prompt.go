package credentials

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/x/term"
)

// osTTYPrompter is the production ttyPrompter backed by /dev/tty.
type osTTYPrompter struct{}

// Prompt reads a single secret from /dev/tty with echo suppressed.
func (osTTYPrompter) Prompt(ctx context.Context, label string) (secret string, err error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrPromptUnavailable, err)
	}
	defer func() {
		// Skip the join when the cancel goroutine already closed the
		// handle as its cancellation mechanism; that "already closed"
		// return is noise on top of the ctx.Err() the caller will see.
		if closeErr := tty.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			err = errors.Join(err, fmt.Errorf("close prompt: %w", closeErr))
		}
	}()

	if _, writeErr := fmt.Fprintf(tty, "%s: ", label); writeErr != nil {
		return "", fmt.Errorf("write prompt: %w", writeErr)
	}

	// Trailing newline so the next CLI line does not run into the masked
	// prompt. Cosmetic; failure is best-effort and must not invalidate a
	// secret that has already been read.
	defer func() { _, _ = fmt.Fprintln(tty) }()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// Closing the FD is the cancellation mechanism: it
			// unblocks term.ReadPassword. Any close error here is
			// captured by the deferred close above, which calls
			// Close a second time on the now-closed handle.
			_ = tty.Close()
		case <-done:
		}
	}()

	password, readErr := term.ReadPassword(tty.Fd())
	close(done)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", fmt.Errorf("canceled: %w", ctxErr)
	}
	if readErr != nil {
		return "", fmt.Errorf("read prompt: %w", readErr)
	}
	return string(password), nil
}
