package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/Phydran6/LocoStor/internal/valid"
)

// EmailConfig is an SMTP account.
type EmailConfig struct {
	Enabled  bool     `json:"enabled"`
	Provider string   `json:"provider"`
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Security string   `json:"security"` // starttls, tls, none
	Username string   `json:"username"`
	Password string   `json:"password,omitempty"`
	From     string   `json:"from"`
	To       []string `json:"to"`
	// OAuth replaces the password for Microsoft and Google sign-in.
	OAuth *Token `json:"oauth,omitempty"`
}

// EmailPublic is EmailConfig without secrets.
type EmailPublic struct {
	Enabled     bool     `json:"enabled"`
	Provider    string   `json:"provider"`
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	Security    string   `json:"security"`
	Username    string   `json:"username"`
	From        string   `json:"from"`
	To          []string `json:"to"`
	HasPassword bool     `json:"has_password"`
	OAuth       bool     `json:"oauth"`
}

func (e EmailConfig) public() EmailPublic {
	return EmailPublic{Enabled: e.Enabled, Provider: e.Provider, Host: e.Host, Port: e.Port, Security: e.Security,
		Username: e.Username, From: e.From, To: append([]string{}, e.To...), HasPassword: e.Password != "", OAuth: e.OAuth != nil}
}

// Provider is a mail provider preset.
type Provider struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Security string   `json:"security"`
	Auth     string   `json:"auth"` // password, app-password, oauth
	Domains  []string `json:"-"`
	MX       []string `json:"-"` // MX host suffixes for custom domains
	Hint     string   `json:"hint"`
	Link     string   `json:"link,omitempty"`
}

// Providers are the common mail services.
var Providers = []Provider{
	{ID: "microsoft", Name: "Microsoft 365 / Outlook.com", Host: "smtp.office365.com", Port: 587, Security: "starttls", Auth: "oauth",
		Domains: []string{"outlook.com", "outlook.de", "hotmail.com", "hotmail.de", "live.com", "live.de", "msn.com"},
		MX:      []string{".mail.protection.outlook.com", ".olc.protection.outlook.com"},
		Hint:    "Sign in once with your Microsoft account – no password is stored."},
	{ID: "google", Name: "Google Gmail / Workspace", Host: "smtp.gmail.com", Port: 587, Security: "starttls", Auth: "oauth",
		Domains: []string{"gmail.com", "googlemail.com"}, MX: []string{".google.com", ".googlemail.com"},
		Hint: "Sign in once with Google, or use an app password (needs 2-step verification).", Link: "https://myaccount.google.com/apppasswords"},
	{ID: "webde", Name: "WEB.DE", Host: "smtp.web.de", Port: 587, Security: "starttls", Auth: "password",
		Domains: []string{"web.de"}, MX: []string{".web.de"},
		Hint: "Turn on \"POP3/IMAP Abruf\" in the WEB.DE settings (E-Mail → Einstellungen), then use your normal password.", Link: "https://web.de/email/einstellungen/"},
	{ID: "gmx", Name: "GMX", Host: "mail.gmx.net", Port: 587, Security: "starttls", Auth: "password",
		Domains: []string{"gmx.de", "gmx.net", "gmx.at", "gmx.ch"}, MX: []string{".gmx.net"},
		Hint: "Turn on \"POP3/IMAP Abruf\" in the GMX settings, then use your normal password.", Link: "https://www.gmx.net/mail/einstellungen/"},
	{ID: "tonline", Name: "Telekom (t-online.de)", Host: "securesmtp.t-online.de", Port: 587, Security: "starttls", Auth: "password",
		Domains: []string{"t-online.de", "magenta.de"}, MX: []string{".t-online.de"},
		Hint: "Use the separate e-mail password (\"Passwort für E-Mail-Programme\") from the Telekom Kundencenter."},
	{ID: "ionos", Name: "IONOS (1&1)", Host: "smtp.ionos.de", Port: 587, Security: "starttls", Auth: "password",
		Domains: []string{"online.de", "onlinehome.de"}, MX: []string{".ionos.de", ".kundenserver.de"}, Hint: "Use the password of the mailbox."},
	{ID: "strato", Name: "STRATO", Host: "smtp.strato.de", Port: 465, Security: "tls", Auth: "password",
		MX: []string{".rzone.de", ".strato.de"}, Hint: "Use the password of the mailbox."},
	{ID: "icloud", Name: "Apple iCloud Mail", Host: "smtp.mail.me.com", Port: 587, Security: "starttls", Auth: "app-password",
		Domains: []string{"icloud.com", "me.com", "mac.com"}, MX: []string{".mail.icloud.com"},
		Hint: "Create an app-specific password in your Apple account (Sign-In and Security).", Link: "https://account.apple.com/account/manage"},
	{ID: "yahoo", Name: "Yahoo Mail", Host: "smtp.mail.yahoo.com", Port: 465, Security: "tls", Auth: "app-password",
		Domains: []string{"yahoo.com", "yahoo.de", "ymail.com"}, MX: []string{".yahoodns.net"},
		Hint: "Generate an app password in the Yahoo account security settings.", Link: "https://login.yahoo.com/account/security"},
	{ID: "posteo", Name: "Posteo", Host: "posteo.de", Port: 587, Security: "starttls", Auth: "password", Domains: []string{"posteo.de", "posteo.net"}, MX: []string{".posteo.de"}},
	{ID: "mailbox", Name: "mailbox.org", Host: "smtp.mailbox.org", Port: 465, Security: "tls", Auth: "password", Domains: []string{"mailbox.org"}, MX: []string{".mailbox.org"}},
	{ID: "freenet", Name: "freenet Mail", Host: "mx.freenet.de", Port: 587, Security: "starttls", Auth: "password", Domains: []string{"freenet.de"}},
	{ID: "custom", Name: "Other (enter server)", Port: 587, Security: "starttls", Auth: "password"},
}

