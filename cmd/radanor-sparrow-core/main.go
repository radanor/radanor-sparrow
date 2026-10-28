package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/radanor/radanor-shield/internal/coreapp"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if coreapp.Run(ctx) != nil {
		os.Exit(1)
	}
}
