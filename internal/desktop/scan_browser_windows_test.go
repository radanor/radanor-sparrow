//go:build windows

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/branding"
	"github.com/radanor/radanor-shield/internal/config"
	"github.com/radanor/radanor-shield/internal/ipc"
)

func TestPackagedBrowserScanAfterCookieRotation(t *testing.T) {
	binary := os.Getenv("RADANOR_SPARROW_TEST_BINARY")
	if binary == "" || os.Getenv("RADANOR_SPARROW_TEST_SCAN_BROWSER") != "1" {
		t.Skip("native browser scan is explicitly opt-in")
	}
	dir := t.TempDir()
	// Run a standalone copy in a directory containing no legacy executables.
	artifact, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal("cannot read distribution")
	}
	binary = filepath.Join(dir, branding.WindowsExecutable)
	if err := os.WriteFile(binary, artifact, 0600); err != nil {
		t.Fatal("cannot copy isolated distribution")
	}
	run := filepath.Join(dir, "run")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--no-browser")
	cmd.Env = append(os.Environ(), "RADANOR_SPARROW_STATE_DIR="+dir, "RADANOR_SPARROW_RUNTIME_DIR="+run)
	configureChild(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal("cannot start isolated artifact")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	var origin string
	for i := 0; i < 200; i++ {
		value, err := os.ReadFile(filepath.Join(run, "desktop-origin"))
		if err == nil && validOrigin(string(value)) {
			origin = string(value)
			break
		}
		select {
		case <-done:
			t.Fatal("isolated artifact exited during startup")
		default:
		}
		time.Sleep(100 * time.Millisecond)
	}
	if origin == "" {
		t.Fatal("artifact readiness failed")
	}
	secret, err := ipc.LoadSecret(ipc.SecretPath(run))
	if err != nil {
		t.Fatal("private IPC credential unavailable")
	}
	urls := make([]string, 2)
	for i := range urls {
		ticket, err := ipc.Call(ctx, run, secret, "issue_ticket", "")
		if err != nil || !ticket.OK {
			t.Fatal("ticket issuance failed")
		}
		urls[i] = origin + "/#ticket=" + ticket.Value
	}
	browser := filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe")
	if _, err := os.Stat(browser); err != nil {
		t.Fatal("installed Edge is required for this opt-in regression")
	}
	input, _ := json.Marshal(map[string]any{"browser": browser, "urls": urls, "privateDir": dir, "exitApplication": true})
	driver := exec.CommandContext(ctx, "node", filepath.Join("..", "..", "scripts", "validate-scan-browser.mjs"))
	configureChild(driver)
	driver.Stdin = bytes.NewReader(input)
	output, err := driver.Output()
	if err != nil {
		t.Fatal("native browser scan regression failed (no credential diagnostics)")
	}
	var result struct {
		Completed bool `json:"completed"`
		Checks    int  `json:"securityCheckCards"`
		History   bool `json:"historyVisible"`
	}
	if json.Unmarshal(output, &result) != nil || !result.Completed || result.Checks == 0 || !result.History {
		t.Fatal("browser did not show completed results")
	}
	t.Logf("NATIVE browser: two tabs, Scan POST 401 then 200, completed assessment, %d Security Checks cards, completed Scan History row", result.Checks)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("UI exit was unclean")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("UI exit did not stop desktop parent")
	}
	if response, err := ipc.Call(ctx, run, secret, "health", ""); err == nil && response.OK {
		t.Fatal("core survived UI exit")
	}
	if _, err := os.Stat(filepath.Join(run, "desktop-origin")); !os.IsNotExist(err) {
		t.Fatal("UI exit left discovery behind")
	}
	// Restart the same isolated application data and verify the completed run.
	cmd = exec.CommandContext(ctx, binary, "--no-browser")
	cmd.Env = append(os.Environ(), "RADANOR_SPARROW_STATE_DIR="+dir, "RADANOR_SPARROW_RUNTIME_DIR="+run)
	configureChild(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal("history restart failed")
	}
	go func() { done <- cmd.Wait() }()
	origin = ""
	for i := 0; i < 200; i++ {
		value, err := os.ReadFile(filepath.Join(run, "desktop-origin"))
		if err == nil && validOrigin(string(value)) {
			origin = string(value)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if origin == "" {
		t.Fatal("history restart not ready")
	}
	// Gracefully exit only this test's artifact using a separate in-memory jar.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	ticket, err := ipc.Call(ctx, run, secret, "issue_ticket", "")
	if err != nil || !ticket.OK {
		t.Fatal("cleanup ticket unavailable")
	}
	body, _ := json.Marshal(map[string]string{"ticket": ticket.Value})
	r, _ := http.NewRequest("POST", origin+"/api/v1/session/exchange", bytes.NewReader(body))
	r.Header.Set("Origin", origin)
	response, err := client.Do(r)
	if err != nil {
		t.Fatal("cleanup exchange unavailable")
	}
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&session)
	response.Body.Close()
	if response.StatusCode != 200 || decodeErr != nil {
		t.Fatal("cleanup exchange failed")
	}
	response, err = client.Get(origin + "/api/v1/assessment/runs")
	if err != nil {
		t.Fatal("persisted history unavailable")
	}
	var history struct {
		Runs []struct {
			Status string `json:"status"`
		} `json:"runs"`
	}
	decodeErr = json.NewDecoder(response.Body).Decode(&history)
	response.Body.Close()
	if response.StatusCode != 200 || decodeErr != nil || len(history.Runs) != 1 || history.Runs[0].Status != "completed" {
		t.Fatal("completed assessment did not persist exactly once across restart")
	}
	t.Log("NATIVE: browser Exit stopped parent and core; exactly one completed assessment persisted after restart; standalone copy had no legacy binaries")
	r, _ = http.NewRequest("POST", origin+"/api/v1/application/exit", nil)
	r.Header.Set("Origin", origin)
	r.Header.Set("X-CSRF-Token", session.CSRF)
	response, err = client.Do(r)
	if err != nil {
		t.Fatal("cleanup exit failed")
	}
	response.Body.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("unclean test artifact exit")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("test artifact did not exit")
	}
}

