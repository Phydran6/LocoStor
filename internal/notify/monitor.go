package notify

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Phydran6/LocoStor/internal/fsutil"
	"github.com/Phydran6/LocoStor/internal/raid"
	"github.com/Phydran6/LocoStor/internal/smart"
	"github.com/Phydran6/LocoStor/internal/sysinfo"
)

// Monitor watches RAID arrays, disk health, temperatures and free space
// and reports changes.
type Monitor struct {
	N     *Manager
	RAID  func() ([]raid.Array, error)
	SMART func(ctx context.Context) ([]smart.Disk, error)
	FS    func() []sysinfo.Filesystem
	Path  string // remembered state, so a restart does not repeat alerts

	st monitorState
}

type monitorState struct {
	RAID  map[string]string `json:"raid"`
	SMART map[string]string `json:"smart"`
	Hot   map[string]bool   `json:"hot"`
	Full  map[string]bool   `json:"full"`
}

// Run checks every few minutes until ctx ends.
func (mo *Monitor) Run(ctx context.Context) {
	_ = fsutil.ReadJSON(mo.Path, &mo.st)
	mo.init()
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Minute):
	}
	lastSmart := time.Time{}
	for {
		mo.CheckRAID()
		mo.CheckSpace()
		if time.Since(lastSmart) >= 30*time.Minute {
			cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			mo.CheckSMART(cctx)
			cancel()
			lastSmart = time.Now()
		}
		_ = fsutil.WriteJSON(mo.Path, mo.st, 0o600)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Minute):
		}
	}
}

func (mo *Monitor) init() {
	if mo.st.RAID == nil {
		mo.st.RAID = map[string]string{}
	}
	if mo.st.SMART == nil {
		mo.st.SMART = map[string]string{}
	}
	if mo.st.Hot == nil {
		mo.st.Hot = map[string]bool{}
	}
	if mo.st.Full == nil {
		mo.st.Full = map[string]bool{}
	}
}

func (mo *Monitor) send(severity, title, body string) {
	log.Printf("alert (%s): %s", severity, title)
	mo.N.Notify("monitor", severity, title, body)
}

// CheckRAID reports arrays whose health changed.
func (mo *Monitor) CheckRAID() {
	mo.init()
	if !mo.N.Events().RAID || mo.RAID == nil {
		return
	}
	arrays, err := mo.RAID()
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, a := range arrays {
		seen[a.Name] = true
		prev, known := mo.st.RAID[a.Name]
		mo.st.RAID[a.Name] = a.Health
		if prev == a.Health || (!known && a.Health == "ok") {
			continue
		}
		var members []string
		for _, mb := range a.Members {
			members = append(members, fmt.Sprintf("%s (%s)", mb.Name, mb.State))
		}
		body := fmt.Sprintf("%s · %d of %d disks active · [%s]\nDisks: %s", a.Level, a.ActiveDisks, a.RaidDisks, a.Status, strings.Join(members, ", "))
		if a.Progress >= 0 && a.SyncAction != "" {
			body += fmt.Sprintf("\n%s: %.1f%%, about %s left", a.SyncAction, a.Progress, a.Finish)
		}
		switch a.Health {
		case "ok":
			mo.send(Info, fmt.Sprintf("RAID %s is healthy again", a.Name), body)
		case "rebuilding":
			mo.send(Warning, fmt.Sprintf("RAID %s is rebuilding", a.Name), body)
		case "degraded":
			mo.send(Error, fmt.Sprintf("RAID %s is DEGRADED – a disk has failed", a.Name), body+"\n\nReplace the failed disk soon: the array has no redundancy left.")
		case "failed":
			mo.send(Error, fmt.Sprintf("RAID %s has FAILED", a.Name), body)
		default:
			mo.send(Warning, fmt.Sprintf("RAID %s is %s", a.Name, a.Health), body)
		}
	}
	for name := range mo.st.RAID {
		if !seen[name] {
			mo.send(Warning, fmt.Sprintf("RAID %s is gone", name), "The array is no longer listed in /proc/mdstat.")
			delete(mo.st.RAID, name)
		}
	}
}

// CheckSMART reports disks whose health got worse (or recovered) and hot
// disks. Sleeping disks are not woken up.
func (mo *Monitor) CheckSMART(ctx context.Context) {
	ev := mo.N.Events()
	if (!ev.SMART && ev.Temperature == 0) || mo.SMART == nil {
		return
	}
	mo.init()
	disks, err := mo.SMART(ctx)
	if err != nil {
		return
	}
	for _, d := range disks {
		key := d.Serial
		if key == "" {
			key = d.Device
		}
		name := fmt.Sprintf("%s (%s, %s)", d.Device, strings.TrimSpace(d.Model), d.Serial)
		if ev.SMART && d.Health != "standby" && d.Health != "unknown" {
			prev, known := mo.st.SMART[key]
			mo.st.SMART[key] = d.Health
			if prev != d.Health && (known || d.Health != "ok") {
				body := name
				if len(d.Messages) > 0 {
					body += "\n" + strings.Join(d.Messages, "\n")
				}
				if d.Reallocated != nil || d.Pending != nil {
					body += fmt.Sprintf("\nReallocated sectors: %s · Pending: %s", num(d.Reallocated), num(d.Pending))
				}
				switch d.Health {
				case "failed":
					mo.send(Error, "Disk "+d.Device+" is FAILING (SMART)", body+"\n\nBack up the data and replace the disk.")
				case "warning":
					mo.send(Warning, "Disk "+d.Device+" shows SMART warnings", body)
				case "ok":
					mo.send(Info, "Disk "+d.Device+" is healthy again", body)
				}
			}
		}
		if ev.Temperature > 0 && d.Temperature != nil {
			t := *d.Temperature
			switch {
			case t >= ev.Temperature && !mo.st.Hot[key]:
				mo.st.Hot[key] = true
				mo.send(Warning, fmt.Sprintf("Disk %s is hot: %d °C", d.Device, t), fmt.Sprintf("%s\nLimit: %d °C", name, ev.Temperature))
			case t <= ev.Temperature-3 && mo.st.Hot[key]:
				delete(mo.st.Hot, key)
				mo.send(Info, fmt.Sprintf("Disk %s cooled down: %d °C", d.Device, t), name)
			}
		}
	}
}

func num(p *int64) string {
	if p == nil {
		return "–"
	}
	return fmt.Sprint(*p)
}

// CheckSpace reports filesystems above the fill limit.
func (mo *Monitor) CheckSpace() {
	mo.init()
	limit := mo.N.Events().Space
	if limit == 0 || mo.FS == nil {
		return
	}
	for _, fs := range mo.FS() {
		if fs.Size == 0 {
			continue
		}
		used := int(fs.Used * 100 / fs.Size)
		switch {
		case used >= limit && !mo.st.Full[fs.Mount]:
			mo.st.Full[fs.Mount] = true
			mo.send(Warning, fmt.Sprintf("%s is %d%% full", fs.Mount, used), fmt.Sprintf("%s (%s): %.1f GiB free of %.1f GiB", fs.Mount, fs.Source, float64(fs.Avail)/(1<<30), float64(fs.Size)/(1<<30)))
		case used < limit-2 && mo.st.Full[fs.Mount]:
			delete(mo.st.Full, fs.Mount)
			mo.send(Info, fmt.Sprintf("%s has space again (%d%%)", fs.Mount, used), fs.Source)
		}
	}
}
