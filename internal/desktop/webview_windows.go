//go:build windows

package desktop

import (
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"log/slog"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"unsafe"

	webview "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/webviewloader"
	"github.com/radanor/radanor-shield/internal/branding"
	"github.com/radanor/radanor-shield/internal/ipc"
	"golang.org/x/sys/windows"
)

// The approved favicon is copied byte-for-byte from web/public by the branding
// integration. It supplies native window and notification-area icons without a
// second artwork source or a separate runtime file.
//
//go:embed assets/radanor.ico
var windowsIcon []byte

const (
	wmClose      = 0x0010
	wmCommand    = 0x0111
	wmSetIcon    = 0x0080
	wmAppTray    = 0x8001
	wmLButtonUp  = 0x0202
	wmLButtonDbl = 0x0203
	wmRButtonUp  = 0x0205
	swHide       = 0
	swRestore    = 9
	gwlpWndProc  = ^uintptr(3)
	iconSmall    = 0
	iconBig      = 1
	nimAdd       = 0
	nimDelete    = 2
	nifMessage   = 1
	nifIcon      = 2
	nifTip       = 4
	mfString     = 0
	tpmReturnCmd = 0x0100
	menuOpen     = 1001
	menuExit     = 1002
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	shell32                 = windows.NewLazySystemDLL("shell32.dll")
	procFindWindowW         = user32.NewProc("FindWindowW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procSetWindowLongPtrW   = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProcW     = user32.NewProc("CallWindowProcW")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procSendMessageW        = user32.NewProc("SendMessageW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForeground       = user32.NewProc("SetForegroundWindow")
	procCreateIconResource  = user32.NewProc("CreateIconFromResourceEx")
	procDestroyIcon         = user32.NewProc("DestroyIcon")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
)

type trayPoint struct{ X, Y int32 }

// notifyIconDataW matches NOTIFYICONDATAW. Keeping the complete native layout is
// important on 64-bit Windows even though Sparrow only uses the message, icon
// and tooltip fields.
type notifyIconDataW struct {
	Size            uint32
	Window          uintptr
	ID, Flags       uint32
	CallbackMessage uint32
	Icon            windows.Handle
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUID            windows.GUID
	BalloonIcon     windows.Handle
}

type windowsWindow struct {
	hwnd     uintptr
	original uintptr
	callback uintptr
	icon     uintptr
	tray     notifyIconDataW
	cancel   context.CancelFunc
	exiting  atomic.Bool
}

func activateDesktopWindow() bool {
	title, _ := windows.UTF16PtrFromString(branding.Product)
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(title)))
	if hwnd == 0 {
		return false
	}
	showAndFocus(hwnd)
	return true
}

func showAndFocus(hwnd uintptr) {
	procShowWindow.Call(hwnd, swRestore)
	procSetForegroundWindow.Call(hwnd)
}

// The returned function is the Linux window component's shutdown; here the window
// runs inside this process, so there is nothing to stop or reap and it is a no-op.
func launchDesktopDashboard(ctx context.Context, cancel context.CancelFunc, origin, runtimeDir, stateDir string, secret []byte, logger *slog.Logger) (func(), error) {
	url, err := dashboardURL(ctx, origin, runtimeDir, secret)
	if err != nil {
		return func() {}, err
	}
	if version, err := webviewloader.GetInstalledVersion(); err != nil || version == "" {
		logger.Warn("WebView2 runtime is unavailable; opening the authenticated dashboard in the default browser")
		return func() {}, openBrowser(url)
	}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		view := webview.NewWithOptions(webview.WebViewOptions{
			Debug: false, AutoFocus: true, DataPath: filepath.Join(stateDir, "webview2"),
			WindowOptions: webview.WindowOptions{Title: branding.Product, Width: 1180, Height: 780, Center: true},
		})
		if view == nil {
			ready <- errors.New("WebView2 runtime is unavailable")
			return
		}
		window := &windowsWindow{hwnd: uintptr(view.Window()), cancel: cancel}
		if err := window.install(); err != nil {
			view.Destroy()
			ready <- err
			return
		}
		defer window.remove()
		defer view.Destroy()
		// This is deliberately not a native bridge. It only prevents the local
		// dashboard from navigating its embedded top-level view away from the
		// authenticated loopback origin.
		view.Init(`document.addEventListener('click',function(e){var a=e.target.closest&&e.target.closest('a[href]');if(!a)return;try{if(new URL(a.href,location.href).origin!==location.origin)e.preventDefault()}catch(_){e.preventDefault()}},true);document.addEventListener('submit',function(e){try{if(new URL(e.target.action,location.href).origin!==location.origin)e.preventDefault()}catch(_){e.preventDefault()}},true);window.open=function(){return null};`)
		view.Navigate(url)
		ready <- nil
		go func() {
			<-ctx.Done()
			window.requestExit()
		}()
		view.Run()
	}()
	if err := <-ready; err != nil {
		logger.Warn("WebView2 unavailable; opening the authenticated dashboard in the default browser")
		return func() {}, openBrowser(url)
	}
	return func() {}, nil
}