// Live verification is separately opt-in: it intentionally adds one read-only
// posture assessment to the operator's normal history and leaves Sparrow running.
func TestLiveBrowserScanAfterCookieRotation(t *testing.T) {
	if os.Getenv("RADANOR_SPARROW_TEST_SCAN_BROWSER") != "1" || os.Getenv("RADANOR_SPARROW_LIVE_ORIGIN") == "" {
		t.Skip("live browser assessment is opt-in")
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	urls := make([]string, 2)
	for i := range urls {
		ticket, err := ipc.Call(ctx, cfg.RuntimeDir, secret, "issue_ticket", "")
		if err != nil || !ticket.OK {
			t.Fatal("live ticket unavailable")
		}
		urls[i] = origin + "/#ticket=" + ticket.Value
	}
	browser := filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe")
	input, _ := json.Marshal(map[string]any{"browser": browser, "urls": urls, "privateDir": cfg.StateDir})
	driver := exec.CommandContext(ctx, "node", filepath.Join("..", "..", "scripts", "validate-scan-browser.mjs"))
	configureChild(driver)
	driver.Stdin = bytes.NewReader(input)
	output, err := driver.Output()
	if err != nil {
		if failure, ok := err.(*exec.ExitError); ok {
			t.Log(string(failure.Stderr))
		}
		t.Fatal("live browser scan failed; no credential diagnostics")
	}
	var result struct {
		Completed bool `json:"completed"`
		Checks    int  `json:"securityCheckCards"`
		History   bool `json:"historyVisible"`
	}
	if json.Unmarshal(output, &result) != nil || !result.Completed || result.Checks == 0 || !result.History {
		t.Fatal("live UI did not show completed results")
	}
	t.Logf("NATIVE live user data: old-tab Scan 401 recovered to 200; completed assessment; %d Security Checks cards with recorded results visible; completed Scan History visible. Sparrow remains running.", result.Checks)
}
