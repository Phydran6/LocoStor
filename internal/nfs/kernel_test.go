package nfs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordRunner struct{ calls []string }

func (r *recordRunner) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return nil, nil
}

const hostExports = `# /etc/exports: the access control list for filesystems which may be exported
#               to NFS clients.  See exports(5).

/srv/nas2/proxmox 192.168.1.2(rw,sync,no_subtree_check,no_root_squash) \
    192.168.1.3(rw,sync,no_subtree_check,no_root_squash)
/srv/nas2/media *(ro,all_squash,insecure)   # media players
/srv/nas2/mixed a(rw) b(ro)
`

func newKernel(t *testing.T) (*Kernel, *recordRunner, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "exports")
	os.WriteFile(path, []byte(hostExports), 0o644)
	os.MkdirAll(path+".d", 0o755)
	os.WriteFile(filepath.Join(path+".d", "backup.exports"), []byte("/srv/backup 10.0.0.0/8(rw)\n"), 0o644)
	r := &recordRunner{}
	return NewKernel(r, path, true), r, path
}

func TestKernelList(t *testing.T) {
	k, _, _ := newKernel(t)
	scan := k.List()
	if len(scan.Exports) != 4 || len(scan.Scanned) != 2 {
		t.Fatalf("got %+v", scan)
	}
	px := scan.Exports[0]
	if !px.Editable || px.Access != "RW" || px.Squash != "no_root_squash" || len(px.Clients) != 2 ||
		strings.Join(px.Options, ",") != "sync,no_subtree_check" {
		t.Errorf("proxmox export parsed wrong: %+v", px)
	}
	if m := scan.Exports[1]; m.Access != "RO" || m.Squash != "all_squash" || len(m.Clients) != 0 || m.Options[0] != "insecure" {
		t.Errorf("media export parsed wrong: %+v", m)
	}
	if scan.Exports[2].Editable {
		t.Error("mixed client options must not be editable")
	}
}

func TestKernelEditAddDelete(t *testing.T) {
	k, r, path := newKernel(t)
	ctx := context.Background()
	scan := k.List()

	px := scan.Exports[0]
	px.Clients = append(px.Clients, "192.168.1.4")
	if _, err := k.Save(ctx, px.Key, px); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	want := "/srv/nas2/proxmox 192.168.1.2(rw,sync,no_subtree_check,no_root_squash) 192.168.1.3(rw,sync,no_subtree_check,no_root_squash) 192.168.1.4(rw,sync,no_subtree_check,no_root_squash)\n/srv/nas2/media"
	if !strings.Contains(got, want) || !strings.HasPrefix(got, "# /etc/exports") || !strings.Contains(got, "# media players") {
		t.Errorf("edit went wrong:\n%s", got)
	}
	if len(r.calls) == 0 || r.calls[len(r.calls)-1] != "exportfs -ra" {
		t.Errorf("exports not reloaded: %v", r.calls)
	}

	if _, err := k.Save(ctx, "", KernelExport{Path: "/srv/nas2/media", Access: "RO"}); err == nil {
		t.Error("duplicate path accepted")
	}
	if _, err := k.Save(ctx, "", KernelExport{Path: "/srv/new", Access: "RW", Options: []string{"sync", "no_subtree_check"}, Clients: []string{"192.168.1.0/24"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Save(ctx, "", KernelExport{Path: "/srv/x", Access: "RW", Options: []string{"rw"}}); err == nil {
		t.Error("access token accepted as extra option")
	}
	if _, err := k.Save(ctx, "", KernelExport{Path: "/srv/y", Access: "RW", Clients: []string{"a(rw)"}}); err == nil {
		t.Error("client with options accepted")
	}

	scan = k.List()
	if err := k.Delete(ctx, scan.Exports[1].Key); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	got = string(data)
	if strings.Contains(got, "/srv/nas2/media") || !strings.Contains(got, "/srv/new 192.168.1.0/24(rw,sync,no_subtree_check)") {
		t.Errorf("unexpected file:\n%s", got)
	}
	if b, _ := filepath.Glob(path + ".locostor-*"); len(b) == 0 {
		t.Error("no backup written")
	}
}
