// Package sysinfo collects basic system information for the dashboard.
package sysinfo

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Info is a system overview.
type Info struct {
	Hostname      string       `json:"hostname"`
	Kernel        string       `json:"kernel"`
	OS            string       `json:"os"`
	Arch          string       `json:"arch"`
	UptimeSeconds int64        `json:"uptime_seconds"`
	Load          [3]float64   `json:"load"`
	MemTotal      int64        `json:"mem_total"`
	MemAvailable  int64        `json:"mem_available"`
	Filesystems   []Filesystem `json:"filesystems"`
}

// Filesystem is a mounted filesystem with usage.
type Filesystem struct {
	Mount  string `json:"mount"`
	Source string `json:"source"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	Used   int64  `json:"used"`
	Avail  int64  `json:"avail"`
}

// Reader reads system information from a proc root (normally "/proc").
type Reader struct {
	ProcRoot string
	// OSRelease is the path of os-release, normally /etc/os-release.
	OSRelease string
}

func (r Reader) read(name string) string {
	b, err := os.ReadFile(filepath.Join(r.ProcRoot, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Read collects the current system info.
func (r Reader) Read() Info {
	info := Info{Arch: runtime.GOARCH, Filesystems: []Filesystem{}}
	info.Hostname, _ = os.Hostname()
	info.Kernel = r.read("sys/kernel/osrelease")
	if f := strings.Fields(r.read("uptime")); len(f) > 0 {
		v, _ := strconv.ParseFloat(f[0], 64)
		info.UptimeSeconds = int64(v)
	}
	if f := strings.Fields(r.read("loadavg")); len(f) >= 3 {
		for i := 0; i < 3; i++ {
			info.Load[i], _ = strconv.ParseFloat(f[i], 64)
		}
	}
	for _, line := range strings.Split(r.read("meminfo"), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseInt(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			info.MemTotal = v * 1024
		case "MemAvailable:":
			info.MemAvailable = v * 1024
		}
	}
	info.OS = osName(r.OSRelease)
	info.Filesystems = r.filesystems()
	return info
}

func osName(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return runtime.GOOS
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return runtime.GOOS
}

// Filesystem types worth showing; everything else (proc, tmpfs, fuse
// helpers, ...) is noise.
var realFS = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true,
	"vfat": true, "exfat": true, "ntfs": true, "ntfs3": true, "f2fs": true, "nfs": true, "nfs4": true, "cifs": true,
}

// parseMountinfo returns (mountpoint, source, fstype) for relevant mounts.
func parseMountinfo(data string) [][3]string {
	var res [][3]string
	seen := map[string]bool{}
	for _, line := range strings.Split(data, "\n") {
		pre, post, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		pf, qf := strings.Fields(pre), strings.Fields(post)
		if len(pf) < 5 || len(qf) < 2 {
			continue
		}
		mount := unescape(pf[4])
		if !realFS[qf[0]] || seen[mount] {
			continue
		}
		if strings.HasPrefix(mount, "/proc") || strings.HasPrefix(mount, "/sys") || strings.HasPrefix(mount, "/dev") || strings.HasPrefix(mount, "/run") {
			continue
		}
		seen[mount] = true
		res = append(res, [3]string{mount, qf[1], qf[0]})
	}
	return res
}

// unescape decodes octal escapes (\040 for space) used in mountinfo.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
