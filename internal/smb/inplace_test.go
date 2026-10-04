package smb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const hostConf = `#======================= Global Settings =======================
[global]
   workgroup = WORKGROUP
   map to guest = bad user

[homes]
   comment = Home Directories
   browseable = no

# Main data share on the RAID
[nas2]
   # used by the office PCs
   path = /srv/nas2/data
   writable = yes
   valid users = alice
   create mask = 0660

# Media for the TV
[media]
   path = /srv/nas2/media
   guest ok = yes
`

func newInPlace(t *testing.T) (*InPlace, string) {
	t.Helper()
	conf := filepath.Join(t.TempDir(), "smb.conf")
	if err := os.WriteFile(conf, []byte(hostConf), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewInPlace(&fakeRunner{}, conf, true), conf
}

func TestInPlaceList(t *testing.T) {
	p, conf := newInPlace(t)
	scan := p.List(context.Background())
	if len(scan.Shares) != 3 {
		t.Fatalf("want homes, nas2, media: %+v", scan.Shares)
	}
	nas := scan.Shares[1]
	if nas.Name != "nas2" || !nas.Editable || nas.ReadOnly || nas.File != conf || len(nas.Options) != 1 {
		t.Errorf("nas2 parsed wrong: %+v", nas)
	}
	if scan.Shares[0].Editable {
		t.Error("homes must not be editable")
	}
}

func TestInPlaceEditKeepsEverythingElse(t *testing.T) {
	p, conf := newInPlace(t)
	ctx := context.Background()
	scan := p.List(ctx)
	nas := scan.Shares[1].Share
	nas.ReadOnly = true
	nas.ValidUsers = []string{"alice", "anna"}
	if _, err := p.Save(ctx, "nas2", nas); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(conf)
	got := string(data)
	for _, want := range []string{
		"#======================= Global Settings",
		"   map to guest = bad user",
		"# Main data share on the RAID\n[nas2]\n   # used by the office PCs\n",
		"   read only = yes\n",
		"   valid users = alice anna\n",
		"   create mask = 0660\n",
		"\n# Media for the TV\n[media]\n   path = /srv/nas2/media\n   guest ok = yes\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "writable = yes") {
		t.Errorf("old value kept:\n%s", got)
	}
	if b, _ := filepath.Glob(conf + ".locostor-*"); len(b) != 1 {
		t.Errorf("want a backup, got %v", b)
	}
}

func TestInPlaceAddRenameDelete(t *testing.T) {
	p, conf := newInPlace(t)
	ctx := context.Background()
	if _, err := p.Save(ctx, "", Share{Name: "Media", Path: "/srv/x", Enabled: true}); err == nil {
		t.Error("duplicate name (case-insensitive) accepted")
	}
	if _, err := p.Save(ctx, "", Share{Name: "backup", Path: "/srv/nas2/backup", Browseable: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Save(ctx, "homes", Share{Name: "homes", Path: "/home", Enabled: true}); err == nil {
		t.Error("special section edited")
	}
	if _, err := p.Save(ctx, "", Share{Name: "etc", Path: "/etc", Enabled: true}); err == nil {
		t.Error("system directory accepted")
	}
	if err := p.Delete(ctx, "media"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(conf)
	got := string(data)
	if strings.Contains(got, "[media]") || !strings.Contains(got, "[backup]") || !strings.Contains(got, "[nas2]") {
		t.Errorf("unexpected result:\n%s", got)
	}
}
