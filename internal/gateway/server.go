package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/radanor/radanor-shield/internal/auth"
	"github.com/radanor/radanor-shield/internal/branding"
	"github.com/radanor/radanor-shield/internal/ipc"
)

const maxBody = 4096

type Server struct {
	http       *http.Server
	listener   net.Listener
	origin     string
	sessions   *auth.Sessions
	runtimeDir string
	secret     []byte
	logger     *slog.Logger
	exit       func()
	// Optional in-package test seam; production always uses authenticated IPC.
	coreCall func(context.Context, string, string) (ipc.Response, error)
}

func New(runtimeDir string, secret []byte, logger *slog.Logger) (*Server, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	s := &Server{listener: l, origin: "http://127.0.0.1:" + strconv.Itoa(port), sessions: auth.NewSessions(), runtimeDir: runtimeDir, secret: secret, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/session/exchange", s.exchange)
	mux.HandleFunc("POST /api/v1/session/csrf", s.requireSession(s.sessionCSRF, false))
	mux.HandleFunc("POST /api/v1/session/logout", s.requireSession(s.logout, true))
	mux.HandleFunc("POST /api/v1/application/exit", s.requireSession(s.exitApplication, true))
	mux.HandleFunc("GET /api/v1/application/status", s.requireSession(s.applicationStatus, false))
	mux.HandleFunc("GET /api/v1/foundation", s.requireSession(s.foundation, false))
	mux.HandleFunc("GET /api/v1/monitoring/status", s.requireSession(s.monitoringStatus, false))
	mux.HandleFunc("GET /api/v1/monitoring/events", s.requireSession(s.monitoringEvents, false))
	mux.HandleFunc("GET /api/v1/monitoring/gaps", s.requireSession(s.monitoringGaps, false))
	// CE-5 assessment access. Reads are session-authenticated; the two
	// operations that change state (start and cancel a scan) additionally
	// require the CSRF token and a same-origin request.
	mux.HandleFunc("GET /api/v1/assessment/status", s.requireSession(s.assessmentStatus, false))
	mux.HandleFunc("GET /api/v1/assessment/device", s.requireSession(s.assessmentDevice, false))
	mux.HandleFunc("GET /api/v1/assessment/checks", s.requireSession(s.assessmentChecks, false))
	mux.HandleFunc("GET /api/v1/assessment/findings", s.requireSession(s.assessmentFindings, false))
	mux.HandleFunc("GET /api/v1/assessment/finding", s.requireSession(s.assessmentFinding, false))
	mux.HandleFunc("GET /api/v1/assessment/runs", s.requireSession(s.assessmentRuns, false))
	mux.HandleFunc("GET /api/v1/assessment/run", s.requireSession(s.assessmentRun, false))
	mux.HandleFunc("POST /api/v1/assessment/scan", s.requireSession(s.assessmentScan, true))
	mux.HandleFunc("POST /api/v1/assessment/cancel", s.requireSession(s.assessmentCancel, true))
	// CE-6 inventory and settings access. Reads are session-authenticated and
	// strictly bounded; the inventory refresh and the preference update are
	// POST-only and require the CSRF token and a same-origin request.
	mux.HandleFunc("GET /api/v1/inventory/status", s.requireSession(s.inventoryStatus, false))
	mux.HandleFunc("GET /api/v1/inventory/software", s.requireSession(s.inventorySoftware, false))
	mux.HandleFunc("GET /api/v1/inventory/endpoints", s.requireSession(s.inventoryEndpoints, false))
	mux.HandleFunc("GET /api/v1/inventory/services", s.requireSession(s.inventoryServices, false))
	mux.HandleFunc("POST /api/v1/inventory/refresh", s.requireSession(s.inventoryRefresh, true))
	mux.HandleFunc("GET /api/v1/settings", s.requireSession(s.settingsRead, false))
	mux.HandleFunc("POST /api/v1/settings", s.requireSession(s.settingsUpdate, true))
	static, _ := fs.Sub(embeddedAssets, "assets")
	mux.Handle("/", http.FileServer(http.FS(static)))
	s.http = &http.Server{Handler: s.security(mux), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	return s, nil
}

func (s *Server) Origin() string { return s.origin }
func (s *Server) Serve() error {
	err := s.http.Serve(s.listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }

// SetApplicationExit is configured before Serve by the desktop supervisor.
// Developer gateways leave it unset and cannot terminate a separate core.
func (s *Server) SetApplicationExit(exit func()) { s.exit = exit }

func (s *Server) applicationStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"managed": s.exit != nil})
}

