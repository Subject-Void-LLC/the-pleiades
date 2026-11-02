// The Crawl tier's Release Gate for persisted facts (Phase 41): a real run of
// a method that gathers facts, against a real sshd container through the
// real binary, stores them under the project's .pleiades/facts; pleiades
// facts reads them back as the device reports them, read beside the method;
// a second gather writes no new value but confirms the old one; and a check
// stores nothing.
package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// factsView is pleiades facts --json.
type factsView struct {
	Host     string `json:"host"`
	DeviceID string `json:"device_id"`
	Key      string `json:"key"`
	Facts    []struct {
		Key        string    `json:"key"`
		Value      any       `json:"value"`
		Hash       string    `json:"hash"`
		ObservedAt time.Time `json:"observed_at"`
		LastSeenAt time.Time `json:"last_seen_at"`
	} `json:"facts"`
}

func TestCLI_FactsReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the facts Release Gate container test in short mode")
	}
	container, host, port := startReleaseGateSSHD(t)
	g := newRollbackGate(t, container, host, port)
	read := func(args ...string) factsView {
		t.Helper()
		out, code := g.pleiades(t, append([]string{"facts", "container1"}, append(args, "--json")...)...)
		var v factsView
		if code != 0 || json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &v) != nil {
			t.Fatalf("pleiades facts %v: exit %d\n%s", args, code, out)
		}
		return v
	}
	gather := func(mode ...string) {
		t.Helper()
		if out, code := g.pleiades(t, append([]string{"adhoc", "container1", "facts.gather"}, mode...)...); code != 0 {
			t.Fatalf("facts.gather %v: exit %d\n%s", mode, code, out)
		}
	}

	if v := read(); len(v.Facts) != 0 {
		t.Fatalf("a host no run has gathered from has facts: %+v", v.Facts)
	}
	gather()
	first := read()
	kernel := ""
	for _, f := range first.Facts {
		if f.Key == "ansible_kernel" {
			kernel, _ = f.Value.(string)
		}
	}
	if want := g.device(t, "uname -r"); kernel != want || first.DeviceID == "" {
		t.Fatalf("stored ansible_kernel %q for device %q, the device says %q", kernel, first.DeviceID, want)
	}
	history := filepath.Join(g.dir, ".pleiades", "facts", first.DeviceID+".jsonl")
	lines := func() int {
		raw, err := os.ReadFile(history) // #nosec G304 -- the test's own project
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(raw), "\n")
	}
	written := lines()
	if written == 0 {
		t.Fatal("the gather wrote no history")
	}

	gather()
	second := read()
	if got := lines(); got > written+2 {
		t.Errorf("a second gather wrote %d lines, more than the few facts that change between runs (uptime)", got-written)
	}
	for _, f := range second.Facts {
		if f.Key == "ansible_kernel" {
			if !f.LastSeenAt.After(f.ObservedAt) {
				t.Errorf("the kernel, gathered again unchanged, was not confirmed: observed %s, last seen %s", f.ObservedAt, f.LastSeenAt)
			}
		}
	}
	if h := read("ansible_kernel"); len(h.Facts) != 1 {
		t.Errorf("the kernel's history has %d values, want 1", len(h.Facts))
	}

	before := lines()
	gather("--mode", "check")
	if got := lines(); got != before {
		t.Errorf("a check stored %d lines", got-before)
	}
	if out, code := g.pleiades(t, "facts", "nosuchhost"); code == 0 {
		t.Errorf("facts of a host not in the inventory: exit 0\n%s", out)
	}
}
