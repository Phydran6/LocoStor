// Package valid provides input validation helpers and a typed error that
// the API maps to HTTP 400.
package valid

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Error is a user-facing validation error.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

// Errorf creates a validation error.
func Errorf(format string, args ...any) error {
	return &Error{Msg: fmt.Sprintf(format, args...)}
}

// Is reports whether err is a validation error.
func Is(err error) bool {
	var e *Error
	return errors.As(err, &e)
}

// NotFound is returned when a named item does not exist.
type NotFound struct{ What string }

func (e *NotFound) Error() string { return e.What + " not found" }

// IsNotFound reports whether err is a NotFound error.
func IsNotFound(err error) bool {
	var e *NotFound
	return errors.As(err, &e)
}

// SingleLine rejects values containing control characters, which could
// otherwise inject extra lines into generated config files.
func SingleLine(field, v string) error {
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return Errorf("%s must not contain control characters", field)
		}
	}
	return nil
}

// AbsPath validates and cleans an absolute filesystem path.
func AbsPath(field, p string) (string, error) {
	if p == "" {
		return "", Errorf("%s is required", field)
	}
	if err := SingleLine(field, p); err != nil {
		return "", err
	}
	if strings.ContainsAny(p, "\"\\") {
		return "", Errorf("%s must not contain quotes or backslashes", field)
	}
	if !strings.HasPrefix(p, "/") {
		return "", Errorf("%s must be an absolute path", field)
	}
	return path.Clean(p), nil
}

// Directories that must never be shared: the system itself and LocoStor's
// own state. Sharing them could expose secrets or break the container.
var forbiddenRoots = []string{
	"/etc", "/proc", "/sys", "/dev", "/boot", "/root", "/run", "/usr", "/bin", "/sbin",
	"/lib", "/lib32", "/lib64", "/libx32", "/var/lib/locostor", "/var/lib/samba", "/var/log",
}

// forbiddenExact may not be shared themselves, but their subdirectories may.
var forbiddenExact = map[string]bool{"/": true, "/var": true, "/var/lib": true}

func forbidden(p string) bool {
	if forbiddenExact[p] {
		return true
	}
	for _, root := range forbiddenRoots {
		if p == root || strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	return false
}

// SharePath validates a directory to be shared over SMB or NFS. With
// mustExist it must be an existing directory, and symlinks are resolved so
// they cannot point into a forbidden location.
func SharePath(field, p string, mustExist bool) (string, error) {
	p, err := AbsPath(field, p)
	if err != nil {
		return "", err
	}
	if forbidden(p) {
		return "", Errorf("%s %s is a system directory and cannot be shared", field, p)
	}
	if !mustExist {
		return p, nil
	}
	fi, err := os.Stat(p)
	if err != nil || !fi.IsDir() {
		return "", Errorf("%s %s does not exist or is not a directory", field, p)
	}
	if real, err := filepath.EvalSymlinks(p); err == nil && forbidden(filepath.ToSlash(real)) {
		return "", Errorf("%s %s points to the system directory %s", field, p, real)
	}
	return p, nil
}
