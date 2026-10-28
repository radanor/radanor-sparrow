package desktop

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestManagedChildHelper(t *testing.T) {
	mode := os.Getenv("RADANOR_CHILD_FIXTURE")
	if mode == "" {
		return
	}
	var gate [1]byte
	if _, err := io.ReadFull(os.Stdin, gate[:]); err != nil || gate[0] != 1 {
		os.Exit(2)
	}
	if mode == "hang" {
		for {
			time.Sleep(time.Second)
		}
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func fixtureCommand(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestManagedChildHelper$")
	cmd.Env = append(os.Environ(), "RADANOR_CHILD_FIXTURE="+mode)
	return cmd
}

func TestManagedChildShutdownAndCrashContainment(t *testing.T) {
	for _, mode := range []string{"graceful", "hang", "supervisor crash"} {
		t.Run(mode, func(t *testing.T) {
			fixture := "hang"
			if mode == "graceful" {
				fixture = "normal"
			}
			child, err := startManaged(fixtureCommand(t, fixture), io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "supervisor crash" {
				// Closing containment/control models loss of supervisor handles.
				child.containment.kill(child.cmd.Process)
				_ = child.control.Close()
			}
			timeout := 100 * time.Millisecond
			if mode == "graceful" {
				timeout = 3 * time.Second
			}
			child.stopWithin(timeout)
			if mode == "graceful" && !child.cmd.ProcessState.Success() {
				t.Fatal("graceful shutdown required force termination")
			}
			select {
			case <-child.done:
			default:
				t.Fatal("managed child survived shutdown")
			}
		})
	}
}

func TestManagedStartupFailure(t *testing.T) {
	if child, err := startManaged(exec.Command(filepath.Join(t.TempDir(), "missing-executable")), io.Discard); err == nil || child != nil {
		t.Fatal("missing executable did not fail startup")
	}
}

func TestDiscoveryNeverAcceptsThirdPartyOrigins(t *testing.T) {
	for _, origin := range []string{"https://127.0.0.1:123", "http://localhost:123", "http://attacker.invalid:123", "http://127.0.0.1:123/path", "http://127.0.0.1:123#ticket=x", "http://user@127.0.0.1:123", "http://127.0.0.1:0", "http://127.0.0.1:65536", "http://127.0.0.1:0123", "http://127.0.0.1:123?x=1"} {
		if validOrigin(origin) {
			t.Fatalf("untrusted origin accepted: %q", origin)
		}
	}
	if !validOrigin("http://127.0.0.1:12345") {
		t.Fatal("valid loopback origin rejected")
	}
}

func TestElevatedExecutionRefusedBeforeStateCreation(t *testing.T) {
	state := filepath.Join(t.TempDir(), "must-not-exist")
	t.Setenv("RADANOR_SPARROW_STATE_DIR", state)
	if err := run(context.Background(), true, func() error { return errors.New("elevated token") }); err == nil {
		t.Fatal("elevated execution accepted")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("refused launch created state")
	}
}

func TestBrowserTicketNeverSentToThirdPartyOrigin(t *testing.T) {
	opened := false
	err := launchDashboard(context.Background(), "https://attacker.invalid", t.TempDir(), nil, func(string) error { opened = true; return nil })
	if err == nil || opened {
		t.Fatal("ticket launch accepted third-party origin")
	}
}
