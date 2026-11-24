package smb

import (
	"regexp"
	"strings"

	"github.com/Phydran6/LocoStor/internal/valid"
)

// Access levels of a share. "deny" is only valid for single entries.
const (
	LevelNone  = "none"
	LevelRead  = "read"
	LevelWrite = "write"
	LevelDeny  = "deny"
)

// ShareGroup is the Unix group LocoStor uses so the filesystem never blocks
// what the share allows: with "fix permissions" every SMB session runs with
// this group (force group) and the share folder grants it read/write.
// Samba itself enforces read, write and deny per user.
const ShareGroup = "locostor"

// GuestAccount is Samba's default guest account.
const GuestAccount = "nobody"

// ACE grants one user or group (@name) a level on a share.
type ACE struct {
	Name  string `json:"name"`
	Level string `json:"level"` // read, write, deny
}

var aceNameRe = regexp.MustCompile(`^[@+&]?[A-Za-z0-9_.$\\-][A-Za-z0-9_.$\\ -]{0,127}$`)

func validLevel(l string, entry bool) bool {
	switch l {
	case LevelRead, LevelWrite:
		return true
	case LevelNone:
		return !entry
	case LevelDeny:
		return entry
	}
	return false
}

// normalizeAccess fills the access fields from the legacy read_only /
// valid_users / guest_ok fields (older clients and state files), validates
// them and sets the legacy fields again so old clients still read them right.
func normalizeAccess(s *Share) error {
	if s.Everyone == "" {
		ro := s.ReadOnly
		lvl := LevelWrite
		if ro {
			lvl = LevelRead
		}
		if len(s.Access) == 0 {
			if len(s.ValidUsers) > 0 {
				s.Everyone = LevelNone
				for _, u := range s.ValidUsers {
					if u = strings.TrimSpace(u); u != "" {
						s.Access = append(s.Access, ACE{Name: u, Level: lvl})
					}
				}
			} else {
				s.Everyone = lvl
			}
		} else {
			s.Everyone = LevelNone
		}
		if s.Guest == "" && s.GuestOK {
			s.Guest = lvl
		}
	}
	if s.Guest == "" {
		s.Guest = LevelNone
	}
	if !validLevel(s.Everyone, false) {
		return valid.Errorf("access for all users must be write, read or none")
	}
	if !validLevel(s.Guest, false) {
		return valid.Errorf("guest access must be write, read or none")
	}
	seen := map[string]bool{}
	list := make([]ACE, 0, len(s.Access))
	for _, a := range s.Access {
		a.Name = strings.TrimSpace(a.Name)
		if a.Name == "" {
			continue
		}
		if !aceNameRe.MatchString(a.Name) || strings.HasSuffix(a.Name, " ") {
			return valid.Errorf("invalid user or group %q", a.Name)
		}
		if strings.EqualFold(a.Name, GuestAccount) {
			return valid.Errorf("use the guest setting instead of listing %q", a.Name)
		}
		if !validLevel(a.Level, true) {
			return valid.Errorf("access of %q must be read, write or deny", a.Name)
		}
		k := strings.ToLower(a.Name)
		if seen[k] {
			return valid.Errorf("%q is listed twice", a.Name)
		}
		seen[k] = true
		list = append(list, a)
	}
	s.Access = list
	if s.Everyone == LevelNone && s.Guest == LevelNone {
		any := false
		for _, a := range list {
			if a.Level != LevelDeny {
				any = true
			}
		}
		if !any {
			return valid.Errorf("nobody could open this share - give a user or group access, or allow all users")
		}
	}

	// Legacy fields, derived.
	s.GuestOK = s.Guest != LevelNone
	s.ReadOnly = !s.Writable()
	s.ValidUsers = []string{}
	if s.Everyone == LevelNone {
		for _, a := range list {
			if a.Level != LevelDeny {
				s.ValidUsers = append(s.ValidUsers, a.Name)
			}
		}
	}
	return nil
}

// Writable reports whether anybody may write to the share.
func (s Share) Writable() bool {
	if s.Everyone == LevelWrite || s.Guest == LevelWrite {
		return true
	}
	for _, a := range s.Access {
		if a.Level == LevelWrite {
			return true
		}
	}
	return false
}

// quoteList renders a Samba user list; names with spaces are quoted.
func quoteList(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		if strings.ContainsAny(n, " \t") {
			n = `"` + n + `"`
		}
		out[i] = n
	}
	return strings.Join(out, " ")
}

