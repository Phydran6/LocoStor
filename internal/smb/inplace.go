package smb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Phydran6/LocoStor/internal/fsutil"
	"github.com/Phydran6/LocoStor/internal/sysexec"
	"github.com/Phydran6/LocoStor/internal/valid"
)

// HostShare is a share in an existing Samba configuration that LocoStor
// edits in place (the Proxmox host's). The configuration stays owned by the
// host: manual edits keep working, LocoStor only rewrites single sections.
type HostShare struct {
	Share
	File     string `json:"file"`
	Editable bool   `json:"editable"`
	Reason   string `json:"reason,omitempty"`
}

// HostScan lists the shares of a Samba configuration.
type HostScan struct {
	Shares   []HostShare `json:"shares"`
	Scanned  []string    `json:"scanned"`
	Warnings []string    `json:"warnings"`
}

// InPlace edits shares directly in an existing smb.conf (and the files it
// includes) without taking ownership of it.
type InPlace struct {
	run           sysexec.Runner
	mainConf      string
	skipPathCheck bool
	mu            sync.Mutex
}

// NewInPlace creates an editor for the Samba configuration at mainConf.
func NewInPlace(run sysexec.Runner, mainConf string, skipPathCheck bool) *InPlace {
	return &InPlace{run: run, mainConf: mainConf, skipPathCheck: skipPathCheck}
}

// Installed reports whether a Samba configuration exists.
func (p *InPlace) Installed() bool {
	_, err := os.Stat(p.mainConf)
	return err == nil
}

// sections returns the file sections and, if testparm works, the names of
// the shares Samba actually serves.
func (p *InPlace) sections(ctx context.Context, scan *Scan) (files []section, effective []section, haveEffective bool) {
	files = readSections(p.mainConf, map[string]bool{}, 0, scan)
	effective, haveEffective = effectiveSections(ctx, p.run, p.mainConf)
	return files, effective, haveEffective
}

func (p *InPlace) describe(sec section) HostShare {
	h := HostShare{Share: shareFromSection(sec), File: sec.file, Editable: true}
	lower := strings.ToLower(sec.name)
	switch {
	case sec.file == "":
		h.Editable, h.Reason = false, "defined in the Samba registry (net conf)"
	case reserved[lower]:
		h.Editable, h.Reason = false, "special Samba section"
	case sec.hasInclude:
		h.Editable, h.Reason = false, "contains an include line"
	case !shareNameRe.MatchString(sec.name):
		h.Editable, h.Reason = false, "name contains characters LocoStor does not support"
	case h.Path == "":
		h.Editable, h.Reason = false, "no path set"
	}
	if h.Editable {
		tmp := h.Share
		if err := validateShare(&tmp, false); err != nil {
			h.Editable, h.Reason = false, err.Error()
		}
	}
	return h
}

// List returns all shares of the configuration.
func (p *InPlace) List(ctx context.Context) HostScan {
	p.mu.Lock()
	defer p.mu.Unlock()
	scan := Scan{Scanned: []string{}, Warnings: []string{}}
	files, eff, ok := p.sections(ctx, &scan)
	out := HostScan{Shares: []HostShare{}, Scanned: scan.Scanned, Warnings: scan.Warnings}
	inFile := map[string]section{}
	for _, s := range files {
		inFile[strings.ToLower(s.name)] = s
	}
	list := files
	if ok {
		out.Scanned = append(out.Scanned, "testparm (effective Samba configuration)")
		list = nil
		for _, s := range eff {
			if fs, found := inFile[strings.ToLower(s.name)]; found {
				list = append(list, fs)
			} else {
				list = append(list, s) // registry share
			}
		}
	} else {
		out.Warnings = append(out.Warnings, "testparm is not available - only config files were read")
	}
	seen := map[string]bool{}
	for _, s := range list {
		k := strings.ToLower(s.name)
		if k == "global" || seen[k] {
			continue
		}
		seen[k] = true
		out.Shares = append(out.Shares, p.describe(s))
	}
	return out
}

