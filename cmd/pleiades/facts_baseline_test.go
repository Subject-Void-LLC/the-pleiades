// Tests of `pleiades facts baseline` and `pleiades facts drift` through the
// real binary over a real project's file store (Phase 41d): a drift names the
// changed key and ends with the drift status, its --json fits the published
// schema, a golden baseline reads equal values in sync, a name is never taken
// twice, both commands refuse without the master key and make nothing, and a
// host named like a subcommand is still read after "--".
package main_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// baselineProject is a project with the hosts named, and each host's device
// ID by name.
func baselineProject(t *testing.T, hosts ...string) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	ids := map[string]string{}
	for _, h := range hosts {
		if out, err := runPleiades(t, dir, "add-host", h, "--type", "linux_server", "--set", "host=10.0.0.5"); err != nil {
			t.Fatalf("add-host %s: %v\n%s", h, err, out)
		}
		out, _ := runPleiades(t, dir, "facts", "--json", "--", h)
		var v struct {
			DeviceID string `json:"device_id"`
		}
		if json.Unmarshal([]byte(out), &v) != nil || v.DeviceID == "" {
			t.Fatalf("facts --json of %s: %s", h, out)
		}
		ids[h] = v.DeviceID
	}
	return dir, ids
}

// recordFacts stores observed as device's facts, as a real run would.
func recordFacts(t *testing.T, dir, device string, at time.Time, observed map[string]any) {
	t.Helper()
	store, err := facts.NewFileStore(filepath.Join(dir, ".pleiades", "facts"), projectFactKeys(dir))
	if err != nil {
		t.Fatal(err)
	}
	entries, problems, err := facts.Prepare(observed, facts.Guard{})
	if err != nil || len(problems) != 0 {
		t.Fatalf("Prepare: %v %v", problems, err)
	}
	if err := store.Record(t.Context(), device, facts.Observation{Entries: entries, At: at}); err != nil {
		t.Fatal(err)
	}
}

