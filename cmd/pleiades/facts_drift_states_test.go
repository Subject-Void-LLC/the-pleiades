// Tests of the states `pleiades facts drift` reports, through the real
// binary (Phase 41d): a key the latest gather did not confirm is not drift,
// a drift that compared nothing is unknown, and a host whose files do not
// open is its own unknown while the others are still compared.
package main_test

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// driftDoc runs `facts drift` with args and --json, and is its document,
// held to the published schema, and its exit status.
func driftDoc(t *testing.T, dir string, args ...string) (clireport.FactsDriftReport, int) {
	t.Helper()
	out, err := runPleiades(t, dir, append(append([]string{"facts", "drift"}, args...), "--json")...)
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("facts drift --json printed no document: %v\n%s", err, out)
	}
	var raw json.RawMessage
	if err := json.NewDecoder(strings.NewReader(out[start:])).Decode(&raw); err != nil {
		t.Fatalf("facts drift --json: %v\n%s", err, out)
	}
	fitsSchema(t, clireport.FactsDriftSchema, "facts drift", raw)
	var doc clireport.FactsDriftReport
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc, exitCode(t, err)
}

// TestCLI_FactsDriftHoldsEveryDifferenceToTheLatestGather: a package
// installed after the baseline and uninstalled again keeps its last value in
// the store, so the host is in sync with it listed as unconfirmed (would be
// added), exit 0, rather than drifted forever; and a package installed and
// still reported drifts.
func TestCLI_FactsDriftHoldsEveryDifferenceToTheLatestGather(t *testing.T) {
	dir, ids := baselineProject(t, "web1")
	at := time.Now().Add(-time.Hour)
	recordFacts(t, dir, ids["web1"], at, map[string]any{"package.busybox": "1.36", "os.family": "alpine"})
	if out, err := runPleiades(t, dir, "facts", "baseline", "web1", "--name", "base", "--reason", "r"); err != nil {
		t.Fatalf("facts baseline: %v\n%s", err, out)
	}
	recordFacts(t, dir, ids["web1"], at.Add(time.Minute), map[string]any{"package.busybox": "1.36", "package.foo": "2.0"})
	recordFacts(t, dir, ids["web1"], at.Add(2*time.Minute), map[string]any{"package.busybox": "1.36"})
	out, err := runPleiades(t, dir, "facts", "drift", "web1")
	if code := exitCode(t, err); code != 0 || !strings.Contains(out, "web1: in_sync (1 unconfirmed) against") || !strings.Contains(out, "unconfirmed package.foo (would be added") || strings.Contains(out, "  added ") {
		t.Fatalf("a package installed and removed again: exit %d\n%s", code, out)
	}
	doc, code := driftDoc(t, dir, "web1")
	if d := doc.Devices[0]; code != 0 || d.State != facts.DriftInSync || len(d.Changes) != 0 || len(d.Unconfirmed) != 1 || d.Unconfirmed[0].Key != "package.foo" || d.Unconfirmed[0].WouldBe != facts.ChangeAdded {
		t.Errorf("the same as JSON: exit %d, %+v", code, d)
	}
	// Installed and still reported: confirmed, and drift.
	recordFacts(t, dir, ids["web1"], at.Add(3*time.Minute), map[string]any{"package.busybox": "1.36", "package.bar": "3.0"})
	if out, err := runPleiades(t, dir, "facts", "drift", "web1"); exitCode(t, err) != 3 || !strings.Contains(out, "  added package.bar") || !strings.Contains(out, "unconfirmed package.foo") {
		t.Errorf("a confirmed added package: %v\n%s", err, out)
	}
}

// TestCLI_FactsDriftCountsAKeyWhoseKindChanged: a key volatile when the
// baseline was taken and stable now is not added, and a key stable then and
// volatile now is not skipped unseen: both are counted and listed as not
// compared, and the host is in sync.
func TestCLI_FactsDriftCountsAKeyWhoseKindChanged(t *testing.T) {
	dir, ids := baselineProject(t, "web1")
	at := time.Now().Add(-time.Hour)
	recordFacts(t, dir, ids["web1"], at, map[string]any{"os.family": "alpine", "os.release": "3.20", "host.boot_id": "a"}, "host.boot_id")
	if out, err := runPleiades(t, dir, "facts", "baseline", "web1", "--name", "base", "--reason", "r"); err != nil {
		t.Fatalf("facts baseline: %v\n%s", err, out)
	}
	recordFacts(t, dir, ids["web1"], at.Add(time.Minute), map[string]any{"os.family": "alpine", "os.release": "3.21", "host.boot_id": "b"}, "os.release")
	out, err := runPleiades(t, dir, "facts", "drift", "web1")
	if code := exitCode(t, err); code != 0 || !strings.Contains(out, "web1: in_sync") || !strings.Contains(out, "2 not compared") || !strings.Contains(out, "host.boot_id, os.release") || strings.Contains(out, "  added ") {
		t.Fatalf("keys whose kind changed: exit %d\n%s", code, out)
	}
	doc, _ := driftDoc(t, dir, "web1")
	if d := doc.Devices[0]; strings.Join(d.KindChanged, ",") != "host.boot_id,os.release" || d.Compared != 1 {
		t.Errorf("the same as JSON: %+v", d)
	}
}

