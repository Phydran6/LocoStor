// Package smart reads disk health via smartctl's JSON output.
package smart

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Phydran6/LocoStor/internal/config"
	"github.com/Phydran6/LocoStor/internal/sysexec"
)

// smartctl exit status bits (see smartctl(8)).
const (
	bitOpenFailed   = 1 << 1
	bitDiskFailing  = 1 << 3
	bitPrefail      = 1 << 4
	bitPastPrefail  = 1 << 5
	bitSelfTestFail = 1 << 7
)

// Attribute is one ATA SMART attribute.
type Attribute struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Value      int    `json:"value"`
	Worst      int    `json:"worst"`
	Thresh     int    `json:"thresh"`
	Raw        string `json:"raw"`
	WhenFailed string `json:"when_failed,omitempty"`
}

// Disk is the SMART summary of one device.
type Disk struct {
	Device        string      `json:"device"`
	Type          string      `json:"type"`
	Protocol      string      `json:"protocol"`
	Model         string      `json:"model"`
	Serial        string      `json:"serial"`
	Firmware      string      `json:"firmware"`
	CapacityBytes int64       `json:"capacity_bytes"`
	RotationRate  int         `json:"rotation_rate"` // 0 = SSD
	Passed        *bool       `json:"passed"`
	Temperature   *int        `json:"temperature"`
	PowerOnHours  *int64      `json:"power_on_hours"`
	PowerCycles   *int64      `json:"power_cycles"`
	Reallocated   *int64      `json:"reallocated"`
	Pending       *int64      `json:"pending"`
	Uncorrectable *int64      `json:"uncorrectable"`
	MediaErrors   *int64      `json:"media_errors"`
	PercentUsed   *int        `json:"percent_used"`
	Health        string      `json:"health"` // ok, warning, failed, standby, unknown
	Messages      []string    `json:"messages"`
	Attributes    []Attribute `json:"attributes"`
}

// Manager queries smartctl and caches the results.
type Manager struct {
	run       sysexec.Runner
	overrides []config.SmartDevice

	mu       sync.Mutex
	cache    []Disk
	cachedAt time.Time
}

// New creates a Manager. overrides replaces device discovery if non-empty.
func New(run sysexec.Runner, overrides []config.SmartDevice) *Manager {
	return &Manager{run: run, overrides: overrides}
}

const cacheTTL = 5 * time.Minute

