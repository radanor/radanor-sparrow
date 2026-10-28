package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/auth"
	"github.com/radanor/radanor-shield/internal/ipc"
)

func sessionRequest(s *Server, method, path, origin, id, csrf, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, s.origin+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Origin", origin)
	r.Header.Set("X-CSRF-Token", csrf)
	if id != "" {
		r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: id})
	}
	w := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(w, r)
	return w
}

func securityServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(t.TempDir(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.listener.Close() })
	return s
}

func TestScanRecoveryDispatchesExactlyOnce(t *testing.T) {
	s := securityServer(t)
	oldID, oldToken, err := s.sessions.Create()
	if err != nil {
		t.Fatal(err)
	}
	s.sessions.Delete(oldID)
	id, _, err := s.sessions.Create()
	if err != nil {
		t.Fatal(err)
	}
	var dispatched, accepted atomic.Int32
	s.coreCall = func(_ context.Context, operation, value string) (ipc.Response, error) {
		if operation != "assessment_scan" || value != "" {
			t.Error("unexpected core operation")
		}
		dispatched.Add(1)
		accepted.Add(1)
		return ipc.Response{OK: true, Value: `{"accepted":true}`}, nil
	}
	rejected := sessionRequest(s, "POST", "/api/v1/assessment/scan", s.origin, id, oldToken, "{}")
	if rejected.Code != 401 || dispatched.Load() != 0 {
		t.Fatal("unauthorized scan reached core boundary")
	}
	recovery := sessionRequest(s, "POST", "/api/v1/session/csrf", s.origin, id, "", "{}")
	var payload struct {
		Token string `json:"csrf_token"`
	}
	if recovery.Code != 200 || json.Unmarshal(recovery.Body.Bytes(), &payload) != nil || dispatched.Load() != 0 {
		t.Fatal("recovery failed or dispatched a scan")
	}
	retry := sessionRequest(s, "POST", "/api/v1/assessment/scan", s.origin, id, payload.Token, "{}")
	if retry.Code != 200 || dispatched.Load() != 1 || accepted.Load() != 1 {
		t.Fatal("recovery retry did not accept exactly one scan")
	}
}

func TestCSRFRecoveryRejectionMatrix(t *testing.T) {
	for _, name := range []string{"missing", "unknown", "logout", "idle-expired", "absolute-expired", "wrong-origin", "missing-origin", "GET", "HEAD", "PUT", "DELETE", "OPTIONS", "query", "fields", "trailing-json", "trailing-garbage", "oversized"} {
		t.Run(name, func(t *testing.T) {
			s := securityServer(t)
			now := time.Now()
			s.sessions = auth.NewSessionsWithClock(func() time.Time { return now })
			id, csrf, err := s.sessions.Create()
			if err != nil {
				t.Fatal(err)
			}
			method, path, origin, body := "POST", "/api/v1/session/csrf", s.origin, "{}"
			want := 401
			switch name {
			case "missing":
				id = ""
			case "unknown":
				id = strings.Repeat("u", 43)
			case "logout":
				s.sessions.Delete(id)
			case "idle-expired":
				now = now.Add(auth.SessionIdleTTL + time.Second)
			case "absolute-expired":
				for i := 0; i < 32; i++ {
					now = now.Add(auth.SessionIdleTTL / 2)
					s.sessions.Validate(id)
				}
				now = now.Add(time.Second)
			case "wrong-origin":
				origin = "https://attacker.invalid"
			case "missing-origin":
				origin = ""
			case "GET", "HEAD", "PUT", "DELETE", "OPTIONS":
				method = name
				want = 404
			case "query":
				path += "?session=ignored"
				want = 400
			case "fields":
				body = `{"session":"ignored"}`
				want = 400
			case "trailing-json":
				body = "{}{}"
				want = 400
			case "trailing-garbage":
				body = "{}invalid"
				want = 400
			case "oversized":
				body = "{}" + strings.Repeat(" ", 65)
				want = 400
			}
			response := sessionRequest(s, method, path, origin, id, csrf, body)
			if response.Code != want {
				t.Fatalf("rejection status=%d, want=%d", response.Code, want)
			}
			if len(response.Result().Cookies()) != 0 || strings.Contains(response.Body.String(), "csrf_token") {
				t.Fatal("rejected recovery disclosed credential or issued cookie")
			}
		})
	}
}

func TestConcurrentTabsRecoverStableSessionBoundToken(t *testing.T) {
	s := securityServer(t)
	id, token, err := s.sessions.Create()
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := s.sessions.Create()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := sessionRequest(s, "POST", "/api/v1/session/csrf", s.origin, id, "", "{}")
			var payload struct {
				Token string `json:"csrf_token"`
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &payload) != nil || payload.Token != token || !s.sessions.ValidateCSRF(id, payload.Token) || s.sessions.ValidateCSRF(other, payload.Token) {
				t.Error("concurrent recovery rotated or crossed session identity")
			}
			if len(response.Result().Cookies()) != 0 || !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
				t.Error("recovery issued cookie or allowed caching")
			}
		}()
	}
	wg.Wait()
	// Possessing a recovered CSRF value alone cannot bootstrap cookie authentication.
	if sessionRequest(s, "POST", "/api/v1/session/csrf", s.origin, "", token, "{}").Code != 401 {
		t.Fatal("CSRF-only authentication bypass")
	}
}
