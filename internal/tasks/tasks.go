// Package tasks runs long operations (system updates, folder permissions,
// domain joins, ...) in the background and keeps their output, so the web
// UI can follow them live like a terminal - also after a page reload or a
// restart of LocoStor itself.
package tasks

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Phydran6/LocoStor/internal/fsutil"
)

// Status values of a task.
const (
	Running     = "running"
	OK          = "ok"
	Failed      = "failed"
	Interrupted = "interrupted"
	Restarting  = "restarting" // LocoStor restarts itself as the last step
)

const (
	maxLines = 20000
	keep     = 40
)

// Info describes a task.
type Info struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	Title   string    `json:"title"`
	Status  string    `json:"status"`
	Error   string    `json:"error,omitempty"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitzero"`
	Lines   int       `json:"lines"`
	// Dropped counts lines removed from the start of a very long log.
	Dropped int `json:"dropped,omitempty"`
}

// ExecFunc runs a command and writes its combined output to out.
type ExecFunc func(ctx context.Context, env []string, out io.Writer, name string, args ...string) error

// RealExec runs commands on the system.
func RealExec(ctx context.Context, env []string, out io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = 5 * time.Second
	return cmd.Run()
}

// Manager keeps running and finished tasks.
type Manager struct {
	dir     string // persisted tasks; empty = memory only
	version string
	Exec    ExecFunc

	mu    sync.Mutex
	tasks map[string]*Task
}

// Task is one background operation.
type Task struct {
	m      *Manager
	ctx    context.Context
	cancel context.CancelFunc

	mu    sync.Mutex
	info  Info
	lines []string
	dirty bool
}

// New creates a manager that persists tasks in dir. Tasks that were still
// running when the process stopped are marked interrupted; a task that
// ended with a planned restart is completed with a note.
func New(dir, version string) *Manager {
	m := &Manager{dir: dir, version: version, Exec: RealExec, tasks: map[string]*Task{}}
	if dir == "" {
		return m
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, f := range files {
		var saved struct {
			Info  Info     `json:"info"`
			Lines []string `json:"lines"`
		}
		if err := fsutil.ReadJSON(f, &saved); err != nil || saved.Info.ID == "" {
			continue
		}
		t := &Task{m: m, info: saved.Info, lines: saved.Lines}
		switch t.info.Status {
		case Running:
			t.info.Status = Interrupted
			t.info.Error = "LocoStor stopped while this was running"
			t.lines = append(t.lines, "", "!! interrupted: LocoStor stopped while this task was running")
			t.dirty = true
		case Restarting:
			t.info.Status = OK
			t.lines = append(t.lines, "", fmt.Sprintf("✔ LocoStor %s is running again", version))
			t.dirty = true
		}
		if t.info.Ended.IsZero() && t.info.Status != Running {
			t.info.Ended = time.Now()
		}
		t.info.Lines = len(t.lines) + t.info.Dropped
		m.tasks[t.info.ID] = t
		if t.dirty {
			t.save()
		}
	}
	return m
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// ErrBusy is returned when a task of the same kind is already running.
var ErrBusy = errors.New("another task of this kind is still running")

// Start runs fn in the background. Only one task per kind runs at a time.
func (m *Manager) Start(kind, title string, fn func(t *Task) error) (*Task, error) {
	m.mu.Lock()
	for _, t := range m.tasks {
		if t.Info().Kind == kind && t.Info().Status == Running {
			m.mu.Unlock()
			return t, fmt.Errorf("%w: %s", ErrBusy, t.Info().Title)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
	t := &Task{m: m, ctx: ctx, cancel: cancel, info: Info{ID: newID(), Kind: kind, Title: title, Status: Running, Started: time.Now()}, dirty: true}
	m.tasks[t.info.ID] = t
	m.pruneLocked()
	m.mu.Unlock()

	t.save()
	go t.run(fn)
	return t, nil
}

func (t *Task) run(fn func(t *Task) error) {
	defer t.cancel()
	stop := make(chan struct{})
	go func() { // persist the log every few seconds while running
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				t.save()
			}
		}
	}()
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("internal error: %v", r)
			}
		}()
		err = fn(t)
	}()
	close(stop)
	t.mu.Lock()
	if t.info.Status == Running {
		t.info.Status = OK
		if err != nil {
			t.info.Status = Failed
			t.info.Error = err.Error()
		}
	}
	t.info.Ended = time.Now()
	t.dirty = true
	t.mu.Unlock()
	if err != nil {
		t.Logf("")
		t.Logf("✖ %s", err)
		log.Printf("task %q failed: %v", t.info.Title, err)
	} else if t.Info().Status == OK {
		t.Logf("")
		t.Logf("✔ done")
	}
	t.save()
}

// ExpectRestart marks the task as finished by a restart of LocoStor: the
// next process completes it.
func (t *Task) ExpectRestart() {
	t.mu.Lock()
	t.info.Status = Restarting
	t.info.Ended = time.Now()
	t.dirty = true
	t.mu.Unlock()
	t.save()
}

// Context is cancelled when the task times out.
func (t *Task) Context() context.Context { return t.ctx }