// TestCLI_FactsDriftThatComparedNothingIsUnknown: a baseline whose every key
// is volatile now compared nothing, so the host is unknown, saying why, and
// the command exits 1, never in sync with 0 compared.
func TestCLI_FactsDriftThatComparedNothingIsUnknown(t *testing.T) {
	dir, ids := baselineProject(t, "web2")
	at := time.Now().Add(-time.Hour)
	recordFacts(t, dir, ids["web2"], at, map[string]any{"os.kernel.release": "6.1"})
	if out, err := runPleiades(t, dir, "facts", "baseline", "web2", "--name", "base", "--reason", "r"); err != nil {
		t.Fatalf("facts baseline web2: %v\n%s", err, out)
	}
	recordFacts(t, dir, ids["web2"], at.Add(time.Minute), map[string]any{"os.kernel.release": "6.2"}, "os.kernel.release")
	out, err := runPleiades(t, dir, "facts", "drift", "web2")
	if code := exitCode(t, err); code != 1 || !strings.Contains(out, "web2: unknown: no key of the baseline could be compared") {
		t.Errorf("a drift that compared nothing: exit %d\n%s", code, out)
	}
}

// TestCLI_FactsDriftOverHostsWhoseFilesDoNotOpen: one host's facts file
// from before sealing, and another's baselines file edited, make those two
// hosts unknown with their own reasons while the healthy one is compared,
// the document is printed and the command exits 1; under a replaced master
// key every host is unknown, the same way.
func TestCLI_FactsDriftOverHostsWhoseFilesDoNotOpen(t *testing.T) {
	dir, ids := baselineProject(t, "web1", "web2", "web3")
	at := time.Now().Add(-time.Hour)
	for _, h := range []string{"web1", "web2", "web3"} {
		recordFacts(t, dir, ids[h], at, map[string]any{"os.family": "alpine"})
	}
	if out, err := runPleiades(t, dir, "facts", "baseline", "web1,web2,web3", "--name", "base", "--reason", "r"); err != nil {
		t.Fatalf("facts baseline: %v\n%s", err, out)
	}
	factsDir := filepath.Join(dir, ".pleiades", "facts")
	// web2's facts as a release before sealing wrote them.
	if err := os.WriteFile(filepath.Join(factsDir, ids["web2"]+".jsonl"), []byte(`{"key":"os.family","value":"alpine"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// One byte of web3's baseline line changed.
	path := filepath.Join(factsDir, ids["web3"]+".baselines.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 1
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runPleiades(t, dir, "facts", "drift")
	if code := exitCode(t, err); code != 1 || !strings.Contains(out, "web1: in_sync") || !strings.Contains(out, "could not be compared with a baseline: web2, web3") {
		t.Fatalf("drift with two damaged hosts: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "web2: unknown: its facts could not be read") || !strings.Contains(out, "written before stored facts were sealed") {
		t.Errorf("web2's reason does not say its facts predate sealing:\n%s", out)
	}
	if !strings.Contains(out, "web3: unknown: its baselines could not be read") || !strings.Contains(out, "sealed under another master key, edited, or moved") {
		t.Errorf("web3's reason does not say its baselines do not open:\n%s", out)
	}
	doc, code := driftDoc(t, dir)
	states := map[string]facts.DriftState{}
	for _, d := range doc.Devices {
		states[d.Host] = d.State
	}
	if code != 1 || len(doc.Devices) != 3 || states["web1"] != facts.DriftInSync || states["web2"] != facts.DriftUnknown || states["web3"] != facts.DriftUnknown {
		t.Errorf("the same as JSON: exit %d, %v", code, states)
	}

	// Another master key: no host's files open, and each says so.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".pleiades", "master.key"), []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, code = driftDoc(t, dir, "web1")
	if d := doc.Devices[0]; code != 1 || d.State != facts.DriftUnknown || !strings.Contains(d.Reason, "sealed under another master key") {
		t.Errorf("under a replaced master key: exit %d, %+v", code, d)
	}
	if out, _ := runPleiades(t, dir, "facts", "drift", "web1"); !strings.Contains(out, "web1: unknown: its baselines could not be read") {
		t.Errorf("the text view under a replaced master key:\n%s", out)
	}
}
