// Package maintenance is LocoStor's patch automation: once a day (and on
// demand) it updates the container's packages, checks or updates the
// Proxmox host, keeps the host agent in sync and installs new LocoStor
// releases - all in one task whose output can be followed live.
package maintenance

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Phydran6/LocoStor/internal/fsutil"
	"github.com/Phydran6/LocoStor/internal/hostagent"
	"github.com/Phydran6/LocoStor/internal/system"
	"github.com/Phydran6/LocoStor/internal/tasks"
	"github.com/Phydran6/LocoStor/internal/update"
	"github.com/Phydran6/LocoStor/internal/valid"
)

// LocoStor update modes.
const (
	AppAuto   = "auto"
	AppNotify = "notify"
	AppOff    = "off"
)

// Settings control the daily run.
type Settings struct {
	Enabled bool   `json:"enabled"`
	Time    string `json:"time"`     // HH:MM, local time
	App     string `json:"locostor"` // auto, notify, off
	System  string `json:"system"`   // container packages: all, security, notify, off
	Host    string `json:"host"`     // Proxmox host packages: all, security, notify, off
}

// Defaults: everything in the container is patched automatically; the
// Proxmox host is only checked (its updates may need a reboot).
func Defaults() Settings {
	return Settings{Enabled: true, Time: "03:30", App: AppAuto, System: system.ModeAll, Host: system.ModeNotify}
}

// RunOptions choose what one run does.
type RunOptions struct {
	App     string `json:"locostor"`
	System  string `json:"system"`
	Host    string `json:"host"`
	Trigger string `json:"-"` // schedule or manual
}

// LastRun summarizes the previous run.
type LastRun struct {
	Task    string    `json:"task"`
	Started time.Time `json:"started"`
	Trigger string    `json:"trigger"`
	Summary []string  `json:"summary"`
	OK      bool      `json:"ok"`
}

type state struct {
	Settings Settings `json:"settings"`
	Last     *LastRun `json:"last,omitempty"`
}

// Manager runs and schedules maintenance.
type Manager struct {
	path    string
	Tasks   *tasks.Manager
	Apt     *system.Apt
	Updater *update.Updater
	Host    *hostagent.Client
	// Notify sends a message to the configured channels (may be nil).
	Notify func(kind, severity, title, body string)
	// Packages are installed if missing (acl, smbclient).
	Packages []string

	mu    sync.Mutex
	st    state
	wake  chan struct{}
	clock func() time.Time
}

// New loads the settings from path.
func New(path string) *Manager {
	m := &Manager{path: path, wake: make(chan struct{}, 1), clock: time.Now, st: state{Settings: Defaults()}}
	_ = fsutil.ReadJSON(path, &m.st)
	return m
}

var timeRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// Status is shown on the update page.
type Status struct {
	Settings Settings      `json:"settings"`
	NextRun  time.Time     `json:"next_run,omitzero"`
	Last     *LastRun      `json:"last,omitempty"`
	System   system.Status `json:"system"`
	Running  string        `json:"running,omitempty"` // task id
}

// Status returns settings, schedule and the last result.
func (m *Manager) Status() Status {
	m.mu.Lock()
	st := m.st
	m.mu.Unlock()
	s := Status{Settings: st.Settings, Last: st.Last, System: m.Apt.Status()}
	if st.Settings.Enabled {
		s.NextRun = m.next(st.Settings)
	}
	if t := m.Tasks.Running("system"); t != nil {
		s.Running = t.Info().ID
	}
	return s
}

// Save stores new settings and reschedules.
func (m *Manager) Save(s Settings) error {
	if !timeRe.MatchString(s.Time) {
		return valid.Errorf("time must be HH:MM")
	}
	if s.App != AppAuto && s.App != AppNotify && s.App != AppOff {
		return valid.Errorf("LocoStor mode must be auto, notify or off")
	}
	if !system.ValidMode(s.System) || !system.ValidMode(s.Host) {
		return valid.Errorf("package mode must be all, security, notify or off")
	}
	m.mu.Lock()
	m.st.Settings = s
	st := m.st
	m.mu.Unlock()
	if err := fsutil.WriteJSON(m.path, st, 0o600); err != nil {
		return err
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return nil
}

func (m *Manager) next(s Settings) time.Time {
	now := m.clock()
	t, _ := time.ParseInLocation("15:04", s.Time, now.Location())
	n := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	if !n.After(now) {
		n = n.AddDate(0, 0, 1)
	}
	return n
}

// RunPeriodic runs the daily maintenance until ctx ends.
func (m *Manager) RunPeriodic(ctx context.Context) {
	for {
		m.mu.Lock()
		s := m.st.Settings
		m.mu.Unlock()
		var timer <-chan time.Time
		if s.Enabled {
			timer = time.After(time.Until(m.next(s)))
		}
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
			continue
		case <-timer:
			_, err := m.Start(RunOptions{App: s.App, System: s.System, Host: s.Host, Trigger: "schedule"})
			if err != nil {
				log.Printf("maintenance: %v", err)
			}
			time.Sleep(time.Minute) // never twice in the same minute
		}
	}
}

// Start begins a maintenance run in the background.
func (m *Manager) Start(o RunOptions) (*tasks.Task, error) {
	for _, mode := range []string{o.System, o.Host} {
		if mode != "" && !system.ValidMode(mode) {
			return nil, valid.Errorf("unknown package mode %q", mode)
		}
	}
	title := "System update"
	if o.Trigger == "schedule" {
		title = "Daily system update"
	}
	return m.Tasks.Start("system", title, func(t *tasks.Task) error { return m.run(t, o) })
}

