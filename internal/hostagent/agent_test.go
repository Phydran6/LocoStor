package hostagent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Phydran6/LocoStor/internal/nfs"
	"github.com/Phydran6/LocoStor/internal/smb"
)

type okRunner struct{}

func (okRunner) Run(context.Context, string, string, ...string) ([]byte, error) { return nil, nil }

func TestAgentThroughProxy(t *testing.T) {
	dir := t.TempDir()
	smbConf := filepath.Join(dir, "smb.conf")
	exports := filepath.Join(dir, "exports")
	os.WriteFile(smbConf, []byte("[global]\n   workgroup = W\n\n[data]\n   path = /srv/data\n   writable = yes\n"), 0o644)
	os.WriteFile(exports, []byte("/srv/data 10.0.0.0/8(rw,sync)\n"), 0o644)

	a := &Agent{
		Version: "1.0.0",
		Run:     okRunner{},
		SMB:     smb.NewInPlace(okRunner{}, smbConf, true),
		Users:   smb.New(okRunner{}, smb.Options{}),
		NFS:     nfs.NewKernel(okRunner{}, exports, true),
	}
	// Unix socket paths are limited to ~100 bytes; keep it short.
	sockDir, err := os.MkdirTemp("", "lsa")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	socket := filepath.Join(sockDir, "a.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Serve(ctx, socket)
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	proxy := Proxy(socket)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)
		return rec
	}

	if rec := call("GET", "/info", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"available":true`) {
		t.Fatalf("info: %d %s", rec.Code, rec.Body)
	}
	if rec := call("GET", "/smb/shares", ""); !strings.Contains(rec.Body.String(), `"name":"data"`) {
		t.Fatalf("shares: %s", rec.Body)
	}
	rec := call("PUT", "/smb/shares/data", `{"name":"data","path":"/srv/data","read_only":true,"browseable":true,"enabled":true,"valid_users":[],"options":[]}`)
	if rec.Code != 200 {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body)
	}
	if data, _ := os.ReadFile(smbConf); !strings.Contains(string(data), "read only = yes") || !strings.Contains(string(data), "workgroup = W") {
		t.Errorf("host smb.conf not edited in place:\n%s", data)
	}
	if rec := call("PUT", "/smb/shares/data", `{"name":"data","path":"/etc","enabled":true}`); rec.Code != 400 {
		t.Errorf("system directory accepted: %d %s", rec.Code, rec.Body)
	}
	if rec := call("POST", "/smb/shares", `{"name":"x","path":"/srv/x","options":[{"key":"root preexec","value":"id"}]}`); rec.Code != 400 {
		t.Errorf("dangerous option accepted: %d", rec.Code)
	}
	if rec := call("GET", "/nfs/exports", ""); !strings.Contains(rec.Body.String(), `"path":"/srv/data"`) {
		t.Errorf("exports: %s", rec.Body)
	}
	if rec := call("POST", "/nfs/exports", `{"export":{"path":"/srv/new","access":"RO"}}`); rec.Code != 200 {
		t.Errorf("new export: %d %s", rec.Code, rec.Body)
	}

	// Without a socket the UI learns that the host is not connected.
	missing := Proxy(filepath.Join(sockDir, "none.sock"))
	rec = httptest.NewRecorder()
	missing.ServeHTTP(rec, httptest.NewRequest("GET", "/info", nil))
	if !strings.Contains(rec.Body.String(), `"available":false`) {
		t.Errorf("missing socket: %s", rec.Body)
	}
	rec = httptest.NewRecorder()
	missing.ServeHTTP(rec, httptest.NewRequest("GET", "/smb/shares", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("missing socket: %d", rec.Code)
	}
}
