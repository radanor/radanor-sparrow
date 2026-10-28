// Package collectors provides native operating-system collectors for the CE-5
// posture assessment. Every collector returns factual observations only; it
// never emits a security verdict, and it never modifies the system.
package collectors

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// isMissingTool reports whether err means the executable genuinely does not
// exist. A missing binary surfaces as *exec.Error wrapping exec.ErrNotFound,
// which deliberately does not satisfy errors.Is(err, os.ErrNotExist), so it
// must be unwrapped explicitly. Callers use this to distinguish
// "tool not installed" (Unsupported) from a real failure.
func isMissingTool(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	var execErr *exec.Error
	return errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound)
}

const (
	// defaultCommandTimeout bounds every external command a collector runs so a
	// hung tool can never stall an assessment.
	defaultCommandTimeout = 8 * time.Second
	// defaultCommandWaitDelay bounds how long a command may keep the collector
	// waiting on output after the process has exited or been cancelled. Without
	// it a tool that leaves a descendant holding the inherited stdout/stderr
	// pipes makes Wait block until that descendant exits, so the command
	// timeout above would not actually be enforced.
	defaultCommandWaitDelay = 1 * time.Second
	// maxCommandOutput caps captured stdout+stderr for a posture collector, so a
	// misbehaving tool cannot exhaust memory or bloat stored evidence. A posture
	// collector derives a bounded sample and a count from its command, so this
	// cap is generous for its purpose; reaching it means the reading is partial
	// and is reported as such.
	maxCommandOutput = 256 * 1024
	// maxInventoryCommandOutput bounds a *full inventory listing*, which is a
	// legitimately large observation rather than a bounded evidence sample. It is
	// sized above the storage cap on purpose: a kind that passes
	// inventory.MaxKindItems (20000) items still fits here at roughly 400 bytes
	// per line, so on a realistic host the storage cap is what binds and the
	// capture cap only stops a runaway producer. It stays a hard bound, so the
	// memory a single command can consume is still fixed.
	maxInventoryCommandOutput = 8 * 1024 * 1024
	// maxFileBytes caps files read directly from disk.
	maxFileBytes = 512 * 1024
)

// commandResult is the outcome of one collector command. Truncation is part of
// the result rather than a silent side effect: a collector that reports a count
// or claims coverage must be able to see that its own observation was clipped by
// the capture cap, so it can say so instead of presenting a partial reading as a
// complete one.
type commandResult struct {
	// Output is the captured stdout and stderr, at most Cap bytes long.
	Output string
	// Truncated reports that the command produced more output than Cap, so
	// Output is a prefix of it and any count derived from it is a lower bound.
	Truncated bool
	// Cap is the byte cap that applied to this capture, so a caller can explain
	// the limit that bit without hard-coding it.
	Cap int
}

// runCommand executes an argv (never a shell string) with a hard timeout and a
// bounded output capture. Arguments are fixed by the collector, so no caller or
// user input ever reaches a shell or an unrestricted path.
func runCommand(ctx context.Context, name string, args ...string) (commandResult, error) {
	return runCommandWithCap(ctx, defaultCommandTimeout, maxCommandOutput, name, args...)
}

// runCommandWithTimeout is runCommand with an explicit deadline, separated so
// the deadline behaviour (timeout, cancellation, descendants holding the output
// pipes) is testable without waiting out the production timeout.
func runCommandWithTimeout(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
	return runCommandWithCap(ctx, timeout, maxCommandOutput, name, args...)
}

// runCommandWithCap is the single implementation behind every collector command.
// The capture limit is a parameter so an inventory listing can be given the
// larger bound it needs without any caller being able to choose an unbounded one.
func runCommandWithCap(ctx context.Context, timeout time.Duration, limit int, name string, args ...string) (commandResult, error) {
	if limit <= 0 {
		limit = maxCommandOutput
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out := &limitWriter{max: limit}
	cmd := exec.CommandContext(cctx, name, args...)
	configureCommandCleanup(cmd)
	cmd.WaitDelay = defaultCommandWaitDelay
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	// result is built from the capture every time, including the error paths: a
	// truncated partial reading is reported as truncated whether or not the
	// command also timed out or failed.
	result := commandResult{Output: out.String(), Truncated: out.truncated, Cap: limit}
	switch {
	case cctx.Err() == context.DeadlineExceeded:
		// Partial output collected before the deadline is still returned: an
		// interrupted read is evidence too, and callers report it as such.
		return result, context.DeadlineExceeded
	case errors.Is(err, exec.ErrWaitDelay):
		// The process itself finished, but something still held its output open
		// past the wait delay. The command is over; report the reason rather
		// than blocking until the holder exits.
		if cctx.Err() != nil {
			return result, cctx.Err()
		}
		return result, err
	case err != nil && cctx.Err() != nil:
		return result, cctx.Err()
	case err != nil:
		return result, err
	}
	return result, nil
}

// limitWriter captures up to max bytes and discards the rest without error, so
// oversized output is bounded rather than fatal — but never silently: every
// discarded byte sets truncated, which the caller must disclose.
type limitWriter struct {
	buf       []byte
	max       int
	truncated bool
}

func (w *limitWriter) Write(p []byte) (int, error) {
	remaining := w.max - len(w.buf)
	if remaining <= 0 {
		if len(p) > 0 {
			// The cap is full and more output arrived: the dropped bytes are
			// what makes what is held a prefix rather than the whole thing.
			w.truncated = true
		}
		return len(p), nil
	}
	if len(p) > remaining {
		w.buf = append(w.buf, p[:remaining]...)
		w.truncated = true
		return len(p), nil
	}
	// This write fits exactly. A capture that is filled to the cap but never
	// receives another byte is complete, not truncated, so the flag is set only
	// when bytes are actually discarded.
	w.buf = append(w.buf, p...)
	return len(p), nil
}

func (w *limitWriter) String() string { return string(w.buf) }

// truncatedNote is the single disclosure used whenever a collector's own
// command output was clipped: the records counted are those actually observed,
// which is fewer than exist. Keeping the wording in one place means no collector
// can describe a partial reading as a complete one.
func truncatedNote(what string, cap int) string {
	return what + " produced more output than the capture limit of " +
		strconv.Itoa(cap/1024) + " KiB, so this is a partial reading: the records below are the ones that were observed, not all of them"
}

// splitLines splits captured command output into lines, dropping the trailing
// fragment when the capture was clipped mid-line: a truncated final line is not
// a complete record and must never be parsed as one.
func splitLines(output string, truncated bool) []string {
	lines := strings.Split(output, "\n")
	if truncated && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// readFileText reads a configuration file with a hard size cap. Callers must
// pass only fixed, allowlisted paths.
func readFileText(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() > maxFileBytes {
		return "", errors.New("configuration file exceeds read limit")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// presentValue builds a bounded string evidence value.
func presentValue(key, text string) assessment.Value {
	return assessment.Value{Key: key, Kind: assessment.ValueString, Text: boundText(text)}
}

// presentBool builds a bool evidence value.
func presentBool(key string, v bool) assessment.Value {
	b := v
	return assessment.Value{Key: key, Kind: assessment.ValueBool, Bool: &b}
}

// presentInt builds an int evidence value.
func presentInt(key string, v int64) assessment.Value {
	i := v
	return assessment.Value{Key: key, Kind: assessment.ValueInt, Int: &i}
}

// boundText trims a value to the documented text limit so collector output can
// never inflate stored records without bound.
func boundText(s string) string {
	if len(s) > assessment.MaxTextValue {
		return s[:assessment.MaxTextValue]
	}
	return strings.TrimSpace(s)
}
