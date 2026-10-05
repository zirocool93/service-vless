package auth

import (
	"fmt"
	"testing"
	"time"
)

func TestLimiterBoundsDistinctPeersAndReclaimsExpired(t *testing.T) {
	now := time.Now()
	s := &Service{attempts: map[string]attempt{}, now: func() time.Time { return now }}
	for i := 0; i < 2048; i++ {
		peer := fmt.Sprint(i)
		if err := s.reserve(peer); err != nil {
			t.Fatal(err)
		}
		s.finish(peer, false)
	}
	if err := s.reserve("overflow"); err == nil {
		t.Fatal("новый peer обошёл ограничение памяти")
	}
	now = now.Add(16 * time.Minute)
	if err := s.reserve("after-expiry"); err != nil {
		t.Fatal(err)
	}
	if len(s.attempts) != 1 {
		t.Fatalf("просроченные записи остались: %d", len(s.attempts))
	}
}

func TestSuccessfulLoginKeepsConcurrentReservations(t *testing.T) {
	s := &Service{attempts: map[string]attempt{}, now: time.Now}
	if err := s.reserve("peer"); err != nil {
		t.Fatal(err)
	}
	if err := s.reserve("peer"); err != nil {
		t.Fatal(err)
	}
	s.finish("peer", true)
	if s.attempts["peer"].reserved != 1 {
		t.Fatal("успешный вход потерял параллельную попытку")
	}
	s.finish("peer", false)
	if s.attempts["peer"].failures != 1 {
		t.Fatal("параллельная ошибка не учтена")
	}
}
