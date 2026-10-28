package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/radanor/radanor-shield/internal/coreapp"
	"github.com/radanor/radanor-shield/internal/desktop"
	"github.com/radanor/radanor-shield/internal/gateway"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// An inherited pipe gates initialization until the supervisor has attached
	// crash containment. EOF then requests graceful shutdown without secrets.
	if len(os.Args) == 2 && os.Args[1] == "--internal-core" {
		if gateway.ValidateNotElevated() != nil {
			os.Exit(1)
		}
		var gate [1]byte
		if _, err := io.ReadFull(os.Stdin, gate[:]); err != nil || gate[0] != 1 {
			os.Exit(1)
		}
		go func() { _, _ = io.Copy(io.Discard, os.Stdin); stop() }()
		if coreapp.Run(ctx) != nil {
			os.Exit(1)
		}
		return
	}
	noBrowser := flag.Bool("no-browser", false, "diagnostics: do not open the browser")
	flag.Parse()
	if err := desktop.Run(ctx, *noBrowser); err != nil {
		if *noBrowser {
			fmt.Fprintln(os.Stderr, err)
		} else {
			desktop.ShowError(err)
		}
		os.Exit(1)
	}
}
