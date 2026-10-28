package coreapp

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
	"github.com/radanor/radanor-shield/internal/auth"
	"github.com/radanor/radanor-shield/internal/branding"
	"github.com/radanor/radanor-shield/internal/collectors"
	"github.com/radanor/radanor-shield/internal/config"
	"github.com/radanor/radanor-shield/internal/ipc"
	"github.com/radanor/radanor-shield/internal/lifecycle"
	sparrowlog "github.com/radanor/radanor-shield/internal/logging"
	"github.com/radanor/radanor-shield/internal/monitoring"
	"github.com/radanor/radanor-shield/internal/storage"
)

const retentionInterval = 15 * time.Minute

func Run(parent context.Context) error {
	logger := sparrowlog.New(os.Stderr, slog.LevelInfo).With("component", "core")
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration failed", "error", err)
		return errors.New("core failed; see application log")
	}
	if err := config.EnsurePrivateDirs(cfg); err != nil {
		logger.Error("directory setup failed", "error", err)
		return errors.New("core failed; see application log")
	}
	lock, err := lifecycle.Acquire(cfg.RuntimeDir, "core")
	if err != nil {
		logger.Error("core start refused", "error", err)
		return errors.New("core failed; see application log")
	}
	defer lock.Close()
	secret, err := ipc.LoadOrCreateSecret(ipc.SecretPath(cfg.RuntimeDir))
	if err != nil {
		logger.Error("IPC setup failed", "error", err)
		return errors.New("core failed; see application log")
	}
	limits, err := storage.LimitsFromEnvironment()
	if err != nil {
		logger.Error("storage configuration failed", "error", err)
		return errors.New("core failed; see application log")
	}
	schedCfg, err := schedulerFromEnv()
	if err != nil {
		logger.Error("assessment scheduling configuration failed", "error", err)
		return errors.New("core failed; see application log")
	}
	// Posture history has its own byte budget; its run-count retention comes
	// from the same configuration the scheduler uses so there is one answer to
	// "how much assessment history is kept".
	limits.MaxAssessmentRuns = schedCfg.MaxRetained
	store, err := storage.Open(filepath.Join(cfg.StateDir, "monitoring.db"), limits)
	if err != nil {
		logger.Error("monitoring storage unavailable", "error", err)
		return errors.New("core failed; see application log")
	}
	defer store.Close()
	// CE-5: an interrupted run from a previous core session is closed out
	// before anything new starts, so a crashed scan is never shown as running.
	if n, rerr := store.ReconcileInterruptedRuns(context.Background()); rerr != nil {
		logger.Error("assessment reconciliation failed", "error", rerr)
	} else if n > 0 {
		logger.Warn("closed interrupted assessment runs", "count", n)
	}
	scanner := assessment.NewScanner(store, collectors.PlatformChecks(), collectors.PlatformCollectors())
	tickets := auth.NewTicketStore()
	launchID := os.Getenv("RADANOR_SPARROW_LAUNCH_ID")
	var engine *monitoring.Engine
	api := &monitoringAPI{logger: logger.With("component", "monitoring-api")}
	assess := &assessmentAPI{scanner: scanner, store: store, logger: logger.With("component", "assessment-api"), scheduler: schedCfg, reload: make(chan struct{}, 1)}
	// CE-6: a stored schedule/retention preference overrides the environment
	// default, so the user's choice survives a restart.
	loadPersistedSettings(context.Background(), store, assess, logger)
	inventoryAPI := &inventoryAPI{
		store:      store,
		collectors: collectors.PlatformInventoryCollectors(),
		logger:     logger.With("component", "inventory-api"),
	}
	settingsAPI := &settingsAPI{store: store, assess: assess, logger: logger.With("component", "settings-api")}
	server, err := ipc.Listen(cfg.RuntimeDir, secret, func(req ipc.Request) ipc.Response {
		switch req.Operation {
		case "health":
			return ipc.Response{OK: true, Value: branding.Version}
		case "launcher_ready":
			if launchID != "" && req.Value == launchID {
				return ipc.Response{OK: true}
			}
			return ipc.Response{Error: "not this managed core"}
		case "issue_ticket":
			value, err := tickets.Issue()
			if err != nil {
				return ipc.Response{Error: "ticket unavailable"}
			}
			return ipc.Response{OK: true, Value: value}
		case "redeem_ticket":
			if tickets.Redeem(req.Value) {
				return ipc.Response{OK: true}
			}
			return ipc.Response{Error: "invalid or expired ticket"}
		case "monitoring_status", "monitoring_events", "monitoring_gaps":
			if engine == nil {
				return ipc.Response{Error: "monitoring unavailable"}
			}
			return api.handle(req)
		case "assessment_scan", "assessment_cancel", "assessment_status", "assessment_device", "assessment_checks",
			"assessment_findings", "assessment_finding", "assessment_runs", "assessment_run":
			return assess.handle(req)
		case "inventory_status", "inventory_software", "inventory_endpoints", "inventory_services", "inventory_refresh":
			return inventoryAPI.handle(req)
		case "settings_get", "settings_update":
			return settingsAPI.handle(req)
		default:
			return ipc.Response{Error: "unsupported operation"}
		}
	})
	if err != nil {
		logger.Error("IPC listen failed", "error", err)
		return errors.New("core failed; see application log")
	}
	ctx, stop := context.WithCancel(parent)
	defer stop()
	sources, sourceErr := nativeSources()
	if sourceErr != nil {
		logger.Warn("native file monitoring unavailable", "reason", "no eligible default roots")
	}
	engineCfg := monitoring.DefaultConfig()
	// Events and their checkpoints are committed in one durable transaction;
	// the engine freezes checkpoint advancement for any source that fails.
	engineCfg.DurableCheckpoints = true
	engine, err = monitoring.New(engineCfg, store.Process, store, sources...)
	if err != nil {
		logger.Error("monitoring setup failed", "error", err)
		return errors.New("core failed; see application log")
	}
	engine.SetGapSink(store)
	api.engine = engine
	api.store = store
	if err := store.Retain(ctx); err != nil {
		logger.Error("startup retention failed", "error", err)
	}
	engineDone := make(chan struct{})
	go func() {
		defer close(engineDone)
		if err := engine.Run(ctx); err != nil {
			logger.Error("monitoring stopped unexpectedly", "error", err)
			stop()
		}
	}()
	defer func() { stop(); <-engineDone }()
	go func() {
		ticker := time.NewTicker(retentionInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := store.Retain(ctx); err != nil {
					logger.Error("retention failed", "error", err)
				}
				// Assessment runs are retained in their own bounded window so
				// posture history can never evict monitoring evidence. Zero selects
				// the effective configured or settings-overridden limit.
				if err := store.RetainAssessments(ctx, 0); err != nil {
					logger.Error("assessment retention failed", "error", err)
				}
			}
		}
	}()
	go assess.runScheduler(ctx)
	// CE-6: a completed scan refreshes the software/network inventory so the
	// Software and Network & Services pages stay current without a manual
	// refresh. It runs in the background and never blocks the scan.
	assess.setOnScanComplete(func() {
		go func() {
			if err := inventoryAPI.Refresh(context.Background()); err != nil {
				logger.Warn("post-scan inventory refresh failed", "error", err)
			}
		}()
	})
	logger.Info("core started", "version", branding.Version, "native_sources", len(sources), "checkpoint_persistence", true,
		"assessment_checks", len(collectors.PlatformChecks()), "assessment_collectors", len(collectors.PlatformCollectors()),
		"assessment_schedule", schedCfg.Enabled)
	if err := server.Serve(ctx); err != nil {
		logger.Error("core stopped unexpectedly", "error", err)
		return errors.New("core failed; see application log")
	}
	<-engineDone
	if err := store.Close(); err != nil {
		logger.Error("storage close failed", "error", err)
	}
	logger.Info("core stopped")
	return nil
}
