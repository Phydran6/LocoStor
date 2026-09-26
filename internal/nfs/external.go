package nfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Phydran6/LocoStor/internal/fsutil"
	"github.com/Phydran6/LocoStor/internal/valid"
)

// ExternalExport is an export defined outside LocoStor: an EXPORT block in
// ganesha.conf or a line in /etc/exports (kernel NFS server).
type ExternalExport struct {
	Export
	Source    string `json:"source"`
	Key       string `json:"key"` // identifies the export for Adopt
	Adoptable bool   `json:"adoptable"`
	Reason    string `json:"reason,omitempty"`

	file       string
	start, end int // byte range in file
}

// --- NFS-Ganesha config parser ---------------------------------------------

type gBlock struct {
	name       string
	params     map[string]string // lower-case key -> value (list items joined by ",")
	children   []gBlock
	start, end int
}

type gParser struct {
	src      string
	pos      int
	includes []string
	dirs     []string // %dir: include every *.conf in the directory
}

func (p *gParser) skipSpace() {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == '#':
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			p.pos++
		default:
			return
		}
	}
}

func (p *gParser) ident() string {
	start := p.pos
	for p.pos < len(p.src) && !strings.ContainsRune(" \t\r\n{};=,\"#", rune(p.src[p.pos])) {
		p.pos++
	}
	return p.src[start:p.pos]
}

// value reads a value list up to ';' and returns the items joined by ",".
func (p *gParser) value() (string, error) {
	var items []string
	var cur strings.Builder
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch c {
		case '"':
			end := strings.IndexByte(p.src[p.pos+1:], '"')
			if end < 0 {
				return "", errors.New("unterminated string")
			}
			cur.WriteString(p.src[p.pos+1 : p.pos+1+end])
			p.pos += end + 2
		case ',':
			items = append(items, strings.TrimSpace(cur.String()))
			cur.Reset()
			p.pos++
		case ';':
			p.pos++
			items = append(items, strings.TrimSpace(cur.String()))
			return strings.Join(items, ","), nil
		case '}':
			// Tolerate a missing ';' before the closing brace.
			items = append(items, strings.TrimSpace(cur.String()))
			return strings.Join(items, ","), nil
		case '{':
			return "", fmt.Errorf("unexpected '{' at offset %d", p.pos)
		case '#':
			p.skipSpace()
		default:
			cur.WriteByte(c)
			p.pos++
		}
	}
	return "", errors.New("unexpected end of file")
}

