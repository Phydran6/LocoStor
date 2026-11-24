package system

import "testing"

func TestParseUpgradable(t *testing.T) {
	pkgs := ParseUpgradable(demoUpgradable + "\nlibsmbclient/noble-updates,noble-security 2:4.19.5 amd64 [upgradable from: 2:4.19.4]\n")
	if len(pkgs) != 5 {
		t.Fatalf("got %+v", pkgs)
	}
	sec := 0
	for _, p := range pkgs {
		if p.Security {
			sec++
		}
	}
	if sec != 3 || !pkgs[0].Security || pkgs[0].From == "" {
		t.Errorf("security detection: %+v", pkgs)
	}
}
