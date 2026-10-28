package auth

import (
	"testing"
	"time"
)

func TestTicketSingleUseAndExpiry(t *testing.T) {
	now := time.Now()
	s := NewTicketStore()
	s.now = func() time.Time { return now }
	ticket, err := s.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Redeem(ticket) {
		t.Fatal("first redemption failed")
	}
	if s.Redeem(ticket) {
		t.Fatal("ticket replay succeeded")
	}
	ticket, _ = s.Issue()
	now = now.Add(TicketTTL + time.Second)
	if s.Redeem(ticket) {
		t.Fatal("expired ticket succeeded")
	}
}

func TestSessionCSRFAndFixationResistance(t *testing.T) {
	s := NewSessions()
	id1, csrf1, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	id2, _, _ := s.Create()
	if id1 == id2 {
		t.Fatal("session identifiers repeated")
	}
	if !s.ValidateCSRF(id1, csrf1) {
		t.Fatal("valid csrf rejected")
	}
	if s.ValidateCSRF(id1, "attacker") {
		t.Fatal("invalid csrf accepted")
	}
	if s.Validate("attacker-supplied-session-id-that-is-long-enough") {
		t.Fatal("unknown fixed session accepted")
	}
}