// blocks parses statements until '}' (inner) or end of input.
func (p *gParser) blocks(inner bool) ([]gBlock, map[string]string, error) {
	var blocks []gBlock
	params := map[string]string{}
	for {
		p.skipSpace()
		if p.pos >= len(p.src) {
			if inner {
				return nil, nil, errors.New("missing '}'")
			}
			return blocks, params, nil
		}
		if p.src[p.pos] == '}' {
			if !inner {
				return nil, nil, errors.New("unexpected '}'")
			}
			p.pos++
			return blocks, params, nil
		}
		if p.src[p.pos] == '%' {
			end := strings.IndexByte(p.src[p.pos:], '\n')
			if end < 0 {
				end = len(p.src) - p.pos
			}
			directive := strings.Fields(p.src[p.pos : p.pos+end])
			if len(directive) == 2 {
				switch directive[0] {
				case "%include":
					p.includes = append(p.includes, strings.Trim(directive[1], `"`))
				case "%dir":
					p.dirs = append(p.dirs, strings.Trim(directive[1], `"`))
				}
			}
			p.pos += end
			continue
		}
		start := p.pos
		name := p.ident()
		if name == "" {
			return nil, nil, fmt.Errorf("unexpected %q at offset %d", p.src[p.pos], p.pos)
		}
		p.skipSpace()
		if p.pos >= len(p.src) {
			return nil, nil, errors.New("unexpected end of file")
		}
		switch p.src[p.pos] {
		case '{':
			p.pos++
			children, bp, err := p.blocks(true)
			if err != nil {
				return nil, nil, err
			}
			blocks = append(blocks, gBlock{name: name, params: bp, children: children, start: start, end: p.pos})
		case '=':
			p.pos++
			v, err := p.value()
			if err != nil {
				return nil, nil, err
			}
			params[strings.ToLower(name)] = v
		default:
			return nil, nil, fmt.Errorf("expected '{' or '=' after %q", name)
		}
	}
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func normSquash(v string) (string, bool) {
	switch strings.ToLower(strings.ReplaceAll(v, "_", "")) {
	case "rootsquash", "root":
		return "root_squash", true
	case "norootsquash", "none", "noidsquash":
		return "no_root_squash", true
	case "allsquash", "all", "allanonymous":
		return "all_squash", true
	}
	return "", false
}

func normProtocols(v string) ([]int, bool) {
	var out []int
	for _, p := range splitList(v) {
		switch strings.ToLower(strings.TrimPrefix(strings.ToLower(p), "nfs")) {
		case "3", "v3":
			out = append(out, 3)
		case "4", "v4":
			out = append(out, 4)
		default:
			return nil, false
		}
	}
	return out, len(out) > 0
}

// exportFromBlock maps a Ganesha EXPORT block. Unknown settings make it
// non-adoptable, so nothing is silently dropped.
func exportFromBlock(b gBlock, defaultProtocols []int) (Export, string) {
	e := Export{Clients: []string{}, Access: "RW", Squash: "root_squash", Protocols: defaultProtocols, Enabled: true}
	var unsupported []string
	for k, v := range b.params {
		switch k {
		case "export_id":
			e.ID, _ = strconv.Atoi(v)
		case "path":
			e.Path = v
		case "pseudo":
			e.Pseudo = v
		case "access_type":
			e.Access = strings.ToUpper(v)
		case "squash":
			s, ok := normSquash(v)
			if !ok {
				unsupported = append(unsupported, "Squash = "+v)
			}
			e.Squash = s
		case "protocols":
			p, ok := normProtocols(v)
			if !ok {
				unsupported = append(unsupported, "Protocols = "+v)
			}
			e.Protocols = p
		case "transports", "tag":
		case "sectype":
			if strings.ToLower(v) != "sys" {
				unsupported = append(unsupported, "SecType = "+v)
			}
		default:
			unsupported = append(unsupported, k)
		}
	}
	clientAccess := ""
	for _, c := range b.children {
		switch strings.ToUpper(c.name) {
		case "FSAL":
			if strings.ToUpper(c.params["name"]) != "VFS" {
				unsupported = append(unsupported, "FSAL "+c.params["name"])
			}
		case "CLIENT":
			access := strings.ToUpper(c.params["access_type"])
			if access == "" {
				access = e.Access
			}
			if clientAccess != "" && access != clientAccess {
				unsupported = append(unsupported, "CLIENT blocks with different access")
			}
			clientAccess = access
			e.Clients = append(e.Clients, splitList(c.params["clients"])...)
			for k := range c.params {
				if k != "clients" && k != "access_type" {
					unsupported = append(unsupported, "CLIENT "+k)
				}
			}
		default:
			unsupported = append(unsupported, c.name+" block")
		}
	}
	if clientAccess != "" {
		e.Access = clientAccess
	}
	if e.Access != "RW" && e.Access != "RO" {
		unsupported = append(unsupported, "Access_Type = "+e.Access)
	}
	if len(unsupported) > 0 {
		sort.Strings(unsupported)
		return e, "unsupported settings: " + strings.Join(unsupported, ", ")
	}
	return e, ""
}

// Scan is the result of looking for exports defined outside LocoStor.
type Scan struct {
	Exports  []ExternalExport `json:"exports"`
	Scanned  []string         `json:"scanned"`
	Warnings []string         `json:"warnings"`
}

// topLevelBlocks is the fallback when a file does not parse as a whole: it
// finds each top-level "NAME { ... }" by brace matching (ignoring comments
// and strings) and parses the blocks one by one.
func topLevelBlocks(src string) (blocks []gBlock, broken []string) {
	depth, start, nameStart := 0, -1, -1
	for i := 0; i < len(src); i++ {
		switch c := src[i]; c {
		case '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case '"':
			if j := strings.IndexByte(src[i+1:], '"'); j >= 0 {
				i += j + 1
			}
		case '{':
			if depth == 0 {
				// The block name is the word before the brace.
				k := i - 1
				for k >= 0 && (src[k] == ' ' || src[k] == '\t' || src[k] == '\r' || src[k] == '\n') {
					k--
				}
				nameStart = k
				for nameStart >= 0 && !strings.ContainsRune(" \t\r\n;{}", rune(src[nameStart])) {
					nameStart--
				}
				start = nameStart + 1
			}
			depth++
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				p := &gParser{src: src[:i+1], pos: start}
				bs, _, err := p.blocks(false)
				if err != nil || len(bs) != 1 {
					broken = append(broken, fmt.Sprintf("block at offset %d: %v", start, err))
				} else {
					blocks = append(blocks, bs[0])
				}
				start = -1
			}
		}
	}
	return blocks, broken
}

