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
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	Comment    string   `json:"comment"`
	ReadOnly   bool     `json:"read_only"`
	Browseable bool     `json:"browseable"`
	GuestOK    bool     `json:"guest_ok"`
	ValidUsers []string `json:"valid_users"`
	Enabled    bool     `json:"enabled"`
	// Options are additional smb.conf parameters, rendered verbatim.
	Options []Option `json:"options"`
}

// Option is one extra "key = value" share parameter.
type Option struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// User is a Samba user from the passdb.
type User struct {
	Name     string `json:"name"`
	UID      string `json:"uid"`
	FullName string `json:"full_name"`
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
	principalRe = regexp.MustCompile(`^[@+&]?[A-Za-z0-9_.\-]{1,64}$`)
	userNameRe  = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	reserved    = map[string]bool{"global": true, "homes": true, "printers": true, "print$": true, "ipc$": true}
)

// Shares returns all shares sorted by name.
func (m *Manager) Shares() ([]Share, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.load()
}

func (m *Manager) load() ([]Share, error) {
	var shares []Share
	if err := fsutil.ReadJSON(m.opts.StatePath, &shares); err != nil {
		return nil, err
	}
	for i := range shares {
		if shares[i].ValidUsers == nil {
			shares[i].ValidUsers = []string{}
		}
		if shares[i].Options == nil {
			shares[i].Options = []Option{}
		}
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
	users := make([]string, 0, len(s.ValidUsers))
	for _, u := range s.ValidUsers {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if !principalRe.MatchString(u) {
			return valid.Errorf("invalid user or group %q", u)
		}
		users = append(users, u)
	}
	s.ValidUsers = users
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
	fmt.Fprintf(b, "   read only = %s\n", yesno(s.ReadOnly))
	fmt.Fprintf(b, "   browseable = %s\n", yesno(s.Browseable))
	fmt.Fprintf(b, "   guest ok = %s\n", yesno(s.GuestOK))
	if len(s.ValidUsers) > 0 {
		fmt.Fprintf(b, "   valid users = %s\n", strings.Join(s.ValidUsers, " "))
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

// Users lists Samba users.
func (m *Manager) Users(ctx context.Context) ([]User, error) {
	out, err := m.run.Run(ctx, "", "pdbedit", "-L")
	if err != nil {
		return nil, err
	}
	users := []User{}
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(parts) < 2 || parts[0] == "" {
			continue
		}
		u := User{Name: parts[0], UID: parts[1]}
		if len(parts) == 3 {
			u.FullName = parts[2]
		}
		users = append(users, u)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })
	return users, nil
}

func checkPassword(pw string) error {
	if len(pw) < 8 || len(pw) > 127 {
		return valid.Errorf("password must be 8 to 127 characters")
	}
	if strings.ContainsAny(pw, "\r\n\x00") {
		return valid.Errorf("password must not contain line breaks")
	}
	return nil
}

// AddUser creates a Samba user. A matching Linux account without login
// shell and home directory is created if needed.
func (m *Manager) AddUser(ctx context.Context, name, password string) error {
	if !userNameRe.MatchString(name) {
		return valid.Errorf("user name must start with a lowercase letter and contain only a-z, 0-9, '_' or '-'")
	}
	if err := checkPassword(password); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, "", "id", "-u", name); err != nil {
		if _, err := m.run.Run(ctx, "", "useradd", "--no-create-home", "--shell", "/usr/sbin/nologin", name); err != nil {
			return fmt.Errorf("create system user: %w", err)
		}
	}
	if _, err := m.run.Run(ctx, password+"\n"+password+"\n", "smbpasswd", "-a", "-s", name); err != nil {
		return fmt.Errorf("add samba user: %w", err)
	}
	return nil
}

// SetPassword changes a Samba user's password.
func (m *Manager) SetPassword(ctx context.Context, name, password string) error {
	if !userNameRe.MatchString(name) {
		return &valid.NotFound{What: "user " + name}
	}
	if err := checkPassword(password); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, password+"\n"+password+"\n", "smbpasswd", "-s", name); err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	return nil
}

// DeleteUser removes a user from the Samba passdb. The Linux account is
// kept so file ownership stays intact.
func (m *Manager) DeleteUser(ctx context.Context, name string) error {
	if !userNameRe.MatchString(name) {
		return &valid.NotFound{What: "user " + name}
	}
	if _, err := m.run.Run(ctx, "", "smbpasswd", "-x", name); err != nil {
		return fmt.Errorf("delete samba user: %w", err)
	}
	return nil
}

// DefaultOptions returns the standard file locations under dataDir.
func DefaultOptions(dataDir string) Options {
	return Options{
		StatePath:   filepath.Join(dataDir, "smb-shares.json"),
		IncludePath: "/etc/samba/locostor.conf",
		MainConf:    "/etc/samba/smb.conf",
	}
}
