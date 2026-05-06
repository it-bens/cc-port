package credentials

import (
	"context"
	"fmt"
	"os"

	"github.com/charmbracelet/x/term"
)

// osTTYPrompter is the production ttyPrompter backed by /dev/tty.
type osTTYPrompter struct{}

// Prompt reads a single secret from /dev/tty with echo suppressed.
func (osTTYPrompter) Prompt(ctx context.Context, label string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrPromptUnavailable, err)
	}
	defer func() { _ = tty.Close() }()

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
			_ = tty.Close()
		case <-done:
		}
	}()

	secret, readErr := term.ReadPassword(tty.Fd())
	close(done)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", fmt.Errorf("canceled: %w", ctxErr)
	}
	if readErr != nil {
		return "", fmt.Errorf("read prompt: %w", readErr)
	}
	return string(secret), nil
}
