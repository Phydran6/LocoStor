// Package config loads and stores the LocoStor configuration file.
package config

import (
	"sync"

	"github.com/Phydran6/LocoStor/internal/fsutil"
)

// DefaultPath is where the config lives on a real installation.
const DefaultPath = "/etc/locostor/config.json"

// SmartDevice overrides SMART device discovery for one disk.
type SmartDevice struct {
	Device string `json:"device"`         // e.g. /dev/sdb
	Type   string `json:"type,omitempty"` // smartctl -d value, e.g. "sat"
}

// Config is the on-disk configuration.
type Config struct {
	Listen       string        `json:"listen"`
	PasswordHash string        `json:"password_hash,omitempty"`
	UpdateRepo   string        `json:"update_repo"`
	DataDir      string        `json:"data_dir"`
	TLSCert      string        `json:"tls_cert,omitempty"`
	TLSKey       string        `json:"tls_key,omitempty"`
	SmartDevices []SmartDevice `json:"smart_devices,omitempty"`

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
	if c.UpdateRepo == "" {
		c.UpdateRepo = "Phydran6/LocoStor"
	}
	if c.DataDir == "" {
		c.DataDir = "/var/lib/locostor"
	}
	return c, nil
}

// Path returns the file the config was loaded from.
func (c *Config) Path() string { return c.path }

// GetPasswordHash returns the current admin password hash.
func (c *Config) GetPasswordHash() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.PasswordHash
}

// SetPasswordHash updates the admin password hash and persists the config.
func (c *Config) SetPasswordHash(hash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.PasswordHash = hash
	return fsutil.WriteJSON(c.path, c, 0o600)
}
