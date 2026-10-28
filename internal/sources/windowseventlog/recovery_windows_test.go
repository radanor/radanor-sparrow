//go:build windows

package windowseventlog

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/radanor/radanor-shield/internal/monitoring"
	"golang.org/x/sys/windows"
)

func TestMissingBookmarkIsRecoverable(t *testing.T) {
	for _, err := range []error{windows.ERROR_NOT_FOUND, windows.Errno(0x3aa3)} {
		if got := classifySubscription(err, true); !errors.Is(got, monitoring.ErrInvalidCheckpoint) {
			t.Errorf("bookmark error %v classified as %v", err, got)
		}
	}
	if !errors.Is(classifyWindows(windows.ERROR_EVT_CHANNEL_NOT_FOUND), monitoring.ErrUnsupported) {
		t.Fatal("missing channel classification changed")
	}
}

func TestSubscriptionErrorsAreContextual(t *testing.T) {
	if got := classifySubscription(windows.ERROR_EVT_INVALID_QUERY, true); !errors.Is(got, windows.ERROR_EVT_INVALID_QUERY) || errors.Is(got, monitoring.ErrInvalidCheckpoint) {
		t.Fatal("genuine invalid query discards the stored bookmark")
	}
	if errors.Is(classifySubscription(windows.ERROR_NOT_FOUND, false), monitoring.ErrInvalidCheckpoint) {
		t.Fatal("not-found without a subscribed bookmark invalidates state")
	}
	if errors.Is(classifySubscription(windows.ERROR_EVT_INVALID_QUERY, false), monitoring.ErrInvalidCheckpoint) {
		t.Fatal("non-bookmark query failure invalidates checkpoints")
	}
	for _, hasCheckpoint := range []bool{false, true} {
		if !errors.Is(classifySubscription(windows.ERROR_ACCESS_DENIED, hasCheckpoint), monitoring.ErrPermissionDenied) {
			t.Fatal("permission denial classification weakened")
		}
		if !errors.Is(classifySubscription(windows.ERROR_EVT_CHANNEL_NOT_FOUND, hasCheckpoint), monitoring.ErrUnsupported) {
			t.Fatal("unavailable channel misclassified")
		}
	}
}

func TestRendererChecksReturnedSize(t *testing.T) {
	raw, err := os.ReadFile("source_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	// The allocation probe is checked, but the successful render must also
	// validate its returned byte length before interpreting pointers/variants.
	if strings.Count(string(raw), "used >") < 2 {
		t.Fatal("successful system render size is not checked")
	}
}

func TestBookmarkConstructionDoesNotDiscardNativeError(t *testing.T) {
	raw, err := os.ReadFile("source_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "bookmark, _, _ = procCreateBookmark.Call") {
		t.Fatal("bookmark creation errors are unconditionally treated as invalid checkpoints")
	}
}

func TestSourceDescriptorDoesNotDenyDurableCheckpoints(t *testing.T) {
	for _, limitation := range New().Descriptor().Limitations {
		if strings.Contains(limitation, "not durable") {
			t.Fatal("descriptor contradicts durable checkpoint storage")
		}
	}
}

// Pins the pre-refactor loss paths; behavioral fixtures cover the processing
// boundary once it exists. No live event subscription or payload is needed.
func TestEventFailuresDoNotSilentlyReturn(t *testing.T) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "source_windows.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		guard, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}
		var condition bytes.Buffer
		_ = printer.Fprint(&condition, set, guard.Cond)
		text := condition.String()
		if strings.Contains(text, "renderErr") || strings.Contains(text, "xml.Unmarshal") || strings.Contains(text, "bmErr") {
			for _, statement := range guard.Body.List {
				if ret, ok := statement.(*ast.ReturnStmt); ok && len(ret.Results) == 0 {
					t.Errorf("silent event loss guarded by %s", text)
				}
			}
		}
		return true
	})
}

func TestInvalidTimestampDoesNotUseCurrentTime(t *testing.T) {
	raw, err := os.ReadFile("source_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "occurred = time.Now()") {
		t.Fatal("invalid record timestamp uses process-local current time")
	}
}
