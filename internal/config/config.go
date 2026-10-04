// Package config loads and stores the LocoStor configuration file.
package config

import (
	"sync"

	"github.com/Phydran6/LocoStor/internal/auth"
	"github.com/Phydran6/LocoStor/internal/fsutil"
)

// DefaultPath is where the config lives on a real installation.
const DefaultPath = "/etc/locostor/config.json"

// SmartDevice overrides SMART device discovery for one disk.
type SmartDevice struct {
	Device string `json:"device"`         // e.g. /dev/sdb
	Type   string `json:"type,omitempty"` // smartctl -d value, e.g. "sat"
}

// Config is the on-disk configuration. It holds secrets and is written
// with mode 0600.
type Config struct {
	Listen        string        `json:"listen"`
	Username      string        `json:"username"`
	PasswordHash  string        `json:"password_hash,omitempty"`
	TOTPSecret    string        `json:"totp_secret,omitempty"`
	RecoveryCodes []string      `json:"recovery_codes,omitempty"`
	UpdateRepo    string        `json:"update_repo"`
	DataDir       string        `json:"data_dir"`
	TLSCert       string        `json:"tls_cert,omitempty"`
	TLSKey        string        `json:"tls_key,omitempty"`
	HTTPRedirect  []string      `json:"http_redirect,omitempty"` // plain HTTP addresses redirecting to HTTPS
	HostSocket    string        `json:"host_socket,omitempty"`   // agent on the Proxmox host
	SmartDevices  []SmartDevice `json:"smart_devices,omitempty"`

	path string
	mu   sync.Mutex
}

// Load reads the config from path, filling in defaults for missing values.
func Load(path string) (*Config, error) {
	c := &Config{path: path}
	if err := fsutil.ReadJSON(path, c); err != nil {
		return nil, err
	}
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.Username == "" {
		c.Username = "admin"
	}
	if c.UpdateRepo == "" {
		c.UpdateRepo = "Phydran6/LocoStor"
	}
	if c.DataDir == "" {
		c.DataDir = "/var/lib/locostor"
	}
	if c.HostSocket == "" {
		c.HostSocket = c.DataDir + "/host/agent.sock"
	}
	return c, nil
}

// Path returns the file the config was loaded from.
func (c *Config) Path() string { return c.path }

// Update changes the config under its lock and saves it.
func (c *Config) Update(fn func(*Config)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(c)
	return fsutil.WriteJSON(c.path, c, 0o600)
}

// Credentials implements auth.Store.
func (c *Config) Credentials() auth.Credentials {
	c.mu.Lock()
	defer c.mu.Unlock()
	return auth.Credentials{
		Username:      c.Username,
		PasswordHash:  c.PasswordHash,
		TOTPSecret:    c.TOTPSecret,
		RecoveryCodes: append([]string(nil), c.RecoveryCodes...),
	}
}

// UpdateCredentials implements auth.Store.
func (c *Config) UpdateCredentials(fn func(*auth.Credentials)) error {
	return c.Update(func(c *Config) {
		cr := auth.Credentials{Username: c.Username, PasswordHash: c.PasswordHash, TOTPSecret: c.TOTPSecret, RecoveryCodes: c.RecoveryCodes}
		fn(&cr)
		c.Username, c.PasswordHash, c.TOTPSecret, c.RecoveryCodes = cr.Username, cr.PasswordHash, cr.TOTPSecret, cr.RecoveryCodes
	})
}
