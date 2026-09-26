//go:build unix

package main

import (
	"log"
	"os"
	"syscall"
)

// reexec replaces the current process with the (possibly updated) binary.
func reexec(exe string) {
	if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
		// Fall back to exiting; systemd (Restart=always) brings us back.
		log.Printf("re-exec failed: %v, exiting", err)
		os.Exit(1)
	}
}