// TestCLI_FactsDriftNamesTheChangedKey: a baseline, a changed package, and a
// drift naming that key alone with both values, ending with the drift status;
// its --json fits the published schema; a golden baseline of another host
// with equal values reads in sync; and a name is never taken twice.
func TestCLI_FactsDriftNamesTheChangedKey(t *testing.T) {
	dir, ids := baselineProject(t, "web1", "web2", "drift")
	at := time.Now().Add(-time.Hour)
	base := map[string]any{"package.busybox": map[string]any{"version": "1.36.1-r29", "source": "apk"}, "os.family": "alpine"}
	recordFacts(t, dir, ids["web1"], at, base)
	recordFacts(t, dir, ids["web2"], at, base)

	if out, err := runPleiades(t, dir, "facts", "baseline", "web1", "--name", "pre-upgrade", "--reason", "before the upgrade"); err != nil || !strings.Contains(out, `baseline "pre-upgrade" taken over 2 stable fact(s)`) {
		t.Fatalf("facts baseline: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "facts", "drift", "web1"); err != nil || !strings.Contains(out, "web1: in_sync") {
		t.Fatalf("drift of an unchanged host: %v\n%s", err, out)
	}
	recordFacts(t, dir, ids["web1"], at.Add(time.Minute), map[string]any{"package.busybox": map[string]any{"version": "1.36.1-r30", "source": "apk"}, "os.family": "alpine"})
	out, err := runPleiades(t, dir, "facts", "drift", "web1")
	if code := exitCode(t, err); code != 3 || !strings.Contains(out, "web1: drifted") || !strings.Contains(out, "changed package.busybox") || !strings.Contains(out, "1.36.1-r30") || strings.Contains(out, "os.family") {
		t.Fatalf("drift of a changed package: exit %d\n%s", code, out)
	}
	doc := stdoutOf(t, dir, "facts", "drift", "web1", "--json")
	fitsSchema(t, clireport.FactsDriftSchema, "facts drift", doc)
	var rep clireport.FactsDriftReport
	if err := json.Unmarshal(doc, &rep); err != nil || rep.SchemaVersion != clireport.FactsDriftVersion || len(rep.Devices) != 1 {
		t.Fatalf("facts drift --json: %v\n%s", err, doc)
	}
	if d := rep.Devices[0]; d.State != facts.DriftDrifted || len(d.Changes) != 1 || d.Changes[0].Key != "package.busybox" || d.Baseline == nil || d.Baseline.Reason != "before the upgrade" || d.Baseline.By == "" {
		t.Errorf("the drift = %+v", d)
	}

	// A golden baseline of web2 from web1's current facts: web2 still holds
	// the old package, so it drifts; one taken from web2's equal twin would not.
	if out, err := runPleiades(t, dir, "facts", "baseline", "web2", "--name", "gold", "--from", "web1", "--reason", "web1 is the reference"); err != nil {
		t.Fatalf("a golden baseline: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "facts", "drift", "web2", "--baseline", "gold"); exitCode(t, err) != 3 || !strings.Contains(out, "changed package.busybox") {
		t.Errorf("web2 against web1's newer package: %v\n%s", err, out)
	}
	recordFacts(t, dir, ids["web2"], at.Add(2*time.Minute), map[string]any{"package.busybox": map[string]any{"version": "1.36.1-r30", "source": "apk"}, "os.family": "alpine"})
	if out, err := runPleiades(t, dir, "facts", "drift", "web2"); err != nil || !strings.Contains(out, "web2: in_sync") || !strings.Contains(out, "from web1") {
		t.Errorf("web2 equal to its golden baseline: %v\n%s", err, out)
	}

	// The name is never taken twice, on any host asked.
	path := filepath.Join(dir, ".pleiades", "facts", ids["web1"]+".baselines.jsonl")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiades(t, dir, "facts", "baseline", "web2,web1", "--name", "pre-upgrade", "--reason", "again"); err == nil || !strings.Contains(out, "never replaced") {
		t.Errorf("a name retaken: %v\n%s", err, out)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("a refused retake changed the baselines file")
	}
	if _, err := os.Stat(filepath.Join(dir, ".pleiades", "facts", ids["web2"]+".baselines.jsonl")); err != nil {
		t.Fatalf("web2's golden baseline: %v", err)
	}
	if b, err := facts.OpenFileStore(filepath.Join(dir, ".pleiades", "facts"), projectFactKeys(dir)); err != nil {
		t.Fatal(err)
	} else if got, err := b.Baselines(t.Context(), ids["web2"]); err != nil || len(got) != 1 {
		t.Errorf("web2's baselines after a refused command: %d, %v; want its one golden baseline", len(got), err)
	}

	// A host never gathered: no baseline is taken of it, and drift names it
	// unknown or without one, never in sync.
	if out, err := runPleiades(t, dir, "facts", "baseline", "--name", "x", "--reason", "r", "--", "drift"); err == nil || !strings.Contains(out, "never gathered") {
		t.Errorf("a baseline of a host never gathered: %v\n%s", err, out)
	}
	// Nor of a list holding one: refused before the first host is written.
	if out, err := runPleiades(t, dir, "facts", "baseline", "web1,drift", "--name", "partial", "--reason", "r"); err == nil || !strings.Contains(out, "never gathered") {
		t.Errorf("a list holding a host never gathered: %v\n%s", err, out)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("a refused list wrote a baseline of its first host")
	}
	out, err = runPleiades(t, dir, "facts", "drift")
	if exitCode(t, err) != 3 || !strings.Contains(out, "drift: no_baseline") || !strings.Contains(out, "web1: drifted") {
		t.Errorf("drift over every host: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "facts", "drift", "--", "drift"); exitCode(t, err) != 1 || !strings.Contains(out, "could not be compared") {
		t.Errorf("drift of a named host with no baseline: %v\n%s", err, out)
	}
	// "--" reads a host named like a subcommand as a host.
	if out, err := runPleiades(t, dir, "facts", "--", "drift"); err != nil || !strings.Contains(out, "has no facts") {
		t.Errorf("facts -- drift: %v\n%s", err, out)
	}
}

// TestCLI_FactsBaselineAndDriftNeedTheMasterKey: with the project's master
// key gone, both commands refuse naming it, make no baselines file and no new
// key, and show nothing; with the key, the baselines file names no key or
// value in the clear.
func TestCLI_FactsBaselineAndDriftNeedTheMasterKey(t *testing.T) {
	dir, ids := baselineProject(t, "web1")
	recordFacts(t, dir, ids["web1"], time.Now(), map[string]any{"package.zq9sealed": "1.2.3-zq9"})
	if out, err := runPleiades(t, dir, "facts", "baseline", "web1", "--name", "first", "--reason", "the-control"); err != nil {
		t.Fatalf("the control, with the key: %v\n%s", err, out)
	}
	baselines := filepath.Join(dir, ".pleiades", "facts", ids["web1"]+".baselines.jsonl")
	raw, err := os.ReadFile(baselines)
	if err != nil {
		t.Fatal(err)
	}
	// Each needle holds a character base64 never writes.
	for _, plain := range []string{"package.zq9sealed", "1.2.3-zq9", "the-control"} {
		if strings.Contains(string(raw), plain) {
			t.Errorf("the baselines file holds %q in plaintext", plain)
		}
	}
	if err := os.Remove(baselines); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(dir, ".pleiades", "master.key")
	if err := os.Remove(keyFile); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"facts", "baseline", "web1", "--name", "second", "--reason", "r"},
		{"facts", "drift", "web1"},
		{"facts", "drift", "web1", "--json"},
	} {
		out, err := runPleiades(t, dir, args...)
		if err == nil || !strings.Contains(out, "master key") || strings.Contains(out, "zq9") {
			t.Errorf("%v with no master key: %v\n%s", args, err, out)
		}
	}
	for _, path := range []string{keyFile, baselines} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a command with no master key made %s: %v", filepath.Base(path), err)
		}
	}
}
