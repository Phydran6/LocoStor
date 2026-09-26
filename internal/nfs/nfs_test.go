package nfs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Phydran6/LocoStor/internal/valid"
)

type fakeRunner struct{}

func (fakeRunner) Run(context.Context, string, string, ...string) ([]byte, error) { return nil, nil }

func TestSaveAndRender(t *testing.T) {
	dir := t.TempDir()
	opts := Options{
		StatePath:     filepath.Join(dir, "state.json"),
		IncludePath:   filepath.Join(dir, "locostor.conf"),
		MainConf:      filepath.Join(dir, "ganesha.conf"),
		SkipPathCheck: true,
	}
	m := New(fakeRunner{}, opts)
	ctx := context.Background()

	e, err := m.Save(ctx, 0, Export{Path: "/mnt/raid/data", Pseudo: "/data", Clients: []string{"192.168.1.0/24"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != firstID || e.Access != "RW" || e.Squash != "root_squash" || len(e.Protocols) != 1 {
		t.Errorf("defaults not applied: %+v", e)
	}
	if _, err := m.Save(ctx, 0, Export{Path: "/mnt/other", Pseudo: "/data"}); !valid.Is(err) {
		t.Errorf("duplicate pseudo accepted: %v", err)
	}
	if _, err := m.Save(ctx, 0, Export{Path: "/mnt/x", Clients: []string{"a;b"}}); !valid.Is(err) {
		t.Errorf("bad client accepted: %v", err)
	}

	inc, _ := os.ReadFile(opts.IncludePath)
	for _, want := range []string{"Export_Id = 100;", `Pseudo = "/data";`, "Clients = 192.168.1.0/24;", "Access_Type = None;", "Squash = Root_Squash;"} {
		if !strings.Contains(string(inc), want) {
			t.Errorf("rendered config lacks %q:\n%s", want, inc)
		}
	}
	main, _ := os.ReadFile(opts.MainConf)
	if !strings.Contains(string(main), `%include "`+opts.IncludePath+`"`) {
		t.Errorf("include missing:\n%s", main)
	}

	if err := m.Delete(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := m.Exports(); len(list) != 0 {
		t.Errorf("export not deleted: %+v", list)
	}
}
