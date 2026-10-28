//go:build windows

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/config"
	"github.com/radanor/radanor-shield/internal/ipc"
)

// Explicit opt-in against the operator's current gateway. Uses an independent
// in-memory cookie jar; never touches browser cookies or persists credentials.
func TestLiveGatewayReproducesRotatedCookieScan401(t *testing.T) {
	origin := os.Getenv("RADANOR_SPARROW_LIVE_ORIGIN")
	if origin == "" {
		t.Skip("live-session reproduction is opt-in")
	}
	if !validOrigin(origin) {
		t.Fatal("invalid live origin")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("cannot load current application configuration")
	}
	discovery, err := os.ReadFile(filepath.Join(cfg.RuntimeDir, "desktop-origin"))
	if err != nil || string(discovery) != origin {
		t.Fatal("live origin is not the private discovered gateway")
	}
	secret, err := ipc.LoadSecret(ipc.SecretPath(cfg.RuntimeDir))
	if err != nil {
		t.Fatal("cannot read private IPC credential")
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	request := func(path, token string, body []byte) *http.Response {
		r, _ := http.NewRequest("POST", origin+path, bytes.NewReader(body))
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", token)
		r.Header.Set("Content-Type", "application/json")
		response, err := client.Do(r)
		if err != nil {
			t.Fatal("live API request failed")
		}
		return response
	}
	login := func() string {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ticket, err := ipc.Call(ctx, cfg.RuntimeDir, secret, "issue_ticket", "")
		if err != nil || !ticket.OK {
			t.Fatal("ticket issuance failed")
		}
		body, _ := json.Marshal(map[string]string{"ticket": ticket.Value})
		response := request("/api/v1/session/exchange", "", body)
		defer response.Body.Close()
		var result struct {
			CSRF string `json:"csrf_token"`
		}
		if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&result) != nil || result.CSRF == "" {
			t.Fatal("ticket exchange failed")
		}
		return result.CSRF
	}
	old := login()
	current := login()
	defer func() { response := request("/api/v1/session/logout", current, nil); response.Body.Close() }()
	response := request("/api/v1/assessment/scan", old, []byte("{}"))
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatalf("old-tab reproduction: POST status=%d", response.StatusCode)
	}
	response, err = client.Get(origin + "/api/v1/assessment/status")
	if err != nil {
		t.Fatal("authenticated GET failed")
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("current-cookie GET status=%d", response.StatusCode)
	}
	t.Log("NATIVE reproduction: old-tab POST=401; same shared-cookie GET=200; correct Origin and both authentication headers present. No scan was started.")
}
