package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/fatih/color"

	"github.com/iawia002/lux/app"
	"github.com/iawia002/lux/logging"
)

func main() {
	// Initialize the structured logger early so that logs emitted before the
	// app action runs (e.g. signal notifications) are also structured.
	logging.Init(false)

	// Graceful shutdown: the first SIGINT/SIGTERM cancels ctx, which makes the
	// app stop accepting new download tasks; tasks already in progress run to
	// completion before the process exits.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A second signal forces an immediate exit.
	go func() {
		<-ctx.Done()
		slog.Warn("shutdown signal received, waiting for ongoing downloads to finish")
		fmt.Fprintln(
			color.Output,
			color.YellowString("\nShutdown signal received, waiting for ongoing downloads to finish... (press Ctrl+C again to force quit)"),
		)
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		slog.Error("forced shutdown by second signal")
		os.Exit(2)
	}()

	if err := app.New().RunContext(ctx, os.Args); err != nil {
		slog.Error("run failed", "error", err)
		fmt.Fprintf(
			color.Output,
			"Run %s failed: %s\n",
			color.CyanString("%s", app.Name), color.RedString("%v", err),
		)
		os.Exit(1)
	}
}
