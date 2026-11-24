// Package notify sends alerts (RAID, SMART, disk space, updates) by e-mail
// and Telegram. Mail accounts are set up with provider presets, including
// OAuth sign-in for Microsoft 365 / Outlook and Google; Telegram bots are
// connected with a link the user opens once.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Phydran6/LocoStor/internal/fsutil"
)

// Severities.
const (
	Info    = "info"
	Warning = "warning"
	Error   = "error"
)

// Events chooses what is reported.
type Events struct {
	RAID        bool `json:"raid"`
	SMART       bool `json:"smart"`
	Temperature int  `json:"temperature"` // °C; 0 = off
	Space       int  `json:"space"`       // % used; 0 = off
	Updates     bool `json:"updates"`
	// MinSeverity drops messages below it (info, warning, error).
	MinSeverity string `json:"min_severity"`
}

// Config is stored in the data directory (mode 0600, holds secrets).
type Config struct {
	Email    *EmailConfig    `json:"email,omitempty"`
	Telegram *TelegramConfig `json:"telegram,omitempty"`
	Events   Events          `json:"events"`
}

// Message is one sent (or attempted) notification.
type Message struct {
	Time     time.Time `json:"time"`
	Kind     string    `json:"kind"`
	Severity string    `json:"severity"`
	Title    string    `json:"title"`
	Body     string    `json:"body"`
	Sent     []string  `json:"sent"`
	Errors   []string  `json:"errors,omitempty"`
}

// Manager holds the configuration and sends messages.
type Manager struct {
	path     string
	histPath string
	Hostname string
	client   *http.Client
	// Clients holds the OAuth app IDs built into this binary (may be empty).
	Clients OAuthClients

	mu      sync.Mutex
	cfg     Config
	history []Message
	oauth   *pendingOAuth
	tgPend  *pendingTelegram
}

// DefaultEvents reports everything important.
func DefaultEvents() Events {
	return Events{RAID: true, SMART: true, Temperature: 55, Space: 90, Updates: true, MinSeverity: Warning}
}

// New loads the configuration from path.
func New(path, histPath, hostname string) *Manager {
	m := &Manager{path: path, histPath: histPath, Hostname: hostname, client: &http.Client{Timeout: 30 * time.Second}}
	m.cfg.Events = DefaultEvents()
	_ = fsutil.ReadJSON(path, &m.cfg)
	_ = fsutil.ReadJSON(histPath, &m.history)
	return m
}

func (m *Manager) saveLocked() error {
	return fsutil.WriteJSON(m.path, m.cfg, 0o600)
}

// update changes the config under the lock and saves it.
func (m *Manager) update(fn func(*Config) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := fn(&m.cfg); err != nil {
		return err
	}
	return m.saveLocked()
}

func (m *Manager) config() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

// Events returns the event settings.
func (m *Manager) Events() Events { return m.config().Events }

// SetEvents stores new event settings.
func (m *Manager) SetEvents(e Events) error {
	if e.Temperature < 0 || e.Temperature > 100 || e.Space < 0 || e.Space > 100 {
		return errors.New("limits must be between 0 and 100")
	}
	switch e.MinSeverity {
	case Info, Warning, Error:
	default:
		e.MinSeverity = Warning
	}
	return m.update(func(c *Config) error { c.Events = e; return nil })
}

var rank = map[string]int{Info: 0, Warning: 1, Error: 2}

// Configured reports whether any channel is enabled.
func (m *Manager) Configured() bool {
	c := m.config()
	return (c.Email != nil && c.Email.Enabled) || (c.Telegram != nil && c.Telegram.Enabled && len(c.Telegram.Chats) > 0)
}

// Notify sends a message if its severity passes the filter.
func (m *Manager) Notify(kind, severity, title, body string) {
	if rank[severity] < rank[m.Events().MinSeverity] && kind != "test" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = m.Send(ctx, kind, severity, title, body)
	}()
}

// Send delivers a message to all enabled channels now.
func (m *Manager) Send(ctx context.Context, kind, severity, title, body string) (Message, error) {
	msg := Message{Time: time.Now(), Kind: kind, Severity: severity, Title: title, Body: body, Sent: []string{}}
	c := m.config()
	full := fmt.Sprintf("[%s] %s", m.Hostname, title)
	if c.Email != nil && c.Email.Enabled {
		if err := m.sendEmail(ctx, *c.Email, full, body+"\n\n-- \nLocoStor on "+m.Hostname); err != nil {
			msg.Errors = append(msg.Errors, "e-mail: "+err.Error())
		} else {
			msg.Sent = append(msg.Sent, "e-mail")
		}
	}
	if c.Telegram != nil && c.Telegram.Enabled && len(c.Telegram.Chats) > 0 {
		if err := m.sendTelegram(ctx, *c.Telegram, severity, full, body); err != nil {
			msg.Errors = append(msg.Errors, "telegram: "+err.Error())
		} else {
			msg.Sent = append(msg.Sent, "telegram")
		}
	}
	m.mu.Lock()
	m.history = append([]Message{msg}, m.history...)
	if len(m.history) > 100 {
		m.history = m.history[:100]
	}
	hist := append([]Message(nil), m.history...)
	m.mu.Unlock()
	if m.histPath != "" {
		_ = fsutil.WriteJSON(m.histPath, hist, 0o600)
	}
	for _, e := range msg.Errors {
		log.Printf("notification %q: %s", title, e)
	}
	if len(msg.Sent) == 0 && len(msg.Errors) == 0 {
		return msg, errors.New("no notification channel is set up")
	}
	if len(msg.Errors) > 0 {
		return msg, errors.New(strings.Join(msg.Errors, "; "))
	}
	return msg, nil
}

// History returns recent messages, newest first.
func (m *Manager) History() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Message{}, m.history...)
}

// Public is the configuration without secrets, for the UI.
type Public struct {
	Email    *EmailPublic    `json:"email"`
	Telegram *TelegramPublic `json:"telegram"`
	Events   Events          `json:"events"`
	History  []Message       `json:"history"`
	OAuth    map[string]bool `json:"oauth_builtin"` // providers with a built-in app
}

// Public returns the configuration for the UI.
func (m *Manager) Public() Public {
	c := m.config()
	p := Public{Events: c.Events, History: m.History(), OAuth: map[string]bool{
		"microsoft": m.Clients.MicrosoftID != "",
		"google":    m.Clients.GoogleID != "",
	}}
	if c.Email != nil {
		e := c.Email.public()
		p.Email = &e
	}
	if c.Telegram != nil {
		t := c.Telegram.public()
		p.Telegram = &t
	}
	return p
}
