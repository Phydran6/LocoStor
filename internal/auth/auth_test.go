package auth

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestManager(t *testing.T) (*Manager, *MemStore) {
	t.Helper()
	s, err := NewMemStore("admin", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	return New(s, filepath.Join(t.TempDir(), "sessions.json")), s
}

func TestLoginWithoutMFA(t *testing.T) {
	m, _ := newTestManager(t)
	if _, err := m.Login("1.1.1.1", "admin", "wrong"); err != ErrInvalid {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := m.Login("1.1.1.1", "root", "correct horse"); err != ErrInvalid {
		t.Fatalf("wrong user: %v", err)
	}
	res, err := m.Login("1.1.1.1", "Admin", "correct horse")
	if err != nil || res.Token == "" || res.MFAToken != "" {
		t.Fatalf("login: %+v %v", res, err)
	}
	if !m.Valid(res.Token) {
		t.Fatal("session not valid")
	}
	m.Logout(res.Token)
	if m.Valid(res.Token) {
		t.Fatal("session valid after logout")
	}
}

func TestSessionsSurviveRestart(t *testing.T) {
	s, _ := NewMemStore("admin", "correct horse")
	file := filepath.Join(t.TempDir(), "sessions.json")
	m := New(s, file)
	res, _ := m.Login("1.1.1.1", "admin", "correct horse")
	if m2 := New(s, file); !m2.Valid(res.Token) {
		t.Fatal("session lost after restart")
	}
}

func TestThrottlePerIP(t *testing.T) {
	m, _ := newTestManager(t)
	for i := 0; i < maxFailsPerIP; i++ {
		m.Login("2.2.2.2", "admin", "bad")
	}
	if _, err := m.Login("2.2.2.2", "admin", "correct horse"); err != ErrThrottled {
		t.Fatalf("expected throttling, got %v", err)
	}
	if _, err := m.Login("3.3.3.3", "admin", "correct horse"); err != nil {
		t.Fatalf("other IP must not be blocked: %v", err)
	}
}

func TestMFAFlow(t *testing.T) {
	m, s := newTestManager(t)
	res, _ := m.Login("1.1.1.1", "admin", "correct horse")

	secret, uri := m.BeginTOTP()
	if secret == "" || uri == "" {
		t.Fatal("no secret")
	}
	if _, err := m.EnableTOTP(res.Token, "000000"); err == nil {
		t.Fatal("wrong code enabled MFA")
	}
	code, _ := totpCode(secret, time.Now())
	recovery, err := m.EnableTOTP(res.Token, code)
	if err != nil || len(recovery) != recoveryCount {
		t.Fatalf("enable: %v %v", recovery, err)
	}
	if s.Credentials().RecoveryCodes[0] == recovery[0] {
		t.Fatal("recovery codes stored in plain text")
	}

	// Password alone no longer gives a session.
	res, _ = m.Login("1.1.1.1", "admin", "correct horse")
	if res.Token != "" || res.MFAToken == "" {
		t.Fatalf("expected MFA step: %+v", res)
	}
	if _, err := m.VerifyMFA("1.1.1.1", res.MFAToken, "123456"); err != ErrMFA {
		t.Fatalf("wrong code: %v", err)
	}
	// The TOTP code used for enrollment cannot be replayed; a recovery code works once.
	tok, err := m.VerifyMFA("1.1.1.1", res.MFAToken, recovery[0])
	if err != nil || !m.Valid(tok) {
		t.Fatalf("recovery code: %v", err)
	}
	res, _ = m.Login("1.1.1.1", "admin", "correct horse")
	if _, err := m.VerifyMFA("1.1.1.1", res.MFAToken, recovery[0]); err != ErrMFA {
		t.Fatalf("recovery code reused: %v", err)
	}
	if n := m.Info().RecoveryCodesLeft; n != recoveryCount-1 {
		t.Fatalf("recovery codes left = %d", n)
	}
	if _, err := m.VerifyMFA("1.1.1.1", "bogus", recovery[1]); err == nil {
		t.Fatal("unknown MFA token accepted")
	}
}
