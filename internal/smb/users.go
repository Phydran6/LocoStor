package smb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Phydran6/LocoStor/internal/sysexec"
	"github.com/Phydran6/LocoStor/internal/valid"
)

// User is a Samba user from the passdb.
type User struct {
	Name     string `json:"name"`
	UID      string `json:"uid"`
	FullName string `json:"full_name"`
	// Disabled accounts cannot log in (smbpasswd -d).
	Disabled bool `json:"disabled"`
	// LinuxAccount is false when the Unix account behind the Samba user is
	// missing - Samba then refuses the login.
	LinuxAccount bool `json:"linux_account"`
	// Problems explains why the user cannot log in.
	Problems []string `json:"problems"`
}

// Group is a Unix group that can be used as @name in share access lists.
type Group struct {
	Name    string   `json:"name"`
	GID     int      `json:"gid"`
	Members []string `json:"members"`
}

// UserManager manages Samba users. It works on whatever Samba runs on the
// machine (the container, or the Proxmox host through the agent).
type UserManager struct {
	run sysexec.Runner
}

// NewUsers creates a UserManager.
func NewUsers(run sysexec.Runner) *UserManager { return &UserManager{run: run} }

// parsePdbedit reads "pdbedit -L -v" output. Older or fake output in the
// short "name:uid:full name" form is understood too.
func parsePdbedit(out string) []User {
	users := []User{}
	if strings.Contains(out, "Unix username:") {
		var cur *User
		for _, line := range strings.Split(out, "\n") {
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			switch k {
			case "Unix username":
				users = append(users, User{Name: v})
				cur = &users[len(users)-1]
			case "Full Name":
				if cur != nil {
					cur.FullName = v
				}
			case "Account Flags":
				if cur != nil {
					cur.Disabled = strings.Contains(strings.Trim(v, "[]"), "D")
				}
			}
		}
		return users
	}
	for _, line := range strings.Split(out, "\n") {
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
	return users
}

// passwdEntries returns name -> uid from the user database (getent, so
// LDAP/AD accounts count too).
func (um *UserManager) passwdEntries(ctx context.Context) map[string]string {
	out, err := um.run.Run(ctx, "", "getent", "passwd")
	if len(strings.TrimSpace(string(out))) == 0 {
		_ = err
		return nil // cannot tell
	}
	m := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, ":")
		if len(f) >= 3 && f[0] != "" {
			m[f[0]] = f[2]
		}
	}
	return m
}

// Users lists Samba users with their state.
func (um *UserManager) Users(ctx context.Context) ([]User, error) {
	out, err := um.run.Run(ctx, "", "pdbedit", "-L", "-v")
	if err != nil {
		return nil, fmt.Errorf("read Samba users: %w", err)
	}
	users := parsePdbedit(string(out))
	accounts := um.passwdEntries(ctx)
	for i := range users {
		u := &users[i]
		u.Problems = []string{}
		if accounts == nil {
			u.LinuxAccount = true // cannot tell
		} else if uid, ok := accounts[u.Name]; ok {
			u.LinuxAccount = true
			u.UID = uid
		} else if !u.LinuxAccount {
			u.Problems = append(u.Problems, "the Linux account is missing - Samba refuses the login")
		}
		if u.Disabled {
			u.Problems = append(u.Problems, "the account is disabled")
		}
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })
	return users, nil
}

