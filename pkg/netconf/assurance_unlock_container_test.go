// Package netconf_test: what an unlock does to an edit nobody discarded, on the
// real Netopeer2 server the assurance suite uses (assurance_container_test.go).
package netconf_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
)

// TestAssurance_AnUnlockWithoutADiscard measures what an unlock does to an
// edit nobody discarded, which RFC 6241 does not say. Measured 2026-11-16:
// Netopeer2 over sysrepo resets the shared candidate on the unlock, so another
// session reads the staged edit before it and nothing after. A server not
// measured may leave the edit staged for the next commit by any client, so
// net.netconf.config never relies on this: a task whose discard failed or was
// not proven keeps the lock and closes the session, which
// TestAssurance_AKilledSessionLeavesTheCandidateAsRunning measures. If this
// server stops resetting on unlock, this test fails and says so.
func TestAssurance_AnUnlockWithoutADiscard(t *testing.T) {
	probe, _ := rawSession(t, netconf.Options{})
	if !probe.HasCapability(netconf.CapabilityCandidate) {
		t.Skip("this server does not advertise :candidate")
	}
	ctx := context.Background()
	content := nacmGroup("assurance-unlocked", "frank")
	s, _ := rawSession(t, netconf.Options{Target: netconf.Candidate})
	t.Cleanup(func() { _ = s.DiscardChanges(context.Background()) })
	if err := s.Lock(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.EditConfig(ctx, netconf.EditConfigRequest{Config: content}); err != nil {
		t.Fatal(err)
	}
	other, _ := rawSession(t, netconf.Options{Target: netconf.Candidate})
	if staged := canonicalRead(t, other, netconf.Candidate, content); !strings.Contains(staged, "assurance-unlocked") {
		t.Fatalf("another session does not see the staged edit before the unlock (%s), so the candidate is not shared and this measures nothing", staged)
	}
	if err := s.Unlock(ctx); err != nil {
		t.Fatal(err)
	}
	if after := canonicalRead(t, other, netconf.Candidate, content); strings.Contains(after, "assurance-unlocked") {
		t.Fatalf("after an unlock with no discard the edit is still staged for any client's commit: %s", after)
	}
}
