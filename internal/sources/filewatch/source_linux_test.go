//go:build linux

package filewatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/monitoring"
)

type testReporter struct {
	healthy chan struct{}
	mu      sync.Mutex
	gaps    int
}

func (r *testReporter) ConfirmHealthy() {
	select {
	case <-r.healthy:
	default:
		close(r.healthy)
	}
}
func (r *testReporter) ReportGap(time.Time, time.Time, string) { r.mu.Lock(); r.gaps++; r.mu.Unlock() }

func TestRealInotifyLifecycleAndEvents(t *testing.T) {
	dir := t.TempDir()
	source, err := New([]Root{{Label: "disposable", Path: dir}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	report := &testReporter{healthy: make(chan struct{})}
	events := make(chan monitoring.Event, 128)
	done := make(chan error, 1)
	go func() {
		done <- source.Run(ctx, nil, func(_ context.Context, e monitoring.Event) error { events <- e; return nil }, report)
	}()
	select {
	case <-report.healthy:
	case <-time.After(time.Second):
		t.Fatal("source did not become healthy")
	}
	path := filepath.Join(dir, "config.txt")
	if err := os.WriteFile(path, []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(dir, "renamed.txt")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(renamed); err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{"file.created": false, "file.modified": false, "file.renamed_from": false, "file.renamed_to": false, "file.deleted": false}
	deadline := time.After(2 * time.Second)
	for {
		complete := true
		for _, seen := range wanted {
			complete = complete && seen
		}
		if complete {
			break
		}
		select {
		case e := <-events:
			if e.Source != SourceID || e.Platform != "linux" || e.Metadata["root"] != "disposable" {
				t.Fatalf("bad event: %#v", e)
			}
			wanted[e.Type] = true
		case <-deadline:
			t.Fatalf("missing events: %v", wanted)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("source did not shut down")
	}
}

func TestInotifyEventClassification(t *testing.T) {
	cases := map[uint32]string{0x00000100: "file.created", 0x00000002: "file.modified", 0x00000200: "file.deleted", 0x00000040: "file.renamed_from", 0x00000080: "file.renamed_to"}
	for mask, want := range cases {
		if got := linuxEventType(mask); got != want {
			t.Fatalf("mask %#x: got %s want %s", mask, got, want)
		}
	}
}

func TestInotifyProlongedIdle(t *testing.T) {
	dir := t.TempDir()
	source, _ := New([]Root{{Label: "idle", Path: dir}})
	ctx, cancel := context.WithCancel(context.Background())
	report := &testReporter{healthy: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- source.Run(ctx, nil, func(context.Context, monitoring.Event) error { return nil }, report)
	}()
	select {
	case <-report.healthy:
	case <-time.After(time.Second):
		t.Fatal("not healthy")
	}
	time.Sleep(500 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idle source did not stop")
	}
}

func BenchmarkInotifyBurst(b *testing.B) {
	dir := b.TempDir()
	source, _ := New([]Root{{Label: "burst", Path: dir}})
	ctx, cancel := context.WithCancel(context.Background())
	report := &testReporter{healthy: make(chan struct{})}
	var created atomic.Uint64
	done := make(chan error, 1)
	go func() {
		done <- source.Run(ctx, nil, func(_ context.Context, e monitoring.Event) error {
			if e.Type == "file.created" {
				created.Add(1)
			}
			return nil
		}, report)
	}()
	<-report.healthy
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("event-%d", i)), []byte("x"), 0600); err != nil {
			b.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for created.Load() < uint64(b.N) && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	b.StopTimer()
	cancel()
	<-done
	if created.Load() < uint64(b.N) {
		b.Fatalf("received %d/%d create events", created.Load(), b.N)
	}
}
