package coreapp

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/radanor/radanor-shield/internal/ipc"
	"github.com/radanor/radanor-shield/internal/storage"
)

// CE-6 settings operations served over the authenticated core IPC socket.
//
// Supported preferences are a closed, reviewed allowlist. An unsupported key is
// rejected outright (the request decoder forbids unknown fields), and every
// value is validated against a domain before anything is persisted. Updates are
// transactional: either the whole batch is stored and applied, or nothing
// changes. Nothing here can store a path, a command or an arbitrary blob.

const (
	settingsSchema = "shield.settings/v1"

	settingAssessEnabled  = "assess_enabled"
	settingAssessInterval = "assess_interval"
	settingAssessMaxRuns  = "assess_max_runs"
	settingTheme          = "theme"

	minAssessIntervalSetting = time.Hour
	maxAssessIntervalSetting = 7 * 24 * time.Hour
	minAssessRunsSetting     = 10
	maxAssessRunsSetting     = 5000
)

type settingsAPI struct {
	store  *storage.Store
	assess *assessmentAPI
	logger *slog.Logger
}

func (a *settingsAPI) handle(req ipc.Request) ipc.Response {
	switch req.Operation {
	case "settings_get":
		return a.get(req.Value)
	case "settings_update":
		return a.update(req.Value)
	default:
		return ipc.Response{Error: "unsupported operation"}
	}
}

type settingDescriptor struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Minimum     string   `json:"minimum,omitempty"`
	Maximum     string   `json:"maximum,omitempty"`
	Options     []string `json:"options,omitempty"`
}

// supportedSettings is the reviewed allowlist the UI renders and the update
// handler validates against.
var supportedSettings = []settingDescriptor{
	{Key: settingAssessEnabled, Type: "bool", Description: "Run the posture assessment automatically on a schedule."},
	{Key: settingAssessInterval, Type: "duration", Description: "How often a scheduled assessment runs.", Minimum: "1h", Maximum: "168h"},
	{Key: settingAssessMaxRuns, Type: "int", Description: "How many assessment runs are retained locally.", Minimum: "10", Maximum: "5000"},
	{Key: settingTheme, Type: "enum", Description: "Dashboard colour theme.", Options: []string{"light", "dark", "system"}},
}

type effectiveSettings struct {
	AssessEnabled  bool   `json:"assess_enabled"`
	AssessInterval string `json:"assess_interval"`
	AssessMaxRuns  int    `json:"assess_max_runs"`
	Theme          string `json:"theme"`
}

type storageUsage struct {
	MonitoringBytes   int64 `json:"monitoring_bytes"`
	MonitoringMax     int64 `json:"monitoring_max_bytes"`
	AssessmentBytes   int64 `json:"assessment_bytes"`
	AssessmentMax     int64 `json:"assessment_max_bytes"`
	InventoryBytes    int64 `json:"inventory_bytes"`
	InventoryMax      int64 `json:"inventory_max_bytes"`
	DatabaseBytes     int64 `json:"database_bytes"`
	EventRetentionDay int   `json:"event_retention_days"`
	MaxEvents         int   `json:"max_events"`
	MaxInventoryItems int   `json:"max_inventory_items_per_kind"`
}

type settingsResponse struct {
	Schema       string              `json:"schema"`
	Settings     map[string]string   `json:"settings"`
	Effective    effectiveSettings   `json:"effective"`
	Supported    []settingDescriptor `json:"supported"`
	Storage      storageUsage        `json:"storage"`
	ConfigSource string              `json:"config_source"`
	NextScanAt   *time.Time          `json:"next_scan_at,omitempty"`
	Limitations  []string            `json:"limitations"`
}