func dashboardURL(ctx context.Context, origin, runtimeDir string, secret []byte) (string, error) {
	if !validOrigin(origin) {
		return "", errors.New("Refusing an invalid dashboard origin")
	}
	response, err := ipcCallTicket(ctx, runtimeDir, secret)
	if err != nil {
		return "", err
	}
	return origin + "/#ticket=" + response, nil
}

// Kept as a small seam so Windows presentation tests can exercise ticket
// issuance without gaining a native JavaScript API.
func ipcCallTicket(ctx context.Context, runtimeDir string, secret []byte) (string, error) {
	response, err := ipc.Call(ctx, runtimeDir, secret, "issue_ticket", "")
	if err != nil || !response.OK || response.Value == "" {
		return "", errors.New("Cannot issue a dashboard launch ticket")
	}
	return response.Value, nil
}

func (w *windowsWindow) install() error {
	w.callback = windows.NewCallback(w.windowProc)
	w.original, _, _ = procSetWindowLongPtrW.Call(w.hwnd, gwlpWndProc, w.callback)
	if w.original == 0 {
		return errors.New("Cannot install the Windows application lifecycle handler")
	}
	w.icon = iconFromICO(windowsIcon)
	if w.icon != 0 {
		procSendMessageW.Call(w.hwnd, wmSetIcon, iconSmall, w.icon)
		procSendMessageW.Call(w.hwnd, wmSetIcon, iconBig, w.icon)
	}
	w.tray.Size = uint32(unsafe.Sizeof(w.tray))
	w.tray.Window = w.hwnd
	w.tray.ID = 1
	w.tray.Flags = nifMessage | nifIcon | nifTip
	w.tray.CallbackMessage = wmAppTray
	w.tray.Icon = windows.Handle(w.icon)
	copy(w.tray.Tip[:], windows.StringToUTF16(branding.Product))
	result, _, _ := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&w.tray)))
	if result == 0 {
		return errors.New("Cannot create the Windows notification-area icon")
	}
	return nil
}

func (w *windowsWindow) remove() {
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&w.tray)))
	if w.icon != 0 {
		procDestroyIcon.Call(w.icon)
	}
}

func (w *windowsWindow) requestExit() {
	w.exiting.Store(true)
	procPostMessageW.Call(w.hwnd, wmClose, 0, 0)
}

func (w *windowsWindow) windowProc(hwnd, message, wparam, lparam uintptr) uintptr {
	switch message {
	case wmClose:
		if !w.exiting.Load() {
			procShowWindow.Call(hwnd, swHide)
			return 0
		}
	case wmAppTray:
		switch lparam {
		case wmLButtonUp, wmLButtonDbl:
			showAndFocus(hwnd)
		case wmRButtonUp:
			w.showMenu()
		}
		return 0
	case wmCommand:
		switch wparam & 0xffff {
		case menuOpen:
			showAndFocus(hwnd)
		case menuExit:
			w.cancel()
			w.requestExit()
		}
		return 0
	}
	result, _, _ := procCallWindowProcW.Call(w.original, hwnd, message, wparam, lparam)
	return result
}

func (w *windowsWindow) showMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	openText, _ := windows.UTF16PtrFromString("Open Radanor Sparrow")
	exitText, _ := windows.UTF16PtrFromString("Exit Radanor Sparrow")
	procAppendMenuW.Call(menu, mfString, menuOpen, uintptr(unsafe.Pointer(openText)))
	procAppendMenuW.Call(menu, mfString, menuExit, uintptr(unsafe.Pointer(exitText)))
	point := trayPoint{}
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	procSetForeground.Call(w.hwnd)
	command, _, _ := procTrackPopupMenu.Call(menu, tpmReturnCmd, uintptr(point.X), uintptr(point.Y), 0, w.hwnd, 0)
	if command != 0 {
		procPostMessageW.Call(w.hwnd, wmCommand, command, 0)
	}
}

func iconFromICO(data []byte) uintptr {
	if len(data) < 6 || binary.LittleEndian.Uint16(data[:2]) != 0 || binary.LittleEndian.Uint16(data[2:4]) != 1 {
		return 0
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	bestArea, bestOffset, bestSize := -1, 0, 0
	for index := 0; index < count; index++ {
		entry := 6 + index*16
		if entry+16 > len(data) {
			break
		}
		width, height := int(data[entry]), int(data[entry+1])
		if width == 0 {
			width = 256
		}
		if height == 0 {
			height = 256
		}
		size := int(binary.LittleEndian.Uint32(data[entry+8 : entry+12]))
		offset := int(binary.LittleEndian.Uint32(data[entry+12 : entry+16]))
		if offset < 0 || size <= 0 || offset+size > len(data) {
			continue
		}
		area := width * height
		if area > bestArea && width <= 48 && height <= 48 {
			bestArea, bestOffset, bestSize = area, offset, size
		}
	}
	if bestSize == 0 {
		return 0
	}
	icon, _, _ := procCreateIconResource.Call(uintptr(unsafe.Pointer(&data[bestOffset])), uintptr(bestSize), 1, 0x00030000, 0, 0, 0)
	return icon
}
