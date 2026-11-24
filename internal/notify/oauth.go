package notify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Phydran6/LocoStor/internal/valid"
)

// OAuthClients are the app registrations used for "Sign in with ...".
// They can be built in (go build -ldflags "-X ...") or entered by the user.
type OAuthClients struct {
	MicrosoftID  string
	GoogleID     string
	GoogleSecret string // Google calls it a secret, but for installed apps it is not confidential
}

// Token is a stored OAuth grant.
type Token struct {
	Provider     string    `json:"provider"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret,omitempty"`
	Tenant       string    `json:"tenant,omitempty"`
	RefreshToken string    `json:"refresh_token"`
	AccessToken  string    `json:"access_token"`
	Expiry       time.Time `json:"expiry"`
}

const (
	msScope     = "https://outlook.office.com/SMTP.Send offline_access openid email"
	googleScope = "https://mail.google.com/"
	googleAuth  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleToken = "https://oauth2.googleapis.com/token"
	// Google allows any loopback port for installed apps. Nothing listens
	// there: the user copies the address the browser ends up on.
	googleRedirect = "http://127.0.0.1:53682/"
)

var msTokenBase = "https://login.microsoftonline.com/"

type pendingOAuth struct {
	tok      Token
	email    string
	device   string
	interval time.Duration
	expires  time.Time
	verifier string
	status   string // pending, ok, error
	err      string
}

// OAuthStart tells the UI what the user has to do.
type OAuthStart struct {
	Provider        string `json:"provider"`
	UserCode        string `json:"user_code,omitempty"`
	VerificationURI string `json:"verification_uri,omitempty"`
	AuthURL         string `json:"auth_url,omitempty"`
	ExpiresIn       int    `json:"expires_in"`
}

var tenantRe = regexp.MustCompile(`^[A-Za-z0-9.-]{1,100}$`)

func (m *Manager) postForm(ctx context.Context, u string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var e struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(data, &e)
	if resp.StatusCode >= 300 || e.Error != "" {
		if e.Error == "" {
			e.Error = resp.Status
		}
		return &oauthError{code: e.Error, desc: e.Description}
	}
	return json.Unmarshal(data, out)
}

type oauthError struct{ code, desc string }

func (e *oauthError) Error() string {
	if e.desc != "" {
		d, _, _ := strings.Cut(e.desc, "\r\n")
		return e.code + ": " + d
	}
	return e.code
}

// StartOAuth begins "Sign in with Microsoft/Google" for email.
func (m *Manager) StartOAuth(ctx context.Context, provider, email, clientID, secret, tenant string) (OAuthStart, error) {
	if clientID == "" {
		switch provider {
		case "microsoft":
			clientID = m.Clients.MicrosoftID
		case "google":
			clientID, secret = m.Clients.GoogleID, m.Clients.GoogleSecret
		}
	}
	if clientID == "" {
		return OAuthStart{}, valid.Errorf("enter the client ID of your app registration")
	}
	p := &pendingOAuth{tok: Token{Provider: provider, ClientID: clientID, ClientSecret: secret}, email: email, status: "pending"}
	switch provider {
	case "microsoft":
		if tenant == "" {
			tenant = "common"
		}
		if !tenantRe.MatchString(tenant) {
			return OAuthStart{}, valid.Errorf("invalid tenant")
		}
		p.tok.Tenant = tenant
		var r struct {
			DeviceCode string `json:"device_code"`
			UserCode   string `json:"user_code"`
			URI        string `json:"verification_uri"`
			ExpiresIn  int    `json:"expires_in"`
			Interval   int    `json:"interval"`
		}
		err := m.postForm(ctx, msTokenBase+tenant+"/oauth2/v2.0/devicecode", url.Values{"client_id": {clientID}, "scope": {msScope}}, &r)
		if err != nil {
			return OAuthStart{}, valid.Errorf("Microsoft refused the sign-in: %v", err)
		}
		p.device, p.interval, p.expires = r.DeviceCode, time.Duration(max(r.Interval, 2))*time.Second, time.Now().Add(time.Duration(r.ExpiresIn)*time.Second)
		m.mu.Lock()
		m.oauth = p
		m.mu.Unlock()
		go m.pollDevice(p)
		return OAuthStart{Provider: provider, UserCode: r.UserCode, VerificationURI: r.URI, ExpiresIn: r.ExpiresIn}, nil
	case "google":
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		p.verifier = base64.RawURLEncoding.EncodeToString(b)
		sum := sha256.Sum256([]byte(p.verifier))
		q := url.Values{
			"client_id":             {clientID},
			"redirect_uri":          {googleRedirect},
			"response_type":         {"code"},
			"scope":                 {googleScope},
			"access_type":           {"offline"},
			"prompt":                {"consent"},
			"login_hint":            {email},
			"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
			"code_challenge_method": {"S256"},
		}
		p.expires = time.Now().Add(15 * time.Minute)
		m.mu.Lock()
		m.oauth = p
		m.mu.Unlock()
		return OAuthStart{Provider: provider, AuthURL: googleAuth + "?" + q.Encode(), ExpiresIn: 900}, nil
	}
	return OAuthStart{}, valid.Errorf("sign-in is available for Microsoft and Google")
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func (m *Manager) pollDevice(p *pendingOAuth) {
	for time.Now().Before(p.expires) {
		time.Sleep(p.interval)
		m.mu.Lock()
		current := m.oauth == p
		m.mu.Unlock()
		if !current {
			return
		}
		var r tokenResponse
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := m.postForm(ctx, msTokenBase+p.tok.Tenant+"/oauth2/v2.0/token", url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"client_id":   {p.tok.ClientID},
			"device_code": {p.device},
		}, &r)
		cancel()
		var oe *oauthError
		if errors.As(err, &oe) && oe.code == "authorization_pending" {
			continue
		}
		if errors.As(err, &oe) && oe.code == "slow_down" {
			p.interval += 5 * time.Second
			continue
		}
		m.finishOAuth(p, r, err)
		return
	}
	m.finishOAuth(p, tokenResponse{}, errors.New("the sign-in code expired - start again"))
}

