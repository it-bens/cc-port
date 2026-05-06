package credentials

import (
	"context"
	"fmt"
	"os"

	"github.com/charmbracelet/x/term"
)

// osTTYPrompter reads from /dev/tty using github.com/charmbracelet/x/term so
// piped stdin is unaffected by the prompt. The cancellation seam closes the
// tty handle when ctx fires, which causes the blocked ReadPassword to return;
// the helper translates the resulting error into context.Canceled wrapped via
// fmt.Errorf("canceled: %w", ctx.Err()).
type osTTYPrompter struct{}

// Prompt opens /dev/tty, races ctx.Done against term.ReadPassword by closing
// the tty handle on cancellation, and returns the secret with echo suppressed.
func (osTTYPrompter) Prompt(ctx context.Context, label string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrPromptUnavailable, err.Error())
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
