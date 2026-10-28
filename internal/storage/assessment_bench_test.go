package storage

import (
	"context"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// These benchmarks answer the CE-5 performance questions directly: does a
// posture scan slow down real-time event ingestion, and how fast are the
// dashboard's assessment reads? They report measurements rather than asserting
// thresholds, because the requirement is to measure honestly rather than to hit
// an arbitrary number.

func BenchmarkEventWriteDuringAssessment(b *testing.B) {
	s, err := Open(b.TempDir()+"/bench.db", testLimits(nil))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	runID, err := s.StartRun(ctx, "manual")
	if err != nil {
		b.Fatal(err)
	}
	result := checkResult("bench_check", assessment.Fail)
	// A background assessment writer runs concurrently with the measured
	// ingestion path, which is exactly the contention the design must survive.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				_ = s.SaveCheckResults(ctx, runID, []assessment.CheckResult{result})
				_, _, _ = s.ApplyFindings(ctx, runID, []assessment.CheckResult{result})
			}
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ev := testEvent("bench-"+time.Now().Format("150405.000000000")+"-"+string(rune('a'+i%26)), "native.file_integrity", []byte("cp"))
		if err := s.Process(ctx, ev); err != nil {
			b.Fatalf("event write failed during assessment: %v", err)
		}
	}
	b.StopTimer()
	close(stop)
	<-done
}

func BenchmarkAssessmentReadLatency(b *testing.B) {
	s, err := Open(b.TempDir()+"/bench.db", testLimits(nil))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		runID, _ := s.StartRun(ctx, "manual")
		result := checkResult("bench_check", assessment.Fail)
		_ = s.SaveCheckResults(ctx, runID, []assessment.CheckResult{result})
		_ = s.FinishRun(ctx, runID, assessment.ScanOutcome{Status: "completed", StartedAt: time.Now(), FinishedAt: time.Now()})
		_, _, _ = s.ApplyFindings(ctx, runID, []assessment.CheckResult{result})
	}

	b.Run("summary", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := s.Summary(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("latest_results", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, _, err := s.LatestResults(ctx, 256); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("findings", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := s.Findings(ctx, "", 0, 50); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("runs", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := s.Runs(ctx, 0, 20); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkEventWriteBaseline measures ingestion with no assessment activity at
// all, so the cost attributable to a concurrent posture scan is the difference
// between this and BenchmarkEventWriteDuringAssessment.
func BenchmarkEventWriteBaseline(b *testing.B) {
	s, err := Open(b.TempDir()+"/bench.db", testLimits(nil))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ev := testEvent("base-"+time.Now().Format("150405.000000000")+"-"+string(rune('a'+i%26)), "native.file_integrity", []byte("cp"))
		if err := s.Process(ctx, ev); err != nil {
			b.Fatalf("baseline event write failed: %v", err)
		}
	}
}
