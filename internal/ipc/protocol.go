package ipc

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ProtocolVersion = 1
	// MaxRequestBytes bounds every client-to-core request. Requests carry only
	// small control values; request values are additionally bounded by Verify.
	MaxRequestBytes = 8192
	// MaxResponseBytes bounds core-to-gateway responses, which since CE-4 carry
	// bounded read-only monitoring pages. Both directions stay size-limited and
	// the socket is only reachable by verified same-user peers.
	MaxResponseBytes = 512 * 1024
	MaxClockSkew     = 30 * time.Second
)

type Request struct {
	Version   int    `json:"version"`
	Operation string `json:"operation"`
	Value     string `json:"value,omitempty"`
	Timestamp int64  `json:"timestamp"`
	Nonce     string `json:"nonce"`
	MAC       string `json:"mac"`
}
type Response struct {
	OK    bool   `json:"ok"`
	Value string `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

func SecretPath(runtimeDir string) string { return filepath.Join(runtimeDir, "ipc.key") }
func Endpoint(runtimeDir string) string   { return endpoint(runtimeDir) }

func LoadSecret(path string) ([]byte, error) {
	if err := protectSecret(path); err != nil {
		return nil, err
	}
	return readSecret(path)
}

func LoadOrCreateSecret(path string) ([]byte, error) {
	if b, err := readSecret(path); err == nil {
		if err := protectSecret(path); err != nil {
			return nil, err
		}
		return b, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if os.IsExist(err) {
			return readSecret(path)
		}
		return nil, err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	if err = protectSecret(path); err != nil {
		return nil, err
	}
	return b, nil
}

func readSecret(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) != 32 {
		return nil, errors.New("invalid IPC secret length")
	}
	return b, nil
}

func NewRequest(secret []byte, operation, value string) (Request, error) {
	nonceRaw := make([]byte, 18)
	if _, err := rand.Read(nonceRaw); err != nil {
		return Request{}, err
	}
	r := Request{Version: ProtocolVersion, Operation: operation, Value: value, Timestamp: time.Now().Unix(), Nonce: base64.RawURLEncoding.EncodeToString(nonceRaw)}
	r.MAC = sign(secret, r)
	return r, nil
}

func Verify(secret []byte, r Request, now time.Time) error {
	if r.Version != ProtocolVersion {
		return errors.New("protocol version mismatch")
	}
	if len(r.Operation) == 0 || len(r.Operation) > 64 || len(r.Value) > 1024 || len(r.Nonce) < 16 || len(r.Nonce) > 64 {
		return errors.New("invalid request bounds")
	}
	if delta := now.Sub(time.Unix(r.Timestamp, 0)); delta > MaxClockSkew || delta < -MaxClockSkew {
		return errors.New("request timestamp outside allowed window")
	}
	want, err := hex.DecodeString(r.MAC)
	if err != nil {
		return errors.New("invalid request authentication")
	}
	got, _ := hex.DecodeString(sign(secret, r))
	if !hmac.Equal(want, got) {
		return errors.New("invalid request authentication")
	}
	return nil
}

func sign(secret []byte, r Request) string {
	h := hmac.New(sha256.New, secret)
	parts := []string{strconv.Itoa(r.Version), r.Operation, r.Value, strconv.FormatInt(r.Timestamp, 10), r.Nonce}
	h.Write([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h.Sum(nil))
}

type Nonces struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func NewNonces() *Nonces { return &Nonces{seen: make(map[string]time.Time)} }
func (n *Nonces) Accept(value string, now time.Time) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	for k, expiry := range n.seen {
		if !now.Before(expiry) {
			delete(n.seen, k)
		}
	}
	if _, ok := n.seen[value]; ok {
		return false
	}
	n.seen[value] = now.Add(MaxClockSkew)
	return true
}

func Encode(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxResponseBytes {
		return nil, fmt.Errorf("IPC message too large")
	}
	return append(b, '\n'), nil
}
