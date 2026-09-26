// Package sysexec wraps external command execution so it can be faked in
// tests and demo mode.
package sysexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Runner executes external commands.
type Runner interface {
	// Run executes name with args, feeding stdin (if non-empty). It returns
	// stdout even when the command fails, because tools like smartctl report
	// results through non-zero exit codes.
	Run(ctx context.Context, stdin, name string, args ...string) ([]byte, error)
}

// Error is returned when a command exits unsuccessfully.
type Error struct {
	Cmd      string
	ExitCode int
	Stderr   string
	Err      error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if e.ExitCode > 0 {
		return fmt.Sprintf("%s: exit %d: %s", e.Cmd, e.ExitCode, msg)
	}
	return fmt.Sprintf("%s: %s", e.Cmd, msg)
}

func (e *Error) Unwrap() error { return e.Err }

// ExitCode returns the exit code carried by err, or -1 if unknown.
func ExitCode(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.ExitCode
	}
	return -1
}

// OS runs real commands.
type OS struct{}

// Run implements Runner.
func (OS) Run(ctx context.Context, stdin, name string, args ...string) ([]byte, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return stdout.Bytes(), &Error{Cmd: name, ExitCode: code, Stderr: stderr.String(), Err: err}
	}
	return stdout.Bytes(), nil
}

// ServiceState returns the systemd state of unit ("active", "inactive",
// "failed", ...) or "unknown" if systemctl is unavailable.
func ServiceState(ctx context.Context, r Runner, unit string) string {
	out, _ := r.Run(ctx, "", "systemctl", "is-active", unit)
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "unknown"
	}
	return s
}
