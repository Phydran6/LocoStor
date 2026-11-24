package api

import (
	"errors"
	"net/http"

	"github.com/Phydran6/LocoStor/internal/smb"
	"github.com/Phydran6/LocoStor/internal/smbapi"
	"github.com/Phydran6/LocoStor/internal/tasks"
	"github.com/Phydran6/LocoStor/internal/valid"
)

func (s *Server) smbRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/smb/shares", s.smbShares)
	api.HandleFunc("POST /api/smb/shares", s.smbSave)
	api.HandleFunc("PUT /api/smb/shares/{name}", s.smbSave)
	api.HandleFunc("DELETE /api/smb/shares/{name}", s.smbDelete)
	api.HandleFunc("POST /api/smb/shares/{name}/permissions", s.smbPermissions)
	api.HandleFunc("GET /api/smb/external", s.smbExternal)
	api.HandleFunc("POST /api/smb/external/adopt", s.smbAdopt)
	api.HandleFunc("GET /api/smb/network", s.smbNetwork)
	api.HandleFunc("PUT /api/smb/network", s.smbNetworkSave)
	api.HandleFunc("POST /api/smb/network/remove-printers", s.smbRemovePrinters)
	smbapi.RegisterUserRoutes(api, "/api/smb", s.Users)
}

func respond(w http.ResponseWriter, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

// taskID returns the id of t, or "" when t is nil.
func taskID(t *tasks.Task) string {
	if t == nil {
		return ""
	}
	return t.Info().ID
}

// startTask maps "already running" to a validation error.
func startTask(t *tasks.Task, err error) (*tasks.Task, error) {
	if errors.Is(err, tasks.ErrBusy) {
		return t, valid.Errorf("%s", err.Error())
	}
	return t, err
}

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
	old := r.PathValue("name")
	var prev *smb.Share
	if old != "" {
		prev, _ = s.SMB.Get(old)
	}
	saved, err := s.SMB.Save(r.Context(), old, sh)
	if err != nil {
		fail(w, err)
		return
	}
	s.Network.Reconcile(r.Context())
	var t *tasks.Task
	if smb.NeedsPermissions(prev, saved) && s.Tasks != nil {
		t, _ = smb.StartPermissions(s.Tasks, s.Run, saved, !s.Demo)
	}
	writeJSON(w, http.StatusOK, map[string]any{"share": saved, "task": taskID(t)})
}

func (s *Server) smbPermissions(w http.ResponseWriter, r *http.Request) {
	sh, err := s.SMB.Get(r.PathValue("name"))
	if err != nil {
		fail(w, err)
		return
	}
	t, err := startTask(smb.StartPermissions(s.Tasks, s.Run, *sh, !s.Demo))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"task": taskID(t)})
}

func (s *Server) smbDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.SMB.Delete(r.Context(), r.PathValue("name")); err != nil {
		fail(w, err)
		return
	}
	s.Network.Reconcile(r.Context())
	ok(w)
}

func (s *Server) smbExternal(w http.ResponseWriter, r *http.Request) {
	list, err := s.SMB.External(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) smbAdopt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	sh, err := s.SMB.Adopt(r.Context(), body.Name)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sh)
}

func (s *Server) smbNetwork(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Network.Info(r.Context()))
}

func (s *Server) smbNetworkSave(w http.ResponseWriter, r *http.Request) {
	var body smb.NetworkSettings
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	info, err := s.Network.Save(r.Context(), body)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) smbRemovePrinters(w http.ResponseWriter, r *http.Request) {
	if err := s.Network.RemovePrinters(r.Context()); err != nil {
		fail(w, err)
		return
	}
	s.Network.Reconcile(r.Context())
	writeJSON(w, http.StatusOK, s.Network.Info(r.Context()))
}