func (m *Manager) ganeshaExternal(path string, depth int, seen map[string]bool, scan *Scan) []ExternalExport {
	if depth > 8 || seen[path] {
		return nil
	}
	seen[path] = true
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) || depth == 0 {
			scan.Warnings = append(scan.Warnings, err.Error())
		}
		return nil
	}
	scan.Scanned = append(scan.Scanned, path)
	p := &gParser{src: string(data)}
	blocks, _, err := p.blocks(false)
	if err != nil {
		var broken []string
		blocks, broken = topLevelBlocks(string(data))
		scan.Warnings = append(scan.Warnings, fmt.Sprintf("%s: %v - read block by block instead", path, err))
		for _, b := range broken {
			scan.Warnings = append(scan.Warnings, path+": could not read "+b)
		}
		// Recover %include / %dir lines too.
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && f[0] == "%include" {
				p.includes = append(p.includes, strings.Trim(f[1], `"`))
			} else if len(f) == 2 && f[0] == "%dir" {
				p.dirs = append(p.dirs, strings.Trim(f[1], `"`))
			}
		}
	}
	var out []ExternalExport
	// Exports without Protocols inherit the server-wide setting.
	protocols := []int{3, 4}
	for _, b := range blocks {
		if strings.EqualFold(b.name, "NFS_CORE_PARAM") {
			if p, ok := normProtocols(b.params["protocols"]); ok {
				protocols = p
			}
		}
	}
	for _, b := range blocks {
		if !strings.EqualFold(b.name, "EXPORT") {
			continue
		}
		e, reason := exportFromBlock(b, protocols)
		out = append(out, ExternalExport{
			Export: e, Source: path, Key: fmt.Sprintf("ganesha:%s:%d", path, b.start),
			Adoptable: reason == "", Reason: reason, file: path, start: b.start, end: b.end,
		})
	}
	rel := func(f string) string {
		if filepath.IsAbs(f) {
			return f
		}
		return filepath.Join(filepath.Dir(path), f)
	}
	for _, inc := range p.includes {
		out = append(out, m.ganeshaExternal(rel(inc), depth+1, seen, scan)...)
	}
	for _, dir := range p.dirs {
		files, _ := filepath.Glob(filepath.Join(rel(dir), "*.conf"))
		sort.Strings(files)
		for _, f := range files {
			out = append(out, m.ganeshaExternal(f, depth+1, seen, scan)...)
		}
	}
	return out
}

// --- /etc/exports parser ----------------------------------------------------

func (m *Manager) kernelExternal(path string, scan *Scan) []ExternalExport {
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			scan.Warnings = append(scan.Warnings, err.Error())
		}
		return nil
	}
	scan.Scanned = append(scan.Scanned, path)
	src := string(data)
	var out []ExternalExport
	start := 0 // byte offset of the current logical line
	var logical strings.Builder
	for off := 0; off < len(src); {
		end := strings.IndexByte(src[off:], '\n')
		if end < 0 {
			end = len(src) - off
		}
		lineEnd := off + end // index of '\n' (or len(src))
		line := strings.TrimRight(src[off:lineEnd], " \t\r")
		off = lineEnd + 1
		if strings.HasSuffix(line, `\`) && off < len(src) {
			logical.WriteString(strings.TrimSuffix(line, `\`) + " ")
			continue
		}
		logical.WriteString(line)
		text := logical.String()
		logical.Reset()
		lineStart := start
		start = off
		if i := strings.IndexByte(text, '#'); i >= 0 {
			text = text[:i]
		}
		fields := strings.Fields(text)
		if len(fields) == 0 {
			continue
		}
		e, reason := exportFromKernelLine(fields)
		out = append(out, ExternalExport{
			Export: e, Source: path + " (kernel NFS)", Key: fmt.Sprintf("exports:%s:%d", path, lineStart),
			Adoptable: reason == "", Reason: reason, file: path, start: lineStart, end: lineEnd,
		})
	}
	return out
}

func exportFromKernelLine(fields []string) (Export, string) {
	e := Export{Path: strings.Trim(fields[0], `"`), Clients: []string{}, Protocols: []int{3, 4}, Enabled: true}
	e.Pseudo = e.Path
	access, squash := "", ""
	defaults := ""
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "-") {
			defaults = strings.TrimPrefix(f, "-")
			continue
		}
		host, opts := f, defaults
		if i := strings.IndexByte(f, '('); i >= 0 {
			host, opts = f[:i], strings.TrimSuffix(f[i+1:], ")")
		}
		if host == "" {
			host = "*"
		}
		a, s := "RO", "root_squash"
		for _, o := range strings.Split(opts, ",") {
			switch o {
			case "rw":
				a = "RW"
			case "ro":
				a = "RO"
			case "no_root_squash":
				s = "no_root_squash"
			case "all_squash":
				s = "all_squash"
			}
		}
		if access != "" && (a != access || s != squash) {
			return e, "clients have different options"
		}
		access, squash = a, s
		if host != "*" {
			e.Clients = append(e.Clients, host)
		}
	}
	if access == "" {
		access, squash = "RO", "root_squash"
	}
	e.Access, e.Squash = access, squash
	return e, ""
}

