package auth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Passkey is a registered WebAuthn credential.
type Passkey struct {
	ID        string    `json:"id"` // base64url credential id
	Name      string    `json:"name"`
	PublicKey []byte    `json:"public_key"` // COSE key
	Alg       int64     `json:"alg"`
	SignCount uint32    `json:"sign_count"`
	RPID      string    `json:"rp_id"`
	Created   time.Time `json:"created"`
	LastUsed  time.Time `json:"last_used,omitzero"`
}

// PasskeyInfo is shown in the settings.
type PasskeyInfo struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	RPID     string    `json:"rp_id"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"last_used,omitzero"`
}

const challengeTTL = 5 * time.Minute

// COSE algorithm identifiers.
const (
	algES256 = -7
	algEdDSA = -8
	algRS256 = -257
)

type challenge struct {
	value   []byte
	kind    string // register, login
	rpID    string
	origin  string
	expires time.Time
}

var b64 = base64.RawURLEncoding

// ErrPasskey is returned for any failed passkey check.
var ErrPasskey = errors.New("passkey not accepted")

// RelyingParty derives the WebAuthn RP ID and origin from the browser's
// Origin header. Passkeys need a domain name (no IP address) and HTTPS
// (or localhost).
func RelyingParty(origin string) (rpID, cleanOrigin string, err error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return "", "", errors.New("the browser sent no origin")
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		return "", "", errors.New("passkeys need a host name (e.g. nas.home.arpa), not an IP address - open LocoStor by name")
	}
	if u.Scheme != "https" && host != "localhost" {
		return "", "", errors.New("passkeys need HTTPS with a trusted certificate")
	}
	return strings.ToLower(host), u.Scheme + "://" + u.Host, nil
}

func (m *Manager) newChallenge(kind, rpID, origin string) (id string, value []byte) {
	value = make([]byte, 32)
	_, _ = rand.Read(value)
	id = b64.EncodeToString(value)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for k, c := range m.challenges {
		if now.After(c.expires) {
			delete(m.challenges, k)
		}
	}
	// Bounded: unauthenticated clients can start logins, so the oldest
	// challenge makes room instead of refusing new ones.
	if len(m.challenges) >= 500 {
		var oldest string
		for k, c := range m.challenges {
			if oldest == "" || c.expires.Before(m.challenges[oldest].expires) {
				oldest = k
			}
		}
		delete(m.challenges, oldest)
	}
	m.challenges[id] = challenge{value: value, kind: kind, rpID: rpID, origin: origin, expires: now.Add(challengeTTL)}
	return id, value
}

// takeChallenge returns and removes the challenge the client echoes back.
func (m *Manager) takeChallenge(clientChallenge, kind string) (challenge, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.challenges[clientChallenge]
	if !ok || c.kind != kind || time.Now().After(c.expires) {
		return challenge{}, false
	}
	delete(m.challenges, clientChallenge)
	return c, true
}

// userHandle returns the stable WebAuthn user id, creating it once.
func (m *Manager) userHandle() []byte {
	c := m.store.Credentials()
	if h, err := b64.DecodeString(c.UserHandle); err == nil && len(h) > 0 {
		return h
	}
	h := make([]byte, 16)
	_, _ = rand.Read(h)
	_ = m.store.UpdateCredentials(func(cr *Credentials) {
		if cr.UserHandle == "" {
			cr.UserHandle = b64.EncodeToString(h)
		}
	})
	h, _ = b64.DecodeString(m.store.Credentials().UserHandle)
	return h
}

