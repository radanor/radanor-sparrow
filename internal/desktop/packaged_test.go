package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/gateway"
	"github.com/radanor/radanor-shield/internal/ipc"
	"github.com/radanor/radanor-shield/internal/lifecycle"
)

// Opt-in real distribution-binary test. Uses only isolated application data;
// does not trigger assessments or alter native security configuration.
func TestPackagedApplicationLifecycle(t *testing.T) {
	binary := os.Getenv("RADANOR_SPARROW_TEST_BINARY")
	if binary == "" {
		t.Skip("set RADANOR_SPARROW_TEST_BINARY to the built distribution artifact")
	}
	if gateway.ValidateNotElevated() != nil {
		t.Skip("real lifecycle test requires normal-user execution")
	}
	dir := t.TempDir()
	run := filepath.Join(dir, "run")
	originFile := filepath.Join(run, "desktop-origin")
	env := append(os.Environ(), "RADANOR_SPARROW_STATE_DIR="+dir, "RADANOR_SPARROW_RUNTIME_DIR="+run)
	var command *exec.Cmd
	var done chan error
	clientProbe := &http.Client{Timeout: time.Second}
	browserOnce := os.Getenv("RADANOR_SPARROW_TEST_BROWSER_ONCE") == "1"
	start := func() string {
		args := []string{"--no-browser"}
		if browserOnce {
			args = nil
			browserOnce = false
		}
		command = exec.Command(binary, args...)
		command.Env = env
		configureChild(command)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		done = make(chan error, 1)
		go func(cmd *exec.Cmd, result chan error) { result <- cmd.Wait() }(command, done)
		deadline := time.After(30 * time.Second)
		for {
			select {
			case err := <-done:
				t.Fatalf("packaged application exited during startup: %v", err)
			case <-deadline:
				_ = command.Process.Kill()
				t.Fatal("packaged application was not ready")
			case <-time.After(100 * time.Millisecond):
				value, err := os.ReadFile(originFile)
				if err == nil && validOrigin(string(value)) {
					log, err := os.ReadFile(filepath.Join(dir, "application.log"))
					if err != nil || !bytes.Contains(log, []byte("application started")) {
						continue
					}
					response, err := clientProbe.Get(string(value) + "/")
					if err == nil {
						response.Body.Close()
						if response.StatusCode == 200 {
							return string(value)
						}
					}
				}
			}
		}
	}
	t.Cleanup(func() {
		if command != nil && command.Process != nil {
			_ = command.Process.Kill()
		}
	})
	client := &http.Client{Timeout: 5 * time.Second}
	login := func(origin string) string {
		secret, err := ipc.LoadSecret(ipc.SecretPath(run))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ticket, err := ipc.Call(ctx, run, secret, "issue_ticket", "")
		if err != nil || !ticket.OK {
			t.Fatal("ticket issuance failed")
		}
		jar, _ := cookiejar.New(nil)
		client.Jar = jar
		body, _ := json.Marshal(map[string]string{"ticket": ticket.Value})
		r, _ := http.NewRequest("POST", origin+"/api/v1/session/exchange", bytes.NewReader(body))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var session struct {
			CSRF string `json:"csrf_token"`
		}
		if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&session) != nil || session.CSRF == "" {
			t.Fatal("ticket exchange failed")
		}
		replay, _ := http.NewRequest("POST", origin+"/api/v1/session/exchange", bytes.NewReader(body))
		replay.Header.Set("Origin", origin)
		replayed, err := client.Do(replay)
		if err != nil {
			t.Fatal(err)
		}
		replayed.Body.Close()
		if replayed.StatusCode != 401 {
			t.Fatal("single-use launch ticket replay accepted")
		}
		return session.CSRF
	}
	post := func(origin, path, csrf string, body []byte) {
		r, _ := http.NewRequest("POST", origin+path, bytes.NewReader(body))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("%s returned %d", path, response.StatusCode)
		}
	}
	waitForCoreExit := func(timeout time.Duration) {
		secret, err := ipc.LoadSecret(ipc.SecretPath(run))
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(timeout)
		for {
			// IPC becomes unavailable as soon as the listener closes, before the
			// process necessarily finishes cleanup and releases its instance lock.
			// A restart is safe only after both conditions are true.
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			response, callErr := ipc.Call(ctx, run, secret, "health", "")
			cancel()
			if callErr != nil || !response.OK {
				lock, lockErr := lifecycle.Acquire(run, "core")
				if lockErr == nil {
					_ = lock.Close()
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("managed core did not terminate and release its lifecycle lock")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	stop := func(origin, csrf string) {
		post(origin, "/api/v1/application/exit", csrf, []byte("{}"))
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("unclean application exit: %v", err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("application failed to exit")
		}
		if _, err := os.Stat(originFile); !os.IsNotExist(err) {
			t.Fatal("discovery state survived exit")
		}
		waitForCoreExit(5 * time.Second)
	}
	origin := start()
	response, err := client.Get(origin + "/api/v1/settings")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("anonymous API access allowed")
	}
	response, err = client.Get(origin + "/")
	if err != nil {
		t.Fatal(err)
	}
	assets, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !bytes.Contains(assets, []byte("/assets/")) {
		t.Fatal("bundled frontend missing")
	}
	resources := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindAllStringSubmatch(string(assets), -1)
	if len(resources) < 2 {
		t.Fatal("frontend script/style references missing")
	}
	for _, resource := range resources {
		response, err := client.Get(origin + resource[1])
		if err != nil {
			t.Fatal(err)
		}
		content, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || len(content) == 0 {
			t.Fatal("embedded frontend resource unavailable")
		}
	}
	csrf := login(origin)
	duplicate := exec.Command(binary, "--no-browser")
	duplicate.Env = env
	configureChild(duplicate)
	if err := duplicate.Run(); err != nil {
		t.Fatalf("duplicate launch: %v", err)
	}
	post(origin, "/api/v1/settings", csrf, []byte(`{"theme":"dark"}`))
	stop(origin, csrf)
	origin = start()
	csrf = login(origin)
	response, err = client.Get(origin + "/api/v1/settings")
	if err != nil {
		t.Fatal(err)
	}
	persisted, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !bytes.Contains(persisted, []byte(`"theme":"dark"`)) {
		t.Fatal("preference did not survive restart")
	}
	stop(origin, csrf)
	// A hard supervisor crash must not orphan the core. Discovery may remain,
	// but the next owner must replace it and safely reopen the persistent DB.
	origin = start()
	_ = login(origin)
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("crashed supervisor did not exit")
	}
	waitForCoreExit(5 * time.Second)
	origin = start()
	csrf = login(origin)
	stop(origin, csrf)
}

func TestPackagedStartupFailureCleansUp(t *testing.T) {
	binary := os.Getenv("RADANOR_SPARROW_TEST_BINARY")
	if binary == "" {
		t.Skip("set RADANOR_SPARROW_TEST_BINARY to the built artifact")
	}
	if gateway.ValidateNotElevated() != nil {
		t.Skip("normal-user execution required")
	}
	dir := t.TempDir()
	run := filepath.Join(dir, "run")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--no-browser")
	cmd.Env = append(os.Environ(), "RADANOR_SPARROW_STATE_DIR="+dir, "RADANOR_SPARROW_RUNTIME_DIR="+run, "RADANOR_SPARROW_ASSESS_INTERVAL=invalid")
	configureChild(cmd)
	if err := cmd.Run(); err == nil || ctx.Err() != nil {
		t.Fatal("startup failure did not exit cleanly within its bound")
	}
	if _, err := os.Stat(filepath.Join(run, "desktop-origin")); !os.IsNotExist(err) {
		t.Fatal("failed startup published a dashboard")
	}
	secret, err := ipc.LoadSecret(ipc.SecretPath(run))
	if err != nil {
		t.Fatal(err)
	}
	if response, err := ipc.Call(ctx, run, secret, "health", ""); err == nil && response.OK {
		t.Fatal("failed-startup core was orphaned")
	}
}
