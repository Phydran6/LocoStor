package smb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Phydran6/LocoStor/internal/sysexec"
	"github.com/Phydran6/LocoStor/internal/tasks"
)

// NeedsPermissions reports whether saving next (previously prev, nil for a
// new share) should adjust the folder permissions.
func NeedsPermissions(prev *Share, next Share) bool {
	if !next.FixPermissions {
		return false
	}
	return prev == nil || !prev.FixPermissions || prev.Path != next.Path
}

// EnsureGroup creates the LocoStor share group if it does not exist.
func EnsureGroup(ctx context.Context, run sysexec.Runner) error {
	if _, err := run.Run(ctx, "", "getent", "group", ShareGroup); err == nil {
		return nil
	}
	if _, err := run.Run(ctx, "", "groupadd", "--system", ShareGroup); err != nil {
		return fmt.Errorf("create group %s: %w", ShareGroup, err)
	}
	return nil
}

// ApplyPermissions lets the LocoStor group read and write everything below
// path, so Samba's read/write/deny rules are the only thing that counts.
// POSIX ACLs are used where the filesystem supports them (ownership and
// modes stay as they are, new files inherit the rights); otherwise the
// group ownership and group bits are changed.
func ApplyPermissions(t *tasks.Task, run sysexec.Runner, path string) error {
	ctx := t.Context()
	fi, err := os.Stat(path)
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	t.Section("Group " + ShareGroup)
	if err := EnsureGroup(ctx, run); err != nil {
		return err
	}
	t.Logf("SMB sessions on shares with fixed permissions use the group %q (force group).", ShareGroup)

	t.Section("Permissions below " + path)
	t.Logf("This can take a while on folders with many files.")
	spec := "g:" + ShareGroup + ":rwX,d:g:" + ShareGroup + ":rwX"
	if err := t.Exec(nil, "setfacl", "-R", "-m", spec, path); err != nil {
		t.Logf("ACLs are not available here (%v) - changing group and group permissions instead.", err)
		if err := t.Exec(nil, "chgrp", "-R", ShareGroup, path); err != nil {
			return err
		}
		if err := t.Exec(nil, "chmod", "-R", "g+rwX", path); err != nil {
			return err
		}
		// New files and folders keep the group.
		if err := t.Exec(nil, "find", path, "-type", "d", "-exec", "chmod", "g+s", "{}", "+"); err != nil {
			return err
		}
	}

	// Every parent folder must at least be passable.
	t.Section("Parent folders")
	for dir := filepath.Dir(path); dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		fi, err := os.Stat(dir)
		if err != nil {
			continue
		}
		if fi.Mode().Perm()&0o001 != 0 {
			continue
		}
		if err := t.Exec(nil, "setfacl", "-m", "g:"+ShareGroup+":x", dir); err != nil {
			if err := t.Exec(nil, "chmod", "o+x", dir); err != nil {
				return err
			}
		}
	}
	t.Logf("Parent folders can be passed.")
	return nil
}

// pathWarnings explains why a share may not work as configured.
func pathWarnings(s Share) []string {
	if s.Path == "" {
		return nil
	}
	fi, err := os.Stat(s.Path)
	if err != nil {
		return []string{"the folder " + s.Path + " does not exist"}
	}
	if !fi.IsDir() {
		return []string{s.Path + " is not a folder"}
	}
	if !s.Writable() || s.FixPermissions {
		return nil
	}
	mode := fi.Mode().Perm()
	if mode&0o002 != 0 {
		return nil
	}
	owner, group := fileOwner(fi)
	if strings.EqualFold(group, ShareGroup) && mode&0o020 != 0 {
		return nil
	}
	return []string{fmt.Sprintf("the folder belongs to %s:%s (%04o) - SMB users may not be able to write. Turn on \"Fix folder permissions\".", owner, group, mode)}
}

// StartPermissions adjusts the folder permissions of s in a background
// task. With mustExist false (demo mode) the folder is not checked.
func StartPermissions(tm *tasks.Manager, run sysexec.Runner, s Share, mustExist bool) (*tasks.Task, error) {
	return tm.Start("permissions:"+s.Path, "Folder permissions for "+s.Name, func(t *tasks.Task) error {
		if !mustExist {
			t.Logf("demo mode - nothing is changed")
			return nil
		}
		return ApplyPermissions(t, run, s.Path)
	})
}
