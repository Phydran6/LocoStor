package smb

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Phydran6/LocoStor/internal/fsutil"
	"github.com/Phydran6/LocoStor/internal/valid"
)

// ExternalShare is a share defined outside LocoStor, e.g. by hand in
// smb.conf. It can be adopted, which moves it into LocoStor's management.
type ExternalShare struct {
	Share
	Source    string `json:"source"`
	Adoptable bool   `json:"adoptable"`
	Reason    string `json:"reason,omitempty"`
}

// section is one [name] block of a Samba config file.
type section struct {
	name       string
	params     []Option
	file       string
	start, end int // line range [start, end) in file
}

var optionKeyRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 :_.-]{0,63}$`)

// normKey normalizes a Samba parameter name: case, spaces and underscores
// are insignificant ("Read Only" == "readonly").
func normKey(k string) string {
	return strings.NewReplacer(" ", "", "_", "", "\t", "").Replace(strings.ToLower(k))
}

// structuredKeys are parameters represented by Share fields.
var structuredKeys = map[string]bool{
	"path": true, "directory": true, "comment": true, "readonly": true, "writable": true,
	"writeable": true, "writeok": true, "browseable": true, "browsable": true,
	"guestok": true, "public": true, "validusers": true, "available": true,
}

func validateOptions(s *Share) error {
	opts := make([]Option, 0, len(s.Options))
	seen := map[string]bool{}
	for _, o := range s.Options {
		o.Key = strings.Join(strings.Fields(o.Key), " ")
		o.Value = strings.TrimSpace(o.Value)
		if o.Key == "" {
			continue
		}
		if !optionKeyRe.MatchString(o.Key) {
			return valid.Errorf("invalid option name %q", o.Key)
		}
		if err := valid.SingleLine("option "+o.Key, o.Value); err != nil {
			return err
		}
		n := normKey(o.Key)
		if structuredKeys[n] {
			return valid.Errorf("option %q is set by the form fields above", o.Key)
		}
		if n == "include" || n == "copy" {
			return valid.Errorf("option %q is not allowed", o.Key)
		}
		if seen[n] {
			return valid.Errorf("option %q is set twice", o.Key)
		}
		seen[n] = true
		opts = append(opts, o)
	}
	s.Options = opts
	return nil
}

func parseBool(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "yes", "true", "1", "on":
		return true
	case "no", "false", "0", "off":
		return false
	}
	return def
}

// shareFromSection maps smb.conf parameters to a Share. Samba defaults
// apply for missing values (read only = yes, browseable = yes).
func shareFromSection(sec section) Share {
	s := Share{Name: sec.name, ReadOnly: true, Browseable: true, Enabled: true, ValidUsers: []string{}, Options: []Option{}}
	for _, p := range sec.params {
		switch normKey(p.Key) {
		case "path", "directory":
			s.Path = p.Value
		case "comment":
			s.Comment = p.Value
		case "readonly":
			s.ReadOnly = parseBool(p.Value, true)
		case "writable", "writeable", "writeok":
			s.ReadOnly = !parseBool(p.Value, false)
		case "browseable", "browsable":
			s.Browseable = parseBool(p.Value, true)
		case "guestok", "public":
			s.GuestOK = parseBool(p.Value, false)
		case "validusers":
			s.ValidUsers = strings.FieldsFunc(p.Value, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' })
		case "available":
			s.Enabled = parseBool(p.Value, true)
		default:
			s.Options = append(s.Options, p)
		}
	}
	return s
}

// readSections parses a Samba config file and the files it includes.
// Files in skip (LocoStor's own include) are ignored.
func readSections(path string, skip map[string]bool, depth int) ([]section, error) {
	if depth > 5 || skip[path] {
		return nil, nil
	}
	skip[path] = true
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	var out []section
	var cur *section
	closeCur := func(end int) {
		if cur != nil {
			cur.end = end
			out = append(out, *cur)
			cur = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		start := i
		line := strings.TrimSpace(lines[i])
		for strings.HasSuffix(line, `\`) && i+1 < len(lines) {
			i++
			line = strings.TrimSuffix(line, `\`) + " " + strings.TrimSpace(lines[i])
		}
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			closeCur(start)
			if j := strings.IndexByte(line, ']'); j > 0 {
				cur = &section{name: strings.TrimSpace(line[1:j]), file: path, start: start}
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if normKey(key) == "include" && !strings.Contains(value, "%") {
			inc, err := readSections(value, skip, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, inc...)
			continue
		}
		if cur != nil {
			cur.params = append(cur.params, Option{Key: key, Value: value})
		}
	}
	closeCur(len(lines))
	return out, nil
}

func (m *Manager) externalSections() ([]section, error) {
	return readSections(m.opts.MainConf, map[string]bool{m.opts.IncludePath: true}, 0)
}

// External lists shares defined outside LocoStor.
func (m *Manager) External() ([]ExternalShare, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	secs, err := m.externalSections()
	if err != nil {
		return nil, err
	}
	managed, err := m.load()
	if err != nil {
		return nil, err
	}
	out := []ExternalShare{}
	for _, sec := range secs {
		lower := strings.ToLower(sec.name)
		if lower == "global" {
			continue
		}
		e := ExternalShare{Share: shareFromSection(sec), Source: sec.file, Adoptable: true}
		switch {
		case reserved[lower]:
			e.Adoptable, e.Reason = false, "special Samba section"
		case !shareNameRe.MatchString(sec.name):
			e.Adoptable, e.Reason = false, "name contains unsupported characters"
		case e.Path == "":
			e.Adoptable, e.Reason = false, "no path set"
		}
		for _, s := range managed {
			if strings.EqualFold(s.Name, sec.name) {
				e.Adoptable, e.Reason = false, "a LocoStor share has the same name"
			}
		}
		if e.Adoptable {
			if err := validateOptions(&e.Share); err != nil {
				e.Adoptable, e.Reason = false, err.Error()
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// Adopt moves an external share into LocoStor: the section is removed from
// its file (a backup is kept) and recreated in LocoStor's include file.
func (m *Manager) Adopt(ctx context.Context, name string) (Share, error) {
	ext, err := m.External()
	if err != nil {
		return Share{}, err
	}
	var target *ExternalShare
	for i := range ext {
		if strings.EqualFold(ext[i].Name, name) {
			target = &ext[i]
		}
	}
	if target == nil {
		return Share{}, &valid.NotFound{What: "share " + name}
	}
	if !target.Adoptable {
		return Share{}, valid.Errorf("cannot take over %q: %s", name, target.Reason)
	}
	s := target.Share
	if err := m.validate(&s); err != nil {
		return s, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	secs, err := m.externalSections()
	if err != nil {
		return s, err
	}
	var sec *section
	for i := range secs {
		if strings.EqualFold(secs[i].name, name) {
			sec = &secs[i]
		}
	}
	if sec == nil {
		return s, &valid.NotFound{What: "share " + name}
	}
	original, err := os.ReadFile(sec.file)
	if err != nil {
		return s, err
	}
	if err := backup(sec.file, original); err != nil {
		return s, err
	}
	lines := strings.Split(string(original), "\n")
	rest := append(append([]string{}, lines[:sec.start]...), lines[sec.end:]...)
	if err := fsutil.WriteFileAtomic(sec.file, []byte(strings.Join(rest, "\n")), 0o644); err != nil {
		return s, err
	}

	shares, err := m.load()
	if err == nil {
		err = m.apply(ctx, append(shares, s))
	}
	if err != nil {
		// Put the section back so Samba keeps serving the share.
		_ = fsutil.WriteFileAtomic(sec.file, original, 0o644)
		return s, err
	}
	return s, nil
}

// backup writes a timestamped copy next to path, e.g. smb.conf.locostor-20260926-193000.
func backup(path string, data []byte) error {
	name := path + ".locostor-" + time.Now().Format("20060102-150405")
	return fsutil.WriteFileAtomic(name, data, 0o644)
}
