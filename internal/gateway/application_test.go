package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/radanor/radanor-shield/internal/auth"
)

func TestApplicationExitRequiresSessionOriginAndCSRF(t *testing.T) {
	s, err := New(t.TempDir(), []byte("fixture-secret"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	id, csrf, err := s.sessions.Create()
	if err != nil {
		t.Fatal(err)
	}
	exited := false
	s.SetApplicationExit(func() { exited = true })
	for _, session := range []string{"", id} {
		r := httptest.NewRequest("GET", s.origin+"/api/v1/application/status", nil)
		r.RemoteAddr = "127.0.0.1:12345"
		if session != "" {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: session})
		}
		w := httptest.NewRecorder()
		s.http.Handler.ServeHTTP(w, r)
		if session == "" && w.Code != 401 || session != "" && w.Code != 200 {
			t.Fatal("lifecycle status authentication failed")
		}
	}
	for _, entry := range []struct {
		name, session, origin, token string
		want                         int
	}{
		{"anonymous", "", s.origin, csrf, 401},
		{"missing CSRF", id, s.origin, "", 401},
		{"foreign origin", id, "https://attacker.invalid", csrf, 401},
		{"authorized", id, s.origin, csrf, 200},
	} {
		t.Run(entry.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", s.origin+"/api/v1/application/exit", nil)
			r.RemoteAddr = "127.0.0.1:12345"
			r.Header.Set("Origin", entry.origin)
			r.Header.Set("X-CSRF-Token", entry.token)
			if entry.session != "" {
				r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: entry.session})
			}
			w := httptest.NewRecorder()
			s.http.Handler.ServeHTTP(w, r)
			if w.Code != entry.want || exited != (entry.want == 200) {
				t.Fatalf("status=%d exited=%v", w.Code, exited)
			}
		})
	}
}

func TestDeveloperGatewayCannotExitUnmanagedCore(t *testing.T) {
	s, err := New(t.TempDir(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	w := httptest.NewRecorder()
	s.exitApplication(w, httptest.NewRequest("POST", "/", nil))
	if w.Code != http.StatusConflict {
		t.Fatal("unmanaged exit was permitted")
	}
}
