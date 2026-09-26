// Package valid provides input validation helpers and a typed error that
// the API maps to HTTP 400.
package valid

import (
	"errors"
	"fmt"
	"path"
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
