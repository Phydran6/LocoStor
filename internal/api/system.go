package api

import (
	"net/http"
	"strings"

	"github.com/Phydran6/LocoStor/internal/maintenance"
	"github.com/Phydran6/LocoStor/internal/system"
	"github.com/Phydran6/LocoStor/internal/tasks"
	"github.com/Phydran6/LocoStor/internal/valid"
)

func (s *Server) systemRoutes(api *http.ServeMux) {
	if s.Maintenance == nil {
		return
	}
	api.HandleFunc("GET /api/maintenance", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.Maintenance.Status())
	})
	api.HandleFunc("PUT /api/maintenance", func(w http.ResponseWriter, r *http.Request) {
		var body maintenance.Settings
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
		if err := s.Maintenance.Save(body); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, s.Maintenance.Status())
	})
	api.HandleFunc("POST /api/maintenance/run", func(w http.ResponseWriter, r *http.Request) {
		var body maintenance.RunOptions
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
		body.Trigger = "manual"
		s.writeTask(w)(startTask(s.Maintenance.Start(body)))
	})
	api.HandleFunc("GET /api/system/updates", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.Maintenance.Apt.Status())
	})
	api.HandleFunc("POST /api/system/check", func(w http.ResponseWriter, r *http.Request) {
		s.writeTask(w)(startTask(s.Tasks.Start("system", "Check for updates", func(t *tasks.Task) error {
			_, err := s.Maintenance.Apt.Check(t)
			return err
		})))
	})
	api.HandleFunc("POST /api/system/install", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Packages []string `json:"packages"`
		}
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
		if len(body.Packages) == 0 {
			fail(w, valid.Errorf("no packages given"))
			return
		}
		for _, p := range body.Packages {
			if _, ok := system.Installable[p]; !ok {
				fail(w, valid.Errorf("package %q cannot be installed from here", p))
				return
			}
		}
		s.writeTask(w)(startTask(s.Tasks.Start("system", "Install "+strings.Join(body.Packages, ", "), func(t *tasks.Task) error {
			if err := s.Maintenance.Apt.Install(t, body.Packages...); err != nil {
				return err
			}
			s.Network.Reconcile(t.Context())
			return nil
		})))
	})
}

// writeTask answers with the id of a started task.
func (s *Server) writeTask(w http.ResponseWriter) func(*tasks.Task, error) {
	return func(t *tasks.Task, err error) {
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"task": taskID(t)})
	}
}
