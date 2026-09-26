package auth

import (
	"encoding/base32"
	"testing"
	"time"
)

// RFC 6238 appendix B test vectors (SHA-1, last 6 digits).
func TestTOTPVectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	cases := map[int64]string{
		59:          "287082",
		1111111109:  "081804",
		1111111111:  "050471",
		1234567890:  "005924",
		2000000000:  "279037",
		20000000000: "353130",
	}
	for ts, want := range cases {
		got, err := totpCode(secret, time.Unix(ts, 0))
		if err != nil || got != want {
			t.Errorf("t=%d: got %s, want %s (%v)", ts, got, want, err)
		}
	}
}

func TestVerifyTOTPReplayAndSkew(t *testing.T) {
	secret := NewTOTPSecret()
	now := time.Unix(1_700_000_000, 0)
	code, _ := totpCode(secret, now)

	step, ok := verifyTOTP(secret, code, now, 0)
	if !ok {
		t.Fatal("valid code rejected")
	}
	if _, ok := verifyTOTP(secret, code, now, step); ok {
		t.Error("replayed code accepted")
	}
	if _, ok := verifyTOTP(secret, code, now.Add(30*time.Second), 0); !ok {
		t.Error("code from previous step rejected (clock skew)")
	}
	if _, ok := verifyTOTP(secret, code, now.Add(90*time.Second), 0); ok {
		t.Error("code accepted 3 steps later")
	}
	if _, ok := verifyTOTP(secret, "12345", now, 0); ok {
		t.Error("short code accepted")
	}
}
