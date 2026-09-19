// Package facts_test: tests of facts.gather's check.
package facts_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// TestGatherCheck_OnlyReads runs facts.gather's registered Check against
// the SSH harness, which records every command it is asked to run: every
// command is one of the fixed reads (uname or cat), none writes, and the
// check reports exactly the facts and the no-change a real run reports.
func TestGatherCheck_OnlyReads(t *testing.T) {
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
	if checked.Changed || ran.Changed || !reflect.DeepEqual(checkRC.facts, runRC.facts) {
		t.Errorf("check %v %v, run %v %v; want the same facts and no change", checked, checkRC.facts, ran, runRC.facts)
	}
}
