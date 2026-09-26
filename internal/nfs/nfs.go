// Package nfs manages NFS-Ganesha exports.
//
// Exports are stored in a JSON state file and rendered into a dedicated
// include file (by default /etc/ganesha/locostor.conf) referenced from
// ganesha.conf via %include.
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

// Service is the systemd unit name of NFS-Ganesha.
const Service = "nfs-ganesha"

// firstID is the lowest Export_Id LocoStor assigns, leaving room for
// exports defined manually in ganesha.conf.
const firstID = 100

// Export is one NFS export.
type Export struct {
	ID        int      `json:"id"`
	Path      string   `json:"path"`
	Pseudo    string   `json:"pseudo"`
	Clients   []string `json:"clients"`
	Access    string   `json:"access"`    // RW or RO
	Squash    string   `json:"squash"`    // root_squash, no_root_squash, all_squash
	Protocols []int    `json:"protocols"` // 3 and/or 4
	Comment   string   `json:"comment"`
	Enabled   bool     `json:"enabled"`
}

// Options configures file locations.
type Options struct {
	StatePath     string
	IncludePath   string
	MainConf      string
	SkipPathCheck bool
}

// Manager manages exports.
type Manager struct {
	run  sysexec.Runner
	opts Options
	mu   sync.Mutex
}

// New creates a Manager.
func New(run sysexec.Runner, opts Options) *Manager {
	return &Manager{run: run, opts: opts}
}

// DefaultOptions returns the standard file locations under dataDir.
func DefaultOptions(dataDir string) Options {
	return Options{
		StatePath:   filepath.Join(dataDir, "nfs-exports.json"),
		IncludePath: "/etc/ganesha/locostor.conf",
		MainConf:    "/etc/ganesha/ganesha.conf",
	}
}

var (
	clientRe  = regexp.MustCompile(`^[A-Za-z0-9.:/*_\-\[\]]{1,255}$`)
	squashMap = map[string]string{
		"root_squash":    "Root_Squash",
		"no_root_squash": "No_Root_Squash",
		"all_squash":     "All_Squash",
	}
)

// Exports returns all exports sorted by pseudo path.
func (m *Manager) Exports() ([]Export, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.load()
}

func (m *Manager) load() ([]Export, error) {
	var exports []Export
	if err := fsutil.ReadJSON(m.opts.StatePath, &exports); err != nil {
		return nil, err
	}
	if exports == nil {
		exports = []Export{}
	}
	for i := range exports {
		if exports[i].Clients == nil {
			exports[i].Clients = []string{}
		}
	}
	sort.Slice(exports, func(i, j int) bool { return exports[i].Pseudo < exports[j].Pseudo })
	return exports, nil
}

func (m *Manager) validate(e *Export) error {
	p, err := valid.AbsPath("path", e.Path)
	if err != nil {
		return err
	}
	e.Path = p
	if !m.opts.SkipPathCheck {
		fi, err := os.Stat(p)
		if err != nil || !fi.IsDir() {
			return valid.Errorf("path %s does not exist or is not a directory", p)
		}
	}
	if strings.TrimSpace(e.Pseudo) == "" {
		e.Pseudo = p
	}
	ps, err := valid.AbsPath("pseudo path", e.Pseudo)
	if err != nil {
		return err
	}
	if ps == "/" {
		return valid.Errorf("pseudo path must not be /")
	}
	e.Pseudo = ps

	clients := make([]string, 0, len(e.Clients))
	for _, c := range e.Clients {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !clientRe.MatchString(c) {
			return valid.Errorf("invalid client %q (use IP, CIDR, hostname or *)", c)
		}
		clients = append(clients, c)
	}
	e.Clients = clients

	e.Access = strings.ToUpper(e.Access)
	if e.Access == "" {
		e.Access = "RW"
	}
	if e.Access != "RW" && e.Access != "RO" {
		return valid.Errorf("access must be RW or RO")
	}
	e.Squash = strings.ToLower(e.Squash)
	if e.Squash == "" {
		e.Squash = "root_squash"
	}
	if _, ok := squashMap[e.Squash]; !ok {
		return valid.Errorf("squash must be root_squash, no_root_squash or all_squash")
	}
	if len(e.Protocols) == 0 {
		e.Protocols = []int{4}
	}
	seen := map[int]bool{}
	protos := []int{}
	for _, v := range e.Protocols {
		if v != 3 && v != 4 {
			return valid.Errorf("protocols must be 3 and/or 4")
		}
		if !seen[v] {
			seen[v] = true
			protos = append(protos, v)
		}
	}
	sort.Ints(protos)
	e.Protocols = protos
	return valid.SingleLine("comment", e.Comment)
}

