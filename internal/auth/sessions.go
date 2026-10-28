package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"sync"
	"time"
)

const (
	SessionCookie      = "radanor_sparrow_session"
	SessionIdleTTL     = 30 * time.Minute
	SessionAbsoluteTTL = 8 * time.Hour
)

type sessionRecord struct {
	idHash, csrfHash  [32]byte
	csrfKey           [32]byte
	created, lastSeen time.Time
}
type Sessions struct {
	mu      sync.Mutex
	records map[[32]byte]sessionRecord
	now     func() time.Time
}

func NewSessions() *Sessions {
	return NewSessionsWithClock(time.Now)
}

// NewSessionsWithClock allows deterministic expiry tests. Production callers
// use NewSessions; request data can never supply or alter this clock.
func NewSessionsWithClock(now func() time.Time) *Sessions {
	return &Sessions{records: make(map[[32]byte]sessionRecord), now: now}
}

func (s *Sessions) Create() (id, csrf string, err error) {
	id, err = RandomToken(32)
	if err != nil {
		return "", "", err
	}
	seed, err := RandomToken(32)
	if err != nil {
		return "", "", err
	}
	now := s.now()
	key := sha256.Sum256([]byte(seed))
	csrf = sessionCSRF(id, key)
	rec := sessionRecord{idHash: sha256.Sum256([]byte(id)), csrfHash: sha256.Sum256([]byte(csrf)), csrfKey: key, created: now, lastSeen: now}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	s.records[rec.idHash] = rec
	return id, csrf, nil
}

// CSRF recovers the existing token for a live session, without rotating either
// credential or extending its absolute lifetime. Only hashes and a per-session
// random derivation key are retained in RAM; raw bearer tokens are not stored.
func (s *Sessions) CSRF(id string) (string, bool) {
	if len(id) < 32 || len(id) > 128 {
		return "", false
	}
	h := sha256.Sum256([]byte(id))
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	rec, ok := s.records[h]
	if !ok {
		return "", false
	}
	rec.lastSeen = now
	s.records[h] = rec
	return sessionCSRF(id, rec.csrfKey), true
}

func sessionCSRF(id string, key [32]byte) string {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte("radanor-sparrow/session-csrf/v1:" + id))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Sessions) Validate(id string) bool           { return s.validate(id, "", false) }
func (s *Sessions) ValidateCSRF(id, csrf string) bool { return s.validate(id, csrf, true) }

func (s *Sessions) validate(id, csrf string, requireCSRF bool) bool {
	if len(id) < 32 || len(id) > 128 {
		return false
	}
	h := sha256.Sum256([]byte(id))
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	rec, ok := s.records[h]
	if !ok {
		return false
	}
	if requireCSRF {
		c := sha256.Sum256([]byte(csrf))
		if subtle.ConstantTimeCompare(c[:], rec.csrfHash[:]) != 1 {
			return false
		}
	}
	rec.lastSeen = now
	s.records[h] = rec
	return true
}

func (s *Sessions) Delete(id string) {
	h := sha256.Sum256([]byte(id))
	s.mu.Lock()
	delete(s.records, h)
	s.mu.Unlock()
}
func (s *Sessions) pruneLocked(now time.Time) {
	for k, r := range s.records {
		if now.Sub(r.lastSeen) > SessionIdleTTL || now.Sub(r.created) > SessionAbsoluteTTL {
			delete(s.records, k)
		}
	}
}
