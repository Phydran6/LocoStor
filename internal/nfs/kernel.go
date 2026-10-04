package nfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Phydran6/LocoStor/internal/fsutil"
	"github.com/Phydran6/LocoStor/internal/sysexec"
	"github.com/Phydran6/LocoStor/internal/valid"
)

// KernelExport is one export of the kernel NFS server (/etc/exports or
// /etc/exports.d/*.exports), edited in place on the Proxmox host.
type KernelExport struct {
	Key      string   `json:"key"`
	File     string   `json:"file"`
	Path     string   `json:"path"`
	Clients  []string `json:"clients"` // empty = everyone (*)
	Access   string   `json:"access"`  // RW or RO
	Squash   string   `json:"squash"`  // root_squash, no_root_squash, all_squash
	Options  []string `json:"options"` // everything else, e.g. sync, no_subtree_check, fsid=0
	Editable bool     `json:"editable"`
	Reason   string   `json:"reason,omitempty"`

	start, end int // byte range of the (logical) line
}

// KernelScan lists kernel NFS exports.
type KernelScan struct {
	Exports  []KernelExport `json:"exports"`
	Scanned  []string       `json:"scanned"`
	Warnings []string       `json:"warnings"`
}

// Kernel edits the kernel NFS server's export files without taking
// ownership of them.
type Kernel struct {
	run           sysexec.Runner
	path          string // normally /etc/exports
	skipPathCheck bool
	mu            sync.Mutex
}

// NewKernel creates an editor for path (and path.d/*.exports).
func NewKernel(run sysexec.Runner, path string, skipPathCheck bool) *Kernel {
	return &Kernel{run: run, path: path, skipPathCheck: skipPathCheck}
}

// Installed reports whether the kernel NFS server tools are present.
func (k *Kernel) Installed() bool {
	_, err := os.Stat(k.path)
	return err == nil
}

var (
	kernelOptRe    = regexp.MustCompile(`^[a-z_]+(=[A-Za-z0-9_.:/@,-]+)?$`)
	kernelClientRe = regexp.MustCompile(`^[A-Za-z0-9.:/*?_@\-\[\]]{1,255}$`)
	accessOpts     = map[string]bool{"rw": true, "ro": true}
	squashOpts     = map[string]bool{"root_squash": true, "no_root_squash": true, "all_squash": true, "no_all_squash": true}
)

func (k *Kernel) files() []string {
	files := []string{k.path}
	more, _ := filepath.Glob(k.path + ".d/*.exports")
	sort.Strings(more)
	return append(files, more...)
}

// List returns all kernel exports.
func (k *Kernel) List() KernelScan {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.list()
}

func (k *Kernel) list() KernelScan {
	scan := KernelScan{Exports: []KernelExport{}, Scanned: []string{}, Warnings: []string{}}
	for _, f := range k.files() {
		data, err := os.ReadFile(f)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				scan.Warnings = append(scan.Warnings, err.Error())
			}
			continue
		}
		scan.Scanned = append(scan.Scanned, f)
		for _, l := range logicalLines(string(data)) {
			e := parseKernelLine(l.text)
			e.File, e.start, e.end = f, l.start, l.end
			e.Key = f + "|" + e.Path
			scan.Exports = append(scan.Exports, e)
		}
	}
	return scan
}

type logicalLine struct {
	text       string
	start, end int // end = index of the terminating '\n' (or len)
}

