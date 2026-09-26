// Package auth implements single-admin password login with in-memory
// session cookies.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// CookieName is the session cookie name.
const CookieName = "locostor_session"

const sessionTTL = 12 * time.Hour

// ErrInvalid is returned for a wrong password.
var ErrInvalid = errors.New("invalid password")

// PasswordStore persists the admin password hash.
type PasswordStore interface {
	GetPasswordHash() string
	SetPasswordHash(string) error
}

// Manager handles login and sessions.
type Manager struct {
	store PasswordStore

	mu       sync.Mutex
	sessions map[string]time.Time
	failures []time.Time
}

// New creates a Manager.
func New(store PasswordStore) *Manager {
	return &Manager{store: store, sessions: map[string]time.Time{}}
}

// HashPassword returns a bcrypt hash of pw.
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h), err
}

// RandomToken returns n random bytes, base64url encoded.
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Login checks pw and returns a new session token.
func (m *Manager) Login(pw string) (string, error) {
	m.mu.Lock()
	now := time.Now()
	recent := m.failures[:0]
	for _, t := range m.failures {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	m.failures = recent
	throttled := len(m.failures) >= 5
	m.mu.Unlock()

	hash := m.store.GetPasswordHash()
	if throttled || hash == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) != nil {
		m.mu.Lock()
		m.failures = append(m.failures, now)
		m.mu.Unlock()
		time.Sleep(500 * time.Millisecond)
		if throttled {
			return "", errors.New("too many failed attempts, try again in a minute")
		}
		return "", ErrInvalid
	}

	token := RandomToken(32)
	m.mu.Lock()
	m.sessions[token] = now.Add(sessionTTL)
	m.mu.Unlock()
	return token, nil
}

// ChangePassword verifies current and stores next. All other sessions are
// dropped.
func (m *Manager) ChangePassword(keep, current, next string) error {
	if bcrypt.CompareHashAndPassword([]byte(m.store.GetPasswordHash()), []byte(current)) != nil {
		return ErrInvalid
	}
	if len(next) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	if err := m.store.SetPasswordHash(hash); err != nil {
		return err
	}
	m.mu.Lock()
	for t := range m.sessions {
		if subtle.ConstantTimeCompare([]byte(t), []byte(keep)) != 1 {
			delete(m.sessions, t)
		}
	}
	m.mu.Unlock()
	return nil
}

// Valid reports whether token is a live session.
func (m *Manager) Valid(token string) bool {
	if token == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	exp, ok := m.sessions[token]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(m.sessions, token)
		return false
	}
	return true
}

// Logout ends a session.
func (m *Manager) Logout(token string) {
	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()
}

// Token extracts the session token from r.
func Token(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// SetCookie writes the session cookie.
func SetCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

// ClearCookie removes the session cookie.
func ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
}