// finishOAuth stores the grant in the mail settings.
func (m *Manager) finishOAuth(p *pendingOAuth, r tokenResponse, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil && r.RefreshToken == "" {
		err = errors.New("no refresh token received - the app needs the offline_access permission")
	}
	if err != nil {
		p.status, p.err = "error", err.Error()
		return
	}
	p.tok.AccessToken, p.tok.RefreshToken = r.AccessToken, r.RefreshToken
	p.tok.Expiry = time.Now().Add(time.Duration(r.ExpiresIn) * time.Second)
	prov, _ := ProviderByID(p.tok.Provider)
	e := EmailConfig{Enabled: true, Provider: prov.ID, Host: prov.Host, Port: prov.Port, Security: prov.Security,
		Username: p.email, From: p.email, To: []string{p.email}}
	if c := m.cfg.Email; c != nil && c.Provider == prov.ID {
		e.To = c.To
	}
	tok := p.tok
	e.OAuth = &tok
	m.cfg.Email = &e
	if serr := m.saveLocked(); serr != nil {
		p.status, p.err = "error", serr.Error()
		return
	}
	p.status = "ok"
}

// OAuthStatus reports the state of the running sign-in.
func (m *Manager) OAuthStatus() (status, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.oauth == nil {
		return "none", ""
	}
	return m.oauth.status, m.oauth.err
}

// FinishGoogle exchanges the code from the address the browser ended up on
// (http://127.0.0.1:53682/?code=...) or the bare code.
func (m *Manager) FinishGoogle(ctx context.Context, pasted string) error {
	m.mu.Lock()
	p := m.oauth
	m.mu.Unlock()
	if p == nil || p.tok.Provider != "google" || time.Now().After(p.expires) {
		return valid.Errorf("start the Google sign-in again")
	}
	code := strings.TrimSpace(pasted)
	if u, err := url.Parse(code); err == nil && u.Query().Get("code") != "" {
		code = u.Query().Get("code")
	} else if err == nil && u.Query().Get("error") != "" {
		return valid.Errorf("Google reported: %s", u.Query().Get("error"))
	}
	if code == "" || strings.ContainsAny(code, " \n") {
		return valid.Errorf("paste the complete address from the browser")
	}
	var r tokenResponse
	err := m.postForm(ctx, googleToken, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {p.tok.ClientID},
		"client_secret": {p.tok.ClientSecret},
		"redirect_uri":  {googleRedirect},
		"code_verifier": {p.verifier},
	}, &r)
	if err != nil {
		err = valid.Errorf("Google refused the code: %v", err)
	}
	m.finishOAuth(p, r, err)
	if st, msg := m.OAuthStatus(); st != "ok" {
		return valid.Errorf("%s", msg)
	}
	return nil
}

// accessToken returns a valid access token, refreshing it when needed.
func (m *Manager) accessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	var tok Token
	if m.cfg.Email != nil && m.cfg.Email.OAuth != nil {
		tok = *m.cfg.Email.OAuth
	}
	m.mu.Unlock()
	if tok.RefreshToken == "" {
		return "", errors.New("not signed in")
	}
	if tok.AccessToken != "" && time.Until(tok.Expiry) > 2*time.Minute {
		return tok.AccessToken, nil
	}
	var r tokenResponse
	var err error
	switch tok.Provider {
	case "microsoft":
		err = m.postForm(ctx, msTokenBase+tok.Tenant+"/oauth2/v2.0/token", url.Values{
			"grant_type": {"refresh_token"}, "client_id": {tok.ClientID}, "refresh_token": {tok.RefreshToken}, "scope": {msScope},
		}, &r)
	case "google":
		err = m.postForm(ctx, googleToken, url.Values{
			"grant_type": {"refresh_token"}, "client_id": {tok.ClientID}, "client_secret": {tok.ClientSecret}, "refresh_token": {tok.RefreshToken},
		}, &r)
	default:
		err = fmt.Errorf("unknown OAuth provider %q", tok.Provider)
	}
	if err != nil {
		return "", fmt.Errorf("renew sign-in: %w (sign in again on the Notifications page)", err)
	}
	m.mu.Lock()
	if m.cfg.Email != nil && m.cfg.Email.OAuth != nil {
		o := m.cfg.Email.OAuth
		o.AccessToken, o.Expiry = r.AccessToken, time.Now().Add(time.Duration(r.ExpiresIn)*time.Second)
		if r.RefreshToken != "" {
			o.RefreshToken = r.RefreshToken
		}
		_ = m.saveLocked()
	}
	m.mu.Unlock()
	return r.AccessToken, nil
}
