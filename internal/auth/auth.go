// Package auth implements the admin login: username + password, optional
// TOTP second factor with one-time recovery codes, brute-force throttling
// and session cookies.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Phydran6/LocoStor/internal/fsutil"
)

// CookieName is the session cookie name.
const CookieName = "locostor_session"

const (
	sessionTTL     = 12 * time.Hour
	mfaTTL         = 5 * time.Minute
	failWindow     = 15 * time.Minute
	maxFailsPerIP  = 5  // then the IP must wait until old failures age out
	maxFailsGlobal = 50 // across all IPs within failWindow
	minPasswordLen = 8
	recoveryCount  = 10
)

// Errors returned to the API.
var (
	ErrInvalid   = errors.New("invalid username or password")
	ErrMFA       = errors.New("invalid code")
	ErrThrottled = errors.New("too many failed attempts - try again in a few minutes")
)

// Credentials are the stored admin credentials.
type Credentials struct {
	Username      string
	PasswordHash  string
	TOTPSecret    string   // empty = MFA off
	RecoveryCodes []string // sha256 hex of unused recovery codes
}

// Store persists credentials (the config file).
type Store interface {
	Credentials() Credentials
	UpdateCredentials(func(*Credentials)) error
}

// Manager handles logins and sessions.
type Manager struct {
	store       Store
	sessionFile string // optional; sessions survive restarts (e.g. updates)

	mu        sync.Mutex
	sessions  map[string]time.Time // sha256(token) -> expiry
	pending   map[string]pendingMFA
	fails     map[string][]time.Time // client IP -> failed attempts
	allFails  []time.Time
	lastStep  int64  // last accepted TOTP step (replay protection)
	setupTOTP string // secret being enrolled, not yet confirmed
}

type pendingMFA struct {
	expires  time.Time
	attempts int
}

// New creates a Manager. sessionFile may be empty.
func New(store Store, sessionFile string) *Manager {
	m := &Manager{
		store:       store,
		sessionFile: sessionFile,
		sessions:    map[string]time.Time{},
		pending:     map[string]pendingMFA{},
		fails:       map[string][]time.Time{},
	}
	if sessionFile != "" {
		var saved map[string]time.Time
		if err := fsutil.ReadJSON(sessionFile, &saved); err == nil {
			now := time.Now()
			for k, exp := range saved {
				if exp.After(now) {
					m.sessions[k] = exp
				}
			}
		}
	}
	return m
}

// HashPassword returns a bcrypt hash of pw.
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
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

// dummyHash is compared against when no password is set, so a login takes
// the same time either way.
var dummyHash = sync.OnceValue(func() string {
	h, _ := bcrypt.GenerateFromPassword([]byte(RandomToken(16)), 12)
	return string(h)
})