// BeginPasskeyRegistration returns PublicKeyCredentialCreationOptions. The
// password is asked again so a stolen session cannot add a passkey.
func (m *Manager) BeginPasskeyRegistration(password, origin string) (map[string]any, error) {
	c := m.store.Credentials()
	if bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte(password)) != nil {
		return nil, ErrInvalid
	}
	rpID, origin, err := RelyingParty(origin)
	if err != nil {
		return nil, err
	}
	_, ch := m.newChallenge("register", rpID, origin)
	exclude := []map[string]any{}
	for _, p := range c.Passkeys {
		exclude = append(exclude, map[string]any{"type": "public-key", "id": p.ID})
	}
	return map[string]any{
		"challenge": b64.EncodeToString(ch),
		"rp":        map[string]any{"id": rpID, "name": "LocoStor"},
		"user":      map[string]any{"id": b64.EncodeToString(m.userHandle()), "name": c.Username, "displayName": c.Username + " (LocoStor)"},
		"pubKeyCredParams": []map[string]any{
			{"type": "public-key", "alg": algES256}, {"type": "public-key", "alg": algEdDSA}, {"type": "public-key", "alg": algRS256},
		},
		"timeout":                int(challengeTTL / time.Millisecond),
		"attestation":            "none",
		"excludeCredentials":     exclude,
		"authenticatorSelection": map[string]any{"residentKey": "required", "requireResidentKey": true, "userVerification": "preferred"},
	}, nil
}

// PasskeyResponse is what the browser returns (binary fields base64url).
type PasskeyResponse struct {
	ID                string `json:"id"`
	ClientDataJSON    string `json:"client_data_json"`
	AttestationObject string `json:"attestation_object,omitempty"`
	AuthenticatorData string `json:"authenticator_data,omitempty"`
	Signature         string `json:"signature,omitempty"`
	UserHandle        string `json:"user_handle,omitempty"`
}

type clientData struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	Origin    string `json:"origin"`
}

func checkClientData(raw []byte, typ string) (clientData, error) {
	var cd clientData
	if err := json.Unmarshal(raw, &cd); err != nil {
		return cd, fmt.Errorf("%w: client data", ErrPasskey)
	}
	if cd.Type != typ {
		return cd, fmt.Errorf("%w: wrong type %q", ErrPasskey, cd.Type)
	}
	return cd, nil
}

type authData struct {
	rpIDHash  []byte
	flags     byte
	signCount uint32
	credID    []byte
	key       map[any]any
	keyRaw    []byte
}

const (
	flagUP = 0x01
	flagUV = 0x04
	flagAT = 0x40
)

func parseAuthData(b []byte) (authData, error) {
	var a authData
	if len(b) < 37 {
		return a, fmt.Errorf("%w: short authenticator data", ErrPasskey)
	}
	a.rpIDHash, a.flags, a.signCount = b[:32], b[32], binary.BigEndian.Uint32(b[33:37])
	if a.flags&flagAT == 0 {
		return a, nil
	}
	rest := b[37:]
	if len(rest) < 18 {
		return a, fmt.Errorf("%w: short credential data", ErrPasskey)
	}
	n := int(binary.BigEndian.Uint16(rest[16:18]))
	rest = rest[18:]
	if n == 0 || n > 1023 || len(rest) < n {
		return a, fmt.Errorf("%w: bad credential id", ErrPasskey)
	}
	a.credID, rest = rest[:n], rest[n:]
	v, used, err := decodeCBOR(rest)
	if err != nil {
		return a, fmt.Errorf("%w: bad public key", ErrPasskey)
	}
	key, ok := v.(map[any]any)
	if !ok {
		return a, fmt.Errorf("%w: bad public key", ErrPasskey)
	}
	a.key, a.keyRaw = key, rest[:used]
	return a, nil
}

func (a authData) check(rpID string) error {
	h := sha256.Sum256([]byte(rpID))
	if subtle.ConstantTimeCompare(a.rpIDHash, h[:]) != 1 {
		return fmt.Errorf("%w: registered for another site", ErrPasskey)
	}
	if a.flags&flagUP == 0 {
		return fmt.Errorf("%w: user not present", ErrPasskey)
	}
	return nil
}

func keyInt(k map[any]any, i int64) (int64, bool) {
	v, ok := k[i].(int64)
	return v, ok
}

func keyBytes(k map[any]any, i int64) []byte {
	v, _ := k[i].([]byte)
	return v
}

