package coreapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/radanor/radanor-shield/internal/inventory"
	"github.com/radanor/radanor-shield/internal/ipc"
	"github.com/radanor/radanor-shield/internal/storage"
)

// CE-6 inventory operations served over the authenticated core IPC socket.
//
// Every operation is read-only except `inventory_refresh`, which re-runs the
// fixed, reviewed inventory collector set. No operation accepts a path, a
// command, a package name to install, or any other attacker-controlled input:
// `inventory_software`/`inventory_endpoints`/`inventory_services` accept only a
// bounded search string, an allowlisted source filter and a name cursor.

const (
	inventoryStatusSchema  = "shield.inventory_status/v1"
	inventoryItemsSchema   = "shield.inventory_items/v1"
	inventoryRefreshSchema = "shield.inventory_refresh/v1"
)

// minInventoryRefreshInterval rate-limits an on-demand refresh so a page cannot
// turn a bounded collector run into a tight loop.
const minInventoryRefreshInterval = 10 * time.Second

// inventoryCollectorTimeout bounds each inventory collector. The package
// inventory reads the whole dpkg database, so it is given a slightly longer
// deadline than a posture collector while staying well inside the IPC timeout.
const inventoryCollectorTimeout = 20 * time.Second

type inventoryAPI struct {
	store      *storage.Store
	collectors []inventory.Collector
	logger     *slog.Logger

	mu          sync.Mutex
	refreshing  bool
	lastRefresh time.Time
	lastError   string
	failures    map[inventory.Kind]string
}

func (a *inventoryAPI) handle(req ipc.Request) ipc.Response {
	switch req.Operation {
	case "inventory_status":
		return a.status(req.Value)
	case "inventory_software":
		return a.items(req.Value, inventory.KindSoftware)
	case "inventory_endpoints":
		return a.items(req.Value, inventory.KindEndpoint)
	case "inventory_services":
		return a.items(req.Value, inventory.KindService)
	case "inventory_refresh":
		return a.refresh(req.Value)
	default:
		return ipc.Response{Error: "unsupported operation"}
	}
}

type inventoryStatusResponse struct {
	Schema string `json:"schema"`
	storage.InventoryStatus
	Refreshing bool `json:"refreshing"`
	// LastRefresh is a pointer because omitempty has no effect on a struct: a
	// time.Time field would be serialised as the zero time (0001-01-01) before the
	// first refresh, presenting a timestamp that never happened.
	LastRefresh *time.Time        `json:"last_refresh,omitempty"`
	LastError   string            `json:"last_error,omitempty"`
	Failures    map[string]string `json:"failures,omitempty"`
}

func (a *inventoryAPI) status(raw string) ipc.Response {
	var body struct{}
	if err := decodeStrict(raw, &body); err != nil {
		return ipc.Response{Error: "invalid inventory status request"}
	}
	status, err := a.store.InventoryStatus(context.Background())
	if err != nil {
		a.logger.Error("inventory status read failed", "error", err)
		return ipc.Response{Error: "storage unavailable"}
	}
	a.mu.Lock()
	refreshing, lastRefresh, lastError := a.refreshing, a.lastRefresh, a.lastError
	failures := map[string]string{}
	for kind, reason := range a.failures {
		failures[string(kind)] = reason
	}
	a.mu.Unlock()
	var last *time.Time
	if !lastRefresh.IsZero() {
		t := lastRefresh
		last = &t
	}
	return marshalPageBounded(inventoryStatusResponse{Schema: inventoryStatusSchema, InventoryStatus: status, Refreshing: refreshing, LastRefresh: last, LastError: lastError, Failures: failures})
}

func (a *inventoryAPI) items(raw string, kind inventory.Kind) ipc.Response {
	var body struct {
		SearchBase64 *string `json:"search_b64,omitempty"`
		SourceBase64 *string `json:"source_b64,omitempty"`
		AfterBase64  *string `json:"after_b64,omitempty"`
		Search       string  `json:"search,omitempty"`
		Source       string  `json:"source,omitempty"`
		After        string  `json:"after,omitempty"`
		Limit        int     `json:"limit,omitempty"`
	}
	if err := decodeStrict(raw, &body); err != nil {
		return ipc.Response{Error: "invalid inventory query"}
	}
	search, err := inventory.DecodeQueryFilter(body.Search, body.SearchBase64, 128)
	if err != nil {
		return ipc.Response{Error: "invalid inventory query"}
	}
	source, err := inventory.DecodeQueryFilter(body.Source, body.SourceBase64, inventory.MaxSourceLen)
	if err != nil {
		return ipc.Response{Error: "invalid inventory query"}
	}
	after := body.After
	if body.AfterBase64 != nil {
		after, err = inventory.DecodeQueryFilter(body.After, body.AfterBase64, inventory.MaxNameLen)
		if err != nil {
			return ipc.Response{Error: "invalid cursor"}
		}
	}
	limit, err := clampPage(body.Limit)
	if err != nil {
		return ipc.Response{Error: err.Error()}
	}
	if _, err := inventory.DecodeCursor(after); err != nil {
		return ipc.Response{Error: "invalid cursor"}
	}
	items, total, err := a.store.InventoryItems(context.Background(), storage.InventoryQuery{
		Kind: kind, Search: search, Source: source, After: after, Limit: limit,
	})
	if err != nil {
		if errors.Is(err, storage.ErrInvalidSetting) {
			return ipc.Response{Error: "invalid inventory query"}
		}
		a.logger.Error("inventory read failed", "kind", kind, "error", err)
		return ipc.Response{Error: "storage unavailable"}
	}
	sources, err := a.store.InventorySources(context.Background(), kind)
	if err != nil {
		return ipc.Response{Error: "storage unavailable"}
	}
	if items == nil {
		items = []storage.ItemRecord{}
	}
	next := ""
	if len(items) == limit {
		last := items[len(items)-1]
		next = inventory.EncodeCursor(last.Name, last.Version, last.Source)
	}
	a.mu.Lock()
	lastError := a.lastError
	a.mu.Unlock()
	resp := struct {
		Schema    string               `json:"schema"`
		Kind      string               `json:"kind"`
		Items     []storage.ItemRecord `json:"items"`
		Total     int                  `json:"total"`
		NextAfter string               `json:"next_after,omitempty"`
		Sources   []string             `json:"sources"`
		LastError string               `json:"last_error,omitempty"`
	}{Schema: inventoryItemsSchema, Kind: string(kind), Items: items, Total: total, NextAfter: next, Sources: sources, LastError: lastError}
	if sources == nil {
		resp.Sources = []string{}
	}
	return marshalPageBounded(resp)
}

