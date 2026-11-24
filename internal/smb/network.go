package smb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/Phydran6/LocoStor/internal/fsutil"
	"github.com/Phydran6/LocoStor/internal/sysexec"
	"github.com/Phydran6/LocoStor/internal/valid"
)

// Discovery modes: whether the server announces itself in the network
// neighbourhood (NetBIOS via nmbd, WS-Discovery via wsdd).
const (
	DiscoveryAuto = "auto" // only while at least one share is offered
	DiscoveryOn   = "on"
	DiscoveryOff  = "off"
)

// NetworkSettings are the [global] settings that decide how the server
// shows up in the network.
type NetworkSettings struct {
	// NetbiosName is the server name in the network; empty = hostname.
	NetbiosName  string `json:"netbios_name"`
	Workgroup    string `json:"workgroup"`
	ServerString string `json:"server_string"`
	Discovery    string `json:"discovery"`
}

// NetworkInfo is the current state of the server in the network.
type NetworkInfo struct {
	NetworkSettings
	Installed bool              `json:"installed"`
	Hostname  string            `json:"hostname"`
	Name      string            `json:"name"`    // the name clients see
	Visible   bool              `json:"visible"` // announced right now
	Serving   bool              `json:"serving"` // smbd runs
	Shares    []string          `json:"shares"`  // everything Samba offers
	Printers  bool              `json:"printers"`
	Services  map[string]string `json:"services"`
	WSDD      bool              `json:"wsdd"` // WS-Discovery daemon installed
	Security  string            `json:"security"`
	// ManagesSmbd: Samba is stopped completely while nothing is shared
	// (inside the container; the host's Samba is never stopped).
	ManagesSmbd bool `json:"manages_smbd"`
}

// Network manages the global Samba settings of one server.
type Network struct {
	run         sysexec.Runner
	mainConf    string
	statePath   string
	defaultMode string
	manageSmbd  bool
	// WSDDDefaults is the wsdd environment file (/etc/default/wsdd).
	WSDDDefaults string
	mu           sync.Mutex
}

// NewNetwork creates a manager for the Samba config at mainConf. The
// discovery mode is stored in statePath. With manageSmbd, smbd itself is
// stopped while no share is offered.
func NewNetwork(run sysexec.Runner, mainConf, statePath, defaultMode string, manageSmbd bool) *Network {
	return &Network{run: run, mainConf: mainConf, statePath: statePath, defaultMode: defaultMode, manageSmbd: manageSmbd, WSDDDefaults: "/etc/default/wsdd"}
}

var (
	netbiosRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,14}$`)
	workgroupRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,14}$`)
)

type networkState struct {
	Discovery string `json:"discovery"`
}

func (n *Network) mode() string {
	var st networkState
	_ = fsutil.ReadJSON(n.statePath, &st)
	switch st.Discovery {
	case DiscoveryAuto, DiscoveryOn, DiscoveryOff:
		return st.Discovery
	}
	return n.defaultMode
}

func globalParams(secs []section) map[string]string {
	out := map[string]string{}
	for _, s := range secs {
		if strings.EqualFold(s.name, "global") {
			for _, p := range s.params {
				out[normKey(p.Key)] = p.Value
			}
		}
	}
	return out
}

// shortHost is the hostname as Samba derives its default NetBIOS name.
func shortHost() string {
	h, _ := os.Hostname()
	h, _, _ = strings.Cut(h, ".")
	return h
}

// offered returns the shares Samba serves (testparm, else the files) and
// whether printer sections exist.
func (n *Network) offered(ctx context.Context) (shares []string, printers bool) {
	secs, ok := effectiveSections(ctx, n.run, n.mainConf)
	if !ok {
		var scan Scan
		secs = readSections(n.mainConf, map[string]bool{}, 0, &scan)
	}
	shares = []string{}
	for _, s := range secs {
		lower := strings.ToLower(s.name)
		if lower == "global" {
			continue
		}
		params := map[string]string{}
		for _, p := range s.params {
			params[normKey(p.Key)] = p.Value
		}
		if lower == "printers" || lower == "print$" || parseBool(params["printable"], false) || parseBool(params["printok"], false) {
			printers = true
			continue
		}
		if !parseBool(params["available"], true) {
			continue
		}
		shares = append(shares, s.name)
	}
	return shares, printers
}

func (n *Network) unitExists(ctx context.Context, unit string) bool {
	out, _ := n.run.Run(ctx, "", "systemctl", "list-unit-files", unit+".service")
	return strings.Contains(string(out), unit+".service")
}

// Info returns the current network settings and state.
func (n *Network) Info(ctx context.Context) NetworkInfo {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.info(ctx)
}

