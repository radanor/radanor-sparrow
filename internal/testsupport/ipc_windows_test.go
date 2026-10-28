//go:build windows

package testsupport

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestIPCMutexSerializesAndBoundsWait(t *testing.T) {
	name := fmt.Sprintf(`Local\Sparrow-MutexRegression-%d-%d`, os.Getpid(), time.Now().UnixNano())
	entered, release := make(chan struct{}), make(chan struct{})
	finished := make(chan int, 1)
	var once sync.Once
	defer once.Do(func() { close(release) })
	go func() {
		finished <- withWindowsIPCMutex(name, 1000, func() int {
			close(entered)
			<-release
			return 17
		})
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("owner did not acquire test mutex")
	}
	if got := withWindowsIPCMutex(name, 20, func() int {
		t.Error("contender entered while another thread owned the mutex")
		return 0
	}); got != 1 {
		t.Fatalf("timed-out contender returned %d, want failure", got)
	}
	once.Do(func() { close(release) })
	select {
	case got := <-finished:
		if got != 17 {
			t.Fatalf("owner return value=%d", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owner failed to release test mutex")
	}
	if got := withWindowsIPCMutex(name, 1000, func() int { return 23 }); got != 23 {
		t.Fatalf("mutex was not reusable after owner release: %d", got)
	}
}

func TestIPCMutexAbandonedHelper(t *testing.T) {
	name := os.Getenv("RADANOR_MUTEX_CRASH_FIXTURE")
	if name == "" {
		return
	}
	os.Exit(withWindowsIPCMutex(name, 2000, func() int {
		fmt.Println("owned")
		time.Sleep(time.Hour) // the parent deliberately terminates only this fixture
		return 0
	}))
}

func TestIPCMutexRecoversAfterOwnerProcessDies(t *testing.T) {
	name := fmt.Sprintf(`Local\Sparrow-AbandonedRegression-%d-%d`, os.Getpid(), time.Now().UnixNano())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestIPCMutexAbandonedHelper$")
	child.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	child.Env = append(os.Environ(), "RADANOR_MUTEX_CRASH_FIXTURE="+name)
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Process.Kill()
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		ready <- scanner.Scan() && scanner.Text() == "owned"
	}()
	select {
	case ok := <-ready:
		if !ok {
			_ = child.Wait()
			t.Fatal("crash fixture never acquired its private test mutex")
		}
	case <-ctx.Done():
		_ = child.Wait()
		t.Fatal("crash fixture startup timed out")
	}
	// Keep the named object alive after the owner dies, ensuring the next wait
	// encounters an abandoned object rather than creating a new unlocked one.
	nativeName, _ := windows.UTF16PtrFromString(name)
	handle, _ := windows.CreateMutex(nil, false, nativeName)
	if handle == 0 {
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatal("cannot retain crash fixture mutex")
	}
	defer windows.CloseHandle(handle)
	_ = child.Process.Kill()
	_ = child.Wait()
	if got := withWindowsIPCMutex(name, 2000, func() int { return 31 }); got != 31 {
		t.Fatalf("abandoned-owner acquisition failed: %d", got)
	}
}