// verifySignature checks sig over data with a COSE public key.
func verifySignature(coseKey []byte, data, sig []byte) error {
	v, _, err := decodeCBOR(coseKey)
	if err != nil {
		return err
	}
	k, ok := v.(map[any]any)
	if !ok {
		return errCBOR
	}
	alg, _ := keyInt(k, 3)
	kty, _ := keyInt(k, 1)
	digest := sha256.Sum256(data)
	switch {
	case alg == algES256 && kty == 2:
		x, y := keyBytes(k, -2), keyBytes(k, -3)
		if crv, _ := keyInt(k, -1); crv != 1 || len(x) != 32 || len(y) != 32 {
			return errors.New("unsupported EC key")
		}
		pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
			return errors.New("invalid EC key")
		}
		if !ecdsa.VerifyASN1(pub, digest[:], sig) {
			return errors.New("bad signature")
		}
	case alg == algEdDSA && kty == 1:
		x := keyBytes(k, -2)
		if crv, _ := keyInt(k, -1); crv != 6 || len(x) != ed25519.PublicKeySize {
			return errors.New("unsupported OKP key")
		}
		if !ed25519.Verify(ed25519.PublicKey(x), data, sig) {
			return errors.New("bad signature")
		}
	case alg == algRS256 && kty == 3:
		n, e := keyBytes(k, -1), keyBytes(k, -2)
		if len(n) < 256 || len(e) == 0 || len(e) > 4 {
			return errors.New("unsupported RSA key")
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
			return errors.New("bad signature")
		}
	default:
		return fmt.Errorf("unsupported key algorithm %d", alg)
	}
	return nil
}

// FinishPasskeyRegistration verifies the new credential and stores it.
func (m *Manager) FinishPasskeyRegistration(name string, r PasskeyResponse) (PasskeyInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Passkey"
	}
	if len(name) > 64 || strings.ContainsAny(name, "\r\n") {
		return PasskeyInfo{}, errors.New("name too long")
	}
	raw, err := b64.DecodeString(r.ClientDataJSON)
	if err != nil {
		return PasskeyInfo{}, ErrPasskey
	}
	cd, err := checkClientData(raw, "webauthn.create")
	if err != nil {
		return PasskeyInfo{}, err
	}
	ch, ok := m.takeChallenge(cd.Challenge, "register")
	if !ok {
		return PasskeyInfo{}, fmt.Errorf("%w: the request expired - try again", ErrPasskey)
	}
	if cd.Origin != ch.origin {
		return PasskeyInfo{}, fmt.Errorf("%w: wrong origin", ErrPasskey)
	}
	att, err := b64.DecodeString(r.AttestationObject)
	if err != nil {
		return PasskeyInfo{}, ErrPasskey
	}
	v, _, err := decodeCBOR(att)
	if err != nil {
		return PasskeyInfo{}, fmt.Errorf("%w: attestation", ErrPasskey)
	}
	obj, _ := v.(map[any]any)
	ad, _ := obj["authData"].([]byte)
	a, err := parseAuthData(ad)
	if err != nil {
		return PasskeyInfo{}, err
	}
	if err := a.check(ch.rpID); err != nil {
		return PasskeyInfo{}, err
	}
	if a.flags&flagAT == 0 || a.key == nil {
		return PasskeyInfo{}, fmt.Errorf("%w: no credential", ErrPasskey)
	}
	alg, _ := keyInt(a.key, 3)
	// The key must be usable: check it with a dummy verify on the parser.
	if alg != algES256 && alg != algEdDSA && alg != algRS256 {
		return PasskeyInfo{}, fmt.Errorf("%w: unsupported key algorithm %d", ErrPasskey, alg)
	}
	p := Passkey{ID: b64.EncodeToString(a.credID), Name: name, PublicKey: a.keyRaw, Alg: alg, SignCount: a.signCount, RPID: ch.rpID, Created: time.Now()}
	err = m.store.UpdateCredentials(func(cr *Credentials) {
		for _, x := range cr.Passkeys {
			if x.ID == p.ID {
				return
			}
		}
		cr.Passkeys = append(cr.Passkeys, p)
	})
	return p.info(), err
}

func (p Passkey) info() PasskeyInfo {
	return PasskeyInfo{ID: p.ID, Name: p.Name, RPID: p.RPID, Created: p.Created, LastUsed: p.LastUsed}
}

