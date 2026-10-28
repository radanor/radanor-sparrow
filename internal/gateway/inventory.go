package gateway

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/radanor/radanor-shield/internal/inventory"
)

// CE-6 inventory gateway handlers.
//
// The gateway holds no inventory state: every request is validated here and
// proxied over the authenticated core IPC socket. The read endpoints accept only
// a bounded search string, an allowlisted source filter, a legacy or opaque cursor and a
// page limit. The one state-changing endpoint (refresh) is POST-only, CSRF- and
// same-origin-guarded, and takes no parameters at all.

const maxInventorySearch = 128

func (s *Server) inventoryStatus(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	s.readCall(w, r, subsystemInventory, "inventory_status", "")
}

func (s *Server) inventorySoftware(w http.ResponseWriter, r *http.Request) {
	s.inventoryItems(w, r, "inventory_software")
}

func (s *Server) inventoryEndpoints(w http.ResponseWriter, r *http.Request) {
	s.inventoryItems(w, r, "inventory_endpoints")
}

func (s *Server) inventoryServices(w http.ResponseWriter, r *http.Request) {
	s.inventoryItems(w, r, "inventory_services")
}

func (s *Server) inventoryItems(w http.ResponseWriter, r *http.Request, operation string) {
	payload, err := inventoryQuery(r)
	if err != nil {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	s.readCall(w, r, subsystemInventory, operation, payload)
}

// inventoryQuery strictly validates the bounded read-only parameters.
func inventoryQuery(r *http.Request) (string, error) {
	query := r.URL.Query()
	allowed := map[string]bool{"search": true, "source": true, "after": true, "limit": true}
	for key := range query {
		if !allowed[key] {
			return "", errInvalidQuery
		}
	}
	search := query.Get("search")
	if len(search) > maxInventorySearch {
		return "", errInvalidQuery
	}
	source := query.Get("source")
	if len(source) > 64 {
		return "", errInvalidQuery
	}
	after := query.Get("after")
	if _, err := inventory.DecodeCursor(after); err != nil {
		return "", errInvalidQuery
	}
	var limit int
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxAssessmentLimit {
			return "", errInvalidQuery
		}
		limit = n
	}
	wire := map[string]any{"search_b64": base64.RawURLEncoding.EncodeToString([]byte(search)), "source_b64": base64.RawURLEncoding.EncodeToString([]byte(source)), "limit": limit}
	if len(after) <= inventory.MaxNameLen {
		wire["after_b64"] = base64.RawURLEncoding.EncodeToString([]byte(after))
	} else {
		wire["after"] = after // Opaque tokens contain only JSON-safe ASCII.
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return "", errInvalidQuery
	}
	return string(payload), nil
}

// inventoryRefresh re-collects the inventory. The request body must be empty:
// the collector set is fixed at compile time and nothing can name a target.
func (s *Server) inventoryRefresh(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64)
	var body map[string]any
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if len(body) != 0 {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	s.assessmentCallStatus(w, r, "inventory_refresh", "", http.StatusConflict)
}

// settingsRead proxies a settings read.
func (s *Server) settingsRead(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	s.assessmentCall(w, r, "settings_get", "")
}

// settingsUpdate validates a preference batch at the boundary and forwards it.
// The body is size-bounded, must be a JSON object, and may name only the
// reviewed preference keys; the core validates the values again and persists
// them transactionally.
func (s *Server) settingsUpdate(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if len(body) == 0 {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	for key := range body {
		switch key {
		case "assess_enabled", "assess_interval", "assess_max_runs", "theme":
		default:
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
	}
	// Re-encode so only the allowlisted keys reach the core, regardless of
	// whitespace or duplicate keys in the original body.
	encoded, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	s.assessmentCall(w, r, "settings_update", string(encoded))
}
