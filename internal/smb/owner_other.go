//go:build !unix

package smb

import "os"

func fileOwner(os.FileInfo) (owner, group string) { return "?", "?" }
