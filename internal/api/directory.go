package api

import (
	"net/http"

	"github.com/Phydran6/LocoStor/internal/directory"
)

func (s *Server) directoryRoutes(api *http.ServeMux) {
	d := s.Directory
	if d == nil {
		return
	}
	api.HandleFunc("GET /api/directory", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, d.Status(r.Context()))
	})
	api.HandleFunc("POST /api/directory/connect", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Settings directory.Settings `json:"settings"`
			User     string             `json:"user"`
			Password string             `json:"password"`
		}
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
		s.writeTask(w)(startTask(d.Connect(body.Settings, body.User, body.Password)))
	})
	api.HandleFunc("POST /api/directory/disconnect", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			User     string `json:"user"`
			Password string `json:"password"`
		}
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
		s.writeTask(w)(startTask(d.Disconnect(body.User, body.Password)))
	})
}
