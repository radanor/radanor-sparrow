package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/radanor/radanor-shield/internal/ipc"
)

// errInvalidQuery is returned by the query parsers when a parameter is out of
// bounds or malformed.
var errInvalidQuery = errors.New("invalid query")

// ipcCall proxies one validated operation to the core over the authenticated
// socket.
func ipcCall(r *http.Request, s *Server, operation, value string) (ipc.Response, error) {
	if s.coreCall != nil {
		return s.coreCall(r.Context(), operation, value)
	}
	return ipc.Call(r.Context(), s.runtimeDir, s.secret, operation, value)
}

// CE-5 assessment gateway handlers.
//
// The gateway holds no assessment state and no database: every request is
// validated here and then proxied over the authenticated core IPC socket. Two
// rules are enforced at this boundary:
//
//   - The read endpoints accept only bounded, allowlisted query parameters.
//     A fingerprint is validated as 64 hex characters and a run id as a
//     positive integer, so no arbitrary string ever reaches the core.
//   - The two state-changing endpoints (start and cancel) are POST-only and are
//     registered behind requireSession(..., csrf=true), so they require both a
//     same-origin request and the session's CSRF token.
//
// No endpoint accepts a command, a filesystem path, or a collector list.

const maxAssessmentLimit = 50

func (s *Server) assessmentStatus(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	s.assessmentCall(w, r, "assessment_status", "")
}

func (s *Server) assessmentDevice(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	s.readCall(w, r, "device", "assessment_device", "")
}

func (s *Server) assessmentChecks(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	s.assessmentCall(w, r, "assessment_checks", "")
}

