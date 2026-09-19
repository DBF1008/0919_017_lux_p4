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
)

func main() {
	// Graceful shutdown: on SIGINT/SIGTERM the context is cancelled, the app
	// stops accepting new download tasks and waits for in-flight downloads
	// to finish before exiting.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		slog.Warn("shutdown signal received, waiting for in-flight downloads to finish")
		fmt.Fprintf(
			color.Output,
			"\n%s\n",
			color.YellowString("Shutdown signal received, waiting for in-flight downloads to finish..."),
		)
	}()

	if err := app.New().RunContext(ctx, os.Args); err != nil {
		fmt.Fprintf(
			color.Output,
			"Run %s failed: %s\n",
			color.CyanString("%s", app.Name), color.RedString("%v", err),
		)
		slog.Error("run failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
