package nfs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ganeshaConf = `NFS_CORE_PARAM {
    Protocols = 4;  # only v4
}

EXPORT {
    Export_Id = 1;
    Path = "/srv/data";
    Pseudo = "/data";
    Squash = No_Root_Squash;
    CLIENT { Clients = 10.0.0.1, 10.0.0.2; Access_Type = RW; }
    FSAL { Name = VFS; }
}

EXPORT {
    Export_Id = 2;
    Path = "/srv/iso";
    Pseudo = "/iso";
    Attr_Expiration_Time = 60;
    FSAL { Name = VFS; }
}
`

const kernelExports = `# comment
/srv/backup 192.168.1.10(rw,no_root_squash) \
    192.168.1.11(rw,no_root_squash)
/srv/mixed a(rw) b(ro)
`

func newExternalTest(t *testing.T) (*Manager, Options) {
	dir := t.TempDir()
	opts := Options{
		StatePath:     filepath.Join(dir, "state.json"),
		IncludePath:   filepath.Join(dir, "locostor.conf"),
		MainConf:      filepath.Join(dir, "ganesha.conf"),
		ExportsPath:   filepath.Join(dir, "exports"),
		SkipPathCheck: true,
	}
	os.WriteFile(opts.MainConf, []byte(ganeshaConf), 0o644)
	os.WriteFile(opts.ExportsPath, []byte(kernelExports), 0o644)
	return New(fakeRunner{}, opts), opts
}

func TestExternal(t *testing.T) {
	m, _ := newExternalTest(t)
	ext, err := m.External()
	if err != nil {
		t.Fatal(err)
	}
	if len(ext) != 4 {
		t.Fatalf("want 4 external exports, got %d: %+v", len(ext), ext)
	}
	data := ext[0]
	if !data.Adoptable || data.Pseudo != "/data" || data.Squash != "no_root_squash" ||
		strings.Join(data.Clients, ",") != "10.0.0.1,10.0.0.2" || data.Access != "RW" ||
		len(data.Protocols) != 1 || data.Protocols[0] != 4 {
		t.Errorf("ganesha export mapped wrong: %+v", data)
	}
	if ext[1].Adoptable || !strings.Contains(ext[1].Reason, "attr_expiration_time") {
		t.Errorf("unknown setting must block adoption: %+v", ext[1])
	}
	backup := ext[2]
	if !backup.Adoptable || backup.Access != "RW" || len(backup.Clients) != 2 || backup.Squash != "no_root_squash" {
		t.Errorf("kernel export mapped wrong: %+v", backup)
	}
	if ext[3].Adoptable {
		t.Errorf("mixed client options must not be adoptable: %+v", ext[3])
	}
}

func TestAdopt(t *testing.T) {
	m, opts := newExternalTest(t)
	ctx := context.Background()
	ext, _ := m.External()

	e, err := m.Adopt(ctx, ext[0].Key)
	if err != nil {
		t.Fatal(err)
	}
	// The original Export_Id is kept so NFS clients keep working.
	if e.ID != 1 {
		t.Errorf("adopted export ID = %d, want 1", e.ID)
	}
	conf, _ := os.ReadFile(opts.MainConf)
	if strings.Contains(string(conf), `"/data"`) || !strings.Contains(string(conf), `"/iso"`) || !strings.Contains(string(conf), "NFS_CORE_PARAM") {
		t.Errorf("wrong block removed:\n%s", conf)
	}

	ext, _ = m.External()
	var key string
	for _, x := range ext {
		if x.Pseudo == "/srv/backup" {
			key = x.Key
		}
	}
	if _, err := m.Adopt(ctx, key); err != nil {
		t.Fatal(err)
	}
	exports, _ := os.ReadFile(opts.ExportsPath)
	if strings.Contains(string(exports), "/srv/backup") || !strings.Contains(string(exports), "/srv/mixed") {
		t.Errorf("kernel export not removed cleanly:\n%s", exports)
	}
	if list, _ := m.Exports(); len(list) != 2 {
		t.Errorf("want 2 managed exports, got %+v", list)
	}
}
