package desktop

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/radanor/radanor-shield/internal/auth"
)

type managedCore struct {
	cmd         *exec.Cmd
	control     io.WriteCloser
	done        chan struct{}
	containment containment
	launchID    string
}

func startCore(executable string, output io.Writer) (*managedCore, error) {
	id, err := auth.RandomToken(32)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(executable, "--internal-core")
	cmd.Env = append(os.Environ(), "RADANOR_SPARROW_LAUNCH_ID="+id)
	child, err := startManaged(cmd, output)
	if err == nil {
		child.launchID = id
	}
	return child, err
}

func startManaged(cmd *exec.Cmd, output io.Writer) (*managedCore, error) {
	guard, err := newContainment()
	if err != nil {
		return nil, err
	}
	configureChild(cmd)
	control, err := cmd.StdinPipe()
	if err != nil {
		guard.close()
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = output, output
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		control.Close()
		guard.close()
		return nil, err
	}
	child := &managedCore{cmd: cmd, control: control, done: make(chan struct{}), containment: guard}
	// Attach before Wait can release the process handle and allow PID reuse.
	attachErr := guard.attach(cmd.Process)
	go func() { _ = cmd.Wait(); close(child.done) }()
	// Core cannot initialize or spawn anything until containment succeeds.
	if attachErr != nil {
		child.stop()
		return nil, errors.New("Cannot contain the managed core process")
	}
	if _, err := control.Write([]byte{1}); err != nil {
		child.stop()
		return nil, err
	}
	return child, nil
}

func (child *managedCore) stop() {
	child.stopWithin(15 * time.Second)
}

func (child *managedCore) stopWithin(timeout time.Duration) {
	_ = child.control.Close() // EOF cancels the child context and flushes SQLite.
	select {
	case <-child.done:
	case <-time.After(timeout):
		child.containment.kill(child.cmd.Process)
		_ = child.cmd.Process.Kill()
		<-child.done
	}
	child.containment.close()
}