// logicalLines joins continuation lines and drops comments and blank lines.
func logicalLines(src string) []logicalLine {
	var out []logicalLine
	var b strings.Builder
	start := 0
	for off := 0; off < len(src); {
		n := strings.IndexByte(src[off:], '\n')
		if n < 0 {
			n = len(src) - off
		}
		lineEnd := off + n
		line := strings.TrimRight(src[off:lineEnd], " \t\r")
		off = lineEnd + 1
		if strings.HasSuffix(line, `\`) && off < len(src) {
			b.WriteString(strings.TrimSuffix(line, `\`) + " ")
			continue
		}
		b.WriteString(line)
		text := b.String()
		b.Reset()
		ls := start
		start = off
		if i := strings.IndexByte(text, '#'); i >= 0 {
			text = text[:i]
		}
		if strings.TrimSpace(text) != "" {
			out = append(out, logicalLine{text, ls, lineEnd})
		}
	}
	return out
}

// splitExportFields splits a line, keeping a quoted path together.
func splitExportFields(s string) []string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, `"`) {
		if j := strings.IndexByte(s[1:], '"'); j >= 0 {
			return append([]string{s[1 : j+1]}, strings.Fields(s[j+2:])...)
		}
	}
	return strings.Fields(s)
}

func parseKernelLine(text string) KernelExport {
	f := splitExportFields(text)
	e := KernelExport{Path: f[0], Clients: []string{}, Options: []string{}, Editable: true}
	defaults := ""
	type client struct{ host, opts string }
	var clients []client
	for _, tok := range f[1:] {
		if strings.HasPrefix(tok, "-") {
			defaults = strings.TrimPrefix(tok, "-")
			continue
		}
		host, opts := tok, defaults
		if i := strings.IndexByte(tok, '('); i >= 0 {
			host, opts = tok[:i], strings.TrimSuffix(tok[i+1:], ")")
		}
		if host == "" {
			host = "*"
		}
		clients = append(clients, client{host, opts})
	}
	if len(clients) == 0 {
		clients = []client{{"*", defaults}}
	}
	for i, c := range clients {
		if i > 0 && c.opts != clients[0].opts {
			e.Editable, e.Reason = false, "clients have different options"
		}
		if c.host != "*" {
			e.Clients = append(e.Clients, c.host)
		}
	}
	e.Access, e.Squash = "RO", "root_squash"
	for _, o := range strings.Split(clients[0].opts, ",") {
		switch {
		case o == "":
		case o == "rw":
			e.Access = "RW"
		case o == "ro":
			e.Access = "RO"
		case o == "no_root_squash" || o == "all_squash" || o == "root_squash":
			e.Squash = o
		case o == "no_all_squash":
		default:
			e.Options = append(e.Options, o)
		}
	}
	if e.Editable {
		if _, err := valid.SharePath("path", e.Path, false); err != nil {
			e.Editable, e.Reason = false, err.Error()
		}
	}
	return e
}

func validateKernel(e *KernelExport, mustExist bool) error {
	p, err := valid.SharePath("path", e.Path, mustExist)
	if err != nil {
		return err
	}
	e.Path = p
	clients := []string{}
	for _, c := range e.Clients {
		c = strings.TrimSpace(c)
		if c == "" || c == "*" {
			continue
		}
		if !kernelClientRe.MatchString(c) {
			return valid.Errorf("invalid client %q (use IP, CIDR, hostname, @netgroup or *)", c)
		}
		clients = append(clients, c)
	}
	e.Clients = clients
	e.Access = strings.ToUpper(e.Access)
	if e.Access != "RW" && e.Access != "RO" {
		return valid.Errorf("access must be RW or RO")
	}
	if e.Squash == "" {
		e.Squash = "root_squash"
	}
	if e.Squash != "root_squash" && e.Squash != "no_root_squash" && e.Squash != "all_squash" {
		return valid.Errorf("squash must be root_squash, no_root_squash or all_squash")
	}
	opts := []string{}
	seen := map[string]bool{}
	for _, o := range e.Options {
		o = strings.TrimSpace(o)
		if o == "" || seen[o] {
			continue
		}
		if !kernelOptRe.MatchString(o) || accessOpts[o] || squashOpts[o] {
			return valid.Errorf("invalid or duplicate export option %q", o)
		}
		seen[o] = true
		opts = append(opts, o)
	}
	e.Options = opts
	return nil
}

func renderKernel(e KernelExport) string {
	path := e.Path
	if strings.ContainsAny(path, " \t") {
		path = `"` + path + `"`
	}
	opts := []string{strings.ToLower(e.Access)}
	opts = append(opts, e.Options...)
	if e.Squash != "root_squash" {
		opts = append(opts, e.Squash)
	}
	o := "(" + strings.Join(opts, ",") + ")"
	clients := e.Clients
	if len(clients) == 0 {
		clients = []string{"*"}
	}
	parts := []string{path}
	for _, c := range clients {
		parts = append(parts, c+o)
	}
	return strings.Join(parts, " ")
}

// Save creates (key == "") or changes an export.
func (k *Kernel) Save(ctx context.Context, key string, e KernelExport) (KernelExport, error) {
	if err := validateKernel(&e, !k.skipPathCheck); err != nil {
		return e, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	scan := k.list()
	var target *KernelExport
	for i := range scan.Exports {
		x := &scan.Exports[i]
		if key != "" && x.Key == key {
			target = x
			continue
		}
		if x.Path == e.Path {
			return e, valid.Errorf("%s is already exported in %s", e.Path, x.File)
		}
	}
	line := renderKernel(e)
	if key == "" {
		data, err := os.ReadFile(k.path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return e, err
		}
		text := string(data)
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		e.File, e.Key = k.path, k.path+"|"+e.Path
		return e, k.write(ctx, k.path, data, text+line+"\n")
	}
	if target == nil {
		return e, &valid.NotFound{What: "export"}
	}
	if !target.Editable {
		return e, valid.Errorf("%s cannot be edited here: %s", target.Path, target.Reason)
	}
	data, err := os.ReadFile(target.File)
	if err != nil {
		return e, err
	}
	e.File, e.Key = target.File, target.File+"|"+e.Path
	next := string(data[:target.start]) + line + string(data[target.end:])
	return e, k.write(ctx, target.File, data, next)
}

// Delete removes an export.
func (k *Kernel) Delete(ctx context.Context, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, x := range k.list().Exports {
		if x.Key != key {
			continue
		}
		data, err := os.ReadFile(x.File)
		if err != nil {
			return err
		}
		end := x.end
		if end < len(data) && data[end] == '\n' {
			end++
		}
		return k.write(ctx, x.File, data, string(data[:x.start])+string(data[end:]))
	}
	return &valid.NotFound{What: "export"}
}

// write keeps a backup, writes the file and re-exports. If exportfs fails,
// the old file is restored.
func (k *Kernel) write(ctx context.Context, file string, original []byte, content string) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(file); err == nil {
		mode = fi.Mode().Perm()
	}
	if original != nil {
		if err := fsutil.Backup(file, original, mode, 5); err != nil {
			return fmt.Errorf("backup %s: %w", file, err)
		}
	}
	if err := fsutil.WriteFileAtomic(file, []byte(content), mode); err != nil {
		return err
	}
	if _, err := k.run.Run(ctx, "", "exportfs", "-ra"); err != nil {
		if original != nil {
			_ = fsutil.WriteFileAtomic(file, original, mode)
		} else {
			_ = os.Remove(file)
		}
		_, _ = k.run.Run(ctx, "", "exportfs", "-ra")
		return valid.Errorf("the NFS server rejected the change: %v", err)
	}
	return nil
}

// Status returns the state of the kernel NFS server.
func (k *Kernel) Status(ctx context.Context) string {
	return sysexec.ServiceState(ctx, k.run, "nfs-server")
}
