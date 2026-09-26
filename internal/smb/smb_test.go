package smb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Phydran6/LocoStor/internal/valid"
)

type fakeRunner struct{ calls []string }

func (f *fakeRunner) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	return nil, nil
}

func newTest(t *testing.T) (*Manager, *fakeRunner, Options) {
	dir := t.TempDir()
	opts := Options{
		StatePath:     filepath.Join(dir, "state.json"),
		IncludePath:   filepath.Join(dir, "locostor.conf"),
		MainConf:      filepath.Join(dir, "smb.conf"),
		SkipPathCheck: true,
	}
	if err := os.WriteFile(opts.MainConf, []byte("[global]\n   workgroup = WORKGROUP\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{}
	return New(r, opts), r, opts
}

func TestSaveRendersAndIncludes(t *testing.T) {
	m, r, opts := newTest(t)
	ctx := context.Background()
	if _, err := m.Save(ctx, "", Share{Name: "Data", Path: "/mnt/raid/data/", ValidUsers: []string{"alice", " "}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// Second save must not add the include line twice.
	if _, err := m.Save(ctx, "Data", Share{Name: "Data2", Path: "/mnt/raid/data", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	main, _ := os.ReadFile(opts.MainConf)
	if n := strings.Count(string(main), "include = "+opts.IncludePath); n != 1 {
		t.Errorf("include line appears %d times:\n%s", n, main)
	}
	inc, _ := os.ReadFile(opts.IncludePath)
	if !strings.Contains(string(inc), "[Data2]") || strings.Contains(string(inc), "[Data]") {
		t.Errorf("rename not applied:\n%s", inc)
	}
	shares, _ := m.Shares()
	if len(shares) != 1 || shares[0].Path != "/mnt/raid/data" {
		t.Errorf("unexpected state: %+v", shares)
	}
	if !strings.HasPrefix(r.calls[0], "testparm") {
		t.Errorf("config not validated first: %v", r.calls)
	}
}

func TestValidation(t *testing.T) {
	m, _, _ := newTest(t)
	ctx := context.Background()
	bad := []Share{
		{Name: "global", Path: "/x"},
		{Name: "a]b", Path: "/x"},
		{Name: "ok", Path: "relative"},
		{Name: "ok", Path: "/x", Comment: "line\n[inject]"},
		{Name: "ok", Path: "/x", ValidUsers: []string{"a b"}},
	}
	for _, s := range bad {
		if _, err := m.Save(ctx, "", s); !valid.Is(err) {
			t.Errorf("Save(%+v) = %v, want validation error", s, err)
		}
	}
	if _, err := m.Save(ctx, "", Share{Name: "A", Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Save(ctx, "", Share{Name: "a", Path: "/y"}); !valid.Is(err) {
		t.Errorf("duplicate name (case-insensitive) accepted: %v", err)
	}
	if err := m.Delete(ctx, "missing"); !valid.IsNotFound(err) {
		t.Errorf("Delete(missing) = %v", err)
	}
}
