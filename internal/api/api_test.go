package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Phydran6/LocoStor/internal/auth"
)

func testServer(t *testing.T) http.Handler {
	t.Helper()
	store, err := auth.NewMemStore("admin", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Version: "1.0.0",
		Auth:    auth.New(store, ""),
		Web: fstest.MapFS{
			"index.html":            {Data: []byte("<!doctype html>app")},
			"assets/logo-abc.png":   {Data: []byte("\x89PNG\r\n\x1a\n")},
			"assets/favicon-12.svg": {Data: []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")},
		},
	}
	return s.Handler()
}

func do(h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var jsonHdr = map[string]string{"X-Requested-With": "LocoStor", "Content-Type": "application/json"}

func TestStaticAssets(t *testing.T) {
	h := testServer(t)
	for path, ctype := range map[string]string{
		"/assets/logo-abc.png":   "image/png",
		"/assets/favicon-12.svg": "image/svg+xml",
		"/":                      "text/html",
		"/smb/shares":            "text/html", // SPA route
	} {
		rec := do(h, "GET", path, "", nil)
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), ctype) {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Header().Get("Content-Type"))
		}
		if rec.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s: no CSP header", path)
		}
	}
	if rec := do(h, "GET", "/assets/logo-abc.png", "", nil); !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Error("hashed assets should be cached immutably")
	}
	if rec := do(h, "GET", "/assets/", "", nil); strings.Contains(rec.Body.String(), "logo-abc") {
		t.Error("directory listing exposed")
	}
	if rec := do(h, "POST", "/", "", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST / = %d", rec.Code)
	}
}

func TestAPIProtection(t *testing.T) {
	h := testServer(t)
	if rec := do(h, "GET", "/api/dashboard", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("dashboard without session: %d", rec.Code)
	}
	body := `{"username":"admin","password":"correct horse"}`
	if rec := do(h, "POST", "/api/auth/login", body, map[string]string{"Content-Type": "application/json"}); rec.Code != http.StatusForbidden {
		t.Errorf("login without X-Requested-With: %d", rec.Code)
	}
	cross := map[string]string{"X-Requested-With": "LocoStor", "Content-Type": "application/json", "Origin": "https://evil.example"}
	if rec := do(h, "POST", "/api/auth/login", body, cross); rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin login: %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/auth/login", `{"username":"admin","password":"nope"}`, jsonHdr); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", rec.Code)
	}
	rec := do(h, "POST", "/api/auth/login", body, jsonHdr)
	if rec.Code != 200 {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	cookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "SameSite=Strict") {
		t.Errorf("weak cookie: %s", cookie)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("API responses must not be cached")
	}
	session := strings.Split(cookie, ";")[0]
	rec = do(h, "GET", "/api/auth/account", "", map[string]string{"Cookie": session})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"username":"admin"`) {
		t.Errorf("account: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "POST", "/api/auth/password", `{"current":"x","new":"y"}`, map[string]string{"Cookie": session, "Content-Type": "text/plain", "X-Requested-With": "LocoStor"})
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("non-JSON body accepted: %d", rec.Code)
	}
}