func (s *Server) assessmentFindings(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for key := range query {
		if key != "lifecycle" && key != "before" && key != "limit" {
			http.Error(w, "invalid query", http.StatusBadRequest)
			return
		}
	}
	lifecycle := query.Get("lifecycle")
	switch lifecycle {
	case "", "new", "continuing", "resolved", "reopened", "superseded":
	default:
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	before, err := parseCursor(query.Get("before"))
	if err != nil {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	limit, err := parseLimit(query.Get("limit"))
	if err != nil {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	payload, err := json.Marshal(map[string]any{"lifecycle": lifecycle, "before": before, "limit": limit})
	if err != nil {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	s.assessmentCall(w, r, "assessment_findings", string(payload))
}

func (s *Server) assessmentFinding(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for key := range query {
		if key != "fingerprint" {
			http.Error(w, "invalid query", http.StatusBadRequest)
			return
		}
	}
	fingerprint := query.Get("fingerprint")
	if !validFingerprint(fingerprint) {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	payload, err := json.Marshal(map[string]any{"fingerprint": fingerprint})
	if err != nil {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	s.assessmentCall(w, r, "assessment_finding", string(payload))
}

func (s *Server) assessmentRuns(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for key := range query {
		if key != "before" && key != "limit" {
			http.Error(w, "invalid query", http.StatusBadRequest)
			return
		}
	}
	before, err := parseCursor(query.Get("before"))
	if err != nil {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	limit, err := parseLimit(query.Get("limit"))
	if err != nil {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	payload, err := json.Marshal(map[string]any{"before": before, "limit": limit})
	if err != nil {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	s.assessmentCall(w, r, "assessment_runs", string(payload))
}

func (s *Server) assessmentRun(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for key := range query {
		if key != "run_id" {
			http.Error(w, "invalid query", http.StatusBadRequest)
			return
		}
	}
	runID, err := strconv.ParseInt(query.Get("run_id"), 10, 64)
	if err != nil || runID <= 0 {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	payload, err := json.Marshal(map[string]any{"run_id": runID})
	if err != nil {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	s.assessmentCall(w, r, "assessment_run", string(payload))
}

// assessmentScan starts one manual scan. The request body must be empty: there
// is no parameter that could name a target, so a scan always covers exactly the
// fixed collector set.
func (s *Server) assessmentScan(w http.ResponseWriter, r *http.Request) {
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
	s.assessmentCallStatus(w, r, "assessment_scan", "", http.StatusConflict)
}

// assessmentCancel stops the active scan. Like the scan endpoint it takes no
// parameters, and its body is validated on exactly the same terms: a body that
// is not empty (or that names any field) is refused, so a caller cannot smuggle
// a target or option into a state-changing call. Previously this endpoint
// proxied straight through without reading the body at all.
func (s *Server) assessmentCancel(w http.ResponseWriter, r *http.Request) {
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
	s.assessmentCallStatus(w, r, "assessment_cancel", "", http.StatusConflict)
}

// Subsystem names shared by a read's operational log line and the fixed failure
// message it reports to the browser.
const (
	subsystemAssessment = "assessment"
	subsystemInventory  = "inventory"
)

// assessmentCall proxies a read and reports a service error when the core
// cannot answer, rather than inventing an empty result.
func (s *Server) assessmentCall(w http.ResponseWriter, r *http.Request, operation, value string) {
	s.readCall(w, r, subsystemAssessment, operation, value)
}

// readCall keeps the security and refusal mapping shared while naming the
// subsystem accurately in operational logs and in the failure it reports, so an
// inventory outage is not announced to the browser as an assessment outage.
func (s *Server) readCall(w http.ResponseWriter, r *http.Request, subsystem, operation, value string) {
	resp, err := ipcCall(r, s, operation, value)
	if err != nil || !resp.OK {
		if resp.Error != "" {
			// A well-formed refusal from the core is a client error; an
			// infrastructure failure (storage unavailable) is not.
			if isClientRefusal(resp.Error) {
				http.Error(w, sanitizeCoreError(resp.Error), http.StatusBadRequest)
				return
			}
			s.logger.Warn(subsystem+" read failed", "operation", operation, "reason", sanitizeCoreError(resp.Error))
			http.Error(w, readFailureMessage(subsystem), http.StatusServiceUnavailable)
			return
		}
		s.logger.Warn(subsystem+" read failed", "operation", operation)
		http.Error(w, readFailureMessage(subsystem), http.StatusServiceUnavailable)
		return
	}
	writeJSONRaw(w, http.StatusOK, resp.Value)
}

// readFailureMessage is the fixed, detail-free body for a failed subsystem
// read. Only these constants ever reach the browser: never the core's error
// text, a socket path or an internal exception.
func readFailureMessage(subsystem string) string {
	switch subsystem {
	case subsystemInventory:
		return "inventory data unavailable"
	default:
		return "assessment data unavailable"
	}
}

// assessmentCallStatus is the same as assessmentCall but maps a refusal (for
// example "a scan is already in progress") onto a caller-actionable status.
func (s *Server) assessmentCallStatus(w http.ResponseWriter, r *http.Request, operation, value string, conflictStatus int) {
	resp, err := ipcCall(r, s, operation, value)
	if err != nil {
		s.logger.Warn("assessment operation failed", "operation", operation)
		http.Error(w, "assessment service unavailable", http.StatusServiceUnavailable)
		return
	}
	if !resp.OK {
		switch {
		case resp.Error == "a scan is already in progress" || resp.Error == "no scan is in progress":
			http.Error(w, sanitizeCoreError(resp.Error), conflictStatus)
		case isClientRefusal(resp.Error):
			http.Error(w, sanitizeCoreError(resp.Error), http.StatusBadRequest)
		default:
			s.logger.Warn("assessment operation failed", "operation", operation, "reason", sanitizeCoreError(resp.Error))
			http.Error(w, "assessment service unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	writeJSONRaw(w, http.StatusOK, resp.Value)
}

// sanitizeCoreError passes only a short, known-shape message back to the
// browser so an unexpected internal error cannot leak detail.
func sanitizeCoreError(msg string) string {
	if len(msg) > 128 {
		return "request refused"
	}
	return msg
}

// isClientRefusal reports whether a core error describes a bad request from the
// caller (as opposed to an infrastructure failure), so it is not misreported as
// a service outage.
//
// The core follows one convention: a refusal the caller caused starts with
// "invalid ". That keeps this mapping from having to grow with every new
// preference or query parameter, and it keeps the interesting judgement — is
// this the caller's fault or ours? — in one place. The remaining cases are the
// not-found and cursor responses, which predate the convention.
func isClientRefusal(msg string) bool {
	return strings.HasPrefix(msg, "invalid ") || msg == "finding not found" || msg == "run not found" ||
		msg == "page out of bounds"
}

func parseCursor(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, errInvalidQuery
	}
	return n, nil
}

func parseLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxAssessmentLimit {
		return 0, errInvalidQuery
	}
	return n, nil
}

func validFingerprint(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
