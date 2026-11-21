// The Crawl tier's Release Gate for baselines and drift (Phase 41d): a real
// gather of a real sshd container's packages through the real binary, a
// baseline of them, one package's version changed on the device, a second
// gather, and `pleiades facts drift` naming that one key, with both versions,
// ending with the drift status, its --json fitting the published schema.
package main_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

func TestCLI_FactsDriftReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the facts drift Release Gate container test in short mode")
	}
	container, host, port := startReleaseGateSSHD(t)
	g := newRollbackGate(t, container, host, port)
	gather := func() {
		t.Helper()
		if out, code := g.pleiades(t, "adhoc", "container1", "facts.gather", "gather_subset:=[packages]"); code != 0 {
			t.Fatalf("facts.gather: exit %d\n%s", code, out)
		}
	}
	gather()
	if out, code := g.pleiades(t, "facts", "baseline", "container1", "--name", "pre-change", "--reason", "before the package change"); code != 0 || !strings.Contains(out, `baseline "pre-change" taken`) {
		t.Fatalf("facts baseline: exit %d\n%s", code, out)
	}
	if out, code := g.pleiades(t, "facts", "drift", "container1"); code != 0 || !strings.Contains(out, "container1: in_sync") {
		t.Fatalf("drift before any change: exit %d\n%s", code, out)
	}

	// One package's version changed in the device's own package database,
	// which apk lists from, and put back afterwards: the container is shared.
	pkg := asRoot(t, container, `sed -n 's/^P://p' /lib/apk/db/installed | head -n 1`)
	asRoot(t, container, "cp /lib/apk/db/installed /tmp/installed.drift-gate")
	t.Cleanup(func() { asRoot(t, container, "cp /tmp/installed.drift-gate /lib/apk/db/installed") })
	asRoot(t, container, `awk -v p='`+pkg+`' '/^P:/{cur=substr($0,3)} /^V:/ && cur==p {print "V:99.0-r0"; next} {print}' /lib/apk/db/installed > /tmp/installed.new && cat /tmp/installed.new > /lib/apk/db/installed`)
	if listed := asRoot(t, container, "apk list --installed "+pkg+" 2>/dev/null"); !strings.Contains(listed, pkg+"-99.0-r0") {
		t.Fatalf("the package database edit did not take: %s", listed)
	}
	gather()

	key := "package." + pkg
	out, code := g.pleiades(t, "facts", "drift", "container1")
	if code != 3 || !strings.Contains(out, "container1: drifted") || !strings.Contains(out, "changed "+key+":") || !strings.Contains(out, "99.0-r0") {
		t.Fatalf("drift after the change: exit %d\n%s", code, out)
	}
	for _, line := range strings.Split(out, "\n") {
		for _, change := range []string{"  added ", "  removed ", "  changed "} {
			if strings.HasPrefix(line, change) && !strings.HasPrefix(line, "  changed "+key+":") {
				t.Errorf("drift names another key: %s", line)
			}
		}
	}

	out, code = g.pleiades(t, "facts", "drift", "container1", "--json")
	if code != 3 {
		t.Fatalf("drift --json: exit %d\n%s", code, out)
	}
	// The document is standard output; the drift's own line follows it on
	// standard error.
	var raw json.RawMessage
	if err := json.NewDecoder(strings.NewReader(out[strings.Index(out, "{"):])).Decode(&raw); err != nil {
		t.Fatalf("drift --json: %v\n%s", err, out)
	}
	fitsSchema(t, clireport.FactsDriftSchema, "facts drift", raw)
	var doc clireport.FactsDriftReport
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil || len(doc.Devices) != 1 {
		t.Fatalf("drift --json: %v\n%s", err, raw)
	}
	d := doc.Devices[0]
	if d.State != facts.DriftDrifted || len(d.Changes) != 1 || d.Changes[0].Key != key || d.Changes[0].Change != facts.ChangeChanged || len(d.Unconfirmed) != 0 {
		t.Fatalf("drift --json = %+v", d)
	}
	if v, _ := d.Changes[0].CurrentValue.(map[string]any); v["version"] != "99.0-r0" {
		t.Errorf("the current value = %v, want version 99.0-r0", d.Changes[0].CurrentValue)
	}
	if v, _ := d.Changes[0].BaselineValue.(map[string]any); v["version"] == nil || v["version"] == "99.0-r0" {
		t.Errorf("the baseline value = %v, want the version before the change", d.Changes[0].BaselineValue)
	}
}