// Disks returns SMART data for all disks, using a cache unless refresh is set.
func (m *Manager) Disks(ctx context.Context, refresh bool) ([]Disk, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !refresh && m.cache != nil && time.Since(m.cachedAt) < cacheTTL {
		return m.cache, m.cachedAt, nil
	}
	devs, err := m.scan(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	disks := make([]Disk, len(devs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, d := range devs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			disks[i] = m.query(ctx, d.Device, d.Type)
		}()
	}
	wg.Wait()
	sort.Slice(disks, func(i, j int) bool { return disks[i].Device < disks[j].Device })
	m.cache, m.cachedAt = disks, time.Now()
	return disks, m.cachedAt, nil
}

func (m *Manager) scan(ctx context.Context) ([]config.SmartDevice, error) {
	if len(m.overrides) > 0 {
		return m.overrides, nil
	}
	out, err := m.run.Run(ctx, "", "smartctl", "--scan-open", "-j")
	if len(out) == 0 && err != nil {
		return nil, fmt.Errorf("smartctl scan: %w", err)
	}
	var res struct {
		Devices []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("parse smartctl scan: %w", err)
	}
	devs := []config.SmartDevice{}
	seen := map[string]bool{}
	for _, d := range res.Devices {
		if seen[d.Name] {
			continue
		}
		seen[d.Name] = true
		devs = append(devs, config.SmartDevice{Device: d.Name, Type: d.Type})
	}
	return devs, nil
}

type rawOutput struct {
	Smartctl struct {
		ExitStatus int `json:"exit_status"`
		Messages   []struct {
			String   string `json:"string"`
			Severity string `json:"severity"`
		} `json:"messages"`
	} `json:"smartctl"`
	Device struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Protocol string `json:"protocol"`
	} `json:"device"`
	ModelName       string `json:"model_name"`
	SerialNumber    string `json:"serial_number"`
	FirmwareVersion string `json:"firmware_version"`
	UserCapacity    struct {
		Bytes int64 `json:"bytes"`
	} `json:"user_capacity"`
	NvmeTotalCapacity int64 `json:"nvme_total_capacity"`
	RotationRate      int   `json:"rotation_rate"`
	SmartStatus       *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature *struct {
		Current int `json:"current"`
	} `json:"temperature"`
	PowerOnTime *struct {
		Hours int64 `json:"hours"`
	} `json:"power_on_time"`
	PowerCycleCount *int64 `json:"power_cycle_count"`
	ATA             *struct {
		Table []struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			Value      int    `json:"value"`
			Worst      int    `json:"worst"`
			Thresh     int    `json:"thresh"`
			WhenFailed string `json:"when_failed"`
			Raw        struct {
				Value  int64  `json:"value"`
				String string `json:"string"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	NVMe *struct {
		PercentageUsed int   `json:"percentage_used"`
		MediaErrors    int64 `json:"media_errors"`
	} `json:"nvme_smart_health_information_log"`
}

func (m *Manager) query(ctx context.Context, dev, typ string) Disk {
	d := m.queryOnce(ctx, dev, typ)
	// USB bridges often need SAT pass-through; retry if auto-detection failed.
	if d.Health == "unknown" && typ != "sat" && strings.Contains(strings.Join(d.Messages, " "), "USB") {
		if d2 := m.queryOnce(ctx, dev, "sat"); d2.Health != "unknown" {
			return d2
		}
	}
	return d
}

func (m *Manager) queryOnce(ctx context.Context, dev, typ string) Disk {
	args := []string{"-a", "-j", "-n", "standby"}
	if typ != "" {
		args = append(args, "-d", typ)
	}
	args = append(args, dev)
	out, runErr := m.run.Run(ctx, "", "smartctl", args...)
	d := Disk{Device: dev, Type: typ, Health: "unknown", Messages: []string{}, Attributes: []Attribute{}}

	var raw rawOutput
	if err := json.Unmarshal(out, &raw); err != nil {
		if runErr != nil {
			d.Messages = append(d.Messages, runErr.Error())
		} else {
			d.Messages = append(d.Messages, "cannot parse smartctl output: "+err.Error())
		}
		return d
	}
	return Convert(dev, typ, &raw)
}

// Convert turns raw smartctl JSON into a Disk.
func Convert(dev, typ string, raw *rawOutput) Disk {
	d := Disk{
		Device:        dev,
		Type:          typ,
		Protocol:      raw.Device.Protocol,
		Model:         raw.ModelName,
		Serial:        raw.SerialNumber,
		Firmware:      raw.FirmwareVersion,
		CapacityBytes: raw.UserCapacity.Bytes,
		RotationRate:  raw.RotationRate,
		PowerCycles:   raw.PowerCycleCount,
		Health:        "unknown",
		Messages:      []string{},
		Attributes:    []Attribute{},
	}
	if raw.Device.Type != "" {
		d.Type = raw.Device.Type
	}
	if d.CapacityBytes == 0 {
		d.CapacityBytes = raw.NvmeTotalCapacity
	}
	standby := false
	for _, msg := range raw.Smartctl.Messages {
		d.Messages = append(d.Messages, msg.String)
		if strings.Contains(strings.ToUpper(msg.String), "STANDBY") {
			standby = true
		}
	}
	if standby {
		d.Health = "standby"
		return d
	}
	if raw.Temperature != nil {
		t := raw.Temperature.Current
		d.Temperature = &t
	}
	if raw.PowerOnTime != nil {
		h := raw.PowerOnTime.Hours
		d.PowerOnHours = &h
	}
	if raw.ATA != nil {
		for _, a := range raw.ATA.Table {
			d.Attributes = append(d.Attributes, Attribute{
				ID: a.ID, Name: a.Name, Value: a.Value, Worst: a.Worst, Thresh: a.Thresh,
				Raw: a.Raw.String, WhenFailed: a.WhenFailed,
			})
			v := a.Raw.Value
			switch a.ID {
			case 5:
				d.Reallocated = &v
			case 197:
				d.Pending = &v
			case 198:
				d.Uncorrectable = &v
			}
		}
	}
	if raw.NVMe != nil {
		me, pu := raw.NVMe.MediaErrors, raw.NVMe.PercentageUsed
		d.MediaErrors, d.PercentUsed = &me, &pu
	}

	exit := raw.Smartctl.ExitStatus
	if raw.SmartStatus == nil {
		if exit&bitOpenFailed != 0 {
			return d
		}
	} else {
		p := raw.SmartStatus.Passed
		d.Passed = &p
	}

	switch {
	case (d.Passed != nil && !*d.Passed) || exit&bitDiskFailing != 0:
		d.Health = "failed"
	case exit&(bitPrefail|bitPastPrefail|bitSelfTestFail) != 0,
		gt0(d.Reallocated), gt0(d.Pending), gt0(d.Uncorrectable), gt0(d.MediaErrors),
		d.PercentUsed != nil && *d.PercentUsed >= 90:
		d.Health = "warning"
	case d.Passed != nil:
		d.Health = "ok"
	}
	return d
}

func gt0(v *int64) bool { return v != nil && *v > 0 }
