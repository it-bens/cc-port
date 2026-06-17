package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/progress"
)

// stderrSink is the sink the progress renderer writes to. It is a package-level
// seam so a test can swap it for a temp file under t.Cleanup and read the
// rendered output back.
var stderrSink = os.Stderr

// runWithProgress is the sole site that installs the SIGINT handler.
// The returned error joins work's error with renderer.Finalize so neither is lost.
func runWithProgress(cmd *cobra.Command, work func(ctx context.Context, reporter progress.Reporter) error) error {
	selection, err := selectionFromFlags(cmd)
	if err != nil {
		return err
	}

	renderer, level := progress.Pick(selection)
	reporter := progress.NewReporter(renderer, level)

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()

	workErr := work(ctx, reporter)
	switch {
	case errors.Is(workErr, context.Canceled):
		reporter.Cancelled(workErr.Error())
	case workErr != nil:
		reporter.Fail(workErr)
	default:
		reporter.Done()
	}

	return errors.Join(workErr, renderer.Finalize())
}

func selectionFromFlags(cmd *cobra.Command) (progress.Selection, error) {
	quiet, err := cmd.Flags().GetBool("quiet")
	if err != nil {
		return progress.Selection{}, fmt.Errorf("read quiet flag: %w", err)
	}
	verbose, err := cmd.Flags().GetBool("verbose")
	if err != nil {
		return progress.Selection{}, fmt.Errorf("read verbose flag: %w", err)
	}
	debug, err := cmd.Flags().GetBool("debug")
	if err != nil {
		return progress.Selection{}, fmt.Errorf("read debug flag: %w", err)
	}
	emitJSON, err := cmd.Flags().GetBool("json")
	if err != nil {
		return progress.Selection{}, fmt.Errorf("read json flag: %w", err)
	}

	return progress.Selection{
		JSON:    emitJSON,
		Quiet:   quiet,
		Verbose: verbose,
		Debug:   debug,
		Output:  stderrSink,
	}, nil
}
