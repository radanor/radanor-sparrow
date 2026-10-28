package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestSensitiveAttributesAreRedacted(t *testing.T) {
	var out bytes.Buffer
	New(&out, slog.LevelDebug).Info("test", "ticket", "do-not-log", "safe", "value")
	got := out.String()
	if strings.Contains(got, "do-not-log") || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("unexpected log: %s", got)
	}
}
