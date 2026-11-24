//go:build unix

package smb

import (
	"os"
	"os/user"
	"strconv"
	"syscall"
)

func fileOwner(fi os.FileInfo) (owner, group string) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "?", "?"
	}
	owner, group = strconv.Itoa(int(st.Uid)), strconv.Itoa(int(st.Gid))
	if u, err := user.LookupId(owner); err == nil {
		owner = u.Username
	}
	if g, err := user.LookupGroupId(group); err == nil {
		group = g.Name
	}
	return owner, group
}