func (a *settingsAPI) get(raw string) ipc.Response {
	var body struct{}
	if err := decodeStrict(raw, &body); err != nil {
		return ipc.Response{Error: "invalid settings request"}
	}
	ctx := context.Background()
	persisted, err := a.store.Settings(ctx)
	if err != nil {
		a.logger.Error("settings read failed", "error", err)
		return ipc.Response{Error: "storage unavailable"}
	}
	usage, err := a.store.StorageUsage(ctx)
	if err != nil {
		return ipc.Response{Error: "storage unavailable"}
	}
	limits := a.store.Limits()
	schedule := a.assess.schedulerConfigSnapshot()
	effective := effectiveSettings{
		AssessEnabled:  schedule.Enabled,
		AssessInterval: schedule.Interval.String(),
		AssessMaxRuns:  a.store.AssessmentRunLimit(),
		Theme:          "system",
	}
	// A persisted value that is still valid wins over the configured default.
	if value, ok := persisted[settingAssessEnabled]; ok {
		if parsed, err := strconv.ParseBool(value); err == nil {
			effective.AssessEnabled = parsed
		}
	}
	if value, ok := persisted[settingAssessInterval]; ok {
		if parsed, err := time.ParseDuration(value); err == nil && parsed >= minAssessIntervalSetting && parsed <= maxAssessIntervalSetting {
			effective.AssessInterval = parsed.String()
		}
	}
	if value, ok := persisted[settingAssessMaxRuns]; ok {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= minAssessRunsSetting && parsed <= maxAssessRunsSetting {
			effective.AssessMaxRuns = parsed
		}
	}
	if value, ok := persisted[settingTheme]; ok && validTheme(value) {
		effective.Theme = value
	}
	if effective.AssessInterval == "0s" {
		effective.AssessInterval = ""
	}
	// A next-scan time is published only while a schedule is actually live. A
	// value left behind by a schedule that was just switched off must not be
	// advertised as a time the core intends to run, and a time that was never
	// computed is omitted rather than reported as the zero time.
	var next *time.Time
	if effective.AssessEnabled && effective.AssessInterval != "" {
		a.assess.mu.Lock()
		if !a.assess.nextScan.IsZero() {
			t := a.assess.nextScan
			next = &t
		}
		a.assess.mu.Unlock()
	}
	resp := settingsResponse{
		Schema:       settingsSchema,
		Settings:     persisted,
		Effective:    effective,
		Supported:    supportedSettings,
		ConfigSource: schedule.ConfigSource,
		NextScanAt:   next,
		Storage: storageUsage{
			MonitoringBytes: usage["monitoring"], MonitoringMax: limits.MaxBytes,
			AssessmentBytes: usage["assessment"], AssessmentMax: limits.MaxAssessmentBytes,
			InventoryBytes: usage["inventory"], InventoryMax: limits.MaxInventoryBytes,
			DatabaseBytes: a.store.DatabaseSize(), EventRetentionDay: int(limits.EventAge / (24 * time.Hour)),
			MaxEvents: limits.MaxEvents, MaxInventoryItems: limits.MaxInventoryItems,
		},
		Limitations: []string{
			"Sparrow runs as your user with no elevation; a preference that would require elevated access is not offered.",
			"Retention limits are local quotas, not a guarantee: monitoring, assessment history and inventory each keep their own independent budget.",
			"Schedule and retention changes take effect immediately; an inventory storage size is read-only here because resizing it would require reopening the database.",
			"Sparrow detects no malware and performs no automatic remediation; enabling a schedule only increases how often it looks at your device.",
			"No preference is ever sent anywhere: everything is stored in your local state directory.",
		},
	}
	return marshalBounded(resp)
}

func validTheme(value string) bool {
	return value == "light" || value == "dark" || value == "system"
}

// settingsUpdate is the strict request shape. Every field is a pointer so an
// omitted preference is distinguishable from one explicitly set to a zero value
// and is left unchanged.
type settingsUpdate struct {
	AssessEnabled  *bool   `json:"assess_enabled,omitempty"`
	AssessInterval *string `json:"assess_interval,omitempty"`
	AssessMaxRuns  *int    `json:"assess_max_runs,omitempty"`
	Theme          *string `json:"theme,omitempty"`
}

func (a *settingsAPI) update(raw string) ipc.Response {
	var body settingsUpdate
	if err := decodeStrict(raw, &body); err != nil {
		return ipc.Response{Error: "invalid settings request"}
	}
	if body.AssessEnabled == nil && body.AssessInterval == nil && body.AssessMaxRuns == nil && body.Theme == nil {
		return ipc.Response{Error: "invalid settings request: no supported setting was provided"}
	}
	updates := map[string]string{}
	// Validate every field before persisting anything, so a rejected batch
	// leaves the stored configuration completely unchanged.
	var interval time.Duration
	if body.AssessInterval != nil {
		parsed, err := time.ParseDuration(*body.AssessInterval)
		if err != nil || parsed < minAssessIntervalSetting || parsed > maxAssessIntervalSetting {
			return ipc.Response{Error: fmt.Sprintf("invalid assess_interval: expected a duration between %s and %s", minAssessIntervalSetting, maxAssessIntervalSetting)}
		}
		interval = parsed
		updates[settingAssessInterval] = parsed.String()
	}
	if body.AssessMaxRuns != nil {
		if *body.AssessMaxRuns < minAssessRunsSetting || *body.AssessMaxRuns > maxAssessRunsSetting {
			return ipc.Response{Error: fmt.Sprintf("invalid assess_max_runs: expected a value between %d and %d", minAssessRunsSetting, maxAssessRunsSetting)}
		}
		updates[settingAssessMaxRuns] = strconv.Itoa(*body.AssessMaxRuns)
	}
	if body.Theme != nil {
		if !validTheme(*body.Theme) {
			return ipc.Response{Error: "invalid theme: expected light, dark or system"}
		}
		updates[settingTheme] = *body.Theme
	}
	if body.AssessEnabled != nil {
		updates[settingAssessEnabled] = strconv.FormatBool(*body.AssessEnabled)
	}
	// Enabling a schedule requires an interval: the one in this request if it
	// carried one, otherwise the interval already in force.
	enabled := a.assess.schedulerConfigSnapshot().Enabled
	if body.AssessEnabled != nil {
		enabled = *body.AssessEnabled
	}
	effectiveInterval := a.resolveInterval(interval)
	if enabled && effectiveInterval < minAssessIntervalSetting {
		return ipc.Response{Error: "invalid schedule: a scheduled assessment requires an interval of at least one hour"}
	}
	if err := a.store.PutSettings(context.Background(), updates); err != nil {
		a.logger.Error("settings update failed", "error", err)
		return ipc.Response{Error: "the settings could not be saved"}
	}
	// Apply the changes. Retention is applied immediately (and at every later
	// prune) without reopening the store; the schedule is applied to the live
	// scheduler loop.
	if body.AssessMaxRuns != nil {
		a.store.SetAssessmentRunLimit(*body.AssessMaxRuns)
		if err := a.store.RetainAssessments(context.Background(), *body.AssessMaxRuns); err != nil {
			a.logger.Warn("applying retention immediately failed", "error", err)
		}
	}
	if body.AssessEnabled != nil || interval > 0 {
		a.assess.applySchedule(enabled, effectiveInterval)
	}
	a.logger.Info("settings updated", "changed", len(updates))
	return a.get("")
}

