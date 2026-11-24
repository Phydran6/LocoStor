// Package smb manages Samba shares and users.
//
// LocoStor keeps its shares in a JSON state file and renders them into a
// dedicated include file (by default /etc/samba/locostor.conf) that is
// referenced from smb.conf. Everything else in smb.conf is left untouched.
package smb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Phydran6/LocoStor/internal/fsutil"
	"github.com/Phydran6/LocoStor/internal/sysexec"
	"github.com/Phydran6/LocoStor/internal/valid"
)

// Share is one SMB share.
type Share struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Comment    string `json:"comment"`
	Browseable bool   `json:"browseable"`
	Enabled    bool   `json:"enabled"`

	// Everyone is the access of every SMB user not listed in Access:
	// write, read or none.
	Everyone string `json:"everyone"`
	// Access lists users and groups (@name) with read, write or deny.
	Access []ACE `json:"access"`
	// Guest is the access without a password: none, read or write.
	Guest string `json:"guest"`
	// FixPermissions makes the filesystem allow what the share allows
	// (force group + folder permissions for the LocoStor group).
	FixPermissions bool `json:"fix_permissions"`

	// Legacy fields, kept in sync for older clients and state files.
	ReadOnly   bool     `json:"read_only"`
	GuestOK    bool     `json:"guest_ok"`
	ValidUsers []string `json:"valid_users"`

	// Options are additional smb.conf parameters, rendered verbatim.
	Options []Option `json:"options"`

	// Warnings are computed when listing (not stored), e.g. when the
	// folder permissions would block writing.
	Warnings []string `json:"warnings,omitempty"`
}

// Option is one extra "key = value" share parameter.
type Option struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Options configures file locations.
type Options struct {
	StatePath   string // JSON state, e.g. /var/lib/locostor/smb-shares.json
	IncludePath string // generated file, e.g. /etc/samba/locostor.conf
	MainConf    string // e.g. /etc/samba/smb.conf
	// SkipPathCheck disables the check that share paths exist (demo mode).
	SkipPathCheck bool
}

// Manager manages shares and users.
type Manager struct {
	run  sysexec.Runner
	opts Options
	mu   sync.Mutex
}

// New creates a Manager.
func New(run sysexec.Runner, opts Options) *Manager {
	return &Manager{run: run, opts: opts}
}

