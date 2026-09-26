//go:build linux

package sysinfo

import (
	"sort"
	"syscall"
)

func (r Reader) filesystems() []Filesystem {
	fss := []Filesystem{}
	for _, m := range parseMountinfo(r.read("self/mountinfo")) {
		var st syscall.Statfs_t
		if err := syscall.Statfs(m[0], &st); err != nil {
			continue
		}
		bs := int64(st.Bsize)
		size := int64(st.Blocks) * bs
		if size == 0 {
			continue
		}
		fss = append(fss, Filesystem{
			Mount:  m[0],
			Source: m[1],
			Type:   m[2],
			Size:   size,
			Used:   size - int64(st.Bfree)*bs,
			Avail:  int64(st.Bavail) * bs,
		})
	}
	sort.Slice(fss, func(i, j int) bool { return fss[i].Mount < fss[j].Mount })
	return fss
}
