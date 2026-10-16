// Tests for selection, the result model, the bounded tail and the ledger.
package gate

import (
	"encoding/json"
	"strings"
	"testing"
)

// planManifest has two kinds, one heavy check and one declared kind.
func planManifest(t *testing.T) *Manifest {
	t.Helper()
	m, err := Parse([]byte("version: 1\nkinds: [{name: browser}]\ndefaults:\n  timeout: 1m\nchecks:\n" +
		"  - {name: unit, kind: pure, shell: x}\n  - {name: db, kind: db, shell: x, heavy: true}\n  - {name: ui, kind: browser, shell: x}\n"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestPlan selects by kind, name and heaviness, and says why for the rest.
func TestPlan(t *testing.T) {
	m := planManifest(t)
	selected := func(sel Selection) string {
		plan, err := m.Plan(sel)
		if err != nil {
			t.Fatalf("Plan(%+v): %v", sel, err)
		}
		var names []string
		for _, p := range plan {
			if p.Selected {
				names = append(names, p.Check.Name)
			} else if p.Reason == "" {
				t.Errorf("%s was left out with no reason", p.Check.Name)
			}
		}
		return strings.Join(names, ",")
	}
	for sel, want := range map[*Selection]string{
		{}:                                 "unit,db,ui",
		{Kinds: []string{"pure"}}:          "unit",
		{Checks: []string{"ui"}}:           "ui",
		{Heavy: HeavyExclude}:              "unit,ui",
		{Heavy: HeavyOnly}:                 "db",
		{Kinds: []string{"browser", "db"}}: "db,ui",
	} {
		if got := selected(*sel); got != want {
			t.Errorf("Plan(%+v) selected %s, want %s", *sel, got, want)
		}
	}
	for sel, want := range map[*Selection]string{
		{Kinds: []string{"gpu"}}:                    "not a kind",
		{Checks: []string{"nope"}}:                  "not a check",
		{Heavy: "sometimes"}:                        "include, exclude or only",
		{Kinds: []string{"pure"}, Heavy: HeavyOnly}: "runs no check",
	} {
		if _, err := m.Plan(*sel); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Plan(%+v) = %v, want it to say %q", *sel, err, want)
		}
	}
	if m.Parallelism(0) != 1 || m.Parallelism(40) != MaxParallelism || m.Parallelism(3) != 3 {
		t.Errorf("Parallelism = %d, %d, %d", m.Parallelism(0), m.Parallelism(40), m.Parallelism(3))
	}
}

// TestVerdict: a FAIL fails the run, a SKIP only with failOnSkip, and the
// error names the checks.
func TestVerdict(t *testing.T) {
	pass := Result{Name: "a", Status: Pass}
	skip := Result{Name: "b", Status: Skip}
	fail := Result{Name: "c", Status: Fail}
	if err := Verdict([]Result{pass, skip}, false); err != nil {
		t.Errorf("a pass and a skip = %v, want no error", err)
	}
	if err := Verdict([]Result{pass, skip}, true); err == nil || !strings.Contains(err.Error(), ": b") {
		t.Errorf("fail_on_skip = %v", err)
	}
	if err := Verdict([]Result{pass, skip, fail}, false); err == nil || !strings.Contains(err.Error(), "1 passed, 1 failed, 1 skipped): c") {
		t.Errorf("a failure = %v", err)
	}
	if _, err := json.Marshal(fail.Map()); err != nil {
		t.Errorf("a result does not encode: %v", err)
	}
}

// TestTail keeps the last lines within both bounds, counts what it left
// out, bounds one endless line, and escapes what a terminal acts on.
func TestTail(t *testing.T) {
	tail := NewTail(3, 1<<10)
	for _, chunk := range []string{"one\ntw", "o\nthree\n", "four\nfi", "ve"} {
		_, _ = tail.Write([]byte(chunk))
	}
	if got := tail.String(); got != "... 2 earlier line(s) left out\nthree\nfour\nfive" {
		t.Errorf("tail = %q", got)
	}
	if tail.LastLine() != "five" {
		t.Errorf("LastLine = %q", tail.LastLine())
	}

	bytesBound := NewTail(100, 16)
	_, _ = bytesBound.Write([]byte("aaaaaaaaaa\nbbbbbbbbbb\ncccccccccc\n"))
	if got := bytesBound.Lines(); len(got) != 1 || got[0] != "cccccccccc" {
		t.Errorf("byte-bounded tail kept %q", got)
	}
	endless := NewTail(10, 8)
	_, _ = endless.Write([]byte(strings.Repeat("x", 100) + "END"))
	if got := endless.Lines(); len(got) != 1 || len(got[0]) != 8 || !strings.HasSuffix(got[0], "END") {
		t.Errorf("an endless line kept %q", got)
	}

	hostile := NewTail(5, 1<<10)
	_, _ = hostile.Write([]byte("red \x1b[31mtext\tand\x00nul \xe2\x80\xae\xff\r\n"))
	bs := string(rune(92))
	want := "red " + bs + "u001b[31mtext" + bs + "u0009and" + bs + "u0000nul " + bs + "u202e" + bs + "xff"
	if got := hostile.Lines()[0]; got != want {
		t.Errorf("escaped = %q", got)
	}
	if kept, cut := LastLines([]string{"a", "b", "c"}, 2); cut != 1 || strings.Join(kept, "") != "bc" {
		t.Errorf("LastLines = %v, %d", kept, cut)
	}
}

// TestLedger groups by reason, largest first, naming up to limit.
func TestLedger(t *testing.T) {
	if Ledger("check(s)", nil, 3) != "" {
		t.Error("an empty ledger printed something")
	}
	got := Ledger("check(s)", []SkipEntry{
		{"a", "needs docker"}, {"b", "needs docker"}, {"c", "needs docker"}, {"d", "needs env X"},
	}, 2)
	want := "4 check(s) skipped, so they are not evidence of anything in this run:\n\n  3: needs docker\n      a\n      b\n      and 1 more\n\n  1: needs env X\n      d\n"
	if got != want {
		t.Errorf("ledger =\n%s\nwant\n%s", got, want)
	}
}
