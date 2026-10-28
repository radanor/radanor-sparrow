//go:build windows

package filewatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/monitoring"
	"golang.org/x/sys/windows"
)

type permissionReporter struct{ healthy bool }

func (r *permissionReporter) ConfirmHealthy()                      { r.healthy = true }
func (*permissionReporter) ReportGap(time.Time, time.Time, string) {}

func TestWindowsDeniedRootReturnsPermissionDenied(t *testing.T) {
	root := t.TempDir()
	source, err := New([]Root{{Label: "denied-isolated-root", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	original, err := windows.GetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	originalACL, _, err := original.DACL()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, originalACL, nil); err != nil {
			t.Errorf("restore isolated root DACL: %v", err)
		}
	}()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(D;;0x1;;;" + user.User.Sid.String() + ")(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	denied, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, denied, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reporter := &permissionReporter{}
	err = source.Run(ctx, nil, func(context.Context, monitoring.Event) error { t.Error("denied root emitted an event"); return nil }, reporter)
	if !errors.Is(err, monitoring.ErrPermissionDenied) || reporter.healthy {
		t.Fatalf("denied root was not refused before health confirmation: err=%v healthy=%v", err, reporter.healthy)
	}
}