// Save creates (id == 0) or updates an export and applies the config.
func (m *Manager) Save(ctx context.Context, id int, e Export) (Export, error) {
	if err := m.validate(&e); err != nil {
		return e, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	exports, err := m.load()
	if err != nil {
		return e, err
	}
	idx, maxID := -1, firstID-1
	for i, x := range exports {
		if x.ID > maxID {
			maxID = x.ID
		}
		if id != 0 && x.ID == id {
			idx = i
			continue
		}
		if x.Pseudo == e.Pseudo {
			return e, valid.Errorf("pseudo path %s is already used", e.Pseudo)
		}
	}
	next := append([]Export(nil), exports...)
	if id != 0 {
		if idx < 0 {
			return e, &valid.NotFound{What: fmt.Sprintf("export %d", id)}
		}
		e.ID = id
		next[idx] = e
	} else {
		e.ID = maxID + 1
		next = append(next, e)
	}
	return e, m.apply(ctx, next)
}

// Delete removes an export.
func (m *Manager) Delete(ctx context.Context, id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	exports, err := m.load()
	if err != nil {
		return err
	}
	next := exports[:0:0]
	for _, e := range exports {
		if e.ID != id {
			next = append(next, e)
		}
	}
	if len(next) == len(exports) {
		return &valid.NotFound{What: fmt.Sprintf("export %d", id)}
	}
	return m.apply(ctx, next)
}

// Render produces the Ganesha include file.
func Render(exports []Export) string {
	var b strings.Builder
	b.WriteString("# Managed by LocoStor. Do not edit - changes will be overwritten.\n")
	for _, e := range exports {
		if !e.Enabled {
			continue
		}
		protos := make([]string, len(e.Protocols))
		for i, p := range e.Protocols {
			protos[i] = fmt.Sprint(p)
		}
		fmt.Fprintf(&b, "\nEXPORT {\n")
		if e.Comment != "" {
			fmt.Fprintf(&b, "    # %s\n", e.Comment)
		}
		fmt.Fprintf(&b, "    Export_Id = %d;\n", e.ID)
		fmt.Fprintf(&b, "    Path = \"%s\";\n", e.Path)
		fmt.Fprintf(&b, "    Pseudo = \"%s\";\n", e.Pseudo)
		fmt.Fprintf(&b, "    Protocols = %s;\n", strings.Join(protos, ", "))
		b.WriteString("    Transports = TCP;\n")
		b.WriteString("    SecType = sys;\n")
		fmt.Fprintf(&b, "    Squash = %s;\n", squashMap[e.Squash])
		if len(e.Clients) == 0 {
			fmt.Fprintf(&b, "    Access_Type = %s;\n", e.Access)
		} else {
			b.WriteString("    Access_Type = None;\n")
			b.WriteString("    CLIENT {\n")
			fmt.Fprintf(&b, "        Clients = %s;\n", strings.Join(e.Clients, ", "))
			fmt.Fprintf(&b, "        Access_Type = %s;\n", e.Access)
			b.WriteString("    }\n")
		}
		b.WriteString("    FSAL {\n        Name = VFS;\n    }\n}\n")
	}
	return b.String()
}

func (m *Manager) apply(ctx context.Context, exports []Export) error {
	if err := m.ensureInclude(); err != nil {
		return fmt.Errorf("update %s: %w", m.opts.MainConf, err)
	}
	if err := fsutil.WriteFileAtomic(m.opts.IncludePath, []byte(Render(exports)), 0o644); err != nil {
		return err
	}
	if err := fsutil.WriteJSON(m.opts.StatePath, exports, 0o600); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, "", "systemctl", "restart", Service); err != nil {
		return fmt.Errorf("restart %s: %w", Service, err)
	}
	return nil
}

func (m *Manager) ensureInclude() error {
	data, err := os.ReadFile(m.opts.MainConf)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "%include") && strings.Contains(l, m.opts.IncludePath) {
			return nil
		}
	}
	s := string(data)
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	s += "\n# Exports managed by LocoStor\n%include \"" + m.opts.IncludePath + "\"\n"
	return fsutil.WriteFileAtomic(m.opts.MainConf, []byte(s), 0o644)
}

// Status returns the state of the NFS service.
func (m *Manager) Status(ctx context.Context) map[string]string {
	return map[string]string{Service: sysexec.ServiceState(ctx, m.run, Service)}
}
