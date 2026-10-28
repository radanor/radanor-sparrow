package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"sync"
	"time"
)

const TicketTTL = 45 * time.Second

type ticketRecord struct {
	hash    [32]byte
	expires time.Time
}
type TicketStore struct {
	mu      sync.Mutex
	tickets []ticketRecord
	now     func() time.Time
}

func NewTicketStore() *TicketStore { return &TicketStore{now: time.Now} }

func (s *TicketStore) Issue() (string, error) {
	raw, err := RandomToken(32)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(raw))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	s.tickets = append(s.tickets, ticketRecord{hash: h, expires: s.now().Add(TicketTTL)})
	return raw, nil
}

func (s *TicketStore) Redeem(raw string) bool {
	if len(raw) < 32 || len(raw) > 128 {
		return false
	}
	h := sha256.Sum256([]byte(raw))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	for i, rec := range s.tickets {
		if subtle.ConstantTimeCompare(h[:], rec.hash[:]) == 1 {
			s.tickets = append(s.tickets[:i], s.tickets[i+1:]...)
			return true
		}
	}
	return false
}

func (s *TicketStore) pruneLocked() {
	now := s.now()
	kept := s.tickets[:0]
	for _, rec := range s.tickets {
		if now.Before(rec.expires) {
			kept = append(kept, rec)
		}
	}
	s.tickets = kept
}
