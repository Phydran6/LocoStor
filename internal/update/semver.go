package update

import (
	"strconv"
	"strings"
)

type semver struct {
	major, minor, patch int
	pre                 string
	ok                  bool
}

func parseSemver(v string) semver {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "+")
	core, pre, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}
	}
	var n [3]int
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 {
			return semver{}
		}
		n[i] = x
	}
	return semver{n[0], n[1], n[2], pre, true}
}

// Newer reports whether version a is newer than b. Unparseable versions
// (e.g. "dev") are treated as older than any release.
func Newer(a, b string) bool {
	x, y := parseSemver(a), parseSemver(b)
	if !x.ok {
		return false
	}
	if !y.ok {
		return true
	}
	if x.major != y.major {
		return x.major > y.major
	}
	if x.minor != y.minor {
		return x.minor > y.minor
	}
	if x.patch != y.patch {
		return x.patch > y.patch
	}
	// A release is newer than its pre-releases.
	if x.pre == "" || y.pre == "" {
		return x.pre == "" && y.pre != ""
	}
	return x.pre > y.pre
}
