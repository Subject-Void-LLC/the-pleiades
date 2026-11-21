// Tests of what `pleiades facts baseline` refuses, through the real binary
// (Phase 41d): a list of hosts is taken whole or not at all, a key pattern
// matching nothing is named, a golden baseline of nothing comparable is
// refused, and a host cannot be its own golden source.
package main_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// TestCLI_FactsBaselineOverHostsIsAllOrNothing: a list whose second host
// has no fact the keys match is refused naming that host and pattern, and no
// host is written, not even the first; the same command once the second
// host has such a fact takes both.
func TestCLI_FactsBaselineOverHostsIsAllOrNothing(t *testing.T) {
	dir, ids := baselineProject(t, "web1", "web2")
	at := time.Now().Add(-time.Hour)
	recordFacts(t, dir, ids["web1"], at, map[string]any{"package.busybox": "1.36", "os.family": "alpine"})
	recordFacts(t, dir, ids["web2"], at, map[string]any{"os.family": "alpine"})
	baselines := func(host string) string {
		return filepath.Join(dir, ".pleiades", "facts", ids[host]+".baselines.jsonl")
	}
	args := []string{"facts", "baseline", "web1,web2", "--name", "pkgs", "--keys", "package.*", "--reason", "r"}
	out, err := runPleiades(t, dir, args...)
	if err == nil || !strings.Contains(out, `host "web2"`) || !strings.Contains(out, `"package.*" matches no stable fact`) || strings.Contains(out, "taken") {
		t.Fatalf("a list whose second host matches nothing: %v\n%s", err, out)
	}
	for _, h := range []string{"web1", "web2"} {
		if _, err := os.Stat(baselines(h)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the refused list wrote %s's baselines: %v", h, err)
		}
	}
	recordFacts(t, dir, ids["web2"], at.Add(time.Minute), map[string]any{"package.busybox": "1.36", "os.family": "alpine"})
	if out, err := runPleiades(t, dir, args...); err != nil || strings.Count(out, `baseline "pkgs" taken`) != 2 {
		t.Fatalf("the corrected rerun: %v\n%s", err, out)
	}
}

// TestCLI_FactsBaselineRefusesAPatternMatchingNothingOrItsOwnSource: one
// --keys pattern that matches nothing among several is refused by name, not
// ignored, and a host named both in --from and among the hosts is refused,
// since a host cannot be its own golden source; neither writes anything.
func TestCLI_FactsBaselineRefusesAPatternMatchingNothingOrItsOwnSource(t *testing.T) {
	dir, ids := baselineProject(t, "web1", "web2")
	for _, h := range []string{"web1", "web2"} {
		recordFacts(t, dir, ids[h], time.Now().Add(-time.Hour), map[string]any{"package.busybox": "1.36", "os.family": "alpine"})
	}
	out, err := runPleiades(t, dir, "facts", "baseline", "web1", "--name", "typo", "--keys", "pacakge.*,os.family", "--reason", "r")
	if err == nil || !strings.Contains(out, `"pacakge.*" matches no stable fact`) {
		t.Errorf("a misspelled pattern beside a good one: %v\n%s", err, out)
	}
	out, err = runPleiades(t, dir, "facts", "baseline", "web1,web2", "--name", "gold", "--from", "web1", "--reason", "r")
	if err == nil || !strings.Contains(out, "cannot be its own golden source") {
		t.Errorf("--from among the hosts: %v\n%s", err, out)
	}
	for _, h := range []string{"web1", "web2"} {
		if got := baselinesOf(t, dir, ids[h]); len(got) != 0 {
			t.Errorf("%s has %d baselines after the refusals, want none", h, len(got))
		}
	}
}

// baselinesOf is device's baselines in the project at dir.
func baselinesOf(t *testing.T, dir, device string) []facts.Baseline {
	t.Helper()
	store, err := facts.OpenFileStore(filepath.Join(dir, ".pleiades", "facts"), projectFactKeys(dir))
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Baselines(t.Context(), device)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestCLI_FactsGoldenBaselineOfNothingComparableIsRefused: a golden baseline
// whose every selected fact holds a digest keyed for its source (a private
// file's) could never read anything but in sync, so it is refused, saying
// how many were not comparable; one beside a comparable fact is taken.
func TestCLI_FactsGoldenBaselineOfNothingComparableIsRefused(t *testing.T) {
	dir, ids := baselineProject(t, "golden", "web1")
	secret, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	keyer, err := facts.NewKeyer(secret, "project")
	if err != nil {
		t.Fatal(err)
	}
	const key = "file.%2Fetc%2Fshadow"
	at := time.Now().Add(-time.Hour)
	for _, h := range []string{"golden", "web1"} {
		sum := sha256.Sum256([]byte("the same content"))
		value := keyer.KeyRaw(ids[h], key, map[string]any{"state": "present", "world_readable": false, facts.RawDigestField: hex.EncodeToString(sum[:])})
		recordFacts(t, dir, ids[h], at, map[string]any{key: value, "os.family": "alpine"})
	}
	out, err := runPleiades(t, dir, "facts", "baseline", "web1", "--name", "files", "--from", "golden", "--keys", "file.*", "--reason", "r")
	if err == nil || !strings.Contains(out, "none of the 1 fact(s) selected can be compared") {
		t.Fatalf("a golden baseline of a private file alone: %v\n%s", err, out)
	}
	if got := baselinesOf(t, dir, ids["web1"]); len(got) != 0 {
		t.Errorf("the refused baseline was written: %+v", got)
	}
	out, err = runPleiades(t, dir, "facts", "baseline", "web1", "--name", "gold", "--from", "golden", "--reason", "r")
	if err != nil || !strings.Contains(out, "over 1 stable fact(s), 1 not comparable") {
		t.Fatalf("a golden baseline with a comparable fact beside it: %v\n%s", err, out)
	}
}
