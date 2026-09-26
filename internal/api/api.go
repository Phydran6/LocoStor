// Package api exposes the REST API and serves the embedded web UI.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Phydran6/LocoStor/internal/auth"
	"github.com/Phydran6/LocoStor/internal/nfs"
	"github.com/Phydran6/LocoStor/internal/raid"
	"github.com/Phydran6/LocoStor/internal/smart"
	"github.com/Phydran6/LocoStor/internal/smb"
	"github.com/Phydran6/LocoStor/internal/sysinfo"
	"github.com/Phydran6/LocoStor/internal/update"
	"github.com/Phydran6/LocoStor/internal/valid"
)

// Server bundles all dependencies of the HTTP API.
type Server struct {
	Version string
	Demo    bool
	Auth    *auth.Manager
	SMB     *smb.Manager
	NFS     *nfs.Manager
	SMART   *smart.Manager
	Updater *update.Updater
	// RAID returns the current md arrays.
	RAID func() ([]raid.Array, error)
	// SysInfo returns the dashboard system info.
	SysInfo func() sysinfo.Info
	// Web is the built frontend (index.html at the root).
	Web fs.FS
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("GET /api/auth/me", s.me)

	api := http.NewServeMux()
	api.HandleFunc("POST /api/auth/password", s.changePassword)
	api.HandleFunc("GET /api/dashboard", s.dashboard)

	api.HandleFunc("GET /api/smb/shares", s.smbShares)
	api.HandleFunc("POST /api/smb/shares", s.smbSave)
	api.HandleFunc("PUT /api/smb/shares/{name}", s.smbSave)
	api.HandleFunc("DELETE /api/smb/shares/{name}", s.smbDelete)
	api.HandleFunc("GET /api/smb/users", s.smbUsers)
	api.HandleFunc("POST /api/smb/users", s.smbAddUser)
	api.HandleFunc("PUT /api/smb/users/{name}/password", s.smbSetPassword)
	api.HandleFunc("DELETE /api/smb/users/{name}", s.smbDeleteUser)

	api.HandleFunc("GET /api/nfs/exports", s.nfsExports)
	api.HandleFunc("POST /api/nfs/exports", s.nfsSave)
	api.HandleFunc("PUT /api/nfs/exports/{id}", s.nfsSave)
	api.HandleFunc("DELETE /api/nfs/exports/{id}", s.nfsDelete)

	api.HandleFunc("GET /api/raid", s.raid)
	api.HandleFunc("GET /api/smart", s.smart)

	api.HandleFunc("GET /api/update", s.updateStatus)
	api.HandleFunc("POST /api/update/check", s.updateCheck)
	api.HandleFunc("POST /api/update/apply", s.updateApply)
	api.HandleFunc("POST /api/update/rollback", s.updateRollback)

	mux.Handle("/api/", s.requireAuth(api))
	mux.Handle("/", s.static())
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.Auth.Valid(auth.Token(r)) {
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		// Mutating requests must be JSON: browsers cannot send that
		// cross-site without a CORS preflight, which we never allow.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.ContentLength != 0 && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				writeError(w, http.StatusUnsupportedMediaType, "expected application/json")
				return
			}
			if r.Header.Get("X-Requested-With") != "LocoStor" {
				writeError(w, http.StatusForbidden, "missing X-Requested-With header")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) static() http.Handler {
	files := http.FileServer(http.FS(s.Web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(s.Web, p); err != nil {
			// Unknown path: serve the SPA shell.
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func fail(w http.ResponseWriter, err error) {
	switch {
	case valid.Is(err):
		writeError(w, http.StatusBadRequest, err.Error())
	case valid.IsNotFound(err):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		log.Printf("error: %v", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return valid.Errorf("invalid request body: %v", err)
	}
	return nil
}

func ok(w http.ResponseWriter) { writeJSON(w, http.StatusOK, map[string]bool{"ok": true}) }

// --- auth ---

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	token, err := s.Auth.Login(body.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	auth.SetCookie(w, r, token)
	s.writeMe(w, true)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.Auth.Logout(auth.Token(r))
	auth.ClearCookie(w)
	ok(w)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	s.writeMe(w, s.Auth.Valid(auth.Token(r)))
}

func (s *Server) writeMe(w http.ResponseWriter, loggedIn bool) {
	writeJSON(w, http.StatusOK, map[string]any{
		"logged_in": loggedIn,
		"version":   s.Version,
		"demo":      s.Demo,
	})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	if s.Demo {
		fail(w, valid.Errorf("password cannot be changed in demo mode"))
		return
	}
	if err := s.Auth.ChangePassword(auth.Token(r), body.Current, body.New); err != nil {
		if errors.Is(err, auth.ErrInvalid) {
			fail(w, valid.Errorf("current password is wrong"))
			return
		}
		fail(w, valid.Errorf("%s", err.Error()))
		return
	}
	ok(w)
}

// --- dashboard ---

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	arrays, err := s.RAID()
	raidErr := ""
	if err != nil {
		raidErr = err.Error()
	}
	services := map[string]string{}
	for k, v := range s.SMB.Status(ctx) {
		services[k] = v
	}
	for k, v := range s.NFS.Status(ctx) {
		services[k] = v
	}
	shares, _ := s.SMB.Shares()
	exports, _ := s.NFS.Exports()
	writeJSON(w, http.StatusOK, map[string]any{
		"system":      s.SysInfo(),
		"services":    services,
		"raid":        arrays,
		"raid_error":  raidErr,
		"smb_shares":  len(shares),
		"nfs_exports": len(exports),
		"update":      s.Updater.Status(ctx),
		"version":     s.Version,
	})
}

// --- SMB ---

func (s *Server) smbShares(w http.ResponseWriter, r *http.Request) {
	shares, err := s.SMB.Shares()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, shares)
}

func (s *Server) smbSave(w http.ResponseWriter, r *http.Request) {
	var sh smb.Share
	if err := decode(r, &sh); err != nil {
		fail(w, err)
		return
	}
	saved, err := s.SMB.Save(r.Context(), r.PathValue("name"), sh)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) smbDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.SMB.Delete(r.Context(), r.PathValue("name")); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) smbUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.SMB.Users(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) smbAddUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	if err := s.SMB.AddUser(r.Context(), body.Name, body.Password); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) smbSetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	if err := s.SMB.SetPassword(r.Context(), r.PathValue("name"), body.Password); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) smbDeleteUser(w http.ResponseWriter, r *http.Request) {
	if err := s.SMB.DeleteUser(r.Context(), r.PathValue("name")); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

// --- NFS ---

func (s *Server) nfsExports(w http.ResponseWriter, r *http.Request) {
	exports, err := s.NFS.Exports()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, exports)
}

func (s *Server) nfsSave(w http.ResponseWriter, r *http.Request) {
	id := 0
	if v := r.PathValue("id"); v != "" {
		var err error
		if id, err = strconv.Atoi(v); err != nil || id <= 0 {
			fail(w, &valid.NotFound{What: "export " + v})
			return
		}
	}
	var e nfs.Export
	if err := decode(r, &e); err != nil {
		fail(w, err)
		return
	}
	saved, err := s.NFS.Save(r.Context(), id, e)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) nfsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		fail(w, &valid.NotFound{What: "export " + r.PathValue("id")})
		return
	}
	if err := s.NFS.Delete(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

// --- RAID / SMART ---

func (s *Server) raid(w http.ResponseWriter, r *http.Request) {
	arrays, err := s.RAID()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, arrays)
}

func (s *Server) smart(w http.ResponseWriter, r *http.Request) {
	disks, at, err := s.SMART.Disks(r.Context(), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"disks": disks, "updated_at": at})
}

// --- update ---

func (s *Server) updateStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Updater.Status(r.Context()))
}

func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Updater.Check(r.Context()))
}

func (s *Server) updateApply(w http.ResponseWriter, r *http.Request) {
	// Downloads may outlive a closed browser tab, so detach from the request.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()
	if err := s.Updater.Apply(ctx); err != nil {
		fail(w, valid.Errorf("%s", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restarting": true})
}

func (s *Server) updateRollback(w http.ResponseWriter, r *http.Request) {
	if err := s.Updater.Rollback(r.Context()); err != nil {
		fail(w, valid.Errorf("%s", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restarting": true})
}
