package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/radanor/radanor-shield/internal/branding"
	"github.com/radanor/radanor-shield/internal/config"
	"github.com/radanor/radanor-shield/internal/gateway"
	"github.com/radanor/radanor-shield/internal/ipc"
	"github.com/radanor/radanor-shield/internal/lifecycle"
	sparrowlog "github.com/radanor/radanor-shield/internal/logging"
)

func main() {
	noBrowser := flag.Bool("no-browser", false, "start without opening a browser (diagnostics only)")
	flag.Parse()
	logger := sparrowlog.New(os.Stderr, slog.LevelInfo).With("component", "gateway")
	if err := gateway.ValidateNotElevated(); err != nil {
		logger.Error("gateway start refused", "error", err)
		os.Exit(1)
	}
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration failed", "error", err)
		os.Exit(1)
	}
	if err := config.EnsurePrivateDirs(cfg); err != nil {
		logger.Error("directory setup failed", "error", err)
		os.Exit(1)
	}
	lock, err := lifecycle.Acquire(cfg.RuntimeDir, "gateway")
	if err != nil {
		logger.Error("gateway start refused", "error", err)
		os.Exit(1)
	}
	defer lock.Close()
	secret, err := ipc.LoadSecret(ipc.SecretPath(cfg.RuntimeDir))
	if err != nil {
		logger.Error("IPC setup failed", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ticketResp, err := ipc.Call(ctx, cfg.RuntimeDir, secret, "issue_ticket", "")
	if err != nil || !ticketResp.OK {
		logger.Error("core unavailable")
		os.Exit(1)
	}
	server, err := gateway.New(cfg.RuntimeDir, secret, logger)
	if err != nil {
		logger.Error("gateway setup failed", "error", err)
		os.Exit(1)
	}
	go func() {
		if err := server.Serve(); err != nil {
			logger.Error("gateway failed", "error", err)
			stop()
		}
	}()
	logger.Info("gateway started", "origin", server.Origin(), "product", branding.DisplayName)
	if !*noBrowser {
		// Fragments are not sent in HTTP requests or server logs; frontend removes it immediately.
		if err := openBrowser(server.Origin() + "/#ticket=" + ticketResp.Value); err != nil {
			logger.Error("browser launch failed", "error", err)
			stop()
		}
	}
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	logger.Info("gateway stopped")
}
