package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const CookieName = "gateway_session"
const SessionLifetime = 12 * time.Hour

type Store interface {
	UserCount(context.Context) (int, error)
	CreateUser(context.Context, string, string) error
	PasswordHash(context.Context, string) (int64, string, error)
	CreateSession(context.Context, []byte, []byte, int64, time.Time) error
	Session(context.Context, []byte) (string, []byte, error)
	DeleteSession(context.Context, []byte) error
}

type attempt struct {
	failures, reserved int
	until, touched     time.Time
}
type Service struct {
	store    Store
	mu       sync.Mutex
	attempts map[string]attempt
	argon    chan struct{}
	now      func() time.Time
}

func New(store Store) *Service {
	return &Service{store: store, attempts: make(map[string]attempt), argon: make(chan struct{}, 4), now: time.Now}
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func csrf(token string) string {
	h := sha256.Sum256([]byte("csrf:" + token))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
func hashToken(token string) []byte { h := sha256.Sum256([]byte(token)); return h[:] }

func HashPassword(password string) (string, error) {
	if len(password) < 12 {
		return "", errors.New("пароль должен содержать не менее 12 байт")
	}
	if len(password) > 1024 {
		return "", errors.New("пароль не должен превышать 1024 байта")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
	return fmt.Sprintf("argon2id$v=19$m=65536,t=3,p=4$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}
func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" || parts[1] != "v=19" {
		return false
	}
	var memory, timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false
	}
	if memory < 8192 || memory > 262144 || timeCost < 1 || timeCost > 10 || threads < 1 || threads > 16 {
		return false
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(parts[3])
	key, e2 := base64.RawStdEncoding.DecodeString(parts[4])
	if e1 != nil || e2 != nil || len(salt) != 16 || len(key) != 32 {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, uint32(len(key)))
	return subtle.ConstantTimeCompare(actual, key) == 1
}

// Bootstrap создаёт начального администратора. Открытый пароль возвращается
// только вызывающей команде init и в базе не хранится.
func (s *Service) Bootstrap(ctx context.Context) (string, error) {
	n, err := s.store.UserCount(ctx)
	if err != nil {
		return "", err
	}
	if n != 0 {
		return "", nil
	}
	password, err := randomToken(24)
	if err != nil {
		return "", err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return "", err
	}
	if err := s.store.CreateUser(ctx, "admin", hash); err != nil {
		return "", err
	}
	return password, nil
}

func (s *Service) reserve(remote string) error {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	a, exists := s.attempts[remote]
	if !exists && len(s.attempts) >= 2048 {
		for key, item := range s.attempts {
			if item.reserved == 0 && !now.Before(item.until) {
				delete(s.attempts, key)
			}
		}
		if len(s.attempts) >= 2048 {
			return errors.New("лимит проверок входа; повторите позже")
		}
	}
	if a.failures+a.reserved >= 5 && now.Before(a.until) {
		return errors.New("слишком много попыток входа; повторите позже")
	}
	if now.After(a.until) {
		a.failures = 0
		a.reserved = 0
		a.until = now.Add(15 * time.Minute)
	}
	a.reserved++
	a.touched = now
	s.attempts[remote] = a
	return nil
}
func (s *Service) finish(remote string, success bool) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.attempts[remote]
	if a.reserved > 0 {
		a.reserved--
	}
	if success {
		a.failures = 0
		if a.reserved == 0 {
			delete(s.attempts, remote)
		} else {
			s.attempts[remote] = a
		}
		return
	}
	a.failures++
	a.touched = now
	if a.until.Before(now) {
		a.until = now.Add(15 * time.Minute)
	}
	s.attempts[remote] = a
	if len(s.attempts) > 2048 {
		for key, item := range s.attempts {
			if item.reserved == 0 && now.Sub(item.touched) > 15*time.Minute {
				delete(s.attempts, key)
			}
		}
	}
}

func (s *Service) Login(ctx context.Context, username, password, remote string) (string, string, error) {
	if len(password) > 1024 {
		return "", "", errors.New("пароль слишком длинный")
	}
	if err := s.reserve(remote); err != nil {
		return "", "", err
	}
	success := false
	defer func() { s.finish(remote, success) }()
	select {
	case s.argon <- struct{}{}:
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
	defer func() { <-s.argon }()
	id, hash, err := s.store.PasswordHash(ctx, username)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", "", err
	}
	if errors.Is(err, sql.ErrNoRows) {
		hash = dummyHash
	}
	if !verifyPassword(hash, password) || err != nil {
		return "", "", errors.New("неверные учётные данные")
	}
	token, err := randomToken(32)
	if err != nil {
		return "", "", err
	}
	csrfToken := csrf(token)
	if err := s.store.CreateSession(ctx, hashToken(token), hashToken(csrfToken), id, s.now().Add(SessionLifetime)); err != nil {
		return "", "", err
	}
	success = true
	return token, csrfToken, nil
}

const dummyHash = "argon2id$v=19$m=65536,t=3,p=4$MDEyMzQ1Njc4OWFiY2RlZg$Zi8vVAnS0dI05i27tEvbIQWrPjCVRK23ZwudcoKtZhg"

func (s *Service) Authenticate(ctx context.Context, token string) (string, string, error) {
	if len(token) < 32 || len(token) > 128 {
		return "", "", sql.ErrNoRows
	}
	username, stored, err := s.store.Session(ctx, hashToken(token))
	if err != nil {
		return "", "", err
	}
	csrfToken := csrf(token)
	if subtle.ConstantTimeCompare(stored, hashToken(csrfToken)) != 1 {
		return "", "", sql.ErrNoRows
	}
	return username, csrfToken, nil
}
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.store.DeleteSession(ctx, hashToken(token))
}
func ValidCSRF(got, want string) bool {
	return got != "" && len(got) == len(want) && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
