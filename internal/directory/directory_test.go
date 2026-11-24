package directory

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Phydran6/LocoStor/internal/system"
	"github.com/Phydran6/LocoStor/internal/tasks"
)

type okRunner struct{}

func (okRunner) Run(context.Context, string, string, ...string) ([]byte, error) { return nil, nil }

func TestSetNSS(t *testing.T) {
	in := "# comment passwd: x\npasswd:         files systemd\ngroup:          files systemd\nshadow:         files\n"
	out := SetNSS(in, "winbind", true)
	if !strings.Contains(out, "passwd:         files systemd winbind\n") || !strings.Contains(out, "group:          files systemd winbind\n") || !strings.Contains(out, "# comment passwd: x") {
		t.Errorf("add:\n%s", out)
	}
	if again := SetNSS(out, "winbind", true); again != out {
		t.Errorf("not idempotent:\n%s", again)
	}
	if back := SetNSS(out, "winbind", false); back != in {
		t.Errorf("remove:\n%q\n%q", back, in)
	}
}

func TestValidate(t *testing.T) {
	s := Settings{Mode: ModeAD, Realm: "corp.example.com"}
	if err := s.validate(); err != nil || s.Workgroup != "CORP" || s.Realm != "CORP.EXAMPLE.COM" {
		t.Errorf("%+v %v", s, err)
	}
	for _, bad := range []Settings{
		{Mode: ModeAD, Realm: "corp"},
		{Mode: ModeLDAP, URI: "http://x", BaseDN: "dc=a", BindDN: "cn=b,dc=a"},
		{Mode: ModeLDAP, URI: "ldap://x", BaseDN: "dc=a\nx", BindDN: "cn=b,dc=a"},
	} {
		if err := bad.validate(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	l := Settings{Mode: ModeLDAP, URI: "ldaps://ldap.example.com:636", BaseDN: "dc=example,dc=com", BindDN: "cn=admin,dc=example,dc=com", StartTLS: false}
	if err := l.validate(); err != nil || l.UserSuffix != "ou=People" {
		t.Errorf("%+v %v", l, err)
	}
	conf := NslcdConf(l, "pw")
	for _, want := range []string{"uri ldaps://ldap.example.com:636", "base passwd ou=People,dc=example,dc=com", "bindpw pw", "tls_reqcert demand"} {
		if !strings.Contains(conf, want) {
			t.Errorf("nslcd.conf missing %q:\n%s", want, conf)
		}
	}
}

func TestJoinFlow(t *testing.T) {
	dir := t.TempDir()
	f := Files{SMBConf: filepath.Join(dir, "smb.conf"), Krb5: filepath.Join(dir, "krb5.conf"), NSSwitch: filepath.Join(dir, "nss"),
		Nslcd: filepath.Join(dir, "nslcd"), Debconf: filepath.Join(dir, "deb"), StatePath: filepath.Join(dir, "state.json")}
	os.WriteFile(f.SMBConf, []byte("[global]\n   workgroup = WORKGROUP\n   netbios name = NAS2\n"), 0o644)
	os.WriteFile(f.NSSwitch, []byte("passwd: files\ngroup: files\n"), 0o644)
	m := New(okRunner{}, f)
	m.Demo = true
	var cmds []string
	m.Tasks = tasks.New("", "t")
	m.Tasks.Exec = func(ctx context.Context, env []string, out io.Writer, name string, args ...string) error {
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		return nil
	}
	m.Apt = system.NewApt("", false)
	task, err := m.Connect(Settings{Mode: ModeAD, Realm: "corp.example.com"}, "Administrator", "S3cret!")
	if err != nil {
		t.Fatal(err)
	}
	for task.Info().Status == tasks.Running {
		time.Sleep(10 * time.Millisecond)
	}
	log := strings.Join(task.Lines(0), "\n")
	if task.Info().Status != tasks.OK {
		t.Fatalf("%s\n%s", task.Info().Error, log)
	}
	if strings.Contains(log, "S3cret") {
		t.Error("password in log")
	}
	conf, _ := os.ReadFile(f.SMBConf)
	for _, want := range []string{"security = ads", "realm = CORP.EXAMPLE.COM", "workgroup = CORP", "idmap config CORP : backend = rid", "netbios name = NAS2"} {
		if !strings.Contains(string(conf), want) {
			t.Errorf("smb.conf missing %q:\n%s", want, conf)
		}
	}
	nss, _ := os.ReadFile(f.NSSwitch)
	if !strings.Contains(string(nss), "files winbind") {
		t.Errorf("nsswitch: %s", nss)
	}
	if !strings.Contains(strings.Join(cmds, "\n"), "net ads join -U Administrator%S3cret!") {
		t.Errorf("join not run: %q", cmds)
	}
	if st := m.Status(context.Background()); st.Settings.Mode != ModeAD || !st.Connected {
		t.Errorf("status %+v", st)
	}
	if _, err := m.Connect(Settings{Mode: ModeAD, Realm: "x.example.com"}, "a", "b"); err == nil {
		t.Error("second join accepted")
	}
	task, _ = m.Disconnect("", "")
	for task.Info().Status == tasks.Running {
		time.Sleep(10 * time.Millisecond)
	}
	conf, _ = os.ReadFile(f.SMBConf)
	if !strings.Contains(string(conf), "security = user") || strings.Contains(string(conf), "realm") {
		t.Errorf("after leave:\n%s", conf)
	}
}
