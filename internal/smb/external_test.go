package smb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const handWritten = `[global]
   workgroup = WORKGROUP

[homes]
   browseable = no

[Scans]
   path = /srv/scans
   writable = yes
   guest ok = yes
   force user = nobody

[Old$]
   path = /srv/old
`

func TestExternalAndAdopt(t *testing.T) {
	m, _, opts := newTest(t)
	if err := os.WriteFile(opts.MainConf, []byte(handWritten), 0o644); err != nil {
		t.Fatal(err)
	}
	ext, err := m.External()
	if err != nil {
		t.Fatal(err)
	}
	if len(ext) != 3 {
		t.Fatalf("want homes, Scans, Old$; got %+v", ext)
	}
	if ext[0].Name != "homes" || ext[0].Adoptable {
		t.Errorf("homes must not be adoptable: %+v", ext[0])
	}
	scans := ext[1]
	if !scans.Adoptable || scans.ReadOnly || !scans.GuestOK || scans.Path != "/srv/scans" {
		t.Errorf("Scans mapped wrong: %+v", scans)
	}
	if len(scans.Options) != 1 || scans.Options[0].Key != "force user" {
		t.Errorf("extra option lost: %+v", scans.Options)
	}
	if !ext[2].Adoptable || !ext[2].ReadOnly {
		t.Errorf("hidden share with Samba default read only: %+v", ext[2])
	}

	if _, err := m.Adopt(context.Background(), "scans"); err != nil {
		t.Fatal(err)
	}
	main, _ := os.ReadFile(opts.MainConf)
	if strings.Contains(string(main), "[Scans]") || !strings.Contains(string(main), "[Old$]") {
		t.Errorf("section not removed cleanly:\n%s", main)
	}
	inc, _ := os.ReadFile(opts.IncludePath)
	if !strings.Contains(string(inc), "force user = nobody") {
		t.Errorf("option not carried over:\n%s", inc)
	}
	backups, _ := filepath.Glob(opts.MainConf + ".locostor-*")
	if len(backups) != 1 {
		t.Errorf("want one backup, got %v", backups)
	}
	ext, _ = m.External()
	if len(ext) != 2 {
		t.Errorf("Scans still listed as external: %+v", ext)
	}
}

func TestOptionValidation(t *testing.T) {
	for _, opts := range [][]Option{
		{{Key: "path", Value: "/x"}},
		{{Key: "Read Only", Value: "no"}},
		{{Key: "include", Value: "/etc/passwd"}},
		{{Key: "force user", Value: "a\nb"}},
		{{Key: "force user", Value: "a"}, {Key: "Force_User", Value: "b"}},
	} {
		s := Share{Options: opts}
		if err := validateOptions(&s); err == nil {
			t.Errorf("options %+v accepted", opts)
		}
	}
}