func (s *Server) exitApplication(w http.ResponseWriter, r *http.Request) {
	if s.exit == nil {
		http.Error(w, "application lifecycle is not managed", http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": "stopping"})
	s.exit()
}

func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if host != strings.TrimPrefix(s.origin, "http://") {
			http.Error(w, "invalid host", http.StatusBadRequest)
			return
		}
		remote, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || net.ParseIP(remote) == nil || !net.ParseIP(remote).IsLoopback() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) validOrigin(r *http.Request) bool { return r.Header.Get("Origin") == s.origin }

func (s *Server) exchange(w http.ResponseWriter, r *http.Request) {
	if !s.validOrigin(r) {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	var body struct {
		Ticket string `json:"ticket"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(&body) != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	resp, err := ipc.Call(r.Context(), s.runtimeDir, s.secret, "redeem_ticket", body.Ticket)
	if err != nil || !resp.OK {
		http.Error(w, "invalid or expired ticket", http.StatusUnauthorized)
		return
	}
	if old, err := r.Cookie(auth.SessionCookie); err == nil {
		s.sessions.Delete(old.Value)
	}
	id, csrf, err := s.sessions.Create()
	if err != nil {
		http.Error(w, "session unavailable", http.StatusServiceUnavailable)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(auth.SessionAbsoluteTTL.Seconds())})
	writeJSON(w, http.StatusOK, map[string]any{"csrf_token": csrf})
}

func (s *Server) requireSession(next http.HandlerFunc, csrf bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(auth.SessionCookie)
		if err != nil {
			s.rejectSession(w, "cookie_missing")
			return
		}
		if !s.sessions.Validate(cookie.Value) {
			s.rejectSession(w, "session_unknown_or_expired")
			return
		}
		if csrf && !s.validOrigin(r) {
			s.rejectSession(w, "origin_mismatch")
			return
		}
		if csrf && !s.sessions.ValidateCSRF(cookie.Value, r.Header.Get("X-CSRF-Token")) {
			s.rejectSession(w, "csrf_mismatch")
			return
		}
		next(w, r)
	}
}

func (s *Server) rejectSession(w http.ResponseWriter, reason string) {
	// Fixed reason codes only: never headers, cookies, tokens, query or body.
	s.logger.Warn("authentication refused", "reason", reason)
	http.Error(w, "authentication required", http.StatusUnauthorized)
}

func (s *Server) sessionCSRF(w http.ResponseWriter, r *http.Request) {
	// This bootstrap endpoint cannot require the token it supplies. A live
	// HttpOnly session plus strict browser Origin is required; no new session,
	// ticket redemption or authentication fallback is permitted.
	if !s.validOrigin(r) {
		s.rejectSession(w, "origin_mismatch")
		return
	}
	if len(r.URL.Query()) != 0 {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64)
	var body map[string]any
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	// Consume the entire bounded body: a valid first object must not conceal
	// trailing JSON, garbage, or input beyond the limit.
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if len(body) != 0 {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	cookie, err := r.Cookie(auth.SessionCookie)
	if err != nil {
		s.rejectSession(w, "cookie_missing")
		return
	}
	token, ok := s.sessions.CSRF(cookie.Value)
	if !ok {
		s.rejectSession(w, "session_unknown_or_expired")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"csrf_token": token})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie(auth.SessionCookie)
	s.sessions.Delete(c.Value)
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) foundation(w http.ResponseWriter, r *http.Request) {
	// The assessment state is derived from a real probe of the core, so the
	// dashboard never shows an availability claim that is not true.
	assessmentState := map[string]string{
		"state":   "AVAILABLE",
		"message": "Posture assessment runs inside the background core while your session is active. It reports what each check could and could not verify; it never claims the computer is secure because nothing suspicious was seen.",
	}
	if resp, err := ipc.Call(r.Context(), s.runtimeDir, s.secret, "assessment_status", ""); err != nil || !resp.OK {
		assessmentState = map[string]string{
			"state":   "UNAVAILABLE",
			"message": "Posture assessment is temporarily unavailable because the background core did not answer.",
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"product": branding.DisplayName,
		"version": branding.Version,
		// The build identity is part of the same About information. Variant says
		// whether this executable came from the release path, and the label is
		// the one sentence naming the exact build — the same string the build
		// log and the Windows version resource carry. Both are read from the
		// metadata injected at build time, so the dashboard cannot show a version
		// or a variant the executable does not have, and both are additive:
		// older clients ignore them and every existing key keeps its meaning.
		"variant":     branding.Variant,
		"build_label": branding.BuildLabel(),
		// Licensing is part of the product's About information. This field is
		// additive: older clients ignore it and every existing key keeps its
		// meaning, so the response stays compatible.
		"license": map[string]string{
			"name":   branding.LicenseName,
			"notice": branding.LicenseNotice,
			"url":    branding.LicenseURL,
		},
		"monitoring": map[string]string{"state": "AVAILABLE", "message": "Live monitoring records genuine local observations from bounded native sources. It does not scan, detect threats, or assess this computer."},
		"assessment": assessmentState,
	})
}

// CE-4 read-only monitoring access. The gateway holds no database: every read
// is proxied over the authenticated core IPC socket, and query bounds are
// validated here before they reach the core.

const maxMonitoringLimit = 50

func (s *Server) monitoringStatus(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	s.monitoringCall(w, r, "monitoring_status", "")
}

func (s *Server) monitoringEvents(w http.ResponseWriter, r *http.Request) {
	limit, before, source, eventType, ok := monitoringQuery(r, true)
	if !ok {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	payload, err := json.Marshal(map[string]any{"limit": limit, "before": before, "source": source, "type": eventType})
	if err != nil {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	s.monitoringCall(w, r, "monitoring_events", string(payload))
}

func (s *Server) monitoringGaps(w http.ResponseWriter, r *http.Request) {
	limit, _, _, _, ok := monitoringQuery(r, false)
	if !ok {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	payload, err := json.Marshal(map[string]any{"limit": limit})
	if err != nil {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	s.monitoringCall(w, r, "monitoring_gaps", string(payload))
}

// monitoringQuery strictly validates bounded read-only query parameters.
func monitoringQuery(r *http.Request, events bool) (limit int, before int64, source, eventType string, ok bool) {
	query := r.URL.Query()
	allowed := map[string]bool{"limit": true}
	if events {
		allowed["before"], allowed["source"], allowed["type"] = true, true, true
	}
	for key := range query {
		if !allowed[key] {
			return 0, 0, "", "", false
		}
	}
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxMonitoringLimit {
			return 0, 0, "", "", false
		}
		limit = n
	}
	if events {
		if raw := query.Get("before"); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n < 0 {
				return 0, 0, "", "", false
			}
			before = n
		}
		if source = query.Get("source"); len(source) > 128 {
			return 0, 0, "", "", false
		}
		if eventType = query.Get("type"); len(eventType) > 128 {
			return 0, 0, "", "", false
		}
	}
	return limit, before, source, eventType, true
}

func (s *Server) monitoringCall(w http.ResponseWriter, r *http.Request, operation, value string) {
	resp, err := ipc.Call(r.Context(), s.runtimeDir, s.secret, operation, value)
	if err != nil || !resp.OK {
		s.logger.Warn("monitoring read failed", "operation", operation)
		http.Error(w, "monitoring data unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSONRaw(w, http.StatusOK, resp.Value)
}

func writeJSONRaw(w http.ResponseWriter, status int, raw string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, raw)
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func ValidateNotElevated() error { return validateNotElevated() }
func (s *Server) String() string { return fmt.Sprintf("gateway(%s)", s.origin) }
