package tasks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func wait(t *testing.T, task *Task) Info {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if i := task.Info(); i.Status != Running {
			return i
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("task did not finish")
	return Info{}
}

func TestTaskStreamsAndPersists(t *testing.T) {
	dir := t.TempDir()
	m := New(dir, "1.0.0")
	m.Exec = func(ctx context.Context, env []string, out io.Writer, name string, args ...string) error {
		fmt.Fprint(out, "Reading lists 10%\rReading lists 100%\nDone\n")
		if name == "false" {
			return errors.New("boom")
		}
		return nil
	}
	task, err := m.Start("system", "Update", func(t *Task) error {
		t.Section("apt")
		return t.ExecHidden([]string{"s3cret"}, nil, "apt-get", "update", "--password=s3cret")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start("system", "Again", func(*Task) error { return nil }); !errors.Is(err, ErrBusy) && task.Info().Status == Running {
		t.Errorf("second task of the same kind started: %v", err)
	}
	info := wait(t, task)
	if info.Status != OK {
		t.Fatalf("status %s: %s", info.Status, info.Error)
	}
	log := strings.Join(task.Lines(0), "\n")
	for _, want := range []string{"▶ apt", "$ apt-get update --password=***", "Reading lists 10%", "Reading lists 100%", "Done", "✔ done"} {
		if !strings.Contains(log, want) {
			t.Errorf("missing %q in log:\n%s", want, log)
		}
	}
	if strings.Contains(log, "s3cret") {
		t.Error("password shown in log")
	}
	if got := task.Lines(info.Lines - 1); len(got) != 1 {
		t.Errorf("Lines(from) = %q", got)
	}

	// A planned restart is completed by the next process.
	task2, _ := m.Start("system", "Self update", func(t *Task) error { t.ExpectRestart(); return nil })
	wait(t, task2)
	m2 := New(dir, "1.1.0")
	got := m2.Get(task2.Info().ID)
	if got == nil || got.Info().Status != OK || !strings.Contains(strings.Join(got.Lines(0), "\n"), "1.1.0 is running") {
		t.Errorf("restart not completed: %+v", got)
	}
	if len(m2.List()) != 2 {
		t.Errorf("tasks not restored: %+v", m2.List())
	}
}
