// Package system keeps the operating system up to date with apt: it finds
// pending updates (security updates separately), installs them and checks
// for packages LocoStor needs. Everything runs as a background task, so the
// output can be followed live.
package system

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Phydran6/LocoStor/internal/fsutil"
	"github.com/Phydran6/LocoStor/internal/tasks"
	"github.com/Phydran6/LocoStor/internal/valid"
)

// Upgrade modes.
const (
	ModeOff      = "off"
	ModeNotify   = "notify"   // only check and report
	ModeSecurity = "security" // install security updates only
	ModeAll      = "all"      // install all updates
)

// ValidMode reports whether m is an upgrade mode.
func ValidMode(m string) bool {
	switch m {
	case ModeOff, ModeNotify, ModeSecurity, ModeAll:
		return true
	}
	return false
}

// Package is one pending update.
type Package struct {
	Name     string `json:"name"`
	From     string `json:"from"`
	To       string `json:"to"`
	Origin   string `json:"origin"`
	Security bool   `json:"security"`
}

// Status is the result of the last check.
type Status struct {
	Available      bool      `json:"available"` // apt exists here
	CheckedAt      time.Time `json:"checked_at,omitzero"`
	Packages       []Package `json:"packages"`
	Security       int       `json:"security"`
	RebootRequired bool      `json:"reboot_required"`
	Error          string    `json:"error,omitempty"`
	LastUpgrade    time.Time `json:"last_upgrade,omitzero"`
	OS             string    `json:"os"`
}

// Apt runs apt on this machine.
type Apt struct {
	statePath string
	// FullUpgrade uses "apt-get dist-upgrade" (required on Proxmox VE).
	FullUpgrade bool
	// RebootFile is /run/reboot-required.
	RebootFile string
	// Demo fakes "apt list --upgradable".
	Demo bool

	mu     sync.Mutex
	status Status
}

// NewApt creates an apt manager that keeps its last status in statePath.
func NewApt(statePath string, fullUpgrade bool) *Apt {
	a := &Apt{statePath: statePath, FullUpgrade: fullUpgrade, RebootFile: "/run/reboot-required"}
	_ = fsutil.ReadJSON(statePath, &a.status)
	return a
}

// Env makes apt and dpkg run without questions and keep changed config files.
var Env = []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a", "APT_LISTCHANGES_FRONTEND=none", "LC_ALL=C"}

var dpkgOpts = []string{"-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "-o", "Dpkg::Progress-Fancy=0"}

func osName() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

func hasApt() bool {
	_, err := os.Stat("/usr/bin/apt-get")
	return err == nil
}

// Status returns the last check result.
func (a *Apt) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.status
	s.Available = hasApt() || a.Demo
	s.OS = osName()
	if s.Packages == nil {
		s.Packages = []Package{}
	}
	if _, err := os.Stat(a.RebootFile); err == nil {
		s.RebootRequired = true
	}
	return s
}

func (a *Apt) save(fn func(*Status)) {
	a.mu.Lock()
	fn(&a.status)
	s := a.status
	a.mu.Unlock()
	if a.statePath != "" {
		_ = fsutil.WriteJSON(a.statePath, s, 0o600)
	}
}

// ParseUpgradable parses "apt list --upgradable" output, e.g.
// "openssl/stable-security 3.0.15-1~deb12u1 amd64 [upgradable from: 3.0.14-1~deb12u2]".
func ParseUpgradable(out string) []Package {
	pkgs := []Package{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !strings.Contains(f[0], "/") {
			continue
		}
		name, origin, _ := strings.Cut(f[0], "/")
		p := Package{Name: name, To: f[1], Origin: origin}
		if i := strings.Index(line, "upgradable from: "); i >= 0 {
			p.From = strings.TrimSuffix(strings.TrimSpace(line[i+len("upgradable from: "):]), "]")
		}
		for _, o := range strings.Split(origin, ",") {
			if strings.Contains(o, "-security") || strings.HasSuffix(o, "security") {
				p.Security = true
			}
		}
		pkgs = append(pkgs, p)
	}
	sort.Slice(pkgs, func(i, j int) bool {
		if pkgs[i].Security != pkgs[j].Security {
			return pkgs[i].Security
		}
		return pkgs[i].Name < pkgs[j].Name
	})
	return pkgs
}

const demoUpgradable = `Listing...
libssl3/stable-security 3.0.15-1~deb12u1 amd64 [upgradable from: 3.0.14-1~deb12u2]
openssl/stable-security 3.0.15-1~deb12u1 amd64 [upgradable from: 3.0.14-1~deb12u2]
samba/stable 2:4.17.12+dfsg-0+deb12u2 amd64 [upgradable from: 2:4.17.12+dfsg-0+deb12u1]
tzdata/stable-updates 2025b-0+deb12u1 all [upgradable from: 2024b-0+deb12u1]
`

