package auth

import (
	"testing"
	"time"
)

func TestSessionRecoveryStableBoundAndRevocation(t *testing.T) {
	now := time.Now()
	s := NewSessions()
	s.now = func() time.Time { return now }
	id, csrf, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	other, otherCSRF, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		recovered, ok := s.CSRF(id)
		if !ok || recovered != csrf || !s.ValidateCSRF(id, recovered) || s.ValidateCSRF(other, recovered) || s.ValidateCSRF(id, otherCSRF) {
			t.Fatal("recovery rotated or crossed session boundaries")
		}
	}
	s.Delete(id)
	if _, ok := s.CSRF(id); ok {
		t.Fatal("logout session recovered")
	}
	now = now.Add(SessionIdleTTL + time.Second)
	if _, ok := s.CSRF(other); ok {
		t.Fatal("idle-expired session recovered")
	}
	id, _, err = s.Create()
	if err != nil {
		t.Fatal(err)
	}
	created := now
	for i := 0; i < 32; i++ {
		now = now.Add(SessionIdleTTL / 2)
		_, _ = s.CSRF(id)
	}
	now = created.Add(SessionAbsoluteTTL + time.Second)
	if _, ok := s.CSRF(id); ok {
		t.Fatal("recovery extended absolute expiry")
	}
	if _, ok := s.CSRF("unknown-session-that-is-long-enough"); ok {
		t.Fatal("unknown session recovered")
	}
}
