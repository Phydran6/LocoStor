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
	"strings"
	"time"

	"github.com/Phydran6/LocoStor/internal/nfs"
	"github.com/Phydran6/LocoStor/internal/smb"
	"github.com/Phydran6/LocoStor/internal/smbapi"
	"github.com/Phydran6/LocoStor/internal/sysexec"
	"github.com/Phydran6/LocoStor/internal/system"
	"github.com/Phydran6/LocoStor/internal/tasks"
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
	Users   *smb.UserManager
	Network *smb.Network
	NFS     *nfs.Kernel
	Tasks   *tasks.Manager
	Apt     *system.Apt
	// Demo skips filesystem checks and changes.
	Demo bool
	// Updater updates the agent binary on request from the UI (optional).
	Updater *update.Updater
}

// StateDir holds the agent's own state on the host (next to the socket).
const StateDir = "/var/lib/locostor-host"

// New creates an agent for the real host configuration.
func New(version string, run sysexec.Runner) *Agent {
	return &Agent{
		Version: version,
		Run:     run,
		SMB:     smb.NewInPlace(run, "/etc/samba/smb.conf", false),
		Users:   smb.NewUsers(run),
		// The host keeps announcing itself as before unless changed.
		Network: smb.NewNetwork(run, "/etc/samba/smb.conf", filepath.Join(StateDir, "network.json"), smb.DiscoveryOn, false),
		NFS:     nfs.NewKernel(run, "/etc/exports", false),
		Tasks:   tasks.New(filepath.Join(StateDir, "tasks"), version),
		// Proxmox VE must be upgraded with dist-upgrade.
		Apt: system.NewApt(filepath.Join(StateDir, "apt.json"), true),
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
	// Features lists what this agent supports, so a newer UI knows when
	// the agent must be updated first.
	Features []string `json:"features"`
}

// Features of this agent version.
var Features = []string{"share-access", "network", "users-v2", "tasks", "system-updates"}

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
		err := a.SMB.Delete(r.Context(), r.PathValue("name"))
		if err == nil {
			a.Network.Reconcile(r.Context())
		}
		respond(w, err)
	})
	mux.HandleFunc("POST /smb/shares/{name}/permissions", func(w http.ResponseWriter, r *http.Request) {
		sh := a.SMB.Get(r.Context(), r.PathValue("name"))
		if sh == nil {
			fail(w, &valid.NotFound{What: "share " + r.PathValue("name")})
			return
		}
		t, err := smb.StartPermissions(a.Tasks, a.Run, *sh, !a.Demo)
		if err != nil {
			fail(w, valid.Errorf("%s", err.Error()))
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"task": t.Info().ID})
	})
	mux.HandleFunc("GET /smb/network", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.Network.Info(r.Context()))
	})
	mux.HandleFunc("PUT /smb/network", func(w http.ResponseWriter, r *http.Request) {
		var body smb.NetworkSettings
		if !decode(w, r, &body) {
			return
		}
		info, err := a.Network.Save(r.Context(), body)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, info)
	})
	mux.HandleFunc("POST /smb/network/remove-printers", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Network.RemovePrinters(r.Context()); err != nil {
			fail(w, err)
			return
		}
		a.Network.Reconcile(r.Context())
		writeJSON(w, http.StatusOK, a.Network.Info(r.Context()))
	})
	smbapi.RegisterUserRoutes(mux, "/smb", a.Users)
	mux.HandleFunc("GET /system/updates", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.Apt.Status())
	})
	mux.HandleFunc("POST /system/check", func(w http.ResponseWriter, r *http.Request) {
		a.startTask(w, "Check for updates on the Proxmox host", func(t *tasks.Task) error {
			_, err := a.Apt.Check(t)
			return err
		})
	})
	mux.HandleFunc("POST /system/upgrade", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Mode string `json:"mode"`
		}
		if !decode(w, r, &body) {
			return
		}
		if body.Mode != system.ModeSecurity && body.Mode != system.ModeAll {
			fail(w, valid.Errorf("mode must be security or all"))
			return
		}
		a.startTask(w, "Update the Proxmox host", func(t *tasks.Task) error {
			_, err := a.Apt.Upgrade(t, body.Mode)
			return err
		})
	})
	mux.HandleFunc("POST /system/install", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Packages []string `json:"packages"`
		}
		if !decode(w, r, &body) {
			return
		}
		for _, p := range body.Packages {
			if _, ok := system.Installable[p]; !ok {
				fail(w, valid.Errorf("package %q cannot be installed from here", p))
				return
			}
		}
		if len(body.Packages) == 0 {
			fail(w, valid.Errorf("no packages given"))
			return
		}
		a.startTask(w, "Install "+strings.Join(body.Packages, ", ")+" on the Proxmox host", func(t *tasks.Task) error {
			if err := a.Apt.Install(t, body.Packages...); err != nil {
				return err
			}
			a.Network.Reconcile(t.Context())
			return nil
		})
	})
	mux.Handle("GET /tasks", a.Tasks.Handler())
	mux.Handle("GET /tasks/{id}", a.Tasks.Handler())
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
	info := Info{Available: true, Version: a.Version, Hostname: host, Samba: a.SMB.Installed(), NFS: a.NFS.Installed(), Services: map[string]string{}, Features: Features}
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
	old := r.PathValue("name")
	var prev *smb.Share
	if old != "" {
		prev = a.SMB.Get(r.Context(), old)
	}
	saved, err := a.SMB.Save(r.Context(), old, s)
	if err != nil {
		fail(w, err)
		return
	}
	a.Network.Reconcile(r.Context())
	task := ""
	if smb.NeedsPermissions(prev, saved) {
		if t, err := smb.StartPermissions(a.Tasks, a.Run, saved, !a.Demo); err == nil {
			task = t.Info().ID
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"share": saved, "task": task})
}

// startTask runs fn as the host's single system task and answers with
// its id.
func (a *Agent) startTask(w http.ResponseWriter, title string, fn func(t *tasks.Task) error) {
	t, err := a.Tasks.Start("system", title, fn)
	if err != nil {
		fail(w, valid.Errorf("%s", err.Error()))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"task": t.Info().ID})
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
