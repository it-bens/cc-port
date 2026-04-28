package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// resolvePassphrase reads the passphrase from envName or fromFile.
// Empty inputs return the empty string with no error. Cobra's
// MarkFlagsMutuallyExclusive enforces the both-set case at parse time;
// this helper still defends against future re-wiring or tests that
// bypass cobra. Trailing CR/LF on file input is trimmed; an empty
// passphrase after the trim is refused.
func resolvePassphrase(envName, fromFile string) (string, error) {
	if envName != "" && fromFile != "" {
		return "", errors.New(
			"--passphrase-env and --passphrase-file are mutually exclusive",
		)
	}
	if envName != "" {
		value := os.Getenv(envName)
		if value == "" {
			return "", fmt.Errorf("$%s unset or empty", envName)
		}
		return value, nil
	}
	if fromFile != "" {
		raw, err := os.ReadFile(fromFile) //nolint:gosec // G304: caller-supplied path
		if err != nil {
			return "", fmt.Errorf("read passphrase file %s: %w", fromFile, err)
		}
		value := strings.TrimRight(string(raw), "\r\n")
		if value == "" {
			return "", fmt.Errorf("passphrase file %s is empty", fromFile)
		}
		return value, nil
	}
	return "", nil
}
