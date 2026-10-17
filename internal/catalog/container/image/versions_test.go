// Pure tests of the version order, the prune plan and the reference
// checks.
package image

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// versionTags are tags of the shapes registries hold, and the order GNU
// sort -V gave them on 2026-10-17 (coreutils 9.4).
var versionTags = []string{"1.10", "1.9", "1.2.3-alpine", "1.2.3", "latest", "1.2.3-rc1", "v2", "v10", "1.0~rc1", "1.0",
	"0.9.128", "0.9.127", "dev", "2.0.beta", "2.0", "1.28.1-alpine", "1.28.0-alpine", "010", "9", "a", "b", "1.2a", "1.2"}

var versionOrder = []string{"0.9.127", "0.9.128", "1.0~rc1", "1.0", "1.2", "1.2a", "1.2.3", "1.2.3-alpine", "1.2.3-rc1", "1.9",
	"1.10", "1.28.0-alpine", "1.28.1-alpine", "2.0", "2.0.beta", "9", "010", "a", "b", "dev", "latest", "v2", "v10"}

// TestCompareVersions_IsSortV: the tags sort as GNU sort -V sorted them,
// and, where sort is on this machine, as it sorts them now.
func TestCompareVersions_IsSortV(t *testing.T) {
	got := slices.Clone(versionTags)
	slices.SortFunc(got, compareVersions)
	if !slices.Equal(got, versionOrder) {
		t.Errorf("order = %v\nwant    %v", got, versionOrder)
	}
	if _, err := exec.LookPath("sort"); err != nil {
		return
	}
	cmd := exec.Command("sort", "-V")
	cmd.Stdin = strings.NewReader(strings.Join(versionTags, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil || !strings.Contains(string(out), "\n") {
		return // a sort without -V (BSD's older ones)
	}
	if live := strings.Fields(string(out)); !slices.Equal(got, live) {
		t.Errorf("order = %v\nsort -V %v", got, live)
	}
}

// TestCompareVersions_Antisymmetric: a before b exactly when b is after a,
// and nothing compares equal but itself.
func TestCompareVersions_Antisymmetric(t *testing.T) {
	for _, a := range versionTags {
		for _, b := range versionTags {
			ab, ba := compareVersions(a, b), compareVersions(b, a)
			if (ab < 0) != (ba > 0) || (ab == 0) != (a == b) {
				t.Errorf("compare(%q, %q) = %d, compare(%q, %q) = %d", a, b, ab, b, a, ba)
			}
		}
	}
}

// TestPlanPrune: each matching repository keeps its newest, an older tag
// of an image a container uses is kept apart, and a repository the
// pattern does not match is left out.
func TestPlanPrune(t *testing.T) {
	byRepo := map[string][]tagged{
		"app": {
			{ref: "app:1.2", tag: "1.2", id: "old"}, {ref: "app:1.10", tag: "1.10", id: "new"},
			{ref: "app:1.9", tag: "1.9", id: "mid"}, {ref: "app:1.1", tag: "1.1", id: "used"},
		},
		"ghcr.io/org/app": {{ref: "ghcr.io/org/app:1", tag: "1", id: "x"}, {ref: "ghcr.io/org/app:2", tag: "2", id: "y"}},
		"other":           {{ref: "other:1", tag: "1", id: "z"}, {ref: "other:2", tag: "2", id: "w"}},
	}
	plan := planPrune(byRepo, "app", 2, map[string][]string{"used": {"c"}})
	if !slices.Equal(plan.keep, []string{"app:1.10", "app:1.9"}) || !slices.Equal(plan.keepInUse, []string{"app:1.1"}) || !slices.Equal(plan.remove, []string{"app:1.2"}) {
		t.Errorf("plan = %+v", plan)
	}
	plan = planPrune(byRepo, "ghcr.io/org/*", 0, nil)
	if !slices.Equal(plan.remove, []string{"ghcr.io/org/app:2", "ghcr.io/org/app:1"}) || len(plan.keep) != 0 {
		t.Errorf("plan with keep 0 = %+v", plan)
	}
	if plan := planPrune(byRepo, "nothing*", 1, nil); len(plan.keep)+len(plan.remove) != 0 {
		t.Errorf("a pattern matching nothing planned %+v", plan)
	}
}

// TestHolds: the registry's digest is the local image's when it is the
// image's ID or one of its repository digests for that repository only.
func TestHolds(t *testing.T) {
	d := "sha256:" + strings.Repeat("a", 64)
	other := "sha256:" + strings.Repeat("b", 64)
	for _, tc := range []struct {
		name   string
		id     string
		digest []string
		repo   string
		want   bool
	}{
		{"its ID", d, nil, "app", true},
		{"a repository digest", other, []string{"app@" + d}, "app", true},
		{"another repository's digest", other, []string{"lib@" + d}, "app", false},
		{"no match", other, []string{"app@" + other}, "app", false},
		{"a malformed digest", other, []string{"::"}, "app", false},
	} {
		if got := holds(tc.id, tc.digest, tc.repo, d); got != tc.want {
			t.Errorf("%s: holds = %v", tc.name, got)
		}
	}
}

// TestReferences: tags are filled in and digests refused where a tag is
// needed; IDs pass where an image may be named either way; option-shaped
// and malformed references are refused.
func TestReferences(t *testing.T) {
	if got, err := taggedRef("tag", "nginx"); err != nil || got != "nginx:latest" {
		t.Errorf("taggedRef(nginx) = %q, %v", got, err)
	}
	if got, err := taggedRef("tag", "ghcr.io/org/app:1.2"); err != nil || got != "ghcr.io/org/app:1.2" {
		t.Errorf("taggedRef(ghcr) = %q, %v", got, err)
	}
	for _, bad := range []string{"nginx@sha256:" + strings.Repeat("a", 64), "-x", "UPPER:1", "a b", ""} {
		if _, err := taggedRef("tag", bad); err == nil {
			t.Errorf("taggedRef(%q) was accepted", bad)
		}
	}
	for _, ok := range []string{"sha256:" + strings.Repeat("a", 64), strings.Repeat("b", 64), "nginx:1", "localhost:5000/app@sha256:" + strings.Repeat("c", 64)} {
		if err := validImageRef("image", ok); err != nil {
			t.Errorf("validImageRef(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"--privileged", "a/../b", "x?y", "a//b"} {
		if err := validImageRef("image", bad); err == nil {
			t.Errorf("validImageRef(%q) was accepted", bad)
		}
	}
}

// TestPrintable drops control characters and cuts on a whole rune.
func TestPrintable(t *testing.T) {
	if got := printable("a\x1b[31mb\nc\u0085d", 100); got != "a[31mbcd" {
		t.Errorf("printable = %q", got)
	}
	if got := printable("ééé", 3); got != "é" {
		t.Errorf("a cut through a rune = %q", got)
	}
}