func hashToken(t string) string {
	s := sha256.Sum256([]byte(t))
	return hex.EncodeToString(s[:])
}

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`)

// ValidateUsername checks the admin username format.
func ValidateUsername(u string) error {
	if !usernameRe.MatchString(u) {
		return errors.New("username may contain letters, digits, '.', '_', '@' and '-' (max 64)")
	}
	return nil
}

// ValidatePassword checks the admin password policy.
func ValidatePassword(pw string) error {
	if len(pw) < minPasswordLen {
		return errors.New("password must be at least 8 characters")
	}
	if len(pw) > 72 {
		return errors.New("password must be at most 72 bytes") // bcrypt limit
	}
	return nil
}

// ClientIP returns the remote IP of r (proxy headers are not trusted).
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// --- throttling ------------------------------------------------------------

func prune(ts []time.Time, now time.Time) []time.Time {
	out := ts[:0]
	for _, t := range ts {
		if now.Sub(t) < failWindow {
			out = append(out, t)
		}
	}
	return out
}

// throttled reports whether ip may not try again right now.
func (m *Manager) throttled(ip string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.allFails = prune(m.allFails, now)
	f := prune(m.fails[ip], now)
	if len(f) == 0 {
		delete(m.fails, ip)
	} else {
		m.fails[ip] = f
	}
	return len(f) >= maxFailsPerIP || len(m.allFails) >= maxFailsGlobal
}

func (m *Manager) recordFail(ip string) {
	m.mu.Lock()
	now := time.Now()
	m.fails[ip] = append(m.fails[ip], now)
	m.allFails = append(m.allFails, now)
	m.mu.Unlock()
	time.Sleep(400 * time.Millisecond) // slows down online guessing
}

func (m *Manager) clearFails(ip string) {
	m.mu.Lock()
	delete(m.fails, ip)
	m.mu.Unlock()
}

// --- sessions --------------------------------------------------------------

func (m *Manager) newSession() string {
	token := RandomToken(32)
	m.mu.Lock()
	m.sessions[hashToken(token)] = time.Now().Add(sessionTTL)
	m.saveLocked()
	m.mu.Unlock()
	return token
}

// saveLocked persists sessions; m.mu must be held.
func (m *Manager) saveLocked() {
	if m.sessionFile == "" {
		return
	}
	now := time.Now()
	for k, exp := range m.sessions {
		if exp.Before(now) {
			delete(m.sessions, k)
		}
	}
	_ = fsutil.WriteJSON(m.sessionFile, m.sessions, 0o600)
}

// Valid reports whether token is a live session.
func (m *Manager) Valid(token string) bool {
	if token == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := hashToken(token)
	exp, ok := m.sessions[k]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(m.sessions, k)
		return false
	}
	return true
}

// Logout ends a session.
func (m *Manager) Logout(token string) {
	m.mu.Lock()
	delete(m.sessions, hashToken(token))
	m.saveLocked()
	m.mu.Unlock()
}

// dropOtherSessions ends every session except keep.
func (m *Manager) dropOtherSessions(keep string) {
	k := hashToken(keep)
	m.mu.Lock()
	for t := range m.sessions {
		if t != k {
			delete(m.sessions, t)
		}
	}
	m.saveLocked()
	m.mu.Unlock()
}

// --- login -----------------------------------------------------------------

// LoginResult is either a session token or, with MFA enabled, a short-lived
// token for the second step.
type LoginResult struct {
	Token    string
	MFAToken string
}

func (m *Manager) checkPassword(user, pw string) bool {
	c := m.store.Credentials()
	// Always run bcrypt so timing does not reveal whether the user exists.
	hash := c.PasswordHash
	if hash == "" {
		hash = dummyHash()
	}
	pwOK := bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
	userOK := subtle.ConstantTimeCompare([]byte(strings.ToLower(user)), []byte(strings.ToLower(c.Username))) == 1
	return pwOK && userOK && c.PasswordHash != ""
}

// Login verifies username and password.
func (m *Manager) Login(ip, user, pw string) (LoginResult, error) {
	if m.throttled(ip) {
		return LoginResult{}, ErrThrottled
	}
	if !m.checkPassword(user, pw) {
		m.recordFail(ip)
		return LoginResult{}, ErrInvalid
	}
	if m.store.Credentials().TOTPSecret == "" {
		m.clearFails(ip)
		return LoginResult{Token: m.newSession()}, nil
	}
	t := RandomToken(32)
	m.mu.Lock()
	for k, p := range m.pending {
		if time.Now().After(p.expires) {
			delete(m.pending, k)
		}
	}
	m.pending[hashToken(t)] = pendingMFA{expires: time.Now().Add(mfaTTL)}
	m.mu.Unlock()
	return LoginResult{MFAToken: t}, nil
}

// VerifyMFA completes a login with a TOTP or recovery code.
func (m *Manager) VerifyMFA(ip, mfaToken, code string) (string, error) {
	if m.throttled(ip) {
		return "", ErrThrottled
	}
	k := hashToken(mfaToken)
	m.mu.Lock()
	p, ok := m.pending[k]
	if ok && time.Now().After(p.expires) {
		delete(m.pending, k)
		ok = false
	}
	m.mu.Unlock()
	if !ok {
		return "", errors.New("login expired - please sign in again")
	}
	if !m.checkSecondFactor(code) {
		m.mu.Lock()
		p.attempts++
		if p.attempts >= 5 {
			delete(m.pending, k)
		} else {
			m.pending[k] = p
		}
		m.mu.Unlock()
		m.recordFail(ip)
		return "", ErrMFA
	}
	m.mu.Lock()
	delete(m.pending, k)
	m.mu.Unlock()
	m.clearFails(ip)
	return m.newSession(), nil
}

// checkSecondFactor accepts a current TOTP code or an unused recovery code
// (which is then consumed).
func (m *Manager) checkSecondFactor(code string) bool {
	c := m.store.Credentials()
	if c.TOTPSecret == "" {
		return false
	}
	m.mu.Lock()
	step, ok := verifyTOTP(c.TOTPSecret, code, time.Now(), m.lastStep)
	if ok {
		m.lastStep = step
	}
	m.mu.Unlock()
	if ok {
		return true
	}
	h := hashRecovery(code)
	found := false
	err := m.store.UpdateCredentials(func(cr *Credentials) {
		for i, rc := range cr.RecoveryCodes {
			if subtle.ConstantTimeCompare([]byte(rc), []byte(h)) == 1 {
				cr.RecoveryCodes = append(cr.RecoveryCodes[:i:i], cr.RecoveryCodes[i+1:]...)
				found = true
				return
			}
		}
	})
	return err == nil && found
}

// --- account management (logged in) ---------------------------------------

// ChangePassword verifies current and stores next; other sessions end.
func (m *Manager) ChangePassword(keep, current, next string) error {
	c := m.store.Credentials()
	if bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte(current)) != nil {
		return ErrInvalid
	}
	if err := ValidatePassword(next); err != nil {
		return err
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	if err := m.store.UpdateCredentials(func(cr *Credentials) { cr.PasswordHash = hash }); err != nil {
		return err
	}
	m.dropOtherSessions(keep)
	return nil
}

// ChangeUsername sets a new username after verifying the password.
func (m *Manager) ChangeUsername(password, name string) error {
	c := m.store.Credentials()
	if bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte(password)) != nil {
		return ErrInvalid
	}
	if err := ValidateUsername(name); err != nil {
		return err
	}
	return m.store.UpdateCredentials(func(cr *Credentials) { cr.Username = name })
}

// BeginTOTP creates a new secret to enroll; it takes effect after
// EnableTOTP confirms a code from the authenticator app.
func (m *Manager) BeginTOTP() (secret, uri string) {
	secret = NewTOTPSecret()
	m.mu.Lock()
	m.setupTOTP = secret
	m.mu.Unlock()
	return secret, TOTPURI(secret, m.store.Credentials().Username)
}

// EnableTOTP confirms enrollment and returns fresh recovery codes (shown
// once, stored hashed).
func (m *Manager) EnableTOTP(keep, code string) ([]string, error) {
	m.mu.Lock()
	secret := m.setupTOTP
	m.mu.Unlock()
	if secret == "" {
		return nil, errors.New("start the setup first")
	}
	step, ok := verifyTOTP(secret, code, time.Now(), 0)
	if !ok {
		return nil, ErrMFA
	}
	codes, hashes := newRecoveryCodes()
	if err := m.store.UpdateCredentials(func(cr *Credentials) {
		cr.TOTPSecret = secret
		cr.RecoveryCodes = hashes
	}); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.setupTOTP = ""
	m.lastStep = step
	m.mu.Unlock()
	m.dropOtherSessions(keep)
	return codes, nil
}

// DisableTOTP turns MFA off after verifying password and a second factor.
func (m *Manager) DisableTOTP(password, code string) error {
	c := m.store.Credentials()
	if bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte(password)) != nil {
		return ErrInvalid
	}
	if !m.checkSecondFactor(code) {
		return ErrMFA
	}
	return m.store.UpdateCredentials(func(cr *Credentials) {
		cr.TOTPSecret = ""
		cr.RecoveryCodes = nil
	})
}

// RegenerateRecovery replaces the recovery codes.
func (m *Manager) RegenerateRecovery(password, code string) ([]string, error) {
	c := m.store.Credentials()
	if bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte(password)) != nil {
		return nil, ErrInvalid
	}
	if !m.checkSecondFactor(code) {
		return nil, ErrMFA
	}
	codes, hashes := newRecoveryCodes()
	return codes, m.store.UpdateCredentials(func(cr *Credentials) { cr.RecoveryCodes = hashes })
}

// Info describes the account for the settings page.
type Info struct {
	Username          string `json:"username"`
	MFAEnabled        bool   `json:"mfa_enabled"`
	RecoveryCodesLeft int    `json:"recovery_codes_left"`
}

// Info returns account details.
func (m *Manager) Info() Info {
	c := m.store.Credentials()
	return Info{Username: c.Username, MFAEnabled: c.TOTPSecret != "", RecoveryCodesLeft: len(c.RecoveryCodes)}
}

// --- recovery codes -------------------------------------------------------

// Lower-case alphabet without the look-alikes i, l and o.
var recoveryAlphabet = base32.NewEncoding("abcdefghjkmnpqrstuvwxyz023456789").WithPadding(base32.NoPadding)

func normalizeRecovery(code string) string {
	return strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

func hashRecovery(code string) string {
	s := sha256.Sum256([]byte("locostor-recovery:" + normalizeRecovery(code)))
	return hex.EncodeToString(s[:])
}

func newRecoveryCodes() (codes, hashes []string) {
	for i := 0; i < recoveryCount; i++ {
		b := make([]byte, 7)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		s := recoveryAlphabet.EncodeToString(b)[:10]
		code := s[:5] + "-" + s[5:]
		codes = append(codes, code)
		hashes = append(hashes, hashRecovery(code))
	}
	return codes, hashes
}

// --- cookies ----------------------------------------------------------------

// Token extracts the session token from r.
func Token(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// secure reports whether the browser talks HTTPS to us (directly or via a
// TLS-terminating reverse proxy).
func secure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// SetCookie writes the session cookie.
func SetCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

// ClearCookie removes the session cookie.
func ClearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteStrictMode})
}
