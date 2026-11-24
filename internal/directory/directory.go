// Package directory connects the container's Samba to a directory
// service: it joins an Active Directory domain (winbind) or uses an
// OpenLDAP server with the Samba schema (ldapsam + nslcd). Shares can then
// grant access to domain users and groups.
package directory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Phydran6/LocoStor/internal/fsutil"
	"github.com/Phydran6/LocoStor/internal/smb"
	"github.com/Phydran6/LocoStor/internal/sysexec"
	"github.com/Phydran6/LocoStor/internal/system"
	"github.com/Phydran6/LocoStor/internal/tasks"
	"github.com/Phydran6/LocoStor/internal/valid"
)

// Modes.
const (
	ModeNone = "none"
	ModeAD   = "ad"
	ModeLDAP = "ldap"
)

// Settings describe the directory connection. Passwords are never stored
// here: the AD admin password is only used for the join, the LDAP bind
// password goes to Samba's secrets.tdb and nslcd.conf (root only).
type Settings struct {
	Mode string `json:"mode"`
	// Active Directory
	Realm     string `json:"realm"`     // e.g. CORP.EXAMPLE.COM
	Workgroup string `json:"workgroup"` // NetBIOS domain, e.g. CORP
	OU        string `json:"ou"`        // optional, e.g. Computers/NAS
	// LDAP
	URI         string `json:"uri"`     // ldap://ldap.example.com or ldaps://...
	BaseDN      string `json:"base_dn"` // dc=example,dc=com
	BindDN      string `json:"bind_dn"` // cn=admin,dc=example,dc=com
	UserSuffix  string `json:"user_suffix"`
	GroupSuffix string `json:"group_suffix"`
	StartTLS    bool   `json:"start_tls"`
}

// Status is shown in the UI.
type Status struct {
	Settings  Settings `json:"settings"`
	Connected bool     `json:"connected"`
	Detail    string   `json:"detail"`
	Since     string   `json:"since,omitempty"`
}

// Files are the system files the manager edits (tests replace them).
type Files struct {
	SMBConf   string
	Krb5      string
	NSSwitch  string
	Nslcd     string
	Debconf   string // preseed file written before installing packages
	StatePath string
}

// DefaultFiles are the real locations.
func DefaultFiles(dataDir string) Files {
	return Files{SMBConf: "/etc/samba/smb.conf", Krb5: "/etc/krb5.conf", NSSwitch: "/etc/nsswitch.conf", Nslcd: "/etc/nslcd.conf",
		Debconf: filepath.Join(dataDir, "directory-debconf.txt"), StatePath: filepath.Join(dataDir, "directory.json")}
}

// Manager manages the directory connection.
type Manager struct {
	run   sysexec.Runner
	files Files
	Apt   *system.Apt
	Tasks *tasks.Manager
	// Demo pretends every step works.
	Demo bool

	mu sync.Mutex
}

// New creates a Manager.
func New(run sysexec.Runner, files Files) *Manager { return &Manager{run: run, files: files} }

type state struct {
	Settings Settings  `json:"settings"`
	Since    time.Time `json:"since"`
}

func (m *Manager) load() state {
	st := state{Settings: Settings{Mode: ModeNone}}
	_ = fsutil.ReadJSON(m.files.StatePath, &st)
	return st
}

