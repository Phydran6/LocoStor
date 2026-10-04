// Package hostagent is the small service LocoStor runs on the Proxmox host.
//
// It lets the LocoStor container see and edit the host's own SMB and NFS
// shares without taking them over: changes are made in place in smb.conf
// and /etc/exports, so the host stays the owner and manual edits keep
// working. The agent only listens on a Unix socket that is bind-mounted
// into the container; it is never reachable over the network.
package hostagent

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Phydran6/LocoStor/internal/nfs"
	"github.com/Phydran6/LocoStor/internal/smb"
	"github.com/Phydran6/LocoStor/internal/sysexec"
	"github.com/Phydran6/LocoStor/internal/update"
	"github.com/Phydran6/LocoStor/internal/valid"
)

// DefaultSocket is the socket path on the host. The installer bind-mounts
// its directory into the container at /var/lib/locostor/host.
const DefaultSocket = "/var/lib/locostor-host/agent.sock"

// Agent serves the host API.
type Agent struct {
	Version string
	Run     sysexec.Runner
	SMB     *smb.InPlace
	Users   *smb.Manager // only the user functions are used
	NFS     *nfs.Kernel
	// Updater updates the agent binary on request from the UI (optional).
	Updater *update.Updater
}

// New creates an agent for the real host configuration.
func New(version string, run sysexec.Runner) *Agent {
	return &Agent{
		Version: version,
		Run:     run,
		SMB:     smb.NewInPlace(run, "/etc/samba/smb.conf", false),
		Users:   smb.New(run, smb.Options{}),
		NFS:     nfs.NewKernel(run, "/etc/exports", false),
	}
}

// Info describes the host.
type Info struct {
	Available bool              `json:"available"`
	Version   string            `json:"version"`
	Hostname  string            `json:"hostname"`
	Samba     bool              `json:"samba"`
	NFS       bool              `json:"nfs"`
	Services  map[string]string `json:"services"`
}

// Handler returns the agent API.
func (a *Agent) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /info", a.info)
	mux.HandleFunc("GET /smb/shares", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.SMB.List(r.Context()))
	})
	mux.HandleFunc("POST /smb/shares", a.smbSave)
	mux.HandleFunc("PUT /smb/shares/{name}", a.smbSave)
	mux.HandleFunc("DELETE /smb/shares/{name}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, a.SMB.Delete(r.Context(), r.PathValue("name")))
	})
	mux.HandleFunc("GET /smb/users", func(w http.ResponseWriter, r *http.Request) {
		users, err := a.Users.Users(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, users)
	})
	mux.HandleFunc("POST /smb/users", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Name, Password string }
		if decode(w, r, &body) {
			respond(w, a.Users.AddUser(r.Context(), body.Name, body.Password))
		}
	})
	mux.HandleFunc("PUT /smb/users/{name}/password", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Password string }
		if decode(w, r, &body) {
			respond(w, a.Users.SetPassword(r.Context(), r.PathValue("name"), body.Password))
		}
	})
	mux.HandleFunc("DELETE /smb/users/{name}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, a.Users.DeleteUser(r.Context(), r.PathValue("name")))
	})
	mux.HandleFunc("GET /nfs/exports", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.NFS.List())
	})
	mux.HandleFunc("POST /nfs/exports", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key    string           `json:"key"`
			Export nfs.KernelExport `json:"export"`
		}
		if !decode(w, r, &body) {
			return
		}
		e, err := a.NFS.Save(r.Context(), body.Key, body.Export)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, e)
	})
	mux.HandleFunc("POST /update", func(w http.ResponseWriter, r *http.Request) {
		if a.Updater == nil {
			fail(w, valid.Errorf("the host agent cannot update itself here"))
			return
		}
		if err := a.Updater.StartUpdate(); err != nil {
			fail(w, valid.Errorf("%s", err.Error()))
			return
		}
		writeJSON(w, http.StatusAccepted, a.Updater.Progress())
	})
	mux.HandleFunc("GET /update/progress", func(w http.ResponseWriter, r *http.Request) {
		if a.Updater == nil {
			writeJSON(w, http.StatusOK, update.Progress{Phase: "idle", Log: []update.LogLine{}})
			return
		}
		writeJSON(w, http.StatusOK, a.Updater.Progress())
	})
	mux.HandleFunc("POST /nfs/exports/delete", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key string `json:"key"`
		}
		if decode(w, r, &body) {
			respond(w, a.NFS.Delete(r.Context(), body.Key))
		}
	})
	return mux
}

func (a *Agent) info(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	info := Info{Available: true, Version: a.Version, Hostname: host, Samba: a.SMB.Installed(), NFS: a.NFS.Installed(), Services: map[string]string{}}
	if info.Samba {
		info.Services["smbd"] = sysexec.ServiceState(ctx, a.Run, "smbd")
	}
	if info.NFS {
		info.Services["nfs-server"] = a.NFS.Status(ctx)
	}
	writeJSON(w, http.StatusOK, info)
}

func (a *Agent) smbSave(w http.ResponseWriter, r *http.Request) {
	var s smb.Share
	if !decode(w, r, &s) {
		return
	}
	saved, err := a.SMB.Save(r.Context(), r.PathValue("name"), s)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
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

func respond(w http.ResponseWriter, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, valid.Errorf("invalid request body: %v", err))
		return false
	}
	return true
}

// Serve listens on the Unix socket until ctx ends. The socket and its
// directory are only accessible by root.
func (a *Agent) Serve(ctx context.Context, socket string) error {
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	_ = os.Remove(socket) // stale socket from a previous run
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		ln.Close()
		return err
	}
	srv := &http.Server{
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("LocoStor host agent %s listening on %s", a.Version, socket)
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