// --- listing and adoption -----------------------------------------------------

func (m *Manager) external() (Scan, error) {
	scan := Scan{Exports: []ExternalExport{}, Scanned: []string{}, Warnings: []string{}}
	out := m.ganeshaExternal(m.opts.MainConf, 0, map[string]bool{m.opts.IncludePath: true}, &scan)
	if m.opts.ExportsPath != "" {
		out = append(out, m.kernelExternal(m.opts.ExportsPath, &scan)...)
		files, _ := filepath.Glob(m.opts.ExportsPath + ".d/*.exports")
		sort.Strings(files)
		for _, f := range files {
			out = append(out, m.kernelExternal(f, &scan)...)
		}
	}
	managed, err := m.load()
	if err != nil {
		return scan, err
	}
	for i := range out {
		e := &out[i]
		if e.Pseudo == "" {
			e.Pseudo = e.Path
		}
		for _, x := range managed {
			if e.Adoptable && x.Pseudo == e.Pseudo {
				e.Adoptable, e.Reason = false, "a LocoStor export uses the same pseudo path"
			}
		}
		if e.Adoptable {
			tmp := e.Export
			if err := m.validate(&tmp); err != nil {
				e.Adoptable, e.Reason = false, err.Error()
			}
		}
	}
	if out != nil {
		scan.Exports = out
	}
	return scan, nil
}

// External lists exports defined outside LocoStor.
func (m *Manager) External() (Scan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.external()
}

// Adopt moves an external export into LocoStor. The original definition is
// removed from its file (a timestamped backup is kept).
func (m *Manager) Adopt(ctx context.Context, key string) (Export, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	scan, err := m.external()
	if err != nil {
		return Export{}, err
	}
	var target *ExternalExport
	for i := range scan.Exports {
		if scan.Exports[i].Key == key {
			target = &scan.Exports[i]
		}
	}
	if target == nil {
		return Export{}, &valid.NotFound{What: "export"}
	}
	if !target.Adoptable {
		return Export{}, valid.Errorf("cannot take over %s: %s", target.Pseudo, target.Reason)
	}
	e := target.Export
	if err := m.validate(&e); err != nil {
		return e, err
	}
	managed, err := m.load()
	if err != nil {
		return e, err
	}
	maxID := firstID - 1
	idUsed := false
	for _, x := range managed {
		if x.ID > maxID {
			maxID = x.ID
		}
		if x.ID == e.ID {
			idUsed = true
		}
	}
	if e.ID <= 0 || idUsed {
		e.ID = maxID + 1
	}

	original, err := os.ReadFile(target.file)
	if err != nil {
		return e, err
	}
	name := target.file + ".locostor-" + time.Now().Format("20060102-150405")
	if err := fsutil.WriteFileAtomic(name, original, 0o644); err != nil {
		return e, err
	}
	end := target.end
	if end < len(original) && original[end] == '\n' {
		end++
	}
	rest := append(append([]byte{}, original[:target.start]...), original[end:]...)
	if err := fsutil.WriteFileAtomic(target.file, rest, 0o644); err != nil {
		return e, err
	}
	if err := m.apply(ctx, append(managed, e)); err != nil {
		_ = fsutil.WriteFileAtomic(target.file, original, 0o644)
		return e, err
	}
	if strings.HasPrefix(target.Key, "exports:") {
		_, _ = m.run.Run(ctx, "", "exportfs", "-ra")
	}
	return e, nil
}