var (
	shareNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,78}\$?$`) // trailing $ = hidden share
	userNameRe  = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	reserved    = map[string]bool{"global": true, "homes": true, "printers": true, "print$": true, "ipc$": true}
)

// Shares returns all shares sorted by name.
func (m *Manager) Shares() ([]Share, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	shares, err := m.load()
	if err == nil && !m.opts.SkipPathCheck {
		for i := range shares {
			shares[i].Warnings = pathWarnings(shares[i])
		}
	}
	return shares, err
}

// Get returns one managed share.
func (m *Manager) Get(name string) (*Share, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	shares, err := m.load()
	if err != nil {
		return nil, err
	}
	for i := range shares {
		if strings.EqualFold(shares[i].Name, name) {
			return &shares[i], nil
		}
	}
	return nil, &valid.NotFound{What: "share " + name}
}

func (m *Manager) load() ([]Share, error) {
	var shares []Share
	if err := fsutil.ReadJSON(m.opts.StatePath, &shares); err != nil {
		return nil, err
	}
	for i := range shares {
		if shares[i].Options == nil {
			shares[i].Options = []Option{}
		}
		_ = normalizeAccess(&shares[i]) // old state files
		if shares[i].Access == nil {
			shares[i].Access = []ACE{}
		}
		shares[i].Warnings = nil
	}
	sort.Slice(shares, func(i, j int) bool {
		return strings.ToLower(shares[i].Name) < strings.ToLower(shares[j].Name)
	})
	if shares == nil {
		shares = []Share{}
	}
	return shares, nil
}

func (m *Manager) validate(s *Share) error { return validateShare(s, !m.opts.SkipPathCheck) }

// validateShare checks and normalizes a share. With mustExist the path must
// be an existing directory.
func validateShare(s *Share, mustExist bool) error {
	s.Name = strings.TrimSpace(s.Name)
	if !shareNameRe.MatchString(s.Name) {
		return valid.Errorf("share name may contain letters, digits, space, '.', '_' and '-' (max 80)")
	}
	if reserved[strings.ToLower(s.Name)] {
		return valid.Errorf("share name %q is reserved", s.Name)
	}
	p, err := valid.SharePath("path", s.Path, mustExist)
	if err != nil {
		return err
	}
	s.Path = p
	if err := valid.SingleLine("comment", s.Comment); err != nil {
		return err
	}
	s.Warnings = nil
	if err := normalizeAccess(s); err != nil {
		return err
	}
	return validateOptions(s)
}

// Save creates (oldName == "") or updates the share oldName and applies the
// configuration.
func (m *Manager) Save(ctx context.Context, oldName string, s Share) (Share, error) {
	if err := m.validate(&s); err != nil {
		return s, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	shares, err := m.load()
	if err != nil {
		return s, err
	}
	idx := -1
	for i, x := range shares {
		if oldName != "" && strings.EqualFold(x.Name, oldName) {
			idx = i
			continue
		}
		if strings.EqualFold(x.Name, s.Name) {
			return s, valid.Errorf("a share named %q already exists", s.Name)
		}
	}
	if oldName != "" && idx < 0 {
		return s, &valid.NotFound{What: "share " + oldName}
	}
	if s.FixPermissions {
		// "force group" must name an existing group, or logins fail.
		if err := EnsureGroup(ctx, m.run); err != nil {
			return s, err
		}
	}
	next := append([]Share(nil), shares...)
	if idx >= 0 {
		next[idx] = s
	} else {
		next = append(next, s)
	}
	return s, m.apply(ctx, next)
}

// Delete removes a share.
func (m *Manager) Delete(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	shares, err := m.load()
	if err != nil {
		return err
	}
	next := shares[:0:0]
	found := false
	for _, s := range shares {
		if strings.EqualFold(s.Name, name) {
			found = true
			continue
		}
		next = append(next, s)
	}
	if !found {
		return &valid.NotFound{What: "share " + name}
	}
	return m.apply(ctx, next)
}

func yesno(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// Render produces the Samba include file for shares.
func Render(shares []Share) string {
	var b strings.Builder
	b.WriteString("# Managed by LocoStor. Do not edit - changes will be overwritten.\n")
	for _, s := range shares {
		b.WriteString("\n")
		writeShare(&b, s, nil)
	}
	return b.String()
}

// writeShare renders one share section. comments are kept from an existing
// section that is rewritten in place.
func writeShare(b *strings.Builder, s Share, comments []string) {
	fmt.Fprintf(b, "[%s]\n", s.Name)
	for _, c := range comments {
		fmt.Fprintf(b, "   %s\n", c)
	}
	fmt.Fprintf(b, "   path = %s\n", s.Path)
	if s.Comment != "" {
		fmt.Fprintf(b, "   comment = %s\n", s.Comment)
	}
	fmt.Fprintf(b, "   browseable = %s\n", yesno(s.Browseable))
	for _, o := range accessLines(s) {
		fmt.Fprintf(b, "   %s = %s\n", o.Key, o.Value)
	}
	for _, o := range permissionLines(s) {
		fmt.Fprintf(b, "   %s = %s\n", o.Key, o.Value)
	}
	for _, o := range s.Options {
		fmt.Fprintf(b, "   %s = %s\n", o.Key, o.Value)
	}
	if !s.Enabled {
		b.WriteString("   available = no\n")
	}
}

func (m *Manager) apply(ctx context.Context, shares []Share) error {
	rendered := Render(shares)

	// Validate the generated shares with testparm before touching anything.
	tmp, err := os.CreateTemp("", "locostor-smb-*.conf")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString("[global]\n" + rendered)
	tmp.Close()
	if err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, "", "testparm", "-s", "--suppress-prompt", tmp.Name()); err != nil {
		return fmt.Errorf("samba rejected the configuration: %w", err)
	}

	if err := m.ensureInclude(); err != nil {
		return fmt.Errorf("update %s: %w", m.opts.MainConf, err)
	}
	if err := fsutil.WriteFileAtomic(m.opts.IncludePath, []byte(rendered), 0o644); err != nil {
		return err
	}
	if err := fsutil.WriteJSON(m.opts.StatePath, shares, 0o600); err != nil {
		return err
	}
	return m.reload(ctx)
}

func (m *Manager) ensureInclude() error {
	data, err := os.ReadFile(m.opts.MainConf)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "include") && strings.Contains(l, m.opts.IncludePath) {
			return nil
		}
	}
	s := string(data)
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	s += "\n# Shares managed by LocoStor\ninclude = " + m.opts.IncludePath + "\n"
	return fsutil.WriteFileAtomic(m.opts.MainConf, []byte(s), 0o644)
}

func (m *Manager) reload(ctx context.Context) error { return reloadSamba(ctx, m.run) }

func reloadSamba(ctx context.Context, run sysexec.Runner) error {
	if _, err := run.Run(ctx, "", "smbcontrol", "smbd", "reload-config"); err == nil {
		return nil
	}
	if _, err := run.Run(ctx, "", "systemctl", "reload-or-restart", "smbd"); err != nil {
		return fmt.Errorf("reload samba: %w", err)
	}
	return nil
}

// Status returns the state of the Samba services.
func (m *Manager) Status(ctx context.Context) map[string]string {
	return map[string]string{"smbd": sysexec.ServiceState(ctx, m.run, "smbd")}
}

// DefaultOptions returns the standard file locations under dataDir.
func DefaultOptions(dataDir string) Options {
	return Options{
		StatePath:   filepath.Join(dataDir, "smb-shares.json"),
		IncludePath: "/etc/samba/locostor.conf",
		MainConf:    "/etc/samba/smb.conf",
	}
}
