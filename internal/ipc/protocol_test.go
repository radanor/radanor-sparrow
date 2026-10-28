package ipc

import (
	"bufio"
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestAuthenticationAndReplayCache(t *testing.T) {
	secret := []byte("01234567890123456789012345678901")
	r, err := NewRequest(secret, "health", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(secret, r, time.Now()); err != nil {
		t.Fatal(err)
	}
	r.Operation = "issue_ticket"
	if Verify(secret, r, time.Now()) == nil {
		t.Fatal("tampered request accepted")
	}
	n := NewNonces()
	if !n.Accept("unique-nonce", time.Now()) || n.Accept("unique-nonce", time.Now()) {
		t.Fatal("nonce replay accepted")
	}
}

// CE-4 widened only the response direction (bounded monitoring reads); requests
// stay strictly limited in both policy and on the wire.
func TestRequestAndResponseSizeBounds(t *testing.T) {
	dir := t.TempDir()
	secret := []byte("01234567890123456789012345678901")
	heavy := strings.Repeat("x", 128*1024)
	var handled atomic.Int32
	srv, err := Listen(dir, secret, func(r Request) Response {
		handled.Add(1)
		if r.Operation == "heavy" {
			return Response{OK: true, Value: heavy}
		}
		return Response{Error: "unsupported"}
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	resp, err := Call(ctx, dir, secret, "heavy", "")
	if err != nil || !resp.OK || len(resp.Value) != len(heavy) {
		t.Fatalf("bounded response not delivered: %v %+v", err, resp)
	}
	// A request value beyond the protocol bound is dropped without a response.
	oversized, err := NewRequest(secret, "health", strings.Repeat("y", 2000))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dial(ctx, Endpoint(dir))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Encode(oversized)
	if _, err = conn.Write(b); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if raw, err := bufio.NewReader(io.LimitReader(conn, 64)).ReadBytes('\n'); err == nil && len(raw) > 0 {
		t.Fatalf("oversized-value request answered: %q", raw)
	}
	conn.Close()
	// An oversized raw frame is cut off before parsing.
	conn, err = dial(ctx, Endpoint(dir))
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, MaxRequestBytes+16)
	for i := range frame {
		frame[i] = 'z'
	}
	frame[len(frame)-1] = '\n'
	if _, err = conn.Write(frame); err != nil {
		// Windows named-pipe writes wait for the peer to consume the frame.
		// The server rejects and closes after MaxRequestBytes+1, so the
		// outstanding oversized write can report a pipe closure. A successful
		// write is not required to demonstrate rejection. Other errors fail.
		if runtime.GOOS != "windows" || (!errors.Is(err, syscall.Errno(109)) && !errors.Is(err, syscall.Errno(232))) {
			t.Fatal(err)
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if raw, err := bufio.NewReader(io.LimitReader(conn, 64)).ReadBytes('\n'); err == nil && len(raw) > 0 {
		t.Fatalf("oversized frame answered: %q", raw)
	}
	conn.Close()
	if got := handled.Load(); got != 1 {
		t.Fatalf("oversized requests reached the handler: calls=%d, want only the valid heavy request", got)
	}
	if _, err := Encode(Response{Value: strings.Repeat("z", MaxResponseBytes)}); err == nil {
		t.Fatal("oversized response encoded")
	}
}
