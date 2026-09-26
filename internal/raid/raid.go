// Package raid reads Linux software RAID (mdadm) status.
//
// The md driver lives in the host kernel, so /proc/mdstat and
// /sys/block/md*/md inside a privileged LXC reflect the host's arrays even
// though the array itself is only bind-mounted into the container.
package raid

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Member is a component device of an array.
type Member struct {
	Name  string `json:"name"`
	Index int    `json:"index"`
	State string `json:"state"` // active, faulty, spare, write-mostly, replacement
}

// Array is one md array.
type Array struct {
	Name        string   `json:"name"`
	State       string   `json:"state"` // active, inactive
	ReadOnly    bool     `json:"read_only"`
	Level       string   `json:"level"`
	SizeBytes   int64    `json:"size_bytes"`
	RaidDisks   int      `json:"raid_disks"`
	ActiveDisks int      `json:"active_disks"`
	Status      string   `json:"status"` // e.g. "UU_"
	Members     []Member `json:"members"`
	SyncAction  string   `json:"sync_action,omitempty"` // recovery, resync, check, ...
	Progress    float64  `json:"progress"`              // percent, -1 if idle
	Finish      string   `json:"finish,omitempty"`
	Speed       string   `json:"speed,omitempty"`
	ArrayState  string   `json:"array_state,omitempty"` // from sysfs
	MismatchCnt int64    `json:"mismatch_cnt"`
	Health      string   `json:"health"` // ok, degraded, rebuilding, failed, inactive
}

var (
	memberRe   = regexp.MustCompile(`^([^\[\s]+)\[(\d+)\](?:\((\w)\))?$`)
	statusRe   = regexp.MustCompile(`\[(\d+)/(\d+)\]\s+\[([U_]+)\]`)
	blocksRe   = regexp.MustCompile(`^(\d+)\s+blocks`)
	progressRe = regexp.MustCompile(`(recovery|resync|reshape|check|repair)\s*=\s*([\d.]+)%`)
	finishRe   = regexp.MustCompile(`finish=(\S+)`)
	speedRe    = regexp.MustCompile(`speed=(\S+)`)
	delayedRe  = regexp.MustCompile(`(recovery|resync|reshape|check|repair)\s*=\s*(DELAYED|PENDING)`)
)

// Read parses <procRoot>/mdstat and enriches it from <sysRoot>/block.
// A missing mdstat (no md driver loaded) yields an empty list.
func Read(procRoot, sysRoot string) ([]Array, error) {
	f, err := os.Open(filepath.Join(procRoot, "mdstat"))
	if errors.Is(err, os.ErrNotExist) {
		return []Array{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	arrays, err := Parse(bufio.NewScanner(f))
	if err != nil {
		return nil, err
	}
	for i := range arrays {
		enrich(&arrays[i], sysRoot)
		arrays[i].Health = health(&arrays[i])
	}
	return arrays, nil
}

// markRebuilding flags members that are being rebuilt: during recovery the
// replacement disk is listed with a slot number >= raid_disks and no flag.
func markRebuilding(a *Array) {
	if a.SyncAction != "recovery" || a.RaidDisks == 0 {
		return
	}
	for i := range a.Members {
		if a.Members[i].State == "active" && a.Members[i].Index >= a.RaidDisks {
			a.Members[i].State = "rebuilding"
		}
	}
}

// Parse parses mdstat content.
func Parse(sc *bufio.Scanner) ([]Array, error) {
	arrays := []Array{}
	var cur *Array
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(line, "md") && strings.Contains(line, " : ") {
			if cur != nil {
				arrays = append(arrays, *cur)
			}
			cur = parseHeader(line)
			continue
		}
		if cur == nil || trimmed == "" {
			if trimmed == "" && cur != nil {
				arrays = append(arrays, *cur)
				cur = nil
			}
			continue
		}
		if m := blocksRe.FindStringSubmatch(trimmed); m != nil {
			n, _ := strconv.ParseInt(m[1], 10, 64)
			cur.SizeBytes = n * 1024
		}
		if m := statusRe.FindStringSubmatch(trimmed); m != nil {
			cur.RaidDisks, _ = strconv.Atoi(m[1])
			cur.ActiveDisks, _ = strconv.Atoi(m[2])
			cur.Status = m[3]
		}
		if m := progressRe.FindStringSubmatch(trimmed); m != nil {
			cur.SyncAction = m[1]
			cur.Progress, _ = strconv.ParseFloat(m[2], 64)
			if f := finishRe.FindStringSubmatch(trimmed); f != nil {
				cur.Finish = f[1]
			}
			if s := speedRe.FindStringSubmatch(trimmed); s != nil {
				cur.Speed = s[1]
			}
		} else if m := delayedRe.FindStringSubmatch(trimmed); m != nil {
			cur.SyncAction = m[1] + " (" + strings.ToLower(m[2]) + ")"
		}
	}
	if cur != nil {
		arrays = append(arrays, *cur)
	}
	for i := range arrays {
		markRebuilding(&arrays[i])
	}
	sort.Slice(arrays, func(i, j int) bool { return arrays[i].Name < arrays[j].Name })
	return arrays, sc.Err()
}

func parseHeader(line string) *Array {
	name, rest, _ := strings.Cut(line, " : ")
	a := &Array{Name: strings.TrimSpace(name), Progress: -1, Members: []Member{}}
	fields := strings.Fields(rest)
	for i, f := range fields {
		switch {
		case i == 0:
			a.State = f
		case strings.HasPrefix(f, "(") && strings.Contains(f, "read-only"):
			a.ReadOnly = true
		case strings.HasPrefix(f, "raid") || f == "linear" || f == "multipath" || f == "faulty":
			if a.Level == "" {
				a.Level = f
			}
		default:
			if m := memberRe.FindStringSubmatch(f); m != nil {
				idx, _ := strconv.Atoi(m[2])
				st := "active"
				switch m[3] {
				case "F":
					st = "faulty"
				case "S":
					st = "spare"
				case "W":
					st = "write-mostly"
				case "R":
					st = "replacement"
				}
				a.Members = append(a.Members, Member{Name: m[1], Index: idx, State: st})
			}
		}
	}
	sort.Slice(a.Members, func(i, j int) bool { return a.Members[i].Index < a.Members[j].Index })
	return a
}

func readSys(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func enrich(a *Array, sysRoot string) {
	dir := filepath.Join(sysRoot, "block", a.Name, "md")
	if s := readSys(filepath.Join(dir, "array_state")); s != "" {
		a.ArrayState = s
	}
	if s := readSys(filepath.Join(dir, "mismatch_cnt")); s != "" {
		a.MismatchCnt, _ = strconv.ParseInt(s, 10, 64)
	}
	if a.SyncAction == "" {
		if s := readSys(filepath.Join(dir, "sync_action")); s != "" && s != "idle" {
			a.SyncAction = s
		}
	}
}

func health(a *Array) string {
	switch {
	case a.State != "active":
		return "inactive"
	case a.RaidDisks > 0 && a.ActiveDisks == 0:
		return "failed"
	case a.SyncAction == "recovery" || a.SyncAction == "reshape" || strings.HasPrefix(a.SyncAction, "recovery"):
		return "rebuilding"
	case a.RaidDisks > 0 && a.ActiveDisks < a.RaidDisks:
		return "degraded"
	}
	for _, m := range a.Members {
		if m.State == "faulty" {
			return "degraded"
		}
	}
	return "ok"
}
