// Tests for balancing shards by time and for holding the recorded times to
// what a run measured.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// TestShardBy_BalancesByTime: the three longest packages, which dealing by
// position put in one shard, land in three, and the shards' times come out
// close; every package is in exactly one shard whatever order the list
// came in; a package with no time counts as the median.
func TestShardBy_BalancesByTime(t *testing.T) {
	seconds := map[string]float64{"tests/e2e": 900, "cmd/pleiades": 850, "cmd/runner": 470, "internal/runner": 370, "a": 60, "b": 50, "c": 40, "d": 30}
	var packages []string
	for p := range seconds {
		packages = append(packages, modulePrefix+p)
	}
	packages = append(packages, modulePrefix+"new/pkg")
	seen := map[string]int{}
	var loads []float64
	for _, shard := range []string{"1/3", "2/3", "3/3"} {
		owned, err := shardBy(packages, shard, seconds)
		if err != nil {
			t.Fatal(err)
		}
		var load float64
		for _, p := range owned {
			seen[p]++
			if s, ok := seconds[strings.TrimPrefix(p, modulePrefix)]; ok {
				load += s
			}
		}
		loads = append(loads, load)
		heavy := 0
		for _, p := range owned {
			switch strings.TrimPrefix(p, modulePrefix) {
			case "tests/e2e", "cmd/pleiades", "cmd/runner":
				heavy++
			}
		}
		if heavy != 1 {
			t.Errorf("shard %s holds %d of the three longest packages: %v", shard, heavy, owned)
		}
	}
	for _, p := range packages {
		if seen[p] != 1 {
			t.Errorf("%s landed in %d shards", p, seen[p])
		}
	}
	for _, l := range loads {
		if l > 1000 {
			t.Errorf("shard loads %v; the longest is far over the even share", loads)
		}
	}
	reversed := append([]string(nil), packages...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	a, _ := shardBy(packages, "2/3", seconds)
	b, _ := shardBy(reversed, "2/3", seconds)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("the list's order changed the shard: %v against %v", a, b)
	}
	if w := weights([]string{modulePrefix + "x", modulePrefix + "a", modulePrefix + "b", modulePrefix + "c"}, seconds); w[modulePrefix+"x"] != 50 {
		t.Errorf("an unknown package weighs %v, want the median 50", w[modulePrefix+"x"])
	}
}

// TestShardOf_TheEmbeddedTimesSplitEvenly: with durations.json, the six
// containers shards of the packages it names come within a quarter of one
// another, where dealing by position left one at three times the rest,
// apart from one package too long for a shard's share that cannot be
// divided, which sits alone. A shard's time counts a divided package's
// tests it runs: those named, or all but those skipped.
func TestShardOf_TheEmbeddedTimesSplitEvenly(t *testing.T) {
	recorded := durations()
	seen := map[string]bool{}
	var packages []string
	for key := range recorded {
		pkg, _, _ := strings.Cut(key, " ")
		if !seen[pkg] {
			seen[pkg] = true
			packages = append(packages, modulePrefix+pkg)
		}
	}
	split := splitTests(packages, recorded)
	var loads []float64
	for k := 1; k <= 6; k++ {
		pieces, err := piecesOf(packages, fmt.Sprintf("%d/6", k), recorded)
		if err != nil {
			t.Fatal(err)
		}
		var load float64
		for _, p := range pieces {
			tests, divided := split[p.pkg]
			if !divided {
				load += recorded[strings.TrimPrefix(p.pkg, modulePrefix)]
				continue
			}
			for _, test := range tests {
				if (len(p.run) == 0 && !slices.Contains(p.skip, test)) || slices.Contains(p.run, test) {
					load += recorded[testKey(p.pkg, test)]
				}
			}
		}
		loads = append(loads, load)
	}
	// One package longer than a shard's fair share, which statements of its
	// own keep from being divided (cmd/pleiades, about 950 seconds), is a
	// floor no dealing can beat: the shard holding it may be that long, and
	// only if it holds nothing else. Every other shard is held within a
	// quarter of the fastest.
	longest := 0.0
	for key, s := range recorded {
		if !strings.Contains(key, " ") && split[modulePrefix+key] == nil {
			longest = max(longest, s)
		}
	}
	sorted := slices.Clone(loads)
	slices.Sort(sorted)
	lo, hi, second := sorted[0], sorted[len(sorted)-1], sorted[len(sorted)-2]
	if hi > lo*1.25 && hi != longest {
		t.Errorf("shard loads %v seconds: the slowest is more than a quarter over the fastest, and is not one undividable package (%.0f) alone", loads, longest)
	}
	if second > lo*1.25 {
		t.Errorf("shard loads %v seconds: beyond the one longest package, a shard is more than a quarter over the fastest", loads)
	}
}

