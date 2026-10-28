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

// Explicit opt-in used only for the requested running-artifact upgrade. This
// follows the normal authenticated Exit workflow; it never kills a process.
func TestLiveGatewayGracefulExitForUpgrade(t *testing.T) {
	if os.Getenv("RADANOR_SPARROW_TEST_UPGRADE_EXIT") != "1" {
		t.Skip("operator-controlled upgrade exit is opt-in")
	}
	origin := os.Getenv("RADANOR_SPARROW_LIVE_ORIGIN")
	cfg, err := config.Load()
	if err != nil || !validOrigin(origin) {
		t.Fatal("invalid live configuration")
	}
	discovery, err := os.ReadFile(filepath.Join(cfg.RuntimeDir, "desktop-origin"))
	if err != nil || string(discovery) != origin {
		t.Fatal("live origin does not match private discovery")
	}
	secret, err := ipc.LoadSecret(ipc.SecretPath(cfg.RuntimeDir))
	if err != nil {
		t.Fatal("private IPC unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ticket, err := ipc.Call(ctx, cfg.RuntimeDir, secret, "issue_ticket", "")
	if err != nil || !ticket.OK {
		t.Fatal("upgrade ticket unavailable")
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	body, _ := json.Marshal(map[string]string{"ticket": ticket.Value})
	r, _ := http.NewRequest("POST", origin+"/api/v1/session/exchange", bytes.NewReader(body))
	r.Header.Set("Origin", origin)
	response, err := client.Do(r)
	if err != nil {
		t.Fatal("upgrade exchange failed")
	}
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&session)
	response.Body.Close()
	if response.StatusCode != 200 || decodeErr != nil || session.CSRF == "" {
		t.Fatal("upgrade exchange refused")
	}
	response, err = client.Get(origin + "/api/v1/assessment/status")
	if err != nil {
		t.Fatal("assessment status unavailable")
	}
	var status struct {
		Running bool `json:"running"`
	}
	decodeErr = json.NewDecoder(response.Body).Decode(&status)
	response.Body.Close()
	if response.StatusCode != 200 || decodeErr != nil || status.Running {
		t.Fatal("refusing to exit while assessment status is unavailable or a scan is active")
	}
	r, _ = http.NewRequest("POST", origin+"/api/v1/application/exit", nil)
	r.Header.Set("Origin", origin)
	r.Header.Set("X-CSRF-Token", session.CSRF)
	response, err = client.Do(r)
	if err != nil {
		t.Fatal("authenticated exit failed")
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("authenticated exit refused")
	}
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(filepath.Join(cfg.RuntimeDir, "desktop-origin")); os.IsNotExist(err) {
			t.Log("Authenticated upgrade exit completed; no active scan was interrupted.")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("application did not finish graceful exit")
}