// resolveInterval answers "which interval would a schedule use?" with the most
// specific answer available: the interval in this request, else the interval
// already in force, else the last valid interval the user saved.
//
// The persisted preference is consulted last because it is the source of truth
// and the runtime scheduler is a cache of it. Before this correction an enable
// request that carried only `assess_enabled=true` was refused after a restart,
// because a disabled schedule had left the runtime interval at zero and nothing
// fell back to the interval the user had already saved.
func (a *settingsAPI) resolveInterval(requested time.Duration) time.Duration {
	if requested > 0 {
		return requested
	}
	if current := a.assess.schedulerConfigSnapshot().Interval; current > 0 {
		return current
	}
	persisted, err := a.store.Settings(context.Background())
	if err != nil {
		a.logger.Warn("the stored schedule interval could not be read", "error", err)
		return 0
	}
	value, ok := persisted[settingAssessInterval]
	if !ok {
		return 0
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < minAssessIntervalSetting || parsed > maxAssessIntervalSetting {
		return 0
	}
	return parsed
}

// loadPersistedSettings applies stored preferences at startup so a restart keeps
// the user's chosen schedule and retention.
//
// The resolved schedule is installed even when it is switched off. Disabling a
// schedule must not discard the interval the user saved: the runtime
// configuration is what a later "enable only" request falls back on, and leaving
// it at zero made re-enabling fail after a restart. Installing it also makes the
// reported configuration source truthful — the schedule in force comes from the
// user's stored preferences, not from the compiled-in default or the
// environment.
func loadPersistedSettings(ctx context.Context, store *storage.Store, assess *assessmentAPI, logger *slog.Logger) {
	persisted, err := store.Settings(ctx)
	if err != nil {
		logger.Warn("stored settings could not be read", "error", err)
		return
	}
	schedule := assess.schedulerConfigSnapshot()
	enabled, interval := schedule.Enabled, schedule.Interval
	source := schedule.ConfigSource
	fromSettings := false
	if value, ok := persisted[settingAssessEnabled]; ok {
		if parsed, perr := strconv.ParseBool(value); perr == nil {
			enabled = parsed
			fromSettings = true
		}
	}
	if value, ok := persisted[settingAssessInterval]; ok {
		if parsed, perr := time.ParseDuration(value); perr == nil && parsed >= minAssessIntervalSetting && parsed <= maxAssessIntervalSetting {
			interval = parsed
			fromSettings = true
		}
	}
	if value, ok := persisted[settingAssessMaxRuns]; ok {
		if parsed, perr := strconv.Atoi(value); perr == nil && parsed >= minAssessRunsSetting && parsed <= maxAssessRunsSetting {
			store.SetAssessmentRunLimit(parsed)
		}
	}
	if fromSettings {
		source = schedulerSourceSettings
	}
	if interval > 0 {
		assess.applyScheduleFrom(enabled, interval, source)
		return
	}
	if fromSettings {
		// Preferences are in force but no usable interval was stored. Say where the
		// configuration came from without enabling a schedule that has no interval:
		// the scheduler loop requires a positive interval before it runs anything.
		assess.applyScheduleFrom(false, 0, source)
	}
}