// measurement writes a -coverage-out file timing seconds.
func measurement(t *testing.T, seconds map[string]float64) string {
	t.Helper()
	data, err := json.Marshal(flakegate.Measurement{Coverage: map[string]float64{}, Seconds: seconds})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "m.json")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestDurationsCheck: a measured package with no recorded time fails,
// naming the line to add; one whose time moved past both bounds is warned
// about and passes; one within them passes quietly.
func TestDurationsCheck(t *testing.T) {
	recorded := map[string]float64{"cmd/pleiades": 800, "pkg/small": 5}
	var out bytes.Buffer
	ok := measurement(t, map[string]float64{modulePrefix + "cmd/pleiades": 900, modulePrefix + "pkg/small": 12})
	if err := durationsCheck(&out, []string{ok}, recorded); err != nil || strings.Contains(out.String(), "refresh") {
		t.Errorf("times within the bounds = %v:\n%s", err, &out)
	}
	out.Reset()
	moved := measurement(t, map[string]float64{modulePrefix + "cmd/pleiades": 2000})
	if err := durationsCheck(&out, []string{moved}, recorded); err != nil || !strings.Contains(out.String(), "cmd/pleiades: recorded 800s, measured 2000s") {
		t.Errorf("a time that moved = %v:\n%s", err, &out)
	}
	added := measurement(t, map[string]float64{modulePrefix + "pkg/new": 312.4})
	err := durationsCheck(&out, []string{added}, recorded)
	if err == nil || !strings.Contains(err.Error(), `"pkg/new": 312,`) {
		t.Errorf("a package with no time = %v", err)
	}
	if err := durationsCheck(&out, nil, recorded); err == nil {
		t.Error("no files was accepted")
	}
	if err := durationsCheck(&out, []string{filepath.Join(t.TempDir(), "absent.json")}, recorded); err == nil {
		t.Error("an absent file was accepted")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(bad, []byte("{"), 0o600)
	if err := durationsCheck(&out, []string{bad}, recorded); err == nil {
		t.Error("a file that is not a measurement was accepted")
	}
}

// TestDurationsCheck_ADividedPackageIsHeldTestByTest: a package
// durations.json times test by test needs no package-level time, since each
// shard timed only its part; its tests are held instead, a test with no time
// failing like a package with none, and a recorded test its measured package
// no longer ran is warned about.
func TestDurationsCheck_ADividedPackageIsHeldTestByTest(t *testing.T) {
	recorded := map[string]float64{"tests/e2e TestA": 300, "tests/e2e TestGone": 40, "pkg/small": 5}
	write := func(m flakegate.Measurement) string {
		t.Helper()
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(t.TempDir(), "m.json")
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	var out bytes.Buffer
	file := write(flakegate.Measurement{Coverage: map[string]float64{},
		Seconds:     map[string]float64{modulePrefix + "tests/e2e": 520, modulePrefix + "pkg/small": 5},
		TestSeconds: map[string]float64{modulePrefix + "tests/e2e TestA": 310, modulePrefix + "tests/e2e TestNew": 204.6}})
	err := durationsCheck(&out, []string{file}, recorded)
	if err == nil || !strings.Contains(err.Error(), `"tests/e2e TestNew": 205,`) {
		t.Errorf("a divided package's untimed test = %v, want the line to add", err)
	}
	if strings.Contains(fmt.Sprint(err), `"tests/e2e":`) {
		t.Errorf("a divided package was asked for a package-level time: %v", err)
	}
	if !strings.Contains(out.String(), "tests/e2e TestGone") {
		t.Errorf("a recorded test the package no longer ran was not named:\n%s", &out)
	}
}