// refresh re-runs the inventory collectors. It is a state-changing operation
// only in that it replaces the stored snapshot; it executes a fixed collector
// set and never accepts a target from the caller.
func (a *inventoryAPI) refresh(raw string) ipc.Response {
	var body struct{}
	if err := decodeStrict(raw, &body); err != nil {
		return ipc.Response{Error: "invalid refresh request"}
	}
	a.mu.Lock()
	if a.refreshing {
		a.mu.Unlock()
		return ipc.Response{Error: "invalid refresh request: a refresh is already in progress"}
	}
	if since := time.Since(a.lastRefresh); !a.lastRefresh.IsZero() && since < minInventoryRefreshInterval {
		a.mu.Unlock()
		return ipc.Response{Error: "invalid refresh request: the last refresh finished moments ago, wait a few seconds and try again"}
	}
	a.refreshing = true
	a.mu.Unlock()
	go func() {
		if err := a.Refresh(context.Background()); err != nil {
			a.logger.Error("inventory refresh failed", "error", err)
		}
	}()
	payload, _ := json.Marshal(map[string]any{"schema": inventoryRefreshSchema, "accepted": true, "started_at": time.Now()})
	return ipc.Response{OK: true, Value: string(payload)}
}

// Refresh collects and stores one inventory snapshot. Kinds whose collector
// could not produce items are skipped so a failed refresh never blanks a page it
// did not cover.
func (a *inventoryAPI) Refresh(ctx context.Context) error {
	a.mu.Lock()
	if !a.refreshing {
		a.refreshing = true
	}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.refreshing = false
		a.lastRefresh = time.Now()
		a.mu.Unlock()
	}()

	snapshot := inventory.Snapshot{ObservedAt: time.Now().UnixNano()}
	failures := map[inventory.Kind]string{}
	for _, collector := range a.collectors {
		cctx, cancel := context.WithTimeout(ctx, inventoryCollectorTimeout)
		result := collector.CollectInventory(cctx)
		cancel()
		// A kind with nothing to merge is skipped rather than merged: recording it
		// would replace the stored snapshot with an empty one whose zero observed
		// count reads as a complete, empty inventory. That covers an unsupported
		// platform, a refused or failed read, and a reading that could not be
		// interpreted at all (an Incomplete result with no usable items). A
		// Collected result with no items is different: the collector really did
		// look and find nothing, and that is worth recording.
		if result.Status == inventory.Unsupported || result.Status == inventory.PermissionDenied || result.Status == inventory.Failed ||
			(len(result.Items) == 0 && result.Status != inventory.Collected) {
			reason := result.Error
			if reason == "" {
				reason = "the collector reported " + string(result.Status) + " without usable records"
			}
			failures[result.Kind] = reason
			continue
		}
		// A partial (Incomplete) reading is still merged: the items really were
		// observed. Plan carries the collector's truncation flag into the snapshot
		// so the store records the kind as truncated even though the number of
		// records observed equals the number stored — a clipped listing yields a
		// partial count, and a partial count is not complete coverage.
		if result.Truncated {
			failures[result.Kind] = result.Error
		}
		snapshot.Merge(inventory.Plan(result, snapshot.ObservedAt))
	}
	a.mu.Lock()
	a.failures = failures
	a.mu.Unlock()
	if len(snapshot.Total) == 0 {
		err := errors.New("no inventory could be collected")
		a.mu.Lock()
		a.lastError = err.Error()
		a.mu.Unlock()
		return err
	}
	if err := a.store.ReplaceInventory(ctx, snapshot); err != nil {
		a.mu.Lock()
		a.lastError = boundedError(err)
		a.mu.Unlock()
		return err
	}
	a.mu.Lock()
	a.lastError = ""
	a.mu.Unlock()
	return nil
}

// marshalPageBounded encodes an inventory response and refuses to exceed the IPC
// frame bound rather than emitting a truncated frame.
func marshalPageBounded(value any) ipc.Response {
	return marshalBounded(value)
}