func (n *Network) info(ctx context.Context) NetworkInfo {
	info := NetworkInfo{Hostname: shortHost(), Services: map[string]string{}, Shares: []string{}, ManagesSmbd: n.manageSmbd}
	if _, err := os.Stat(n.mainConf); err != nil {
		return info
	}
	info.Installed = true
	var scan Scan
	g := globalParams(readSections(n.mainConf, map[string]bool{}, 0, &scan))
	info.NetbiosName = g["netbiosname"]
	info.Workgroup = g["workgroup"]
	if info.Workgroup == "" {
		info.Workgroup = "WORKGROUP"
	}
	info.ServerString = g["serverstring"]
	info.Security = strings.ToLower(g["security"])
	if info.Security == "" {
		info.Security = "user"
	}
	info.Discovery = n.mode()
	info.Name = strings.ToUpper(info.NetbiosName)
	if info.Name == "" {
		info.Name = strings.ToUpper(info.Hostname)
		if len(info.Name) > 15 {
			info.Name = info.Name[:15]
		}
	}
	info.Shares, info.Printers = n.offered(ctx)
	for _, unit := range []string{"smbd", "nmbd", "wsdd", "wsdd2"} {
		if unit == "smbd" || n.unitExists(ctx, unit) {
			info.Services[unit] = sysexec.ServiceState(ctx, n.run, unit)
		}
	}
	_, info.WSDD = info.Services["wsdd"]
	if _, ok := info.Services["wsdd2"]; ok {
		info.WSDD = true
	}
	info.Serving = info.Services["smbd"] == "active"
	info.Visible = info.Serving && (info.Services["nmbd"] == "active" || info.Services["wsdd"] == "active" || info.Services["wsdd2"] == "active")
	return info
}

// Save changes the network settings. The name and workgroup only take
// effect after Samba restarts, which drops open connections briefly.
func (n *Network) Save(ctx context.Context, s NetworkSettings) (NetworkInfo, error) {
	s.NetbiosName = strings.TrimSpace(s.NetbiosName)
	s.Workgroup = strings.ToUpper(strings.TrimSpace(s.Workgroup))
	s.ServerString = strings.TrimSpace(s.ServerString)
	if s.NetbiosName != "" && !netbiosRe.MatchString(s.NetbiosName) {
		return NetworkInfo{}, valid.Errorf("the server name may have up to 15 letters, digits and '-'")
	}
	if s.Workgroup == "" {
		s.Workgroup = "WORKGROUP"
	}
	if !workgroupRe.MatchString(s.Workgroup) {
		return NetworkInfo{}, valid.Errorf("the workgroup may have up to 15 letters, digits, '.', '_' and '-'")
	}
	if err := valid.SingleLine("description", s.ServerString); err != nil {
		return NetworkInfo{}, err
	}
	switch s.Discovery {
	case DiscoveryAuto, DiscoveryOn, DiscoveryOff:
	default:
		return NetworkInfo{}, valid.Errorf("discovery must be auto, on or off")
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	before := n.info(ctx)
	if !before.Installed {
		return before, valid.Errorf("Samba is not installed on this server")
	}
	if before.Security == "ads" && !strings.EqualFold(s.Workgroup, before.Workgroup) {
		return before, valid.Errorf("the workgroup is the domain's NetBIOS name while joined to Active Directory - leave the domain first")
	}
	set := []Option{{"workgroup", s.Workgroup}}
	var remove []string
	if s.NetbiosName != "" {
		set = append(set, Option{"netbios name", s.NetbiosName})
	} else {
		remove = append(remove, "netbios name")
	}
	if s.ServerString != "" {
		set = append(set, Option{"server string", s.ServerString})
	} else {
		remove = append(remove, "server string")
	}
	if err := EditGlobal(ctx, n.run, n.mainConf, set, remove); err != nil {
		return before, err
	}
	if err := fsutil.WriteJSON(n.statePath, networkState{Discovery: s.Discovery}, 0o600); err != nil {
		return before, err
	}
	renamed := !strings.EqualFold(before.NetbiosName, s.NetbiosName) || !strings.EqualFold(before.Workgroup, s.Workgroup)
	if renamed && before.Serving {
		_, _ = n.run.Run(ctx, "", "systemctl", "restart", "smbd")
		if before.Services["nmbd"] == "active" {
			_, _ = n.run.Run(ctx, "", "systemctl", "restart", "nmbd")
		}
	}
	n.reconcile(ctx, renamed)
	return n.info(ctx), nil
}

// Reconcile starts or stops Samba and its announcements to match the
// discovery mode and what is shared. Call it after shares changed.
func (n *Network) Reconcile(ctx context.Context) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.reconcile(ctx, false)
}

func (n *Network) setUnit(ctx context.Context, unit string, on bool) {
	active := sysexec.ServiceState(ctx, n.run, unit) == "active"
	if on && !active {
		_, _ = n.run.Run(ctx, "", "systemctl", "enable", "--now", unit)
	} else if !on && active {
		_, _ = n.run.Run(ctx, "", "systemctl", "disable", "--now", unit)
	}
}

