package auth

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sort"
	"testing"
)

// Tiny CBOR encoder for the test authenticator.
func cb(v any) []byte {
	head := func(major byte, n uint64) []byte {
		switch {
		case n < 24:
			return []byte{major<<5 | byte(n)}
		case n < 256:
			return []byte{major<<5 | 24, byte(n)}
		default:
			b := []byte{major<<5 | 25, 0, 0}
			binary.BigEndian.PutUint16(b[1:], uint16(n))
			return b
		}
	}
	switch x := v.(type) {
	case int:
		if x >= 0 {
			return head(0, uint64(x))
		}
		return head(1, uint64(-1-x))
	case []byte:
		return append(head(2, uint64(len(x))), x...)
	case string:
		return append(head(3, uint64(len(x))), x...)
	case map[any]any:
		keys := make([]any, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return string(cb(keys[i])) < string(cb(keys[j])) })
		out := head(5, uint64(len(x)))
		for _, k := range keys {
			out = append(out, cb(k)...)
			out = append(out, cb(x[k])...)
		}
		return out
	}
	panic("cb")
}

type softKey struct {
	ec    *ecdsa.PrivateKey
	ed    ed25519.PrivateKey
	id    []byte
	count uint32
}

func (k *softKey) cose() []byte {
	if k.ed != nil {
		return cb(map[any]any{1: 1, 3: -8, -1: 6, -2: []byte(k.ed.Public().(ed25519.PublicKey))})
	}
	x, y := make([]byte, 32), make([]byte, 32)
	k.ec.X.FillBytes(x)
	k.ec.Y.FillBytes(y)
	return cb(map[any]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
}

func (k *softKey) authData(rpID string, attested bool) []byte {
	h := sha256.Sum256([]byte(rpID))
	flags := byte(flagUP | flagUV)
	if attested {
		flags |= flagAT
	}
	b := append(h[:], flags, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(b[33:], k.count)
	if attested {
		b = append(b, make([]byte, 16)...)
		b = binary.BigEndian.AppendUint16(b, uint16(len(k.id)))
		b = append(b, k.id...)
		b = append(b, k.cose()...)
	}
	return b
}

func clientJSON(typ string, opts map[string]any, origin string) []byte {
	cd, _ := json.Marshal(map[string]any{"type": typ, "challenge": opts["challenge"], "origin": origin})
	return cd
}

func (k *softKey) register(opts map[string]any, origin string) PasskeyResponse {
	rp := opts["rp"].(map[string]any)["id"].(string)
	att := cb(map[any]any{"fmt": "none", "attStmt": map[any]any{}, "authData": k.authData(rp, true)})
	return PasskeyResponse{ID: b64.EncodeToString(k.id), ClientDataJSON: b64.EncodeToString(clientJSON("webauthn.create", opts, origin)), AttestationObject: b64.EncodeToString(att)}
}

func (k *softKey) login(opts map[string]any, origin string) PasskeyResponse {
	k.count++
	ad := k.authData(opts["rpId"].(string), false)
	cd := clientJSON("webauthn.get", opts, origin)
	h := sha256.Sum256(cd)
	msg := append(append([]byte{}, ad...), h[:]...)
	var sig []byte
	if k.ed != nil {
		sig = ed25519.Sign(k.ed, msg)
	} else {
		d := sha256.Sum256(msg)
		sig, _ = ecdsa.SignASN1(rand.Reader, k.ec, d[:])
	}
	return PasskeyResponse{ID: b64.EncodeToString(k.id), ClientDataJSON: b64.EncodeToString(cd), AuthenticatorData: b64.EncodeToString(ad), Signature: b64.EncodeToString(sig)}
}

func TestPasskeys(t *testing.T) {
	store, _ := NewMemStore("admin", "correct horse")
	m := New(store, "")
	const origin = "https://nas.home.arpa"

	if _, _, err := RelyingParty("https://192.168.1.5"); err == nil {
		t.Error("IP address accepted as RP ID")
	}
	if _, _, err := RelyingParty("http://nas.home.arpa:8080"); err == nil {
		t.Error("plain HTTP accepted")
	}
	if _, err := m.BeginPasskeyRegistration("wrong", origin); !errors.Is(err, ErrInvalid) {
		t.Errorf("registration without password: %v", err)
	}

	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	_, edPriv, _ := ed25519.GenerateKey(rand.Reader)
	keys := []*softKey{{ec: ec, id: []byte("ec-credential-1")}, {ed: edPriv, id: []byte("ed-credential-2")}}
	for i, k := range keys {
		opts, err := m.BeginPasskeyRegistration("correct horse", origin)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.FinishPasskeyRegistration("key", k.register(opts, "https://evil.example")); err == nil {
			t.Error("registration from another origin accepted")
		}
		opts, _ = m.BeginPasskeyRegistration("correct horse", origin)
		info, err := m.FinishPasskeyRegistration("key", k.register(opts, origin))
		if err != nil {
			t.Fatalf("register %d: %v", i, err)
		}
		if info.RPID != "nas.home.arpa" {
			t.Errorf("rp id %q", info.RPID)
		}
	}
	if len(m.Passkeys()) != 2 || !m.HasPasskeys() {
		t.Fatalf("passkeys: %+v", m.Passkeys())
	}

	for _, k := range keys {
		opts, err := m.BeginPasskeyLogin("1.2.3.4", origin)
		if err != nil {
			t.Fatal(err)
		}
		resp := k.login(opts, origin)
		tok, err := m.FinishPasskeyLogin("1.2.3.4", resp)
		if err != nil || !m.Valid(tok) {
			t.Fatalf("login: %v", err)
		}
		// Replay of the same assertion must fail (challenge is used up).
		if _, err := m.FinishPasskeyLogin("1.2.3.5", resp); err == nil {
			t.Error("replayed assertion accepted")
		}
		// A tampered signature fails.
		opts, _ = m.BeginPasskeyLogin("1.2.3.6", origin)
		bad := k.login(opts, origin)
		sig, _ := b64.DecodeString(bad.Signature)
		sig[len(sig)-1] ^= 1
		bad.Signature = b64.EncodeToString(sig)
		if _, err := m.FinishPasskeyLogin("1.2.3.6", bad); err == nil {
			t.Error("bad signature accepted")
		}
	}
	// Counter going backwards = cloned key.
	opts, _ := m.BeginPasskeyLogin("1.2.3.7", origin)
	keys[0].count = 0
	if _, err := m.FinishPasskeyLogin("1.2.3.7", keys[0].login(opts, origin)); err == nil {
		t.Error("old counter accepted")
	}

	if err := m.DeletePasskey("correct horse", b64.EncodeToString(keys[1].id)); err != nil || len(m.Passkeys()) != 1 {
		t.Errorf("delete: %v %d", err, len(m.Passkeys()))
	}
}