var (
	realmRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,62}\.)+[A-Za-z]{2,63}$`)
	wgRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,14}$`)
	dnRe    = regexp.MustCompile(`^[A-Za-z]+=[^,=\n\r"]+(,[A-Za-z]+=[^,=\n\r"]+)*$`)
	userRe  = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,128}$`)
	ouRe    = regexp.MustCompile(`^[A-Za-z0-9 _/.-]{1,200}$`)
)

func (s *Settings) validate() error {
	switch s.Mode {
	case ModeAD:
		s.Realm = strings.ToUpper(strings.TrimSpace(s.Realm))
		if !realmRe.MatchString(s.Realm) {
			return valid.Errorf("enter the domain, e.g. CORP.EXAMPLE.COM")
		}
		s.Workgroup = strings.ToUpper(strings.TrimSpace(s.Workgroup))
		if s.Workgroup == "" {
			s.Workgroup, _, _ = strings.Cut(s.Realm, ".")
			if len(s.Workgroup) > 15 {
				s.Workgroup = s.Workgroup[:15]
			}
		}
		if !wgRe.MatchString(s.Workgroup) {
			return valid.Errorf("the NetBIOS domain name has up to 15 letters, digits, '_' and '-'")
		}
		if s.OU != "" && !ouRe.MatchString(s.OU) {
			return valid.Errorf("invalid organizational unit")
		}
	case ModeLDAP:
		u, err := url.Parse(strings.TrimSpace(s.URI))
		if err != nil || (u.Scheme != "ldap" && u.Scheme != "ldaps") || u.Host == "" || u.Path != "" && u.Path != "/" {
			return valid.Errorf("the server must look like ldap://ldap.example.com or ldaps://ldap.example.com")
		}
		s.URI = u.Scheme + "://" + u.Host
		for name, v := range map[string]string{"base DN": s.BaseDN, "bind DN": s.BindDN} {
			if !dnRe.MatchString(strings.TrimSpace(v)) {
				return valid.Errorf("invalid %s (e.g. dc=example,dc=com)", name)
			}
		}
		s.BaseDN, s.BindDN = strings.TrimSpace(s.BaseDN), strings.TrimSpace(s.BindDN)
		if s.UserSuffix == "" {
			s.UserSuffix = "ou=People"
		}
		if s.GroupSuffix == "" {
			s.GroupSuffix = "ou=Groups"
		}
		for _, v := range []string{s.UserSuffix, s.GroupSuffix} {
			if !dnRe.MatchString(v) {
				return valid.Errorf("invalid suffix %q (e.g. ou=People)", v)
			}
		}
	default:
		return valid.Errorf("unknown mode")
	}
	return nil
}

// Status returns the current connection state.
func (m *Manager) Status(ctx context.Context) Status {
	st := m.load()
	s := Status{Settings: st.Settings}
	if !st.Since.IsZero() {
		s.Since = st.Since.Format(time.RFC3339)
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	switch st.Settings.Mode {
	case ModeAD:
		out, err := m.run.Run(ctx, "", "wbinfo", "-t")
		s.Connected = err == nil || m.Demo
		s.Detail = strings.TrimSpace(string(out))
		if err != nil && !m.Demo {
			s.Detail = "trust check failed: " + err.Error()
		}
	case ModeLDAP:
		s.Connected = sysexec.ServiceState(ctx, m.run, "nslcd") == "active"
		out, _ := m.run.Run(ctx, "", "pdbedit", "-L")
		n := 0
		for _, l := range strings.Split(string(out), "\n") {
			if strings.Contains(l, ":") {
				n++
			}
		}
		s.Detail = fmt.Sprintf("%d Samba accounts in the directory", n)
	default:
		s.Detail = "Local Samba users only"
	}
	return s
}

// Connect joins the AD domain or configures LDAP in a background task.
// password is the AD admin's (join only) or the LDAP bind password.
func (m *Manager) Connect(s Settings, user, password string) (*tasks.Task, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if m.load().Settings.Mode != ModeNone {
		return nil, valid.Errorf("disconnect the current directory first")
	}
	if password == "" || strings.ContainsAny(password, "\r\n") {
		return nil, valid.Errorf("enter the password")
	}
	if s.Mode == ModeAD && !userRe.MatchString(user) {
		return nil, valid.Errorf("enter the user name of a domain admin")
	}
	title := "Join the Active Directory domain " + s.Realm
	if s.Mode == ModeLDAP {
		title = "Connect to LDAP " + s.URI
	}
	return m.Tasks.Start("directory", title, func(t *tasks.Task) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		var err error
		if s.Mode == ModeAD {
			err = m.joinAD(t, s, user, password)
		} else {
			err = m.connectLDAP(t, s, password)
		}
		if err != nil {
			return err
		}
		return fsutil.WriteJSON(m.files.StatePath, state{Settings: s, Since: time.Now()}, 0o600)
	})
}

// Disconnect leaves the domain / stops using LDAP. For AD, user and
// password remove the computer account (optional).
func (m *Manager) Disconnect(user, password string) (*tasks.Task, error) {
	st := m.load()
	if st.Settings.Mode == ModeNone {
		return nil, valid.Errorf("no directory is connected")
	}
	return m.Tasks.Start("directory", "Disconnect the directory", func(t *tasks.Task) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		var err error
		if st.Settings.Mode == ModeAD {
			err = m.leaveAD(t, st.Settings, user, password)
		} else {
			err = m.disconnectLDAP(t)
		}
		if err != nil {
			return err
		}
		return fsutil.WriteJSON(m.files.StatePath, state{Settings: Settings{Mode: ModeNone}}, 0o600)
	})
}

func (m *Manager) preseed(t *tasks.Task, lines ...string) error {
	if err := fsutil.WriteFileAtomic(m.files.Debconf, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	return t.Exec(nil, "debconf-set-selections", m.files.Debconf)
}

func (m *Manager) install(t *tasks.Task, pkgs ...string) error {
	if m.Apt == nil {
		return errors.New("package manager not available")
	}
	return m.Apt.Install(t, pkgs...)
}

// Krb5Conf renders /etc/krb5.conf for realm (KDCs are found via DNS).
func Krb5Conf(realm string) string {
	return fmt.Sprintf(`# Written by LocoStor for the Active Directory domain %[1]s