// Groups lists regular Unix groups (GID 1000+) and the LocoStor group.
func (um *UserManager) Groups(ctx context.Context) ([]Group, error) {
	out, err := um.run.Run(ctx, "", "getent", "group")
	if err != nil && len(out) == 0 {
		return []Group{}, nil
	}
	groups := []Group{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 4 || f[0] == "" {
			continue
		}
		gid, err := strconv.Atoi(f[2])
		if err != nil || ((gid < 1000 || gid == 65534) && f[0] != ShareGroup && f[0] != "users") {
			continue
		}
		members := []string{}
		for _, m := range strings.Split(f[3], ",") {
			if m = strings.TrimSpace(m); m != "" {
				members = append(members, m)
			}
		}
		groups = append(groups, Group{Name: f[0], GID: gid, Members: members})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return groups, nil
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

func (um *UserManager) ensureLinuxAccount(ctx context.Context, name string) error {
	if _, err := um.run.Run(ctx, "", "id", "-u", name); err == nil {
		return nil
	}
	args := []string{"--no-create-home", "--shell", "/usr/sbin/nologin"}
	// A group with the user's name may already exist (useradd refuses then).
	if _, err := um.run.Run(ctx, "", "getent", "group", name); err == nil {
		args = append(args, "-g", name)
	}
	if _, err := um.run.Run(ctx, "", "useradd", append(args, name)...); err != nil {
		return fmt.Errorf("create the Linux account: %w", err)
	}
	return nil
}

// AddUser creates a Samba user. A matching Linux account without login
// shell and home directory is created if needed, and the account is
// enabled.
func (um *UserManager) AddUser(ctx context.Context, name, password string) error {
	if !userNameRe.MatchString(name) {
		return valid.Errorf("user name must start with a lowercase letter and contain only a-z, 0-9, '_' or '-'")
	}
	if err := checkPassword(password); err != nil {
		return err
	}
	if err := um.ensureLinuxAccount(ctx, name); err != nil {
		return err
	}
	if _, err := um.run.Run(ctx, password+"\n"+password+"\n", "smbpasswd", "-a", "-s", name); err != nil {
		return fmt.Errorf("add samba user: %w", err)
	}
	if _, err := um.run.Run(ctx, "", "smbpasswd", "-e", name); err != nil {
		return fmt.Errorf("enable samba user: %w", err)
	}
	return um.checkListed(ctx, name)
}

// checkListed makes sure the user really shows up in the passdb, so a
// silent failure does not look like success.
func (um *UserManager) checkListed(ctx context.Context, name string) error {
	users, err := um.Users(ctx)
	if err != nil {
		return err
	}
	for _, u := range users {
		if u.Name == name {
			return nil
		}
	}
	return fmt.Errorf("samba accepted the user %q but does not list it (pdbedit -L) - check the passdb backend in smb.conf", name)
}

func (um *UserManager) known(name string) error {
	if !userNameRe.MatchString(name) {
		return &valid.NotFound{What: "user " + name}
	}
	return nil
}

// SetPassword changes a Samba user's password.
func (um *UserManager) SetPassword(ctx context.Context, name, password string) error {
	if err := um.known(name); err != nil {
		return err
	}
	if err := checkPassword(password); err != nil {
		return err
	}
	if _, err := um.run.Run(ctx, password+"\n"+password+"\n", "smbpasswd", "-s", name); err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	return nil
}

// SetEnabled enables or disables a Samba user.
func (um *UserManager) SetEnabled(ctx context.Context, name string, enabled bool) error {
	if err := um.known(name); err != nil {
		return err
	}
	flag := "-d"
	if enabled {
		flag = "-e"
	}
	if _, err := um.run.Run(ctx, "", "smbpasswd", flag, name); err != nil {
		return fmt.Errorf("change user: %w", err)
	}
	return nil
}

// Repair fixes what keeps a user from logging in: a missing Linux account
// is created and the account is enabled.
func (um *UserManager) Repair(ctx context.Context, name string) error {
	if err := um.known(name); err != nil {
		return err
	}
	if err := um.ensureLinuxAccount(ctx, name); err != nil {
		return err
	}
	return um.SetEnabled(ctx, name, true)
}

// DeleteUser removes a user from the Samba passdb. The Linux account is
// kept so file ownership stays intact.
func (um *UserManager) DeleteUser(ctx context.Context, name string) error {
	if err := um.known(name); err != nil {
		return err
	}
	if _, err := um.run.Run(ctx, "", "smbpasswd", "-x", name); err != nil {
		return fmt.Errorf("delete samba user: %w", err)
	}
	return nil
}

// LoginResult is the outcome of a test login against the local Samba.
type LoginResult struct {
	OK     bool     `json:"ok"`
	Shares []string `json:"shares"`
	Error  string   `json:"error,omitempty"`
	Hint   string   `json:"hint,omitempty"`
}

var loginHints = map[string]string{
	"NT_STATUS_LOGON_FAILURE":      "wrong password, or the user is not a Samba user on this server",
	"NT_STATUS_ACCOUNT_DISABLED":   "the account is disabled - enable it in the user list",
	"NT_STATUS_NO_SUCH_USER":       "the Linux account behind the Samba user is missing - use \"Repair\"",
	"NT_STATUS_CONNECTION_REFUSED": "Samba (smbd) is not running on this server",
	"NT_STATUS_ACCESS_DENIED":      "login worked, but access was denied",
	"NT_STATUS_PASSWORD_EXPIRED":   "the password expired - set a new one",
}

// TestLogin logs in to the local Samba as name and lists the shares the
// user sees - the same check a Windows PC does.
func (um *UserManager) TestLogin(ctx context.Context, name, password string) LoginResult {
	dir, err := os.MkdirTemp("", "locostor-login-")
	if err != nil {
		return LoginResult{Error: err.Error()}
	}
	defer os.RemoveAll(dir)
	auth := filepath.Join(dir, "auth")
	if err := os.WriteFile(auth, []byte("username = "+name+"\npassword = "+password+"\n"), 0o600); err != nil {
		return LoginResult{Error: err.Error()}
	}
	out, err := um.run.Run(ctx, "", "smbclient", "-L", "127.0.0.1", "-A", auth, "-g")
	text := string(out)
	if se, ok := err.(*sysexec.Error); ok {
		text += "\n" + se.Stderr
	}
	if err != nil {
		res := LoginResult{Error: strings.TrimSpace(firstLine(out, err))}
		if strings.Contains(err.Error(), "executable file not found") {
			res.Error, res.Hint = "smbclient is not installed", "install it with: apt-get install smbclient (the update on the Update page does it too)"
			return res
		}
		for code, hint := range loginHints {
			if strings.Contains(text, code) {
				res.Error, res.Hint = code, hint
			}
		}
		return res
	}
	res := LoginResult{OK: true, Shares: []string{}}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Split(line, "|")
		if len(f) >= 2 && f[0] == "Disk" {
			res.Shares = append(res.Shares, f[1])
		}
	}
	return res
}
