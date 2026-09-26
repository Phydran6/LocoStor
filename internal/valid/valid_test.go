package valid

import "testing"

func TestSharePath(t *testing.T) {
	for _, p := range []string{"/", "/etc", "/etc/samba", "/root", "/var", "/var/lib/locostor/x", "/proc/1", "relative", "/a\nb", `/a"b`} {
		if _, err := SharePath("path", p, false); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
	for _, p := range []string{"/mnt/raid", "/srv/nas2/data", "/var/data", "/home/alice", "/etcetera"} {
		if _, err := SharePath("path", p, false); err != nil {
			t.Errorf("%q rejected: %v", p, err)
		}
	}
	if got, _ := SharePath("path", "/mnt/raid//x/../y/", false); got != "/mnt/raid/y" {
		t.Errorf("path not cleaned: %q", got)
	}
}
