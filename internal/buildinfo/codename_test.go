// Package buildinfo tests the release codename table: its names follow the rule
// and a development build has none.
package buildinfo

import "testing"

// TestCodenames_FollowTheRule: every name is one adjective and one animal with
// the same first letter, every key is a release, and no two share a name.
func TestCodenames_FollowTheRule(t *testing.T) {
	seen := map[string]string{}
	for release, name := range Codenames {
		if got, ok := Release(release); !ok || got != release {
			t.Errorf("key %q is not a bare release", release)
		}
		if !validCodename(name) {
			t.Errorf("%s: %q is not two words with the same first letter", release, name)
		}
		if other, dup := seen[name]; dup {
			t.Errorf("%q names both %s and %s", name, other, release)
		}
		seen[name] = release
	}
	for _, bad := range []string{"", "Porpoise", "Paranoid  Porpoise", "Paranoid Walrus", "Three Word Name", "1st 1ine"} {
		if validCodename(bad) {
			t.Errorf("%q passed the rule", bad)
		}
	}
}

func TestCodenameFor(t *testing.T) {
	for v, want := range map[string]string{
		"0.3.0": "Paranoid Porpoise", "v0.3.0": "Paranoid Porpoise", "0.3.0-rc1": "Paranoid Porpoise",
		"0.2.0": "", "0.0.0-dev+abc1234": "", "dev": "", "": "",
	} {
		if got := CodenameFor(v); got != want {
			t.Errorf("CodenameFor(%q) = %q, want %q", v, got, want)
		}
	}
}
