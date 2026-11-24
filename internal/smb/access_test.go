package smb

import (
	"reflect"
	"strings"
	"testing"
)

func render(t *testing.T, s Share) string {
	t.Helper()
	if err := validateShare(&s, false); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	writeShare(&b, s, nil)
	return b.String()
}

func TestAccessRendering(t *testing.T) {
	cases := []struct {
		name string
		s    Share
		want []string
		not  []string
	}{
		{"all write", Share{Everyone: LevelWrite}, []string{"read only = no", "guest ok = no"}, []string{"valid users", "write list"}},
		{"all read, one writer, one denied",
			Share{Everyone: LevelRead, Access: []ACE{{"alice", LevelWrite}, {"@kids", LevelDeny}}},
			[]string{"read only = yes", "write list = alice", "invalid users = @kids"}, []string{"valid users"}},
		{"all write, one reader",
			Share{Everyone: LevelWrite, Access: []ACE{{"bob", LevelRead}}},
			[]string{"read only = no", "read list = bob"}, []string{"write list", "valid users"}},
		{"only listed",
			Share{Everyone: LevelNone, Access: []ACE{{"alice", LevelWrite}, {"bob", LevelRead}, {"@domain users", LevelRead}}},
			[]string{"read only = yes", `valid users = alice bob "@domain users"`, "write list = alice"}, nil},
		{"guest writes",
			Share{Everyone: LevelRead, Guest: LevelWrite},
			[]string{"guest ok = yes", "write list = nobody"}, nil},
		{"fix permissions",
			Share{Everyone: LevelWrite, FixPermissions: true},
			[]string{"force group = locostor", "create mask = 0664", "directory mask = 0775"}, nil},
		{"fix permissions keeps own mask",
			Share{Everyone: LevelWrite, FixPermissions: true, Options: []Option{{"create mask", "0660"}}},
			[]string{"force group = locostor", "create mask = 0660", "directory mask = 0775"}, []string{"0664"}},
	}
	for _, c := range cases {
		c.s.Name, c.s.Path, c.s.Enabled = "Data", "/srv/data", true
		got := render(t, c.s)
		for _, w := range c.want {
			if !strings.Contains(got, "   "+w+"\n") {
				t.Errorf("%s: missing %q in\n%s", c.name, w, got)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, "   "+n) {
				t.Errorf("%s: unexpected %q in\n%s", c.name, n, got)
			}
		}
	}
}

// Whatever is rendered must parse back to the same settings.
func TestAccessRoundTrip(t *testing.T) {
	for _, s := range []Share{
		{Everyone: LevelRead, Access: []ACE{{"alice", LevelWrite}, {"@kids", LevelDeny}}, Guest: LevelRead},
		{Everyone: LevelWrite, Access: []ACE{{"bob", LevelRead}}, FixPermissions: true},
		{Everyone: LevelNone, Access: []ACE{{"alice", LevelWrite}, {"@domain users", LevelRead}}, Guest: LevelWrite},
		{Everyone: LevelNone, Access: []ACE{{"alice", LevelRead}}},
	} {
		s.Name, s.Path, s.Enabled, s.Browseable = "Data", "/srv/data", true, true
		if s.Guest == "" {
			s.Guest = LevelNone
		}
		text := render(t, s)
		secs, _ := parseSections(text, "x")
		got := shareFromSection(secs[0])
		if got.Everyone != s.Everyone || got.Guest != s.Guest || got.FixPermissions != s.FixPermissions || !reflect.DeepEqual(got.Access, s.Access) {
			t.Errorf("round trip changed access:\n%s\nwant %+v %+v %s\ngot  %+v %+v %s", text, s.Everyone, s.Access, s.Guest, got.Everyone, got.Access, got.Guest)
		}
		if len(got.Options) != 0 {
			t.Errorf("own parameters came back as options: %+v", got.Options)
		}
	}
}

func TestLegacyFieldsConverted(t *testing.T) {
	s := Share{Name: "Old", Path: "/srv/old", ReadOnly: false, ValidUsers: []string{"alice", "bob"}, GuestOK: false}
	if err := validateShare(&s, false); err != nil {
		t.Fatal(err)
	}
	if s.Everyone != LevelNone || len(s.Access) != 2 || s.Access[0].Level != LevelWrite {
		t.Errorf("legacy share converted wrong: %+v", s)
	}
	s = Share{Name: "Pub", Path: "/srv/pub", ReadOnly: true, GuestOK: true}
	if err := validateShare(&s, false); err != nil {
		t.Fatal(err)
	}
	if s.Everyone != LevelRead || s.Guest != LevelRead || !s.ReadOnly {
		t.Errorf("legacy public share converted wrong: %+v", s)
	}
}

func TestSplitList(t *testing.T) {
	got := splitList(`alice, bob  "@Domain Users",@staff`)
	want := []string{"alice", "bob", "@Domain Users", "@staff"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
}