// BeginPasskeyLogin returns PublicKeyCredentialRequestOptions. The
// passkey is discoverable, so no username is needed.
func (m *Manager) BeginPasskeyLogin(ip, origin string) (map[string]any, error) {
	if m.throttled(ip) {
		return nil, ErrThrottled
	}
	rpID, origin, err := RelyingParty(origin)
	if err != nil {
		return nil, err
	}
	_, ch := m.newChallenge("login", rpID, origin)
	return map[string]any{
		"challenge":        b64.EncodeToString(ch),
		"rpId":             rpID,
		"timeout":          int(challengeTTL / time.Millisecond),
		"userVerification": "preferred",
		"allowCredentials": []any{},
	}, nil
}

// FinishPasskeyLogin verifies an assertion and starts a session. A passkey
// is phishing-resistant on its own, so no second factor is asked.
func (m *Manager) FinishPasskeyLogin(ip string, r PasskeyResponse) (string, error) {
	if m.throttled(ip) {
		return "", ErrThrottled
	}
	token, err := m.verifyAssertion(r)
	if err != nil {
		m.recordFail(ip)
		return "", err
	}
	m.clearFails(ip)
	return token, nil
}

func (m *Manager) verifyAssertion(r PasskeyResponse) (string, error) {
	raw, err := b64.DecodeString(r.ClientDataJSON)
	if err != nil {
		return "", ErrPasskey
	}
	cd, err := checkClientData(raw, "webauthn.get")
	if err != nil {
		return "", err
	}
	ch, ok := m.takeChallenge(cd.Challenge, "login")
	if !ok {
		return "", fmt.Errorf("%w: the request expired - try again", ErrPasskey)
	}
	if cd.Origin != ch.origin {
		return "", fmt.Errorf("%w: wrong origin", ErrPasskey)
	}
	var pk *Passkey
	c := m.store.Credentials()
	for i := range c.Passkeys {
		if subtle.ConstantTimeCompare([]byte(c.Passkeys[i].ID), []byte(r.ID)) == 1 {
			pk = &c.Passkeys[i]
		}
	}
	if pk == nil {
		return "", fmt.Errorf("%w: unknown passkey (it may have been removed)", ErrPasskey)
	}
	if r.UserHandle != "" && r.UserHandle != c.UserHandle {
		return "", fmt.Errorf("%w: wrong user", ErrPasskey)
	}
	ad, err := b64.DecodeString(r.AuthenticatorData)
	if err != nil {
		return "", ErrPasskey
	}
	a, err := parseAuthData(ad)
	if err != nil {
		return "", err
	}
	if err := a.check(ch.rpID); err != nil {
		return "", err
	}
	sig, err := b64.DecodeString(r.Signature)
	if err != nil {
		return "", ErrPasskey
	}
	cdHash := sha256.Sum256(raw)
	if err := verifySignature(pk.PublicKey, append(append([]byte{}, ad...), cdHash[:]...), sig); err != nil {
		return "", fmt.Errorf("%w: %v", ErrPasskey, err)
	}
	if (a.signCount != 0 || pk.SignCount != 0) && a.signCount <= pk.SignCount {
		return "", fmt.Errorf("%w: signature counter went backwards - the key may be cloned", ErrPasskey)
	}
	_ = m.store.UpdateCredentials(func(cr *Credentials) {
		for i := range cr.Passkeys {
			if cr.Passkeys[i].ID == pk.ID {
				cr.Passkeys[i].SignCount, cr.Passkeys[i].LastUsed = a.signCount, time.Now()
			}
		}
	})
	return m.newSession(), nil
}

// Passkeys lists the registered passkeys.
func (m *Manager) Passkeys() []PasskeyInfo {
	out := []PasskeyInfo{}
	for _, p := range m.store.Credentials().Passkeys {
		out = append(out, p.info())
	}
	return out
}

// DeletePasskey removes a passkey after checking the password.
func (m *Manager) DeletePasskey(password, id string) error {
	c := m.store.Credentials()
	if bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte(password)) != nil {
		return ErrInvalid
	}
	found := false
	err := m.store.UpdateCredentials(func(cr *Credentials) {
		out := cr.Passkeys[:0:0]
		for _, p := range cr.Passkeys {
			if p.ID == id {
				found = true
				continue
			}
			out = append(out, p)
		}
		cr.Passkeys = out
	})
	if err == nil && !found {
		return errors.New("passkey not found")
	}
	return err
}
