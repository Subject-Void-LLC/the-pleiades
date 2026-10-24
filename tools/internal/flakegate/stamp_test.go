// Tests for a record's stamp: what testgate and prreport step accept as
// the commit a run began at and whether its tree was clean, and that the
// stamp survives a record's round trip through its file.
package flakegate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sha1Commit and sha256Commit are full object names in the two hashes git
// can use.
var (
	sha1Commit   = strings.Repeat("ab", 20)
	sha256Commit = strings.Repeat("cd", 32)
)

// TestParseStamp: both empty is no stamp; a full hash with yes or no is one;
// either alone, a short or uppercase hash, a flag-like value and a clean
// that is not yes or no are each refused, saying which.
func TestParseStamp(t *testing.T) {
	for name, tc := range map[string]struct {
		commit, clean string
		want          Stamp
	}{
		"no stamp":         {"", "", Stamp{}},
		"clean, sha-1":     {sha1Commit, "yes", Stamp{Commit: sha1Commit, Clean: true}},
		"dirty, sha-256":   {sha256Commit, "no", Stamp{Commit: sha256Commit}},
		"dirty, sha-1 too": {sha1Commit, "no", Stamp{Commit: sha1Commit}},
	} {
		got, err := ParseStamp(tc.commit, tc.clean)
		if err != nil || got != tc.want {
			t.Errorf("%s: %+v %v, want %+v", name, got, err, tc.want)
		}
	}
	for name, tc := range map[string]struct{ commit, clean, want string }{
		"a commit alone":     {sha1Commit, "", "go together"},
		"a clean alone":      {"", "yes", "go together"},
		"a short hash":       {sha1Commit[:12], "yes", "not a full commit hash"},
		"an uppercase hash":  {strings.ToUpper(sha1Commit), "yes", "not a full commit hash"},
		"a flag for a hash":  {"-clean", "yes", "not a full commit hash"},
		"a ref for a hash":   {"HEAD", "yes", "not a full commit hash"},
		"a clean of true":    {sha1Commit, "true", "yes or no"},
		"a clean in capital": {sha1Commit, "Yes", "yes or no"},
	} {
		if _, err := ParseStamp(tc.commit, tc.clean); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want it refused saying %q", name, err, tc.want)
		}
	}
}

// TestRun_TheStampRoundTrips: a stamped record reads back with its commit
// and cleanliness, and an unstamped one carries neither field at all, so an
// older reader sees nothing new.
func TestRun_TheStampRoundTrips(t *testing.T) {
	dir := t.TempDir()
	stamped := filepath.Join(dir, "stamped.json")
	if err := WriteRun(stamped, Run{Verdict: VerdictPass, Commit: sha1Commit, Clean: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(stamped)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ReadRun(data)
	if err != nil || back.Commit != sha1Commit || !back.Clean {
		t.Errorf("a stamped record read back as %+v (%v)", back, err)
	}
	bare := filepath.Join(dir, "bare.json")
	if err := WriteRun(bare, Run{Verdict: VerdictPass}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(bare)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"commit"`) || strings.Contains(string(data), `"clean"`) {
		t.Errorf("an unstamped record carries stamp fields:\n%s", data)
	}
}
