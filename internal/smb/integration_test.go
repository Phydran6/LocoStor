//go:build integration

// Runs against a real Samba on this machine (as root): go test -tags integration ./internal/smb/
// It creates users, a share below /srv and changes /etc/samba/locostor.conf.
package smb

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Phydran6/LocoStor/internal/sysexec"
	"github.com/Phydran6/LocoStor/internal/tasks"
)

func smbclient(t *testing.T, user, pw, cmd string) (string, error) {
	t.Helper()
	out, err := exec.Command("smbclient", "//127.0.0.1/ITData", "-U", user+"%"+pw, "-c", cmd).CombinedOutput()
	return string(out), err
}

func TestRealSamba(t *testing.T) {
	ctx := context.Background()
	run := sysexec.OS{}
	um := NewUsers(run)
	for _, u := range []string{"itwriter", "itreader", "itdenied"} {
		if err := um.AddUser(ctx, u, "secret123"); err != nil {
			t.Fatalf("add %s: %v", u, err)
		}
	}
	users, err := um.Users(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, u := range users {
		if strings.HasPrefix(u.Name, "it") && u.LinuxAccount && !u.Disabled && len(u.Problems) == 0 {
			found++
		}
	}
	if found != 3 {
		t.Fatalf("users not listed correctly: %+v", users)
	}

	dir := "/srv/locostor-it"
	os.RemoveAll(dir)
	os.MkdirAll(dir, 0o755) // root:root 755 - the classic "read only" trap
	m := New(run, Options{StatePath: filepath.Join(t.TempDir(), "s.json"), IncludePath: "/etc/samba/locostor.conf", MainConf: "/etc/samba/smb.conf"})
	share := Share{Name: "ITData", Path: dir, Browseable: true, Enabled: true, Everyone: LevelNone, FixPermissions: true,
		Access: []ACE{{"itwriter", LevelWrite}, {"itreader", LevelRead}, {"itdenied", LevelDeny}}}
	saved, err := m.Save(ctx, "", share)
	if err != nil {
		t.Fatal(err)
	}
	if w := pathWarnings(Share{Path: dir, Everyone: LevelWrite}); len(w) == 0 {
		t.Error("expected a warning for a root-owned folder")
	}
	tm := tasks.New("", "it")
	task, err := StartPermissions(tm, run, saved, true)
	if err != nil {
		t.Fatal(err)
	}
	for task.Info().Status == tasks.Running {
		time.Sleep(50 * time.Millisecond)
	}
	if task.Info().Status != tasks.OK {
		t.Fatalf("permissions: %s\n%s", task.Info().Error, strings.Join(task.Lines(0), "\n"))
	}
	t.Logf("permission task log:\n%s", strings.Join(task.Lines(0), "\n"))
	time.Sleep(time.Second)

	os.WriteFile("/tmp/it.txt", []byte("hi"), 0o644)
	if out, err := smbclient(t, "itwriter", "secret123", "put /tmp/it.txt w.txt; mkdir sub; put /tmp/it.txt sub/x.txt"); err != nil || strings.Contains(out, "NT_STATUS") {
		t.Errorf("writer cannot write: %v %s", err, out)
	}
	if out, err := smbclient(t, "itreader", "secret123", "ls; get w.txt /tmp/it-got.txt"); err != nil || strings.Contains(out, "NT_STATUS") {
		t.Errorf("reader cannot read: %v %s", err, out)
	}
	if out, _ := smbclient(t, "itreader", "secret123", "put /tmp/it.txt r.txt"); !strings.Contains(out, "NT_STATUS_ACCESS_DENIED") {
		t.Errorf("reader could write: %s", out)
	}
	if out, _ := smbclient(t, "itdenied", "secret123", "ls"); !strings.Contains(out, "NT_STATUS") {
		t.Errorf("denied user got in: %s", out)
	}
	if res := um.TestLogin(ctx, "itwriter", "secret123"); !res.OK || !strings.Contains(strings.Join(res.Shares, ","), "ITData") {
		t.Errorf("test login: %+v", res)
	}
	if res := um.TestLogin(ctx, "itwriter", "wrong-password"); res.OK || res.Error != "NT_STATUS_LOGON_FAILURE" {
		t.Errorf("wrong password: %+v", res)
	}
	// Switch the reader to write: now it works, files keep the group.
	saved.Access[1].Level = LevelWrite
	if _, err := m.Save(ctx, "ITData", saved); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if out, err := smbclient(t, "itreader", "secret123", "put /tmp/it.txt sub/r.txt"); err != nil || strings.Contains(out, "NT_STATUS") {
		t.Errorf("reader (now writer) cannot write into a folder made by another user: %v %s", err, out)
	}
	out, _ := exec.Command("ls", "-lR", dir).CombinedOutput()
	t.Logf("%s", out)
	acl, _ := exec.Command("getfacl", "-p", dir+"/sub").CombinedOutput()
	t.Logf("%s", acl)
	m.Delete(ctx, "ITData")
}
