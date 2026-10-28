//go:build windows

package windowseventlog

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/monitoring"
	"github.com/radanor/radanor-shield/internal/storage"
)

type fixtureReporter struct{ gaps int }

func (*fixtureReporter) ConfirmHealthy()                          {}
func (r *fixtureReporter) ReportGap(time.Time, time.Time, string) { r.gaps++ }

func fixtureHeader() eventXML {
	var header eventXML
	header.System.Channel = channel
	header.System.Provider.Name = "Microsoft-Windows-Windows Defender"
	header.System.RecordID = 123
	header.System.EventID = 5007
	header.System.TimeCreated.SystemTime = time.Now().Add(-time.Second).UTC().Format("2006-01-02T15:04:05.0000000Z")
	return header
}

func TestProcessingFailuresReportGapsAndDoNotEmit(t *testing.T) {
	for _, name := range []string{"render", "decode", "bookmark", "empty bookmark", "invalid timestamp", "missing timestamp", "missing record", "wrong channel", "wrong provider"} {
		t.Run(name, func(t *testing.T) {
			header := fixtureHeader()
			report := &fixtureReporter{}
			emitted := 0
			if name == "invalid timestamp" {
				header.System.TimeCreated.SystemTime = "invalid"
			}
			if name == "missing timestamp" {
				header.System.TimeCreated.SystemTime = ""
			}
			if name == "missing record" {
				header.System.RecordID = 0
			}
			if name == "wrong channel" {
				header.System.Channel = "unrelated"
			}
			if name == "wrong provider" {
				header.System.Provider.Name = "unrelated"
			}
			read := func(uintptr) (eventXML, error) {
				if name == "render" || name == "decode" {
					return eventXML{}, errors.New("fixture read failure")
				}
				return header, nil
			}
			bookmark := func(uintptr) (string, error) {
				if name == "bookmark" {
					return "", errors.New("fixture bookmark failure")
				}
				if name == "empty bookmark" {
					return "", nil
				}
				return "fixture checkpoint", nil
			}
			err := processEvent(context.Background(), 0, read, bookmark, func(context.Context, monitoring.Event) error { emitted++; return nil }, report)
			if err == nil || report.gaps != 1 || emitted != 0 {
				t.Fatalf("err=%v gaps=%d emitted=%d", err, report.gaps, emitted)
			}
		})
	}
}

func TestProcessingPreservesReplayIdentityAndAtomicCheckpoint(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "events.db"), storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	header := fixtureHeader()
	report := &fixtureReporter{}
	read := func(uintptr) (eventXML, error) { return header, nil }
	bookmark := func(uintptr) (string, error) { return "checkpoint-123", nil }
	for i := 0; i < 2; i++ {
		if err := processEvent(context.Background(), 0, read, bookmark, store.Process, report); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.Recent(context.Background(), storage.Query{Source: SourceID, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].EventID != EventIdentity(channel, 123, header.System.TimeCreated.SystemTime) {
		t.Fatalf("replay changed identity: %+v", rows)
	}
	cp, err := store.Load(context.Background(), SourceID)
	if err != nil || string(cp) != "checkpoint-123" {
		t.Fatalf("checkpoint: %q %v", cp, err)
	}
	readFailure := func(uintptr) (eventXML, error) { return eventXML{}, errors.New("fixture system decode error") }
	if processEvent(context.Background(), 0, readFailure, bookmark, store.Process, report) == nil {
		t.Fatal("read failure hidden")
	}
	cp, err = store.Load(context.Background(), SourceID)
	if err != nil || string(cp) != "checkpoint-123" || report.gaps != 1 {
		t.Fatal("failure advanced checkpoint or omitted gap")
	}
	// Record-number reuse with a later record time must be new evidence.
	header.System.TimeCreated.SystemTime = time.Now().UTC().Format("2006-01-02T15:04:05.0000000Z")
	if err := processEvent(context.Background(), 0, read, bookmark, store.Process, report); err != nil {
		t.Fatal(err)
	}
	rows, err = store.Recent(context.Background(), storage.Query{Source: SourceID, Limit: 50})
	if err != nil || len(rows) != 2 {
		t.Fatal("new record time was falsely deduplicated")
	}
}

func TestUnallowlistedRecordAndDeliveryFailure(t *testing.T) {
	header := fixtureHeader()
	report := &fixtureReporter{}
	emitted := 0
	read := func(uintptr) (eventXML, error) { return header, nil }
	bookmark := func(uintptr) (string, error) { return "cp", nil }
	header.System.EventID = 4688
	if err := processEvent(context.Background(), 0, read, bookmark, func(context.Context, monitoring.Event) error { emitted++; return nil }, report); err != nil || emitted != 0 {
		t.Fatal("unrelated event emitted")
	}
	header.System.EventID = 5007
	if err := processEvent(context.Background(), 0, read, bookmark, func(context.Context, monitoring.Event) error { return errors.New("delivery failed") }, report); err == nil || report.gaps != 1 {
		t.Fatal("delivery failure not observable")
	}
}
