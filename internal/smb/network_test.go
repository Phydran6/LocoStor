package smb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const debianConf = `#
# Sample configuration file
[global]
   workgroup = WORKGROUP
   server string = %h server (Samba, Ubuntu)
#   wins support = no
   map to guest = bad user

[printers]
   comment = All Printers
   browseable = no
   path = /var/tmp
   printable = yes

[print$]
   comment = Printer Drivers
   path = /var/lib/samba/printers

[Data]
   path = /srv/data
`

func TestNetworkSaveAndPrinters(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "smb.conf")
	os.WriteFile(conf, []byte(debianConf), 0o644)
	r := &fakeRunner{}
	n := NewNetwork(r, conf, filepath.Join(dir, "net.json"), DiscoveryAuto, true)
	n.WSDDDefaults = filepath.Join(dir, "wsdd")

	info := n.Info(context.Background())
	if !info.Printers || len(info.Shares) != 1 || info.Shares[0] != "Data" || info.Workgroup != "WORKGROUP" {
		t.Errorf("info: %+v", info)
	}
	if _, err := n.Save(context.Background(), NetworkSettings{NetbiosName: "this name is too long", Workgroup: "x", Discovery: "auto"}); err == nil {
		t.Error("long NetBIOS name accepted")
	}
	info, err := n.Save(context.Background(), NetworkSettings{NetbiosName: "NAS2", Workgroup: "home", Discovery: DiscoveryOff})
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "NAS2" || info.Discovery != DiscoveryOff || info.Workgroup != "HOME" {
		t.Errorf("after save: %+v", info)
	}
	data, _ := os.ReadFile(conf)
	got := string(data)
	for _, want := range []string{"   workgroup = HOME\n", "   netbios name = NAS2\n", "#   wins support = no\n", "[Data]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "server string") {
		t.Errorf("empty server string not removed:\n%s", got)
	}

	if err := n.RemovePrinters(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(conf)
	got = string(data)
	if strings.Contains(got, "[printers]") || strings.Contains(got, "[print$]") || !strings.Contains(got, "load printers = no") || !strings.Contains(got, "\n[Data]\n   path = /srv/data") {
		t.Errorf("printers not removed cleanly:\n%s", got)
	}
}

func TestHostUntouchedUntilSaved(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "smb.conf")
	os.WriteFile(conf, []byte(debianConf), 0o644)
	r := &fakeRunner{}
	n := NewNetwork(r, conf, filepath.Join(dir, "net.json"), DiscoveryOn, false)
	n.Reconcile(context.Background())
	for _, c := range r.calls {
		if strings.HasPrefix(c, "systemctl enable") || strings.HasPrefix(c, "systemctl disable") {
			t.Errorf("host changed without saved settings: %v", r.calls)
		}
	}
}
