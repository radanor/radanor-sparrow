package coreapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/radanor/radanor-shield/internal/ipc"
	"github.com/radanor/radanor-shield/internal/monitoring"
	"github.com/radanor/radanor-shield/internal/storage"
)

// Read-only monitoring operations served over the authenticated core IPC
// socket (CE-4). The gateway is the only expected peer; every payload is
// bounded and no operation can create, alter, or delete an observation.

const (
	statusSchema   = "shield.monitoring_status/v1"
	eventsSchema   = "shield.monitoring_events/v1"
	gapsSchema     = "shield.monitoring_gaps/v1"
	defaultPage    = 20
	minPage        = 1
	responseMargin = 2048
)

type monitoringAPI struct {
	engine *monitoring.Engine
	store  *storage.Store
	logger *slog.Logger
}

type sourceStatus struct {
	ID                  string                 `json:"id"`
	State               monitoring.HealthState `json:"state"`
	Recovery            string                 `json:"recovery"`
	Platforms           []string               `json:"platforms,omitempty"`
	Capabilities        []string               `json:"capabilities,omitempty"`
	Limitations         []string               `json:"limitations,omitempty"`
	LastSuccessfulEvent *time.Time             `json:"last_successful_event,omitempty"`
	LastConfirmation    *time.Time             `json:"last_confirmation,omitempty"`
	LastFailure         *time.Time             `json:"last_failure,omitempty"`
	QueueOverflows      uint64                 `json:"queue_overflows"`
	DroppedEvents       uint64                 `json:"dropped_events"`
	Duplicates          uint64                 `json:"duplicates"`
	RestartCount        uint64                 `json:"restart_count"`
	MonitoringGaps      uint64                 `json:"monitoring_gaps"`
	LastError           string                 `json:"last_error,omitempty"`
}

type monitoringStatus struct {
	Schema          string         `json:"schema"`
	GeneratedAt     time.Time      `json:"generated_at"`
	State           string         `json:"state"`
	ProcessedEvents uint64         `json:"processed_events"`
	Sources         []sourceStatus `json:"sources"`
	Storage         storage.Info   `json:"storage"`
	// Ingest reports what storage did with each event it was given, per source,
	// so an ignored duplicate is visible as a duplicate rather than vanishing
	// into a successful ingestion count. A collision is not a duplicate: it means
	// an event was not stored, and it is reported here so it can be found.
	Ingest       []storage.IngestCounters `json:"ingest"`
	ReplayLimits []string                 `json:"replay_limits"`
}

type eventsRequest struct {
	Limit  int    `json:"limit,omitempty"`
	Before int64  `json:"before,omitempty"`
	Source string `json:"source,omitempty"`
	Type   string `json:"type,omitempty"`
}

type eventsResponse struct {
	Schema     string                `json:"schema"`
	Events     []storage.EventRecord `json:"events"`
	NextBefore int64                 `json:"next_before,omitempty"`
	Truncated  bool                  `json:"truncated"`
}

type gapsRequest struct {
	Limit int `json:"limit,omitempty"`
}

type gapsResponse struct {
	Schema string              `json:"schema"`
	Gaps   []storage.GapRecord `json:"gaps"`
	Total  int64               `json:"total_gaps"`
}

func (a *monitoringAPI) handle(req ipc.Request) ipc.Response {
	switch req.Operation {
	case "monitoring_status":
		return a.status()
	case "monitoring_events":
		return a.events(req.Value)
	case "monitoring_gaps":
		return a.gaps(req.Value)
	default:
		return ipc.Response{Error: "unsupported operation"}
	}
}

func (a *monitoringAPI) status() ipc.Response {
	health := a.engine.Health()
	descriptors := a.engine.Descriptors()
	sources := make([]sourceStatus, 0, len(health))
	for id, h := range health {
		s := sourceStatus{ID: id, State: h.State, Recovery: "none", QueueOverflows: h.QueueOverflows, DroppedEvents: h.DroppedEvents, Duplicates: h.Duplicates, RestartCount: h.RestartCount, MonitoringGaps: h.MonitoringGaps, LastError: h.LastError}
		if d, ok := descriptors[id]; ok {
			s.Recovery = string(d.Recovery)
			s.Platforms = d.Platforms
			s.Capabilities = d.Capabilities
			s.Limitations = d.Limitations
		}
		if !h.LastSuccessfulEvent.IsZero() {
			t := h.LastSuccessfulEvent
			s.LastSuccessfulEvent = &t
		}
		if !h.LastConfirmation.IsZero() {
			t := h.LastConfirmation
			s.LastConfirmation = &t
		}
		if !h.LastFailure.IsZero() {
			t := h.LastFailure
			s.LastFailure = &t
		}
		sources = append(sources, s)
	}
	// Stable order for the dashboard and tests.
	for i := 1; i < len(sources); i++ {
		for j := i; j > 0 && sources[j].ID < sources[j-1].ID; j-- {
			sources[j], sources[j-1] = sources[j-1], sources[j]
		}
	}
	info, err := a.store.Info(context.Background())
	if err != nil {
		a.logger.Error("monitoring status storage read failed", "error", err)
		return ipc.Response{Error: "storage unavailable"}
	}
	ingest := a.store.IngestStats()
	if ingest == nil {
		ingest = []storage.IngestCounters{}
	}
	status := monitoringStatus{Schema: statusSchema, GeneratedAt: time.Now(), State: overallState(health), ProcessedEvents: a.engine.Processed(), Sources: sources, Storage: info, Ingest: ingest, ReplayLimits: []string{
		"file-change sources cannot replay events missed while the core was stopped or a queue overflowed; such periods are recorded as monitoring gaps",
		"file changes are observations, not evidence of malicious activity",
		"an ignored duplicate is counted as a duplicate rather than as stored evidence; a collision means the event was not stored at all",
	}}
	payload, err := json.Marshal(status)
	if err != nil {
		return ipc.Response{Error: "status encoding failed"}
	}
	if len(payload) > ipc.MaxResponseBytes-responseMargin {
		return ipc.Response{Error: "status too large"}
	}
	return ipc.Response{OK: true, Value: string(payload)}
}