// Save creates (oldName == "") or changes a share in place.
func (p *InPlace) Save(ctx context.Context, oldName string, s Share) (Share, error) {
	if err := validateShare(&s, !p.skipPathCheck); err != nil {
		return s, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var scan Scan
	files, eff, _ := p.sections(ctx, &scan)

	// The new name must not collide with any other share.
	for _, list := range [][]section{files, eff} {
		for _, x := range list {
			if strings.EqualFold(x.name, s.Name) && !strings.EqualFold(x.name, oldName) {
				return s, valid.Errorf("a share named %q already exists", s.Name)
			}
		}
	}

	var b strings.Builder
	if oldName == "" {
		data, err := os.ReadFile(p.mainConf)
		if err != nil {
			return s, err
		}
		text := strings.TrimRight(string(data), "\n") + "\n\n"
		writeShare(&b, s, nil)
		return s, p.write(ctx, p.mainConf, data, text+b.String())
	}

	sec := findSection(files, oldName)
	if sec == nil {
		return s, &valid.NotFound{What: "share " + oldName}
	}
	if d := p.describe(*sec); !d.Editable {
		return s, valid.Errorf("%q cannot be edited here: %s", oldName, d.Reason)
	}
	data, err := os.ReadFile(sec.file)
	if err != nil {
		return s, err
	}
	writeShare(&b, s, sec.comments)
	lines := splitLines(data)
	block := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	next := append(append(append([]string{}, lines[:sec.start]...), block...), lines[sec.end:]...)
	return s, p.write(ctx, sec.file, data, strings.Join(next, "\n"))
}

// Delete removes a share section.
func (p *InPlace) Delete(ctx context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var scan Scan
	files, _, _ := p.sections(ctx, &scan)
	sec := findSection(files, name)
	if sec == nil {
		return &valid.NotFound{What: "share " + name}
	}
	if d := p.describe(*sec); !d.Editable {
		return valid.Errorf("%q cannot be deleted here: %s", name, d.Reason)
	}
	data, err := os.ReadFile(sec.file)
	if err != nil {
		return err
	}
	lines := splitLines(data)
	start := sec.start
	// Drop the blank line that separated the section from the previous one.
	if start > 0 && strings.TrimSpace(lines[start-1]) == "" {
		start--
	}
	next := append(append([]string{}, lines[:start]...), lines[sec.end:]...)
	return p.write(ctx, sec.file, data, strings.Join(next, "\n"))
}

func findSection(secs []section, name string) *section {
	var found *section
	for i := range secs {
		if strings.EqualFold(secs[i].name, name) {
			found = &secs[i] // the last definition wins, as in Samba
		}
	}
	return found
}

func splitLines(data []byte) []string {
	return strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
}

// write validates the new file content with testparm, keeps a backup, writes
// it and reloads Samba. If Samba rejects the result, the old file is restored.
func (p *InPlace) write(ctx context.Context, file string, original []byte, content string) error {
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".locostor-check-*.conf")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(content)
	tmp.Close()
	if err != nil {
		return err
	}
	if out, err := p.run.Run(ctx, "", "testparm", "-s", "--suppress-prompt", tmp.Name()); err != nil {
		return valid.Errorf("Samba rejected the change: %s", firstLine(out, err))
	}

	mode := os.FileMode(0o644)
	if fi, err := os.Stat(file); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := backup(file, original); err != nil {
		return fmt.Errorf("backup %s: %w", file, err)
	}
	if err := fsutil.WriteFileAtomic(file, []byte(content), mode); err != nil {
		return err
	}
	if err := reloadSamba(ctx, p.run); err != nil {
		_ = fsutil.WriteFileAtomic(file, original, mode)
		_ = reloadSamba(ctx, p.run)
		return fmt.Errorf("change reverted: %w", err)
	}
	return nil
}

func firstLine(out []byte, err error) string {
	var se *sysexec.Error
	if errors.As(err, &se) && strings.TrimSpace(se.Stderr) != "" {
		for _, l := range strings.Split(se.Stderr, "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "Load smb config") {
				return l
			}
		}
	}
	if s := strings.TrimSpace(string(out)); s != "" {
		return strings.SplitN(s, "\n", 2)[0]
	}
	return err.Error()
}
