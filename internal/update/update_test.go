package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// versionRunner answers "<binary> version" like a real LocoStor binary.
type versionRunner map[string]string

func (v versionRunner) Run(_ context.Context, _, name string, _ ...string) ([]byte, error) {
	data, _ := os.ReadFile(name)
	return []byte(v[string(data)] + "\n"), nil
}

func fakeGitHub(t *testing.T, binary []byte, sum string) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v1.1.0","body":"notes","assets":[
				{"name":%q,"browser_download_url":%q,"size":%d},
				{"name":"SHA256SUMS","browser_download_url":%q}]}`,
				AssetName(), srv.URL+"/bin", len(binary), srv.URL+"/sums")
		case "/bin":
			w.Write(binary)
		case "/sums":
			fmt.Fprintf(w, "%s  %s\n", sum, AssetName())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func waitPhase(t *testing.T, u *Updater, want string) Progress {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p := u.Progress()
		if p.Phase == want || p.Phase == "failed" {
			return p
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("phase %q not reached: %+v", want, u.Progress())
	return Progress{}
}

func TestUpdateAndRollback(t *testing.T) {
	newBin := []byte("new-binary")
	h := sha256.Sum256(newBin)
	srv := fakeGitHub(t, newBin, hex.EncodeToString(h[:]))

	exe := filepath.Join(t.TempDir(), "locostor")
	os.WriteFile(exe, []byte("old-binary"), 0o755)
	restarted := make(chan struct{}, 2)
	u := New(Options{
		Repo: "o/r", Current: "1.0.0", APIBase: srv.URL, Exe: exe,
		Run:     versionRunner{"old-binary": "1.0.0", "new-binary": "1.1.0"},
		Restart: func() { restarted <- struct{}{} },
	})

	if err := u.StartUpdate(); err != nil {
		t.Fatal(err)
	}
	p := waitPhase(t, u, "restarting")
	if p.Phase != "restarting" || p.Bytes != int64(len(newBin)) || p.Target != "1.1.0" {
		t.Fatalf("unexpected progress: %+v", p)
	}
	<-restarted
	if data, _ := os.ReadFile(exe); string(data) != "new-binary" {
		t.Fatalf("binary not replaced: %q", data)
	}
	if prev, _ := os.ReadFile(exe + ".previous"); string(prev) != "old-binary" {
		t.Fatalf("previous binary not kept: %q", prev)
	}

	// The new process would start fresh; simulate that for the rollback.
	u2 := New(Options{Repo: "o/r", Current: "1.1.0", Exe: exe, Run: versionRunner{"old-binary": "1.0.0", "new-binary": "1.1.0"},
		Restart: func() { restarted <- struct{}{} }})
	if v := u2.Status(context.Background()).PreviousVersion; v != "1.0.0" {
		t.Fatalf("previous version = %q", v)
	}
	if err := u2.StartRollback(); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, u2, "restarting")
	<-restarted
	if data, _ := os.ReadFile(exe); string(data) != "old-binary" {
		t.Fatalf("rollback did not restore the old binary: %q", data)
	}
}

func TestUpdateRejectsBadChecksum(t *testing.T) {
	srv := fakeGitHub(t, []byte("evil"), hex.EncodeToString(make([]byte, 32)))
	exe := filepath.Join(t.TempDir(), "locostor")
	os.WriteFile(exe, []byte("old-binary"), 0o755)
	u := New(Options{Repo: "o/r", Current: "1.0.0", APIBase: srv.URL, Exe: exe, Run: versionRunner{}})
	if err := u.StartUpdate(); err != nil {
		t.Fatal(err)
	}
	p := waitPhase(t, u, "failed")
	if p.Phase != "failed" {
		t.Fatalf("bad checksum accepted: %+v", p)
	}
	if data, _ := os.ReadFile(exe); string(data) != "old-binary" {
		t.Fatal("binary replaced despite checksum mismatch")
	}
	if err := u.StartUpdate(); err != nil {
		t.Fatalf("a failed run must not block the next one: %v", err)
	}
}
