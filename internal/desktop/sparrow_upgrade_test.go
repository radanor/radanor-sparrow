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
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/gateway"
	"github.com/radanor/radanor-shield/internal/ipc"
)

// TestPackagedSparrowUpgradesAnExistingShieldProfile is the end-to-end form of
// the promise in the private engineering record: an installation that was
// created by Radanor Shield under the pre-rebrand environment names is opened
// in place by the renamed executable, and the history the user can see survives.
//
// Step one runs the artifact with only RADANOR_SHIELD_STATE_DIR and
// RADANOR_SHIELD_RUNTIME_DIR set, which is how an existing launcher or service
// unit invokes it, and records a preference. Step two runs the same artifact at
// the same profile with the current RADANOR_SPARROW_* names and reads the
// preference back. A renamed private path would instead show a fresh profile.
//
// Opt-in: set RADANOR_SPARROW_TEST_BINARY to the built distribution artifact.
func TestPackagedSparrowUpgradesAnExistingShieldProfile(t *testing.T) {
	binary := os.Getenv("RADANOR_SPARROW_TEST_BINARY")
	if binary == "" {
		t.Skip("set RADANOR_SPARROW_TEST_BINARY to the built distribution artifact")
	}
	if gateway.ValidateNotElevated() != nil {
		t.Skip("the packaged upgrade test requires normal-user execution")
	}
	dir := t.TempDir()
	run := filepath.Join(dir, "run")
	originFile := filepath.Join(run, "desktop-origin")
	applicationLog := filepath.Join(dir, "application.log")

	// start returns the loopback origin and a wait function. Waiting for the
	// process, not for the discovery file, is what makes the restart safe: the
	// launcher removes its discovery record before it releases the instance
	// lock, so a second launch that only watched that file would be treated as
	// a duplicate and exit without doing anything.
	start := func(environment []string) (string, func()) {
		t.Helper()
		command := exec.Command(binary, "--no-browser")
		command.Env = environment
		configureChild(command)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		// The result is buffered and the channel is then closed, so a receive that
		// has already consumed the exit status still unblocks instead of hanging.
		done := make(chan error, 1)
		go func() { done <- command.Wait(); close(done) }()
		wait := func() {
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("unclean application exit: %v", err)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("the application did not finish exiting")
			}
		}
		t.Cleanup(func() {
			select {
			case <-done:
			default:
				_ = command.Process.Kill()
				<-done
			}
		})
		deadline := time.After(30 * time.Second)
		for {
			select {
			case err := <-done:
				t.Fatalf("the packaged application exited during startup: %v", err)
			case <-deadline:
				t.Fatal("the packaged application was not ready")
			case <-time.After(100 * time.Millisecond):
				value, err := os.ReadFile(originFile)
				if err != nil || !validOrigin(string(value)) {
					continue
				}
				log, err := os.ReadFile(applicationLog)
				if err != nil || !bytes.Contains(log, []byte("application started")) {
					continue
				}
				response, err := (&http.Client{Timeout: time.Second}).Get(string(value) + "/")
				if err != nil {
					continue
				}
				response.Body.Close()
				if response.StatusCode != 200 {
					continue
				}
				return string(value), wait
			}
		}
	}
	client := &http.Client{Timeout: 5 * time.Second}
	login := func(origin string) string {
		t.Helper()
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
		request, _ := http.NewRequest("POST", origin+"/api/v1/session/exchange", bytes.NewReader(body))
		request.Header.Set("Origin", origin)
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
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
		return session.CSRF
	}
	stop := func(origin, csrf string, wait func()) {
		t.Helper()
		request, _ := http.NewRequest("POST", origin+"/api/v1/application/exit", nil)
		request.Header.Set("Origin", origin)
		request.Header.Set("X-CSRF-Token", csrf)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("the authenticated exit returned %d", response.StatusCode)
		}
		wait()
		if _, err := os.Stat(originFile); !os.IsNotExist(err) {
			t.Error("the discovery record survived a graceful exit")
		}
	}
	theme := func(origin string) string {
		t.Helper()
		response, err := client.Get(origin + "/api/v1/settings")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		payload, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != 200 {
			t.Fatalf("the settings read returned %d", response.StatusCode)
		}
		var document struct {
			Effective struct {
				Theme string `json:"theme"`
			} `json:"effective"`
			Schema string `json:"schema"`
		}
		if err := json.Unmarshal(payload, &document); err != nil {
			t.Fatal(err)
		}
		if document.Schema != "shield.settings/v1" {
			t.Errorf("settings schema = %q, want the unrebranded wire contract", document.Schema)
		}
		return document.Effective.Theme
	}
	setTheme := func(origin, csrf, value string) {
		t.Helper()
		request, _ := http.NewRequest("POST", origin+"/api/v1/settings", bytes.NewReader([]byte(`{"theme":"`+value+`"}`)))
		request.Header.Set("Origin", origin)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", csrf)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("the settings update returned %d", response.StatusCode)
		}
	}

	// Step 1: an existing pre-rebrand installation, addressed only by its own
	// environment variable names.
	legacyOrigin, legacyWait := start(append(os.Environ(),
		"RADANOR_SHIELD_STATE_DIR="+dir,
		"RADANOR_SHIELD_RUNTIME_DIR="+run))
	legacyCSRF := login(legacyOrigin)
	if got := theme(legacyOrigin); got == "dark" {
		t.Fatalf("the fresh profile already reported theme %q; the test could not tell an upgrade from a coincidence", got)
	}
	setTheme(legacyOrigin, legacyCSRF, "dark")
	stop(legacyOrigin, legacyCSRF, legacyWait)

	database := filepath.Join(dir, "monitoring.db")
	before, err := os.Stat(database)
	if err != nil {
		t.Fatalf("the pre-rebrand environment names did not select the profile: %v", err)
	}
	t.Logf("Shield-named profile created: %s (%d bytes)", database, before.Size())

	// Step 2: the renamed product, addressed by the new names, at that profile.
	origin, wait := start(append(os.Environ(),
		"RADANOR_SPARROW_STATE_DIR="+dir,
		"RADANOR_SPARROW_RUNTIME_DIR="+run))
	csrf := login(origin)
	if got := theme(origin); got != "dark" {
		t.Errorf("stored theme after the rename = %q, want %q: the upgrade did not preserve history", got, "dark")
	}
	stop(origin, csrf, wait)

	// The upgrade must not fork the profile: one database, and nothing new
	// created beside it.
	databases, err := filepath.Glob(filepath.Join(dir, "*.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(databases) != 1 || databases[0] != database {
		t.Errorf("profile contains %v, want only %s", databases, database)
	}
	after, err := os.Stat(database)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() < before.Size() {
		t.Errorf("database shrank from %d to %d bytes across the upgrade", before.Size(), after.Size())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "run" {
			t.Errorf("the upgrade created an unexpected directory %q; the profile was forked", entry.Name())
		}
	}
	t.Log("Sparrow reused the Shield profile in place: one database, preference intact")
}