// ProviderByID returns a preset.
func ProviderByID(id string) (Provider, bool) {
	for _, p := range Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// Detect finds the provider and server for an e-mail address: by domain,
// then by MX record (Microsoft 365 / Google Workspace on own domains), then
// via Thunderbird's autoconfig database.
func (m *Manager) Detect(ctx context.Context, email string) (Provider, error) {
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return Provider{}, valid.Errorf("not a valid e-mail address")
	}
	_, domain, _ := strings.Cut(strings.ToLower(addr.Address), "@")
	for _, p := range Providers {
		for _, d := range p.Domains {
			if d == domain {
				return p, nil
			}
		}
	}
	var r net.Resolver
	if mxs, err := r.LookupMX(ctx, domain); err == nil {
		for _, mx := range mxs {
			host := strings.TrimSuffix(strings.ToLower(mx.Host), ".")
			for _, p := range Providers {
				for _, suf := range p.MX {
					if strings.HasSuffix(host, suf) {
						return p, nil
					}
				}
			}
		}
	}
	if p, ok := m.autoconfig(ctx, domain); ok {
		return p, nil
	}
	p, _ := ProviderByID("custom")
	p.Host = "smtp." + domain
	return p, nil
}

// autoconfig asks Thunderbird's ISP database for the SMTP server.
func (m *Manager) autoconfig(ctx context.Context, domain string) (Provider, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://autoconfig.thunderbird.net/v1.1/"+domain, nil)
	if err != nil {
		return Provider{}, false
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return Provider{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Provider{}, false
	}
	var doc struct {
		Provider struct {
			Name     string `xml:"displayName"`
			Outgoing []struct {
				Type     string   `xml:"type,attr"`
				Host     string   `xml:"hostname"`
				Port     int      `xml:"port"`
				Socket   string   `xml:"socketType"`
				AuthType []string `xml:"authentication"`
			} `xml:"outgoingServer"`
		} `xml:"emailProvider"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return Provider{}, false
	}
	for _, o := range doc.Provider.Outgoing {
		if o.Type != "smtp" || o.Host == "" {
			continue
		}
		sec := "starttls"
		if strings.EqualFold(o.Socket, "SSL") {
			sec = "tls"
		}
		return Provider{ID: "custom", Name: doc.Provider.Name, Host: o.Host, Port: o.Port, Security: sec, Auth: "password",
			Hint: "Server found in the Thunderbird provider database."}, true
	}
	return Provider{}, false
}

func validateEmail(e *EmailConfig) error {
	e.Host = strings.TrimSpace(e.Host)
	if e.Host == "" || strings.ContainsAny(e.Host, " /:") {
		return valid.Errorf("enter the SMTP server name")
	}
	if e.Port <= 0 || e.Port > 65535 {
		return valid.Errorf("invalid port")
	}
	switch e.Security {
	case "starttls", "tls", "none":
	default:
		return valid.Errorf("security must be starttls, tls or none")
	}
	if _, err := mail.ParseAddress(e.From); err != nil {
		return valid.Errorf("invalid sender address")
	}
	if len(e.To) == 0 {
		return valid.Errorf("enter at least one recipient")
	}
	for _, t := range e.To {
		if _, err := mail.ParseAddress(t); err != nil {
			return valid.Errorf("invalid recipient %q", t)
		}
	}
	for _, f := range []string{e.Username, e.Password, e.From} {
		if strings.ContainsAny(f, "\r\n") {
			return valid.Errorf("line breaks are not allowed")
		}
	}
	return nil
}

// SetEmail stores the mail account. An empty password keeps the stored one;
// OAuth tokens stay unless the provider or user changes.
func (m *Manager) SetEmail(e EmailConfig) error {
	e.OAuth = nil
	if err := validateEmail(&e); err != nil {
		return err
	}
	return m.update(func(c *Config) error {
		if c.Email != nil {
			if e.Password == "" {
				e.Password = c.Email.Password
			}
			if c.Email.OAuth != nil && c.Email.Provider == e.Provider && strings.EqualFold(c.Email.Username, e.Username) {
				e.OAuth = c.Email.OAuth
			}
		}
		if p, ok := ProviderByID(e.Provider); ok && p.Auth == "oauth" && e.OAuth == nil && e.Password == "" {
			return valid.Errorf("sign in with %s first, or enter an app password", p.Name)
		}
		c.Email = &e
		return nil
	})
}

// DeleteEmail removes the mail account.
func (m *Manager) DeleteEmail() error {
	return m.update(func(c *Config) error { c.Email = nil; return nil })
}

// loginAuth implements the LOGIN mechanism (some providers offer no PLAIN).
type loginAuth struct{ user, pass string }

func (a loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) { return "LOGIN", nil, nil }
func (a loginAuth) Next(from []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(from))) {
	case "username:":
		return []byte(a.user), nil
	case "password:":
		return []byte(a.pass), nil
	}
	return nil, fmt.Errorf("unexpected LOGIN challenge %q", from)
}

// xoauth2 implements Google's and Microsoft's XOAUTH2 mechanism.
type xoauth2 struct{ user, token string }

func (a xoauth2) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "XOAUTH2", []byte("user=" + a.user + "\x01auth=Bearer " + a.token + "\x01\x01"), nil
}
func (a xoauth2) Next(from []byte, more bool) ([]byte, error) {
	if more {
		return []byte{}, nil // the server sent an error description
	}
	return nil, nil
}

func encodeHeader(s string) string { return mime.QEncoding.Encode("utf-8", s) }

func buildMessage(from string, to []string, subject, body, host string) []byte {
	var b bytes.Buffer
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	fmt.Fprintf(&b, "From: LocoStor <%s>\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", encodeHeader(subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), host)
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	qp := quotedprintable.NewWriter(&b)
	_, _ = qp.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
	_ = qp.Close()
	return b.Bytes()
}

func (m *Manager) sendEmail(ctx context.Context, e EmailConfig, subject, body string) error {
	addr := net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
	d := net.Dialer{Timeout: 20 * time.Second}
	var conn net.Conn
	var err error
	if e.Security == "tls" {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: e.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(time.Minute))
	c, err := smtp.NewClient(conn, e.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if err := c.Hello(strings.ReplaceAll(m.Hostname, "_", "-")); err != nil {
		return err
	}
	if e.Security == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the server does not offer STARTTLS")
		}
		if err := c.StartTLS(&tls.Config{ServerName: e.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if e.Username != "" {
		_, mechs := c.Extension("AUTH")
		var auth smtp.Auth
		switch {
		case e.OAuth != nil:
			tok, err := m.accessToken(ctx)
			if err != nil {
				return err
			}
			auth = xoauth2{e.Username, tok}
		case strings.Contains(mechs, "PLAIN") && e.Security != "none":
			auth = smtp.PlainAuth("", e.Username, e.Password, e.Host)
		default:
			auth = loginAuth{e.Username, e.Password}
		}
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("login failed: %w", err)
		}
	}
	if err := c.Mail(e.From); err != nil {
		return err
	}
	for _, t := range e.To {
		if err := c.Rcpt(t); err != nil {
			return fmt.Errorf("recipient %s: %w", t, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(buildMessage(e.From, e.To, subject, body, e.Host)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
