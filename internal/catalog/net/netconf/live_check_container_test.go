// Package netconf: net.netconf.config's check through the candidate datastore
// against the real Netopeer2 server (assurance_container_test.go): a check
// that validates and changes nothing, its refusals of a dirty or locked
// candidate, and a check whose discard is lost keeping the lock and closing
// the session, which leaves the candidate as running.
package netconf

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func TestLive_TheCheckValidatesThroughTheCandidateAndChangesNothing(t *testing.T) {
	const name = "ac-check"
	dropGroup(t, name)
	t.Cleanup(func() { dropGroup(t, name) })
	content := groups(`<group><name>` + name + `</name><user-name>erin</user-name></group>`)
	changed, rc, err := call(t, true, map[string]any{paramContent: content, paramCandidateCheck: true})
	if err != nil || !changed || rc.stats[statCandidateCheck] != checkProved {
		t.Fatalf("the check: changed %v, %v, stats %v", changed, err, rc.stats)
	}
	if readGroup(t, netconf.Running, name) != "" || readGroup(t, netconf.Candidate, name) != "" {
		t.Fatal("the check left its edit in running or the candidate")
	}
	setGroup(t, name, "erin")
	if changed, _, err := call(t, true, map[string]any{paramContent: content, paramCandidateCheck: true}); err != nil || changed {
		t.Fatalf("a check of what is there: changed %v, %v", changed, err)
	}
	// The device's refusal is the check's answer: a real run would fail.
	if _, _, err := call(t, true, map[string]any{paramContent: groups(`<group><name>` + name + `</name><user-name>` + strings.Repeat("x", 3) + `</user-name><nonesuch/></group>`), paramCandidateCheck: true}); err == nil || !strings.Contains(err.Error(), "a real run would fail") {
		t.Fatalf("a document the device refuses: %v", err)
	}
	if readGroup(t, netconf.Candidate, name) != readGroup(t, netconf.Running, name) {
		t.Fatal("a refused check left the candidate different from running")
	}
}

func TestLive_TheCheckRefusesACandidateItCannotHoldAlone(t *testing.T) {
	const name = "ac-dirty"
	dropGroup(t, name)
	t.Cleanup(func() { dropGroup(t, name) })
	params := func() map[string]any {
		return map[string]any{paramContent: groups(`<group><name>` + name + `</name></group>`), paramCandidateCheck: true}
	}

	other := session(t, netconf.Candidate)
	staged := groups(`<group><name>` + name + `</name><user-name>someone-else</user-name></group>`)
	if err := other.EditConfig(context.Background(), netconf.EditConfigRequest{Config: staged}); err != nil {
		t.Fatal(err)
	}
	_, _, err := call(t, true, params())
	if !cannotCheck(err, "refused the candidate's lock") {
		t.Fatalf("a check of a dirty candidate = %v, want it unchecked", err)
	}
	if !strings.Contains(readGroup(t, netconf.Candidate, name), "someone-else") {
		t.Fatal("the check discarded another session's staged change")
	}
	if err := other.DiscardChanges(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := other.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, err = call(t, true, params())
	if !cannotCheck(err, "locked by session "+other.SessionID()) {
		t.Fatalf("a check of a locked candidate = %v", err)
	}
	_ = other.Unlock(context.Background())
}

// lostDiscard is a real session whose discard is lost before it is sent, as a
// connection dropping at that moment leaves it.
type lostDiscard struct{ netconfSession }

func (lostDiscard) DiscardChanges(context.Context) error {
	return errors.New("netconf: writing discard-changes: the connection was lost")
}

// TestLive_ALostDiscardKeepsTheLockAndLeavesTheCandidateAsRunning: the check
// whose discard is lost does not unlock; its session closes holding the lock,
// and the server then leaves the candidate as running and the lock free.
func TestLive_ALostDiscardKeepsTheLockAndLeavesTheCandidateAsRunning(t *testing.T) {
	const name = "ac-lostdiscard"
	dropGroup(t, name)
	t.Cleanup(func() { dropGroup(t, name) })
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, target netconf.Datastore) (netconfSession, error) {
		s, err := realOpenSession(ctx, rc, device, params, fqcn, target)
		if err != nil {
			return nil, err
		}
		return lostDiscard{s}, nil
	})
	_, _, err := call(t, true, map[string]any{paramContent: groups(`<group><name>` + name + `</name></group>`), paramCandidateCheck: true})
	if err == nil || !strings.Contains(err.Error(), "candidate state unverified") || !strings.Contains(err.Error(), "lock was kept") {
		t.Fatalf("the check = %v, want it unverified with the lock kept", err)
	}
	if readGroup(t, netconf.Candidate, name) != readGroup(t, netconf.Running, name) {
		t.Fatal("the check's edit survived in the candidate after its session closed")
	}
	next := session(t, netconf.Candidate)
	if err := next.Lock(context.Background()); err != nil {
		t.Fatalf("the candidate's lock was not freed by the session's close: %v", err)
	}
	_ = next.Unlock(context.Background())
}
