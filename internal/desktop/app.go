// Package desktop supervises a separate core using the same distribution binary.
// It retains the loopback/session gateway and authenticated IPC security boundary.
package desktop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/radanor/radanor-shield/internal/config"
	"github.com/radanor/radanor-shield/internal/gateway"
	"github.com/radanor/radanor-shield/internal/ipc"
	"github.com/radanor/radanor-shield/internal/lifecycle"
	sparrowlog "github.com/radanor/radanor-shield/internal/logging"
	"github.com/radanor/radanor-shield/internal/privatefs"
)

func Run(parent context.Context, noBrowser bool) error {
	return run(parent, noBrowser, gateway.ValidateNotElevated)
}

func run(parent context.Context, noBrowser bool, validate func() error) error {
	if validate() != nil {
		return errors.New("Run Radanor Sparrow as a normal user, without Administrator or sudo")
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load application configuration: %w", err)
	}
	if err = config.EnsurePrivateDirs(cfg); err != nil {
		return fmt.Errorf("protect application directories: %w", err)
	}
	lock, err := lifecycle.Acquire(cfg.RuntimeDir, "desktop")
	if err != nil {
		if !errors.Is(err, lifecycle.ErrAlreadyRunning) {
			return fmt.Errorf("Cannot acquire the application lock: %w", err)
		}
		return reopenDashboard(parent, cfg.RuntimeDir, noBrowser)
	}
	defer lock.Close()
	// Do not attach to or interfere with a separately launched developer gateway.
	gatewayLock, err := lifecycle.Acquire(cfg.RuntimeDir, "gateway")
	if err != nil {
		return errors.New("A separate Radanor Sparrow gateway is running. Exit that instance before launching the desktop application")
	}
	defer gatewayLock.Close()
	discovery := filepath.Join(cfg.RuntimeDir, "desktop-origin")
	if err := os.Remove(discovery); err != nil && !os.IsNotExist(err) {
		return errors.New("Cannot remove stale desktop discovery state")
	}
	logPath := filepath.Join(cfg.StateDir, "application.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("Cannot open the private application log")
	}
	defer logFile.Close()
	if err = privatefs.ProtectFile(logPath); err != nil {
		return errors.New("Cannot protect the application log")
	}
	output := &applicationLog{file: logFile}
	logger := sparrowlog.New(output, slog.LevelInfo)
	executable, err := os.Executable()
	if err != nil {
		return errors.New("Cannot locate the application executable")
	}
	child, err := startCore(executable, output)
	if err != nil {
		return fmt.Errorf("Cannot start the managed core: %w", err)
	}
	defer child.stop()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	ready, readyCancel := context.WithTimeout(ctx, 20*time.Second)
	secret, err := waitReady(ready, cfg.RuntimeDir, child.launchID, child.done)
	readyCancel()
	if err != nil {
		return fmt.Errorf("Core startup failed; see %s: %w", logPath, err)
	}
	server, err := gateway.New(cfg.RuntimeDir, secret, logger)
	if err != nil {
		return errors.New("Cannot bind the loopback dashboard")
	}
	server.SetApplicationExit(cancel)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve() }()
	defer func() {
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = server.Shutdown(shutdown)
	}()
	if err := publishOrigin(discovery, server.Origin()); err != nil {
		return errors.New("Cannot publish the private dashboard origin")
	}
	defer os.Remove(discovery)
	if !noBrowser {
		// The window is stopped and its component reaped before the gateway and
		// the core are shut down, and before this process exits: a component the
		// supervisor owned is never left for the kernel to reparent, and the
		// dashboard cannot be left talking to a gateway that is already going away.
		stopDashboard, err := launchDesktopDashboard(ctx, cancel, server.Origin(), cfg.RuntimeDir, cfg.StateDir, secret, logger)
		if stopDashboard != nil {
			defer stopDashboard()
		}
		if err != nil {
			return err
		}
	}
	logger.Info("application started", "origin", server.Origin())
	select {
	case <-ctx.Done():
		return nil
	case <-child.done:
		return fmt.Errorf("The core stopped unexpectedly; see %s", logPath)
	case <-serveDone:
		return fmt.Errorf("The dashboard stopped unexpectedly; see %s", logPath)
	}
}

func waitReady(ctx context.Context, runtimeDir, launchID string, done <-chan struct{}) ([]byte, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-done:
			return nil, errors.New("managed core exited")
		case <-ticker.C:
			secret, err := ipc.LoadSecret(ipc.SecretPath(runtimeDir))
			if err != nil {
				continue
			}
			probe, cancel := context.WithTimeout(ctx, time.Second)
			response, err := ipc.Call(probe, runtimeDir, secret, "launcher_ready", launchID)
			cancel()
			if err == nil && response.OK {
				select {
				case <-done:
					return nil, errors.New("managed core exited")
				default:
				}
				return secret, nil
			}
		}
	}
}

func launchDashboard(ctx context.Context, origin, runtimeDir string, secret []byte, open func(string) error) error {
	if !validOrigin(origin) {
		return errors.New("Refusing an invalid dashboard origin")
	}
	response, err := ipc.Call(ctx, runtimeDir, secret, "issue_ticket", "")
	if err != nil || !response.OK || response.Value == "" {
		return errors.New("Cannot issue a dashboard launch ticket")
	}
	// Neither the ticket URL nor browser error (which can contain argv) is logged.
	if open(origin+"/#ticket="+response.Value) != nil {
		return errors.New("Cannot open the default browser. Check the OS browser association and launch again")
	}
	return nil
}