func overallState(health map[string]monitoring.Health) string {
	if len(health) == 0 {
		return "unavailable"
	}
	healthy, active := 0, 0
	for _, h := range health {
		switch h.State {
		case monitoring.HealthHealthy:
			healthy++
			active++
		case monitoring.HealthStarting, monitoring.HealthRecovering:
			active++
		}
	}
	switch {
	case healthy == len(health):
		return "active"
	case healthy > 0:
		return "partial"
	case active > 0:
		return "starting"
	default:
		return "unavailable"
	}
}

func (a *monitoringAPI) events(raw string) ipc.Response {
	var req eventsRequest
	if err := decodeStrict(raw, &req); err != nil {
		return ipc.Response{Error: "invalid events query"}
	}
	limit, err := clampPage(req.Limit)
	if err != nil {
		return ipc.Response{Error: err.Error()}
	}
	if req.Before < 0 || len(req.Source) > 128 || len(req.Type) > 128 {
		return ipc.Response{Error: "invalid events query"}
	}
	budget := ipc.MaxResponseBytes - responseMargin
	for {
		page, readErr := a.store.Recent(context.Background(), storage.Query{Source: req.Source, Type: req.Type, Before: req.Before, Limit: limit})
		if readErr != nil {
			a.logger.Error("monitoring event read failed", "error", readErr)
			return ipc.Response{Error: "storage unavailable"}
		}
		resp := eventsResponse{Schema: eventsSchema, Events: page}
		if resp.Events == nil {
			resp.Events = []storage.EventRecord{}
		}
		if len(page) == limit {
			resp.NextBefore = page[len(page)-1].ID
		}
		payload, marshalErr := json.Marshal(resp)
		if marshalErr != nil {
			return ipc.Response{Error: "events encoding failed"}
		}
		if len(payload) <= budget {
			return ipc.Response{OK: true, Value: string(payload)}
		}
		if limit == minPage {
			return ipc.Response{Error: "events too large"}
		}
		// Oversized pages shrink until they fit the IPC response bound.
		limit = max(minPage, limit/2)
	}
}

func (a *monitoringAPI) gaps(raw string) ipc.Response {
	var req gapsRequest
	if err := decodeStrict(raw, &req); err != nil {
		return ipc.Response{Error: "invalid gaps query"}
	}
	limit, err := clampPage(req.Limit)
	if err != nil {
		return ipc.Response{Error: err.Error()}
	}
	gapList, readErr := a.store.Gaps(context.Background(), limit)
	if readErr != nil {
		a.logger.Error("monitoring gap read failed", "error", readErr)
		return ipc.Response{Error: "storage unavailable"}
	}
	if gapList == nil {
		gapList = []storage.GapRecord{}
	}
	payload, err := json.Marshal(gapsResponse{Schema: gapsSchema, Gaps: gapList, Total: a.mostRecentGapCount()})
	if err != nil {
		return ipc.Response{Error: "gaps encoding failed"}
	}
	return ipc.Response{OK: true, Value: string(payload)}
}

func (a *monitoringAPI) mostRecentGapCount() int64 {
	info, err := a.store.Info(context.Background())
	if err != nil {
		return 0
	}
	return info.GapCount
}

func decodeStrict(raw string, target any) error {
	if raw == "" {
		raw = "{}"
	}
	if len(raw) > 1024 {
		return errors.New("query too large")
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	return nil
}

func clampPage(limit int) (int, error) {
	if limit == 0 {
		return defaultPage, nil
	}
	if limit < minPage || limit > storage.DefaultLimits().MaxPageSize {
		return 0, errors.New("page out of bounds")
	}
	return limit, nil
}
