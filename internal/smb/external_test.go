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
	scan, err := m.External(context.Background())
	ext := scan.Shares
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
	scan, _ = m.External(context.Background())
	ext = scan.Shares
	if len(ext) != 2 {
		t.Errorf("Scans still listed as external: %+v", ext)
	}
}

func TestOptionValidation(t *testing.T) {
	for _, opts := range [][]Option{
		{{Key: "path", Value: "/x"}},
		{{Key: "Read Only", Value: "no"}},
		{{Key: "include", Value: "/etc/passwd"}},
		{{Key: "root preexec", Value: "rm -rf /"}},
		{{Key: "Magic_Script", Value: "x.sh"}},
		{{Key: "print command", Value: "id"}},
		{{Key: "force user", Value: "a\nb"}},
		{{Key: "force user", Value: "a"}, {Key: "Force_User", Value: "b"}},
	} {
		s := Share{Options: opts}
		if err := validateOptions(&s); err == nil {
			t.Errorf("options %+v accepted", opts)
		}
	}
}

// testparmRunner answers testparm like real Samba would, including a share
// that only exists in the registry.
type testparmRunner struct{ fakeRunner }

func (r *testparmRunner) Run(ctx context.Context, stdin, name string, args ...string) ([]byte, error) {
	if name == "testparm" && len(args) > 0 && args[0] == "-s" && !strings.Contains(args[len(args)-1], "locostor-smb-") {
		return []byte("# Global parameters\n[global]\n\tworkgroup = WORKGROUP\n\n[Scans]\n\tpath = /srv/scans\n\tread only = No\n\n[Reg]\n\tpath = /srv/reg\n"), nil
	}
	return r.fakeRunner.Run(ctx, stdin, name, args...)
}

func TestExternalUsesTestparm(t *testing.T) {
	_, _, opts := newTest(t)
	os.WriteFile(opts.MainConf, []byte(handWritten), 0o644)
	m := New(&testparmRunner{}, opts)
	scan, err := m.External(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]ExternalShare{}
	for _, s := range scan.Shares {
		byName[s.Name] = s
	}
	if s, ok := byName["Scans"]; !ok || s.Source != opts.MainConf || !s.Adoptable || len(s.Options) != 1 {
		t.Errorf("Scans should come from the file with its options: %+v", s)
	}
	if s, ok := byName["Reg"]; !ok || s.Adoptable {
		t.Errorf("registry share should be listed but not adoptable: %+v", s)
	}
	if _, ok := byName["Old$"]; ok {
		t.Errorf("share unknown to testparm must not be listed")
	}
}