func (m *Manager) run(t *tasks.Task, o RunOptions) error {
	ctx := t.Context()
	var summary, problems []string
	security := 0
	t.Logf("LocoStor %s · %s · started %s", m.Updater.Current(), o.Trigger, m.clock().Format("2006-01-02 15:04"))

	// 1. Packages of the container.
	switch o.System {
	case "", system.ModeOff:
	case system.ModeNotify:
		st, err := m.Apt.Check(t)
		if err != nil {
			problems = append(problems, "container: "+err.Error())
		} else if len(st.Packages) > 0 {
			security += st.Security
			summary = append(summary, fmt.Sprintf("Container: %d updates available (%d security)", len(st.Packages), st.Security))
		}
	default:
		n, err := m.Apt.Upgrade(t, o.System)
		if err != nil {
			problems = append(problems, "container: "+err.Error())
		} else if n > 0 {
			summary = append(summary, fmt.Sprintf("Container: %d packages updated", n))
		}
		if len(m.Packages) > 0 {
			if err := m.Apt.EnsurePackages(t, m.Packages...); err != nil {
				problems = append(problems, "container packages: "+err.Error())
			}
		}
	}

	// 2. The Proxmox host, through the agent.
	var hostInfo hostagent.Info
	hostOK := false
	if m.Host.Available() {
		if i, err := m.Host.Info(ctx); err == nil {
			hostInfo, hostOK = i, true
		}
	}
	if hostOK && o.Host != "" && o.Host != system.ModeOff {
		if !hostInfo.HasFeature("system-updates") {
			t.Section("Proxmox host")
			t.Logf("The host agent %s cannot run updates yet - it is updated below first.", hostInfo.Version)
		} else {
			path, body := "/system/check", any(nil)
			if o.Host == system.ModeAll || o.Host == system.ModeSecurity {
				path, body = "/system/upgrade", map[string]string{"mode": o.Host}
			}
			t.Section("Proxmox host " + hostInfo.Hostname)
			var r struct {
				Task string `json:"task"`
			}
			if err := m.Host.Do(ctx, http.MethodPost, path, body, &r); err != nil {
				problems = append(problems, err.Error())
			} else if info, err := m.Host.Follow(t, r.Task, "  host │ "); err != nil || info.Status != tasks.OK {
				problems = append(problems, fmt.Sprintf("host update: %v%s", err, info.Error))
			}
			var hs system.Status
			if err := m.Host.Do(ctx, http.MethodGet, "/system/updates", nil, &hs); err == nil {
				if len(hs.Packages) > 0 {
					security += hs.Security
					summary = append(summary, fmt.Sprintf("Proxmox host: %d updates pending (%d security)", len(hs.Packages), hs.Security))
				}
				if hs.RebootRequired {
					summary = append(summary, "Proxmox host: a reboot is required")
				}
			}
		}
	}

	// 3. LocoStor itself and the host agent.
	restart := false
	if o.App != "" && o.App != AppOff {
		t.Section("LocoStor")
		st := m.Updater.Check(ctx)
		switch {
		case st.Error != "":
			problems = append(problems, "LocoStor release check: "+st.Error)
		case !st.UpdateAvailable:
			t.Logf("LocoStor %s is the latest release.", st.Current)
		case o.App == AppNotify || st.DisabledReason != "":
			t.Logf("LocoStor %s is available (installed: %s). %s", st.Latest, st.Current, st.DisabledReason)
			summary = append(summary, fmt.Sprintf("LocoStor %s is available", st.Latest))
		default:
			if hostOK {
				t.Logf("Updating the host agent first…")
				if err := m.Host.UpdateAgent(t); err != nil {
					problems = append(problems, "host agent: "+err.Error())
				}
			}
			t.Logf("Installing LocoStor %s…", st.Latest)
			if err := m.Updater.InstallLatest(ctx, func(s string) { t.Logf("%s", s) }); err != nil {
				problems = append(problems, "LocoStor update: "+err.Error())
			} else {
				summary = append(summary, fmt.Sprintf("LocoStor updated %s → %s", st.Current, st.Latest))
				restart = true
			}
		}
		if !restart && hostOK && hostInfo.Version != m.Updater.Current() && !strings.Contains(m.Updater.Current(), "dev") && !strings.Contains(m.Updater.Current(), "demo") {
			t.Logf("The host agent runs %s, LocoStor %s - bringing it in line.", hostInfo.Version, m.Updater.Current())
			if err := m.Host.UpdateAgent(t); err != nil {
				problems = append(problems, "host agent: "+err.Error())
			}
		}
	}

	// Summary and notification.
	t.Section("Summary")
	if len(summary) == 0 && len(problems) == 0 {
		summary = append(summary, "Everything is up to date.")
	}
	for _, s := range summary {
		t.Logf("• %s", s)
	}
	for _, p := range problems {
		t.Logf("✖ %s", p)
	}
	last := &LastRun{Task: t.Info().ID, Started: t.Info().Started, Trigger: o.Trigger, Summary: append(summary, problems...), OK: len(problems) == 0}
	m.mu.Lock()
	m.st.Last = last
	st := m.st
	m.mu.Unlock()
	_ = fsutil.WriteJSON(m.path, st, 0o600)
	if m.Notify != nil {
		body := strings.Join(last.Summary, "\n")
		switch {
		case len(problems) > 0:
			m.Notify("update", "error", "System update had problems", body)
		case security > 0:
			m.Notify("update", "warning", "Security updates pending", body)
		case len(summary) > 0 && summary[0] != "Everything is up to date.":
			m.Notify("update", "info", "System update", body)
		}
	}
	if restart {
		t.ExpectRestart()
		m.Updater.RestartSoon()
		return nil
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d problem(s) - see above", len(problems))
	}
	return nil
}
