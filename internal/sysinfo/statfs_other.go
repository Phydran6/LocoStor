//go:build !linux

package sysinfo

// filesystems is only supported on Linux; other platforms are used for
// development in demo mode.
func (r Reader) filesystems() []Filesystem {
	_ = parseMountinfo
	return []Filesystem{}
}