// Check refreshes the package lists and records pending updates.
func (a *Apt) Check(t *tasks.Task) (Status, error) {
	t.Section("Refreshing package lists")
	if err := t.Exec(Env, "apt-get", "update"); err != nil {
		a.save(func(s *Status) { s.Error = err.Error(); s.CheckedAt = time.Now() })
		return a.Status(), err
	}
	var buf bytes.Buffer
	if a.Demo {
		buf.WriteString(demoUpgradable)
	} else if err := runCapture(t.Context(), &buf, "apt", "list", "--upgradable"); err != nil {
		return a.Status(), err
	}
	pkgs := ParseUpgradable(buf.String())
	sec := 0
	for _, p := range pkgs {
		if p.Security {
			sec++
		}
	}
	a.save(func(s *Status) {
		s.Packages, s.Security, s.CheckedAt, s.Error = pkgs, sec, time.Now(), ""
	})
	t.Section("Pending updates")
	if len(pkgs) == 0 {
		t.Logf("Everything is up to date.")
	}
	for _, p := range pkgs {
		mark := "  "
		if p.Security {
			mark = "! "
		}
		t.Logf("%s%-28s %s → %s (%s)", mark, p.Name, p.From, p.To, p.Origin)
	}
	if sec > 0 {
		t.Logf("%d of %d updates fix security issues (marked with !).", sec, len(pkgs))
	}
	return a.Status(), nil
}

// Upgrade installs pending updates: mode security installs only security
// updates, all installs everything. It checks first.
func (a *Apt) Upgrade(t *tasks.Task, mode string) (installed int, err error) {
	st, err := a.Check(t)
	if err != nil {
		return 0, err
	}
	var names []string
	for _, p := range st.Packages {
		if mode == ModeAll || p.Security {
			names = append(names, p.Name)
		}
	}
	if len(names) == 0 {
		t.Section("Installing updates")
		t.Logf("Nothing to install.")
		return 0, nil
	}
	switch mode {
	case ModeSecurity:
		t.Section(fmt.Sprintf("Installing %d security updates", len(names)))
		args := append(append([]string{"install", "-y", "--only-upgrade"}, dpkgOpts...), names...)
		err = t.Exec(Env, "apt-get", args...)
	case ModeAll:
		cmd := "upgrade"
		if a.FullUpgrade {
			cmd = "dist-upgrade"
		}
		t.Section(fmt.Sprintf("Installing %d updates (apt-get %s)", len(names), cmd))
		err = t.Exec(Env, "apt-get", append([]string{cmd, "-y"}, dpkgOpts...)...)
	default:
		return 0, valid.Errorf("unknown upgrade mode %q", mode)
	}
	if err != nil {
		return 0, err
	}
	a.save(func(s *Status) { s.LastUpgrade = time.Now() })
	if _, err := a.Check(t); err != nil {
		return len(names), err
	}
	if a.Status().RebootRequired {
		t.Logf("")
		t.Logf("W: a reboot is required to finish the update (e.g. a new kernel).")
	}
	return len(names), nil
}

// EnsurePackages installs packages LocoStor relies on if they are missing.
func (a *Apt) EnsurePackages(t *tasks.Task, pkgs ...string) error {
	var missing []string
	for _, p := range pkgs {
		var buf bytes.Buffer
		if a.Demo {
			continue
		}
		if err := runCapture(t.Context(), &buf, "dpkg-query", "-W", "-f=${Status}", p); err != nil || !strings.Contains(buf.String(), "install ok installed") {
			missing = append(missing, p)
		}
	}
	t.Section("Packages LocoStor needs")
	if len(missing) == 0 {
		t.Logf("All present: %s", strings.Join(pkgs, ", "))
		return nil
	}
	return t.Exec(Env, "apt-get", append(append([]string{"install", "-y", "--no-install-recommends"}, dpkgOpts...), missing...)...)
}

// Install installs the given packages (e.g. wsdd on request).
func (a *Apt) Install(t *tasks.Task, pkgs ...string) error {
	t.Section("Installing " + strings.Join(pkgs, ", "))
	if err := t.Exec(Env, "apt-get", "update"); err != nil {
		return err
	}
	return t.Exec(Env, "apt-get", append(append([]string{"install", "-y", "--no-install-recommends"}, dpkgOpts...), pkgs...)...)
}

// Installable lists packages the UI may install on request.
var Installable = map[string]string{
	"wsdd":      "WS-Discovery, so Windows 10/11 lists the server in the network",
	"acl":       "POSIX ACLs for folder permissions",
	"smbclient": "test logins of SMB users",
}

func runCapture(ctx context.Context, out *bytes.Buffer, name string, args ...string) error {
	return tasks.RealExec(ctx, Env, out, name, args...)
}