// splitList parses a Samba user list: entries are separated by spaces or
// commas, double quotes group an entry with spaces.
func splitList(v string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for _, r := range v {
		switch {
		case r == '"':
			quoted = !quoted
		case !quoted && (r == ' ' || r == ',' || r == '\t'):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// accessLines returns the smb.conf parameters that implement the access
// settings: read only, guest ok, valid users, read list, write list and
// invalid users.
func accessLines(s Share) []Option {
	var read, write, deny, validU []string
	for _, a := range s.Access {
		switch a.Level {
		case LevelRead:
			read = append(read, a.Name)
			validU = append(validU, a.Name)
		case LevelWrite:
			write = append(write, a.Name)
			validU = append(validU, a.Name)
		case LevelDeny:
			deny = append(deny, a.Name)
		}
	}
	switch s.Guest {
	case LevelRead:
		read = append(read, GuestAccount)
		validU = append(validU, GuestAccount)
	case LevelWrite:
		write = append(write, GuestAccount)
		validU = append(validU, GuestAccount)
	}
	opts := []Option{
		{"read only", yesno(s.Everyone != LevelWrite)},
		{"guest ok", yesno(s.Guest != LevelNone)},
	}
	if s.Everyone == LevelNone && len(validU) > 0 {
		opts = append(opts, Option{"valid users", quoteList(validU)})
	}
	// With "read only = no" only the read list matters, otherwise the
	// write list. Listing the other one would be a no-op.
	if s.Everyone == LevelWrite {
		if len(read) > 0 {
			opts = append(opts, Option{"read list", quoteList(read)})
		}
	} else if len(write) > 0 {
		opts = append(opts, Option{"write list", quoteList(write)})
	}
	if len(deny) > 0 {
		opts = append(opts, Option{"invalid users", quoteList(deny)})
	}
	return opts
}

// permissionLines are added when Samba should make sure the filesystem
// allows what the share allows. Options the user set win.
func permissionLines(s Share) []Option {
	if !s.FixPermissions {
		return nil
	}
	have := map[string]bool{}
	for _, o := range s.Options {
		have[normKey(o.Key)] = true
	}
	opts := []Option{{"force group", ShareGroup}}
	if !have["createmask"] && !have["createmode"] {
		opts = append(opts, Option{"create mask", "0664"})
	}
	if !have["directorymask"] && !have["directorymode"] {
		opts = append(opts, Option{"directory mask", "0775"})
	}
	return opts
}

// accessFromParams derives the access settings from smb.conf parameters,
// following Samba's rules (read only defaults to yes).
type rawAccess struct {
	readOnly           bool
	guestOK            bool
	valid, read, write []string
	invalid            []string
	forceGroup         string
}

func (r rawAccess) apply(s *Share) {
	isGuest := func(n string) bool { return strings.EqualFold(n, GuestAccount) }
	base := LevelWrite
	if r.readOnly {
		base = LevelRead
	}
	s.Access = []ACE{}
	idx := map[string]int{}
	set := func(name, level string) {
		k := strings.ToLower(name)
		if i, ok := idx[k]; ok {
			s.Access[i].Level = level
			return
		}
		idx[k] = len(s.Access)
		s.Access = append(s.Access, ACE{Name: name, Level: level})
	}
	inValid := map[string]bool{}
	if len(r.valid) == 0 {
		s.Everyone = base
	} else {
		s.Everyone = LevelNone
		for _, v := range r.valid {
			inValid[strings.ToLower(v)] = true
			if !isGuest(v) {
				set(v, base)
			}
		}
	}
	allowed := func(n string) bool { return len(r.valid) == 0 || inValid[strings.ToLower(n)] }
	guest := base
	for _, n := range r.read {
		if isGuest(n) {
			guest = LevelRead
		} else if allowed(n) {
			set(n, LevelRead)
		}
	}
	for _, n := range r.write {
		if isGuest(n) {
			guest = LevelWrite
		} else if allowed(n) {
			set(n, LevelWrite)
		}
	}
	for _, n := range r.invalid {
		if !isGuest(n) {
			set(n, LevelDeny)
		}
	}
	s.Guest = LevelNone
	if r.guestOK {
		s.Guest = guest
	}
	s.FixPermissions = strings.EqualFold(strings.TrimPrefix(r.forceGroup, "+"), ShareGroup)
}
