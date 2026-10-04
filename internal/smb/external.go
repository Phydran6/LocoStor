package smb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Phydran6/LocoStor/internal/fsutil"
	"github.com/Phydran6/LocoStor/internal/sysexec"
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
	start, end int // line range [start, end) in file, without trailing blank lines
	comments   []string
	hasInclude bool // an include line sits inside the section
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

// dangerousOption reports parameters that run programs as root (preexec,
// *command, *script, magic script ...) or redirect Samba's own files. They
// are refused so a web login can never be turned into command execution.
func dangerousOption(n string) bool {
	if strings.Contains(n, "exec") || strings.HasSuffix(n, "command") || strings.HasSuffix(n, "script") {
		return true
	}
	switch n {
	case "include", "copy", "magicoutput", "configfile", "lockdirectory", "statedirectory",
		"cachedirectory", "privatedir", "smbpasswdfile", "passdbbackend", "usernamemap", "logfile":
		return true
	}
	return false
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
		if dangerousOption(n) {
			return valid.Errorf("option %q is not allowed: it can run commands or change Samba's own files", o.Key)
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

// Scan is the result of looking for shares defined outside LocoStor.
type Scan struct {
	Shares   []ExternalShare `json:"shares"`
	Scanned  []string        `json:"scanned"`  // files and sources that were read
	Warnings []string        `json:"warnings"` // problems that did not stop the scan
}

// parseSections parses Samba config text. It returns the sections and the
// include targets (in order).
func parseSections(data, file string) ([]section, []string) {
	lines := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	var out []section
	var includes []string
	var cur *section
	last := 0 // last line of the current section that is not blank or a comment
	type comment struct {
		line int
		text string
	}
	var comments []comment
	closeCur := func() {
		if cur != nil {
			cur.end = last + 1
			for _, c := range comments {
				if c.line < cur.end {
					cur.comments = append(cur.comments, c.text)
				}
			}
			out = append(out, *cur)
			cur = nil
		}
		comments = nil
	}
	for i := 0; i < len(lines); i++ {
		start := i
		line := strings.TrimSpace(lines[i])
		for strings.HasSuffix(line, `\`) && i+1 < len(lines) {
			i++
			line = strings.TrimSuffix(line, `\`) + " " + strings.TrimSpace(lines[i])
		}
		if line == "" {
			continue
		}
		if line[0] == '#' || line[0] == ';' {
			if cur != nil {
				comments = append(comments, comment{start, line})
			}
			continue
		}
		if line[0] == '[' {
			closeCur()
			if j := strings.IndexByte(line, ']'); j > 0 {
				cur = &section{name: strings.TrimSpace(line[1:j]), file: file, start: start}
				last = i
			}
			continue
		}
		if cur != nil {
			last = i
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if normKey(key) == "include" {
			includes = append(includes, value)
			if cur != nil {
				cur.hasInclude = true
			}
			continue
		}
		if cur != nil {
			cur.params = append(cur.params, Option{Key: key, Value: value})
		}
	}
	closeCur()
	return out, includes
}

// readSections parses a Samba config file and the files it includes.
// Files in skip (LocoStor's own include) are ignored.
func readSections(path string, skip map[string]bool, depth int, scan *Scan) []section {
	if depth > 8 || skip[path] {
		return nil
	}
	skip[path] = true
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) || depth == 0 {
			scan.Warnings = append(scan.Warnings, err.Error())
		}
		return nil
	}
	scan.Scanned = append(scan.Scanned, path)
	secs, includes := parseSections(string(data), path)
	for _, inc := range includes {
		if strings.Contains(inc, "%") {
			scan.Warnings = append(scan.Warnings, fmt.Sprintf("%s: include with variables (%s) is not followed", path, inc))
			continue
		}
		if !filepath.IsAbs(inc) {
			inc = filepath.Join(filepath.Dir(path), inc)
		}
		secs = append(secs, readSections(inc, skip, depth+1, scan)...)
	}
	return secs
}

// effectiveSections asks Samba for the configuration it actually uses
// (testparm), which also covers registry shares and anything the file
// parser might miss. ok is false if testparm is not available.
func (m *Manager) effectiveSections(ctx context.Context) ([]section, bool) {
	return effectiveSections(ctx, m.run, m.opts.MainConf)
}

func effectiveSections(ctx context.Context, run sysexec.Runner, mainConf string) (secs []section, ok bool) {
	out, err := run.Run(ctx, "", "testparm", "-s", "--suppress-prompt", mainConf)
	if err != nil && len(out) == 0 {
		return nil, false
	}
	secs, _ = parseSections(string(out), "")
	for _, s := range secs {
		if strings.EqualFold(s.name, "global") {
			return secs, true
		}
	}
	return nil, false // not real testparm output
}

// userShares lists shares created with "net usershare" (e.g. by desktop
// file managers). They live outside smb.conf.
func (m *Manager) userShares(ctx context.Context) []section {
	out, err := m.run.Run(ctx, "", "net", "usershare", "info")
	if err != nil {
		return nil
	}
	secs, _ := parseSections(string(out), "")
	for i := range secs {
		for j, p := range secs[i].params {
			switch p.Key {
			case "guest_ok":
				secs[i].params[j] = Option{Key: "guest ok", Value: map[string]string{"y": "yes"}[p.Value]}
			case "usershare_acl":
				secs[i].params[j].Key = "usershare acl"
			}
		}
	}
	return secs
}

func (m *Manager) externalSections() []section {
	var scan Scan
	return readSections(m.opts.MainConf, map[string]bool{m.opts.IncludePath: true}, 0, &scan)
}

// External lists shares defined outside LocoStor.
func (m *Manager) External(ctx context.Context) (Scan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	scan := Scan{Shares: []ExternalShare{}, Scanned: []string{}, Warnings: []string{}}
	fileSecs := readSections(m.opts.MainConf, map[string]bool{m.opts.IncludePath: true}, 0, &scan)
	managed, err := m.load()
	if err != nil {
		return scan, err
	}
	isManaged := map[string]bool{}
	for _, s := range managed {
		isManaged[strings.ToLower(s.Name)] = true
	}
	inFile := map[string]section{}
	for _, s := range fileSecs {
		inFile[strings.ToLower(s.name)] = s
	}

	// Samba's own view decides what exists; the files tell us where.
	type found struct {
		sec    section
		source string
	}
	var list []found
	seen := map[string]bool{}
	if eff, ok := m.effectiveSections(ctx); ok {
		scan.Scanned = append(scan.Scanned, "testparm (effective Samba configuration)")
		for _, s := range eff {
			k := strings.ToLower(s.name)
			if k == "global" || isManaged[k] || seen[k] {
				continue
			}
			seen[k] = true
			if fs, ok := inFile[k]; ok {
				list = append(list, found{fs, fs.file})
			} else {
				list = append(list, found{s, "Samba registry (net conf)"})
			}
		}
	} else {
		scan.Warnings = append(scan.Warnings, "testparm is not available - only config files were read")
		for _, s := range fileSecs {
			k := strings.ToLower(s.name)
			if k == "global" || isManaged[k] || seen[k] {
				continue
			}
			seen[k] = true
			list = append(list, found{s, s.file})
		}
	}
	if us := m.userShares(ctx); us != nil {
		scan.Scanned = append(scan.Scanned, "net usershare")
		for _, s := range us {
			k := strings.ToLower(s.name)
			if !seen[k] && !isManaged[k] {
				seen[k] = true
				list = append(list, found{s, "usershare (net usershare)"})
			}
		}
	}

	for _, f := range list {
		lower := strings.ToLower(f.sec.name)
		e := ExternalShare{Share: shareFromSection(f.sec), Source: f.source, Adoptable: true}
		switch {
		case f.sec.file == "":
			e.Adoptable, e.Reason = false, "not defined in a config file"
		case reserved[lower]:
			e.Adoptable, e.Reason = false, "special Samba section"
		case !shareNameRe.MatchString(f.sec.name):
			e.Adoptable, e.Reason = false, "name contains unsupported characters"
		case e.Path == "":
			e.Adoptable, e.Reason = false, "no path set"
		}
		if e.Adoptable {
			if err := validateOptions(&e.Share); err != nil {
				e.Adoptable, e.Reason = false, err.Error()
			} else if err := m.validate(&e.Share); err != nil {
				e.Adoptable, e.Reason = false, err.Error()
			}
		}
		scan.Shares = append(scan.Shares, e)
	}
	return scan, nil
}

// Adopt moves an external share into LocoStor: the section is removed from
// its file (a backup is kept) and recreated in LocoStor's include file.
func (m *Manager) Adopt(ctx context.Context, name string) (Share, error) {
	scan, err := m.External(ctx)
	if err != nil {
		return Share{}, err
	}
	var target *ExternalShare
	for i := range scan.Shares {
		if strings.EqualFold(scan.Shares[i].Name, name) {
			target = &scan.Shares[i]
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
	secs := m.externalSections()
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
	return fsutil.Backup(path, data, 0o644, 5)
}
