package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/radanor/radanor-shield/internal/auth"
)

func TestRotatedCookieWithOldTabCSRFCanRecover(t *testing.T) {
	s, err := New(t.TempDir(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	oldID, oldCSRF, err := s.sessions.Create()
	if err != nil {
		t.Fatal(err)
	}
	// Mirrors successful ticket exchange: invalidate the old session, set the
	// shared browser cookie to a new session, leave the old tab's RAM unchanged.
	s.sessions.Delete(oldID)
	id, _, err := s.sessions.Create()
	if err != nil {
		t.Fatal(err)
	}
	request := func(path, origin, cookie, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", s.origin+path, nil)
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", token)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
		}
		w := httptest.NewRecorder()
		s.http.Handler.ServeHTTP(w, r)
		return w
	}
	if !s.sessions.Validate(id) || s.sessions.ValidateCSRF(id, oldCSRF) {
		t.Fatal("fixture did not reproduce a valid cookie with mismatched CSRF")
	}
	s.SetApplicationExit(func() {})
	if response := request("/api/v1/application/exit", s.origin, id, oldCSRF); response.Code != 401 {
		t.Fatal("mismatched CSRF was not rejected")
	}
	response := request("/api/v1/session/csrf", s.origin, id, oldCSRF)
	if response.Code != 200 {
		t.Fatalf("valid current session cannot recover its tab token: status=%d", response.Code)
	}
	var token struct {
		CSRF string `json:"csrf_token"`
	}
	if json.Unmarshal(response.Body.Bytes(), &token) != nil || token.CSRF == "" {
		t.Fatal("session recovery did not return a token")
	}
	if response := request("/api/v1/application/exit", s.origin, id, token.CSRF); response.Code != 200 {
		t.Fatal("recovered cookie/token pair failed")
	}
	for _, entry := range []struct{ origin, cookie string }{{"https://attacker.invalid", id}, {s.origin, ""}, {s.origin, oldID}} {
		if response := request("/api/v1/session/csrf", entry.origin, entry.cookie, ""); response.Code == 200 {
			t.Fatal("session recovery bypassed origin/session validation")
		}
	}
}
