package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"
)

type Handler func(Request) Response

type Server struct {
	listener net.Listener
	secret   []byte
	nonces   *Nonces
	handler  Handler
}

func Listen(runtimeDir string, secret []byte, handler Handler) (*Server, error) {
	l, err := listen(Endpoint(runtimeDir))
	if err != nil {
		return nil, err
	}
	return &Server{listener: l, secret: secret, nonces: NewNonces(), handler: handler}, nil
}

func (s *Server) Serve(ctx context.Context) error {
	go func() { <-ctx.Done(); _ = s.listener.Close() }()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := verifyPeer(conn); err != nil {
		return
	}
	raw, err := bufio.NewReader(io.LimitReader(conn, MaxRequestBytes+1)).ReadBytes('\n')
	if err != nil || len(raw) > MaxRequestBytes {
		return
	}
	var req Request
	if json.Unmarshal(raw, &req) != nil {
		return
	}
	if err := Verify(s.secret, req, time.Now()); err != nil || !s.nonces.Accept(req.Nonce, time.Now()) {
		return
	}
	response := s.handler(req)
	b, err := Encode(response)
	if err == nil {
		_, _ = conn.Write(b)
	}
}

func Call(ctx context.Context, runtimeDir string, secret []byte, operation, value string) (Response, error) {
	req, err := NewRequest(secret, operation, value)
	if err != nil {
		return Response{}, err
	}
	conn, err := dial(ctx, Endpoint(runtimeDir))
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	b, _ := Encode(req)
	if len(b) > MaxRequestBytes {
		return Response{}, errors.New("IPC request too large")
	}
	if _, err = conn.Write(b); err != nil {
		return Response{}, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	raw, err := bufio.NewReader(io.LimitReader(conn, MaxResponseBytes+1)).ReadBytes('\n')
	if err != nil {
		return Response{}, err
	}
	if len(raw) > MaxResponseBytes {
		return Response{}, errors.New("IPC response too large")
	}
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}
