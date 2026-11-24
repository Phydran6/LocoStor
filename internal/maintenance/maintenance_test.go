package maintenance

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Phydran6/LocoStor/internal/system"
	"github.com/Phydran6/LocoStor/internal/tasks"
	"github.com/Phydran6/LocoStor/internal/update"
)

func TestRunAndSchedule(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"tag_name":"v9.9.9","html_url":"x","body":"notes","assets":[]}`)
	}))
	defer gh.Close()
	dir := t.TempDir()
	m := New(filepath.Join(dir, "m.json"))
	m.Tasks = tasks.New("", "1.0.0")
	m.Tasks.Exec = func(ctx context.Context, env []string, out io.Writer, name string, args ...string) error {
		io.WriteString(out, "ok "+name+" "+strings.Join(args, " ")+"\n")
		return nil
	}
	m.Apt = system.NewApt(filepath.Join(dir, "apt.json"), false)
	m.Apt.Demo = true
	m.Updater = update.New(update.Options{Repo: "x/y", Current: "1.0.0", APIBase: gh.URL, Disabled: "test"})
	var notes []string
	m.Notify = func(kind, sev, title, body string) { notes = append(notes, sev+":"+title+":"+body) }

	task, err := m.Start(RunOptions{App: AppNotify, System: system.ModeSecurity, Trigger: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	for task.Info().Status == tasks.Running {
		time.Sleep(10 * time.Millisecond)
	}
	log := strings.Join(task.Lines(0), "\n")
	if task.Info().Status != tasks.OK {
		t.Fatalf("status %s\n%s", task.Info().Error, log)
	}
	for _, want := range []string{"apt-get update", "install -y --only-upgrade", "libssl3", "LocoStor 9.9.9 is available", "Container: 2 packages updated"} {
		if !strings.Contains(log, want) {
			t.Errorf("missing %q in\n%s", want, log)
		}
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "9.9.9") {
		t.Errorf("notifications: %q", notes)
	}
	if st := m.Status(); st.Last == nil || !st.Last.OK || st.NextRun.IsZero() {
		t.Errorf("status: %+v", st)
	}

	// Schedule: next run is the coming 03:30.
	m.clock = func() time.Time { return time.Date(2026, 1, 1, 4, 0, 0, 0, time.Local) }
	if n := m.next(Defaults()); n.Day() != 2 || n.Hour() != 3 || n.Minute() != 30 {
		t.Errorf("next run %v", n)
	}
	if err := m.Save(Settings{Enabled: true, Time: "25:00", App: AppAuto, System: "all", Host: "off"}); err == nil {
		t.Error("bad time accepted")
	}
}
