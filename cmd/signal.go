package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// signalContext returns a context cancelled by the first SIGINT or SIGTERM,
// so the run can stop cleanly. A second signal exits at once with code 130,
// even if the run does not stop. stop releases the signal handler.
func signalContext() (ctx context.Context, stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-signals:
		case <-ctx.Done():
			return
		}
		cancel()
		<-signals
		os.Exit(ExitInterrupted)
	}()
	return ctx, func() {
		signal.Stop(signals)
		cancel()
	}
}
