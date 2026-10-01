// Package facts_test: tests of facts.gather's check.
package facts_test

import (
	"context"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// TestGatherCheck_OnlyReads runs facts.gather's registered Check against
// the SSH harness, which records every command it is asked to run: every
// command is one of the fixed reads (uname or cat), none writes, and the
// check reports exactly the facts and the no-change a real run reports.
func TestGatherCheck_OnlyReads(t *testing.T) {
	// The harness runs the method's reads on this machine, and a real run
	// answers only on one that has Linux's files; gather_test.go's own
	// skips say the same per file.
	testsupport.Require(t, "linux", runtime.GOOS == "linux",
		"facts.gather reads Linux's files (/etc/os-release, /proc) from the machine the SSH harness runs on, and this one is "+runtime.GOOS)
	d, ok := collection.Lookup("facts.gather")
	if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
		t.Fatalf("facts.gather does not declare a check: %+v", d.Manifest)
	}
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	server := gatherServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password}
	params := map[string]any{"insecure_skip_host_key_verify": true}

	checkRC := newGatherContext(server)
	checked, err := d.Check(context.Background(), checkRC, newGatherTarget(server), params)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	commands := srv.Commands()
	if len(commands) == 0 {
		t.Fatal("the check sent no command, so it read nothing")
	}
	for _, c := range commands {
		if !strings.HasPrefix(c, "uname ") && !strings.HasPrefix(c, "cat ") || strings.ContainsAny(c, ">|;&`$") {
			t.Errorf("the check sent %q, which is not one of the fixed reads", c)
		}
	}

	runRC := newGatherContext(server)
	ran, err := gatherRun(t, server, runRC, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Uptime is the one fact that is a clock: the check and the run each
	// read /proc/uptime, and the two reads straddle a second boundary often
	// enough to fail this test on an unchanged tree. It must still be
	// present in both and move forward by at most a little, never be equal
	// by fiat.
	checkUp, checkOK := checkRC.facts["ansible_uptime_seconds"].(int64)
	runUp, runOK := runRC.facts["ansible_uptime_seconds"].(int64)
	if !checkOK || !runOK || runUp < checkUp || runUp-checkUp > 60 {
		t.Errorf("uptime: check %#v, run %#v; want both present and the run's read no earlier and at most a minute later", checkRC.facts["ansible_uptime_seconds"], runRC.facts["ansible_uptime_seconds"])
	}
	checkRest, runRest := withoutFact(checkRC.facts, "ansible_uptime_seconds"), withoutFact(runRC.facts, "ansible_uptime_seconds")
	if checked.Changed || ran.Changed || !reflect.DeepEqual(checkRest, runRest) {
		t.Errorf("check %v %v, run %v %v; want the same facts and no change", checked, checkRC.facts, ran, runRC.facts)
	}
}

// withoutFact is a copy of facts with name left out.
func withoutFact(facts map[string]any, name string) map[string]any {
	out := make(map[string]any, len(facts))
	for k, v := range facts {
		if k != name {
			out[k] = v
		}
	}
	return out
}
