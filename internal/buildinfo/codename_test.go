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

// TestCodename_ReadsThisBuildsStampedVersion holds Codename to the version
// this binary carries: an unstamped test binary is a development build with
// no name, and one stamped as a release (what -ldflags -X writes into
// version) carries that release's name.
func TestCodename_ReadsThisBuildsStampedVersion(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })

	version = ""
	if got := Codename(); got != "" {
		t.Errorf("an unstamped build's Codename() = %q, want none", got)
	}
	version = "0.3.0"
	if got := Codename(); got != "Paranoid Porpoise" {
		t.Errorf("a build stamped 0.3.0 has Codename() = %q, want %q", got, "Paranoid Porpoise")
	}
	version = "0.2.0"
	if got := Codename(); got != "" {
		t.Errorf("a build stamped with an unnamed release has Codename() = %q, want none", got)
	}
}

// TestCodenameFor reads the name of each spelling of a release, and none for
// an unnamed release or a development version.
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