[libdefaults]
	default_realm = %[1]s
	dns_lookup_realm = false
	dns_lookup_kdc = true
	rdns = false
`, realm)
}

// ADGlobals are the [global] settings for a winbind domain member.
func ADGlobals(s Settings) []smb.Option {
	return []smb.Option{
		{Key: "security", Value: "ads"},
		{Key: "realm", Value: s.Realm},
		{Key: "workgroup", Value: s.Workgroup},
		{Key: "idmap config * : backend", Value: "tdb"},
		{Key: "idmap config * : range", Value: "3000-7999"},
		{Key: "idmap config " + s.Workgroup + " : backend", Value: "rid"},
		{Key: "idmap config " + s.Workgroup + " : range", Value: "10000-999999"},
		{Key: "winbind use default domain", Value: "yes"},
		{Key: "winbind enum users", Value: "yes"},
		{Key: "winbind enum groups", Value: "yes"},
		{Key: "winbind refresh tickets", Value: "yes"},
		{Key: "template shell", Value: "/usr/sbin/nologin"},
		{Key: "kerberos method", Value: "secrets and keytab"},
	}
}

var adKeys = []string{"realm", "idmap config * : backend", "idmap config * : range", "winbind use default domain", "winbind enum users",
	"winbind enum groups", "winbind refresh tickets", "template shell", "kerberos method"}

// SetNSS adds or removes a source (winbind, ldap) on the passwd and group
// lines of nsswitch.conf.
func SetNSS(data, source string, on bool) string {
	lines := strings.Split(data, "\n")
	found := map[string]bool{}
	for i, l := range lines {
		key, rest, ok := strings.Cut(l, ":")
		key = strings.TrimSpace(key)
		if !ok || strings.HasPrefix(strings.TrimSpace(l), "#") || (key != "passwd" && key != "group") {
			continue
		}
		found[key] = true
		fields := strings.Fields(rest)
		var out []string
		for _, f := range fields {
			if f != source {
				out = append(out, f)
			}
		}
		if on {
			out = append(out, source)
		}
		lines[i] = key + ":" + strings.Repeat(" ", max(1, 15-len(key))) + strings.Join(out, " ")
	}
	for _, k := range []string{"passwd", "group"} {
		if !found[k] && on {
			lines = append(lines, k+":"+strings.Repeat(" ", 15-len(k))+"files "+source)
		}
	}
	return strings.Join(lines, "\n")
}

func (m *Manager) editNSS(t *tasks.Task, source string, on bool) error {
	data, err := os.ReadFile(m.files.NSSwitch)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(data) > 0 {
		_ = fsutil.Backup(m.files.NSSwitch, data, 0o644, 5)
	}
	t.Logf("%s %s in %s (passwd, group)", map[bool]string{true: "Adding", false: "Removing"}[on], source, m.files.NSSwitch)
	return fsutil.WriteFileAtomic(m.files.NSSwitch, []byte(SetNSS(string(data), source, on)), 0o644)
}

func (m *Manager) joinAD(t *tasks.Task, s Settings, user, password string) error {
	ctx := t.Context()
	t.Section("Checking the domain")
	var r net.Resolver
	if _, srv, err := r.LookupSRV(ctx, "ldap", "tcp", "dc._msdcs."+strings.ToLower(s.Realm)); err != nil || len(srv) == 0 {
		if !m.Demo {
			return fmt.Errorf("no domain controller found for %s in DNS - the container's DNS server (Proxmox: container → DNS) must be the domain controller", s.Realm)
		}
	} else {
		for _, x := range srv {
			t.Logf("Domain controller: %s:%d", strings.TrimSuffix(x.Target, "."), x.Port)
		}
	}

	if err := m.preseed(t,
		"krb5-config krb5-config/default_realm string "+s.Realm,
		"krb5-config krb5-config/add_servers_realm string "+s.Realm,
		"krb5-config krb5-config/add_servers boolean false",
	); err != nil {
		return err
	}
	if err := m.install(t, "winbind", "libnss-winbind", "libpam-winbind", "krb5-user"); err != nil {
		return err
	}

	t.Section("Kerberos")
	if old, err := os.ReadFile(m.files.Krb5); err == nil {
		_ = fsutil.Backup(m.files.Krb5, old, 0o644, 5)
	}
	if err := fsutil.WriteFileAtomic(m.files.Krb5, []byte(Krb5Conf(s.Realm)), 0o644); err != nil {
		return err
	}
	t.Logf("Wrote %s", m.files.Krb5)

	t.Section("Samba settings")
	if err := smb.EditGlobal(ctx, m.run, m.files.SMBConf, ADGlobals(s), nil); err != nil && !m.Demo {
		return err
	}
	t.Logf("security = ads, realm = %s, workgroup = %s, winbind ID mapping (rid)", s.Realm, s.Workgroup)

	t.Section("Joining " + s.Realm)
	args := []string{"ads", "join", "-U", user + "%" + password}
	if s.OU != "" {
		args = append(args, "createcomputer="+s.OU)
	}
	if err := t.ExecHidden([]string{password}, nil, "net", args...); err != nil {
		_ = m.restoreUser(t)
		return fmt.Errorf("join failed: %w", err)
	}

	if err := m.editNSS(t, "winbind", true); err != nil {
		return err
	}
	t.Section("Services")
	for _, cmd := range [][]string{{"systemctl", "enable", "--now", "winbind"}, {"systemctl", "restart", "winbind"}, {"systemctl", "restart", "smbd"}} {
		if err := t.Exec(nil, cmd[0], cmd[1:]...); err != nil {
			return err
		}
	}
	t.Section("Check")
	time.Sleep(2 * time.Second)
	if err := t.Exec(nil, "wbinfo", "-t"); err != nil {
		return err
	}
	_ = t.Exec(nil, "wbinfo", "--ping-dc")
	t.Logf("")
	t.Logf("Domain users and groups (e.g. @\"domain users\") can now be used on the shares.")
	return nil
}

// restoreUser switches Samba back to local users.
func (m *Manager) restoreUser(t *tasks.Task) error {
	return smb.EditGlobal(t.Context(), m.run, m.files.SMBConf, []smb.Option{{Key: "security", Value: "user"}}, adKeys)
}

func (m *Manager) leaveAD(t *tasks.Task, s Settings, user, password string) error {
	if user != "" && password != "" {
		t.Section("Leaving " + s.Realm)
		if err := t.ExecHidden([]string{password}, nil, "net", "ads", "leave", "-U", user+"%"+password); err != nil {
			t.Logf("W: could not remove the computer account (%v) - continuing", err)
		}
	}
	t.Section("Local users again")
	keys := append(append([]string{}, adKeys...), "idmap config "+s.Workgroup+" : backend", "idmap config "+s.Workgroup+" : range")
	if err := smb.EditGlobal(t.Context(), m.run, m.files.SMBConf, []smb.Option{{Key: "security", Value: "user"}}, keys); err != nil && !m.Demo {
		return err
	}
	if err := m.editNSS(t, "winbind", false); err != nil {
		return err
	}
	_ = t.Exec(nil, "systemctl", "disable", "--now", "winbind")
	return t.Exec(nil, "systemctl", "restart", "smbd")
}

// NslcdConf renders /etc/nslcd.conf.
func NslcdConf(s Settings, password string) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Written by LocoStor\nuid nslcd\ngid nslcd\nuri %s\nbase %s\nbinddn %s\nbindpw %s\n", s.URI, s.BaseDN, s.BindDN, password)
	fmt.Fprintf(&b, "base passwd %s,%s\nbase group %s,%s\n", s.UserSuffix, s.BaseDN, s.GroupSuffix, s.BaseDN)
	if s.StartTLS {
		b.WriteString("ssl start_tls\n")
	}
	if strings.HasPrefix(s.URI, "ldaps://") || s.StartTLS {
		b.WriteString("tls_reqcert demand\ntls_cacertfile /etc/ssl/certs/ca-certificates.crt\n")
	}
	return b.String()
}

// LDAPGlobals are the [global] settings for the ldapsam passdb.
func LDAPGlobals(s Settings) []smb.Option {
	ssl := "off"
	if s.StartTLS {
		ssl = "start tls"
	}
	return []smb.Option{
		{Key: "passdb backend", Value: `ldapsam:"` + s.URI + `"`},
		{Key: "ldap suffix", Value: s.BaseDN},
		{Key: "ldap user suffix", Value: s.UserSuffix},
		{Key: "ldap group suffix", Value: s.GroupSuffix},
		{Key: "ldap admin dn", Value: s.BindDN},
		{Key: "ldap ssl", Value: ssl},
		{Key: "ldap passwd sync", Value: "yes"},
	}
}

var ldapKeys = []string{"ldap suffix", "ldap user suffix", "ldap group suffix", "ldap admin dn", "ldap ssl", "ldap passwd sync"}

func (m *Manager) connectLDAP(t *tasks.Task, s Settings, password string) error {
	ctx := t.Context()
	t.Section("Reaching " + s.URI)
	u, _ := url.Parse(s.URI)
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), map[string]string{"ldap": "389", "ldaps": "636"}[u.Scheme])
	}
	if c, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", host); err != nil {
		if !m.Demo {
			return fmt.Errorf("cannot reach %s: %w", host, err)
		}
	} else {
		c.Close()
		t.Logf("%s answers.", host)
	}
	if err := m.preseed(t,
		"nslcd nslcd/ldap-uris string "+s.URI,
		"nslcd nslcd/ldap-base string "+s.BaseDN,
		"libnss-ldapd libnss-ldapd/nsswitch multiselect passwd, group",
	); err != nil {
		return err
	}
	if err := m.install(t, "libnss-ldapd", "nslcd", "ldap-utils"); err != nil {
		return err
	}
	t.Section("nslcd (Unix accounts from LDAP)")
	if err := fsutil.WriteFileAtomic(m.files.Nslcd, []byte(NslcdConf(s, password)), 0o640); err != nil {
		return err
	}
	_ = t.Exec(nil, "chgrp", "nslcd", m.files.Nslcd)
	if err := m.editNSS(t, "ldap", true); err != nil {
		return err
	}
	if err := t.Exec(nil, "systemctl", "restart", "nslcd"); err != nil {
		return err
	}

	t.Section("Samba passwords from LDAP (ldapsam)")
	if err := smb.EditGlobal(ctx, m.run, m.files.SMBConf, LDAPGlobals(s), nil); err != nil && !m.Demo {
		return err
	}
	if err := t.ExecHidden([]string{password}, nil, "smbpasswd", "-w", password); err != nil {
		return err
	}
	if err := t.Exec(nil, "systemctl", "restart", "smbd"); err != nil {
		return err
	}
	t.Section("Check")
	if err := t.Exec(nil, "pdbedit", "-L"); err != nil {
		return fmt.Errorf("samba cannot read the accounts - does the directory use the Samba schema (sambaSamAccount)? %w", err)
	}
	return nil
}

func (m *Manager) disconnectLDAP(t *tasks.Task) error {
	t.Section("Local users again")
	if err := smb.EditGlobal(t.Context(), m.run, m.files.SMBConf, []smb.Option{{Key: "passdb backend", Value: "tdbsam"}}, ldapKeys); err != nil && !m.Demo {
		return err
	}
	if err := m.editNSS(t, "ldap", false); err != nil {
		return err
	}
	_ = t.Exec(nil, "systemctl", "disable", "--now", "nslcd")
	return t.Exec(nil, "systemctl", "restart", "smbd")
}