// Info returns a snapshot of the task's state.
func (t *Task) Info() Info {
	t.mu.Lock()
	defer t.mu.Unlock()
	i := t.info
	i.Lines = len(t.lines) + t.info.Dropped
	return i
}

// Logf adds a line to the log.
func (t *Task) Logf(format string, args ...any) {
	t.addLine(fmt.Sprintf(format, args...))
}

// Section starts a new step in the log.
func (t *Task) Section(title string) {
	t.mu.Lock()
	n := len(t.lines)
	t.mu.Unlock()
	if n > 0 {
		t.addLine("")
	}
	t.addLine("▶ " + title)
}

func (t *Task) addLine(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		t.lines = append(t.lines, strings.TrimRight(l, "\r"))
	}
	if over := len(t.lines) - maxLines; over > 0 {
		t.lines = append([]string{}, t.lines[over:]...)
		t.info.Dropped += over
	}
	t.dirty = true
}

// lineWriter turns command output into log lines. Carriage returns (apt
// progress, curl) also end a line so progress shows up live.
type lineWriter struct {
	t   *Task
	buf bytes.Buffer
}

func (w *lineWriter) Write(p []byte) (int, error) {
	for _, c := range p {
		if c == '\n' || c == '\r' {
			if w.buf.Len() > 0 || c == '\n' {
				w.t.addLine(w.buf.String())
			}
			w.buf.Reset()
			continue
		}
		w.buf.WriteByte(c)
		if w.buf.Len() > 4096 {
			w.t.addLine(w.buf.String())
			w.buf.Reset()
		}
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	if w.buf.Len() > 0 {
		w.t.addLine(w.buf.String())
		w.buf.Reset()
	}
}

// Writer returns an io.Writer that appends to the log line by line.
func (t *Task) Writer() io.Writer { return &lineWriter{t: t} }

// Exec runs a command and streams its output into the log. The command
// line is shown first, like in a shell. hide lists arguments (passwords)
// that are shown as "***".
func (t *Task) Exec(env []string, name string, args ...string) error {
	return t.ExecHidden(nil, env, name, args...)
}

// ExecHidden is Exec with arguments that are masked in the log.
func (t *Task) ExecHidden(hide []string, env []string, name string, args ...string) error {
	shown := make([]string, len(args))
	for i, a := range args {
		shown[i] = a
		for _, h := range hide {
			if h != "" && strings.Contains(a, h) {
				shown[i] = strings.ReplaceAll(a, h, "***")
			}
		}
		if strings.ContainsAny(shown[i], " \t\"'") {
			shown[i] = strconv.Quote(shown[i])
		}
	}
	t.addLine("$ " + strings.TrimSpace(name+" "+strings.Join(shown, " ")))
	w := &lineWriter{t: t}
	err := t.m.Exec(t.ctx, env, w, name, args...)
	w.flush()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("%s exited with code %d", name, ee.ExitCode())
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func (t *Task) save() {
	if t.m.dir == "" {
		return
	}
	t.mu.Lock()
	if !t.dirty {
		t.mu.Unlock()
		return
	}
	t.dirty = false
	info := t.info
	info.Lines = len(t.lines) + t.info.Dropped
	data, err := json.Marshal(map[string]any{"info": info, "lines": t.lines})
	t.mu.Unlock()
	if err == nil {
		_ = fsutil.WriteFileAtomic(filepath.Join(t.m.dir, info.ID+".json"), data, 0o600)
	}
}

// pruneLocked drops the oldest finished tasks; m.mu must be held.
func (m *Manager) pruneLocked() {
	if len(m.tasks) <= keep {
		return
	}
	list := make([]*Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Info().Started.Before(list[j].Info().Started) })
	for _, t := range list[:len(list)-keep] {
		if t.Info().Status == Running {
			continue
		}
		delete(m.tasks, t.info.ID)
		if m.dir != "" {
			os.Remove(filepath.Join(m.dir, t.info.ID+".json"))
		}
	}
}

// List returns all tasks, newest first.
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, t.Info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// Get returns a task.
func (m *Manager) Get(id string) *Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasks[id]
}

// Running returns the running task of kind, if any.
func (m *Manager) Running(kind string) *Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		if i := t.Info(); i.Kind == kind && i.Status == Running {
			return t
		}
	}
	return nil
}

// Lines returns the log from line number from (counting dropped lines).
func (t *Task) Lines(from int) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	i := from - t.info.Dropped
	if i < 0 {
		i = 0
	}
	if i >= len(t.lines) {
		return []string{}
	}
	return append([]string{}, t.lines[i:]...)
}

// Handler serves GET /tasks and GET /tasks/{id}?from=N (mounted with the
// prefix stripped by the caller).
func (m *Manager) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tasks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, m.List())
	})
	mux.HandleFunc("GET /tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		t := m.Get(r.PathValue("id"))
		if t == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
			return
		}
		from, _ := strconv.Atoi(r.URL.Query().Get("from"))
		info := t.Info()
		lines := t.Lines(from)
		start := from
		if start < info.Dropped {
			start = info.Dropped
		}
		writeJSON(w, http.StatusOK, map[string]any{"task": info, "from": start, "lines": lines, "next": start + len(lines)})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
