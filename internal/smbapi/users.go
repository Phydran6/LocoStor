// Package smbapi holds the HTTP handlers for Samba users, shared by the
// container API and the host agent.
package smbapi

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/Phydran6/LocoStor/internal/smb"
	"github.com/Phydran6/LocoStor/internal/valid"
)

// RegisterUserRoutes adds the Samba user endpoints below prefix. The host
// agent uses the same handlers.
func RegisterUserRoutes(mux *http.ServeMux, prefix string, um *smb.UserManager) {
	mux.HandleFunc("GET "+prefix+"/users", func(w http.ResponseWriter, r *http.Request) {
		users, err := um.Users(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, users)
	})
	mux.HandleFunc("GET "+prefix+"/groups", func(w http.ResponseWriter, r *http.Request) {
		groups, err := um.Groups(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, groups)
	})
	mux.HandleFunc("POST "+prefix+"/users", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name     string `json:"name"`
			Password string `json:"password"`
		}
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
		respond(w, um.AddUser(r.Context(), body.Name, body.Password))
	})
	mux.HandleFunc("PUT "+prefix+"/users/{name}/password", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Password string `json:"password"`
		}
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
		respond(w, um.SetPassword(r.Context(), r.PathValue("name"), body.Password))
	})
	mux.HandleFunc("PUT "+prefix+"/users/{name}/enabled", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
		respond(w, um.SetEnabled(r.Context(), r.PathValue("name"), body.Enabled))
	})
	mux.HandleFunc("POST "+prefix+"/users/{name}/repair", func(w http.ResponseWriter, r *http.Request) {
		respond(w, um.Repair(r.Context(), r.PathValue("name")))
	})
	mux.HandleFunc("POST "+prefix+"/users/{name}/test", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Password string `json:"password"`
		}
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		writeJSON(w, http.StatusOK, um.TestLogin(ctx, r.PathValue("name"), body.Password))
	})
	mux.HandleFunc("DELETE "+prefix+"/users/{name}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, um.DeleteUser(r.Context(), r.PathValue("name")))
	})
}

func respond(w http.ResponseWriter, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	switch {
	case valid.Is(err):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case valid.IsNotFound(err):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	default:
		log.Printf("error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
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
