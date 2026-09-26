//go:build !unix

package main

import "os"

func reexec(string) { os.Exit(0) }