func (n *Network) reconcile(ctx context.Context, restartWSDD bool) {
	if _, err := os.Stat(n.mainConf); err != nil {
		return
	}
	// The Proxmox host stays as it is until its settings are saved once.
	if _, err := os.Stat(n.statePath); err != nil && !n.manageSmbd {
		return
	}
	mode := n.mode()
	shares, _ := n.offered(ctx)
	visible := mode == DiscoveryOn || (mode == DiscoveryAuto && len(shares) > 0)
	if n.manageSmbd {
		n.setUnit(ctx, "smbd", len(shares) > 0 || mode == DiscoveryOn)
	}
	if n.unitExists(ctx, "nmbd") {
		n.setUnit(ctx, "nmbd", visible)
	}
	for _, unit := range []string{"wsdd", "wsdd2"} {
		if !n.unitExists(ctx, unit) {
			continue
		}
		if unit == "wsdd" && n.writeWSDDDefaults() && visible && restartWSDD {
			_, _ = n.run.Run(ctx, "", "systemctl", "restart", unit)
		}
		n.setUnit(ctx, unit, visible)
	}
}

// writeWSDDDefaults makes wsdd announce the Samba name and workgroup
// instead of the hostname. It reports whether the file changed.
func (n *Network) writeWSDDDefaults() bool {
	var scan Scan
	g := globalParams(readSections(n.mainConf, map[string]bool{}, 0, &scan))
	params := ""
	if v := g["netbiosname"]; v != "" {
		params += "--hostname " + v + " "
	}
	if v := g["workgroup"]; v != "" {
		params += "--workgroup " + v
	}
	content := "# Written by LocoStor: announce the Samba name and workgroup.\nWSDD_PARAMS=\"" + strings.TrimSpace(params) + "\"\n"
	old, err := os.ReadFile(n.WSDDDefaults)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false
	}
	if string(old) == content {
		return false
	}
	return fsutil.WriteFileAtomic(n.WSDDDefaults, []byte(content), 0o644) == nil
}

// RemovePrinters removes the default [printers] and [print$] sections and
// turns printer sharing off, so the server offers only real shares.
func (n *Network) RemovePrinters(ctx context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	data, err := os.ReadFile(n.mainConf)
	if err != nil {
		return err
	}
	secs, _ := parseSections(string(data), n.mainConf)
	lines := splitLines(data)
	drop := map[int]bool{}
	for _, s := range secs {
		lower := strings.ToLower(s.name)
		if lower != "printers" && lower != "print$" {
			continue
		}
		start := s.start
		for start > 0 && strings.TrimSpace(lines[start-1]) == "" {
			start--
		}
		for i := start; i < s.end; i++ {
			drop[i] = true
		}
	}
	var out []string
	for i, l := range lines {
		if !drop[i] {
			out = append(out, l)
		}
	}
	content := setGlobalLines(out, []Option{{"load printers", "no"}, {"printing", "bsd"}, {"printcap name", "/dev/null"}, {"disable spoolss", "yes"}}, nil)
	return writeChecked(ctx, n.run, n.mainConf, data, strings.Join(content, "\n"))
}

// EditGlobal sets and removes [global] parameters of a Samba config in
// place: the first occurrence is replaced, duplicates are dropped, missing
// ones are appended to the section. The change is checked with testparm,
// backed up and Samba is reloaded.
func EditGlobal(ctx context.Context, run sysexec.Runner, file string, set []Option, remove []string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	for _, o := range set {
		if err := valid.SingleLine(o.Key, o.Value); err != nil {
			return err
		}
	}
	lines := setGlobalLines(splitLines(data), set, remove)
	return writeChecked(ctx, run, file, data, strings.Join(lines, "\n"))
}

func setGlobalLines(lines []string, set []Option, remove []string) []string {
	text := strings.Join(lines, "\n")
	secs, _ := parseSections(text, "")
	var g *section
	for i := range secs {
		if strings.EqualFold(secs[i].name, "global") {
			g = &secs[i]
			break
		}
	}
	if g == nil {
		head := []string{"[global]"}
		for _, o := range set {
			head = append(head, fmt.Sprintf("   %s = %s", o.Key, o.Value))
		}
		return append(append(head, ""), lines...)
	}
	want := map[string]Option{}
	for _, o := range set {
		want[normKey(o.Key)] = o
	}
	drop := map[string]bool{}
	for _, k := range remove {
		drop[normKey(k)] = true
	}
	done := map[string]bool{}
	var out []string
	out = append(out, lines[:g.start+1]...)
	for i := g.start + 1; i < g.end && i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if l == "" || l[0] == '#' || l[0] == ';' {
			out = append(out, lines[i])
			continue
		}
		k, _, ok := strings.Cut(l, "=")
		nk := normKey(strings.TrimSpace(k))
		switch {
		case !ok:
			out = append(out, lines[i])
		case drop[nk]:
		case want[nk].Key != "":
			if !done[nk] {
				out = append(out, fmt.Sprintf("   %s = %s", want[nk].Key, want[nk].Value))
				done[nk] = true
			}
		default:
			out = append(out, lines[i])
		}
	}
	for _, o := range set {
		if !done[normKey(o.Key)] {
			out = append(out, fmt.Sprintf("   %s = %s", o.Key, o.Value))
		}
	}
	if g.end < len(lines) {
		out = append(out, lines[g.end:]...)
	}
	return out
}
