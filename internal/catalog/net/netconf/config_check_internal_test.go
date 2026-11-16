// Package netconf: tests of the check through the candidate datastore over
// the test double, each gate refusing with its reason and the check's own
// path from the lock to the proven discard.
package netconf

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// stagedContext is a context on the command line, which allows a staged
// check.
type stagedContext struct{ *stubContext }

func (stagedContext) StagedChecksAllowed() bool { return true }

// typedDevice is a device of a named type.
type typedDevice struct {
	*inventorytest.Stub
	typ string
}

func (d *typedDevice) DeviceType() string { return d.typ }

// checkDevice is a generic_netconf device named name.
func checkDevice(name string) *typedDevice {
	return &typedDevice{Stub: &inventorytest.Stub{StubID: inventory.DeviceID(name), StubName: name, Caps: []capability.Name{capability.NameNetconf}}, typ: "generic_netconf"}
}

// netopeerDiscovery is what a Netopeer2 server's library shows of itself.
func netopeerDiscovery() netconf.Discovery {
	return netconf.Discovery{Modules: []netconf.Module{
		{Name: "sysrepo", Namespace: "http://www.sysrepo.org/yang/sysrepo"},
		{Name: "netopeer-notifications", Namespace: "urn:cesnet:netopeer-notifcations"},
	}}
}

// checkSession is a qualified server's candidate session over running.
func checkSession() *fakeSession {
	return &fakeSession{
		target:       netconf.Candidate,
		running:      twoGroups,
		discovery:    netopeerDiscovery(),
		capabilities: map[string]bool{netconf.CapabilityCandidate: true, netconf.CapabilityValidate11: true},
	}
}

// checkParams asks for a check creating group ops.
func checkParams(extra map[string]any) map[string]any {
	params := map[string]any{paramContent: groups(`<group><name>ops</name></group>`), paramCandidateCheck: true}
	for k, v := range extra {
		params[k] = v
	}
	return params
}

// cannotCheck reports whether err is a check's refusal naming why.
func cannotCheck(err error, why string) bool {
	var cc *collection.CannotCheckError
	return errors.As(err, &cc) && strings.Contains(cc.Reason, why)
}

func TestConfigCheck_ValidatesThroughTheLockedCandidateAndDiscards(t *testing.T) {
	f := checkSession()
	target := useSession(t, f)
	rc := stagedContext{newStubContext()}
	res, err := ConfigCheck(context.Background(), rc, checkDevice("c1"), checkParams(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || rc.stats[statCandidateCheck] != checkProved || *target != netconf.Candidate {
		t.Fatalf("changed %v, stat %v, target %q", res.Changed, rc.stats[statCandidateCheck], *target)
	}
	if !f.limited || f.locked != 1 || f.unlocked != 1 || f.validated != 1 || f.discarded != 1 || f.committed != 0 {
		t.Errorf("limited %v lock %d unlock %d validate %d discard %d commit %d", f.limited, f.locked, f.unlocked, f.validated, f.discarded, f.committed)
	}
	if f.staged != nil || f.running != twoGroups {
		t.Error("the check left a change behind")
	}
	if _, recorded := rc.stats[sdk.StatInverse]; recorded {
		t.Error("a check recorded an undo")
	}
}

func TestConfigCheck_PredictsNoChangeWhereNoneWouldBe(t *testing.T) {
	for name, params := range map[string]map[string]any{
		"what is there already": checkParams(map[string]any{paramContent: twoGroups}),
		"commit false":          checkParams(map[string]any{paramCommit: false}),
	} {
		t.Run(name, func(t *testing.T) {
			f := checkSession()
			if name == "what is there already" {
				f.afterEdit = ptr(twoGroups)
			}
			useSession(t, f)
			res, err := ConfigCheck(context.Background(), stagedContext{newStubContext()}, checkDevice("c1"), params)
			if err != nil || res.Changed {
				t.Fatalf("ConfigCheck() = %v, %v, want no change", res, err)
			}
		})
	}
}

func TestConfigCheck_RefusesEachCallItCannotCheck(t *testing.T) {
	locked := checkDevice("c1")
	locked.StubState = inventory.StateSimulateLocked
	cisco := checkDevice("c1")
	cisco.typ = "cisco_router"
	for name, tc := range map[string]struct {
		rc     sdk.RunbookContext
		device inventory.InventoryItem
		params map[string]any
		why    string
	}{
		"not asked for":          {stagedContext{newStubContext()}, checkDevice("c1"), map[string]any{paramContent: twoGroups}, "candidate_check: true"},
		"a write to startup":     {stagedContext{newStubContext()}, checkDevice("c1"), checkParams(map[string]any{paramDatastore: "startup"}), "startup"},
		"content naming secrets": {stagedContext{newStubContext()}, checkDevice("c1"), checkParams(map[string]any{paramContent: keyringDoc}), "names a secret"},
		"off the command line":   {newStubContext(), checkDevice("c1"), checkParams(nil), "only from the command line"},
		"no device":              {stagedContext{newStubContext()}, nil, checkParams(nil), "no target device"},
		"a simulate-locked one":  {stagedContext{newStubContext()}, locked, checkParams(nil), "simulate-locked"},
		"a type not measured":    {stagedContext{newStubContext()}, cisco, checkParams(nil), "generic_netconf"},
	} {
		t.Run(name, func(t *testing.T) {
			swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, target netconf.Datastore) (netconfSession, error) {
				t.Fatal("a session was opened for a call that cannot be checked")
				return nil, nil
			})
			_, err := ConfigCheck(context.Background(), tc.rc, tc.device, tc.params)
			if !cannotCheck(err, tc.why) {
				t.Errorf("ConfigCheck() = %v, want it unchecked naming %q", err, tc.why)
			}
		})
	}
	if _, err := ConfigCheck(context.Background(), stagedContext{newStubContext()}, checkDevice("c1"), map[string]any{paramContent: `<a>`}); err == nil || cannotCheck(err, "") {
		t.Errorf("a malformed call = %v, want the run's own refusal", err)
	}
}

func TestConfigCheck_RefusesASessionThatDoesNotQualify(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*fakeSession)
		why  string
	}{
		"no :validate":  {func(f *fakeSession) { delete(f.capabilities, netconf.CapabilityValidate11) }, ":candidate and :validate"},
		"no :candidate": {func(f *fakeSession) { delete(f.capabilities, netconf.CapabilityCandidate) }, ":candidate and :validate"},
		"another server": {func(f *fakeSession) {
			f.discovery = netconf.Discovery{Modules: []netconf.Module{{Name: "sysrepo", Namespace: "urn:elsewhere"}, {Name: "netopeer-notifications"}}}
		}, "Netopeer2 over sysrepo"},
	} {
		t.Run(name, func(t *testing.T) {
			f := checkSession()
			tc.edit(f)
			useSession(t, f)
			_, err := ConfigCheck(context.Background(), stagedContext{newStubContext()}, checkDevice("c1"), checkParams(nil))
			if !cannotCheck(err, tc.why) || f.locked != 0 || f.limited {
				t.Errorf("ConfigCheck() = %v, locks %d, limited %v", err, f.locked, f.limited)
			}
		})
	}
}

func TestConfigCheck_ARefusedLockSendsNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		err       error
		unchecked string
	}{
		"held by another session": {netconf.RPCErrors{{Tag: "lock-denied", Info: `<session-id>7</session-id>`}}, "locked by session 7"},
		"a dirty candidate":       {netconf.RPCErrors{{Tag: "operation-failed", Message: "candidate datastore data have already been modified"}}, "refused the candidate's lock"},
		"a lost session":          {errors.New("broken pipe"), ""},
	} {
		t.Run(name, func(t *testing.T) {
			f := checkSession()
			f.lockErr = tc.err
			useSession(t, f)
			_, err := ConfigCheck(context.Background(), stagedContext{newStubContext()}, checkDevice("c1"), checkParams(nil))
			switch {
			case tc.unchecked != "" && !cannotCheck(err, tc.unchecked):
				t.Errorf("ConfigCheck() = %v, want it unchecked naming %q", err, tc.unchecked)
			case tc.unchecked == "" && (err == nil || cannotCheck(err, "")):
				t.Errorf("ConfigCheck() = %v, want a failure", err)
			}
			if len(f.editCalls) != 0 || f.discarded != 0 {
				t.Errorf("%d edits and %d discards after a refused lock", len(f.editCalls), f.discarded)
			}
		})
	}
}

func TestConfigCheck_ADirtyCandidateIsNeitherEditedNorDiscarded(t *testing.T) {
	f := checkSession()
	f.staged = ptr(groups(`<group><name>someone-elses</name></group>`))
	useSession(t, f)
	_, err := ConfigCheck(context.Background(), stagedContext{newStubContext()}, checkDevice("c1"), checkParams(nil))
	if !cannotCheck(err, "changes not committed") || len(f.editCalls) != 0 || f.discarded != 0 || f.staged == nil {
		t.Fatalf("ConfigCheck() = %v, %d edits, %d discards", err, len(f.editCalls), f.discarded)
	}
}

func TestConfigCheck_FailsOnAnythingItCannotProve(t *testing.T) {
	for name, tc := range map[string]struct {
		edit       func(*fakeSession)
		want       string
		unverified bool
	}{
		"the device refuses the document":  {func(f *fakeSession) { f.editErr = netconf.RPCErrors{{Tag: "invalid-value"}} }, "a real run would fail", false},
		"the device refuses the validate":  {func(f *fakeSession) { f.validateErr = netconf.RPCErrors{{Tag: "operation-failed"}} }, "a real run would fail", false},
		"the edit's answer is lost":        {func(f *fakeSession) { f.editErr = errors.New("broken pipe") }, "proven to read as it did", false},
		"the discard fails":                {func(f *fakeSession) { f.discardErr = errors.New("broken pipe") }, "discard-changes", true},
		"the discard leaves the edit":      {func(f *fakeSession) { f.discardLeaves = true }, "does not read as it did", true},
		"the first read fails":             {func(f *fakeSession) { f.readErrAt = 1 }, "nothing was sent", false},
		"the read of running fails":        {func(f *fakeSession) { f.readErrAt = 2 }, "nothing was sent", false},
		"the read after the edit fails":    {func(f *fakeSession) { f.readErrAt = 3 }, "after the edit", false},
		"the read after the discard fails": {func(f *fakeSession) { f.readErrAt = 4 }, "after the discard", true},
	} {
		t.Run(name, func(t *testing.T) {
			f := checkSession()
			tc.edit(f)
			useSession(t, f)
			_, err := ConfigCheck(context.Background(), stagedContext{newStubContext()}, checkDevice("c1"), checkParams(nil))
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "unverified") != tc.unverified {
				t.Fatalf("ConfigCheck() = %v, want %q (unverified %v)", err, tc.want, tc.unverified)
			}
			if f.unlocked != 1 {
				t.Errorf("the lock was released %d times", f.unlocked)
			}
		})
	}
}

func TestConfigCheck_HoldsTheLockNoLongerThanItsBound(t *testing.T) {
	saved := checkLockBound
	checkLockBound = 50 * time.Millisecond
	t.Cleanup(func() { checkLockBound = saved })
	f := checkSession()
	f.blockEdit = true
	useSession(t, f)
	start := time.Now()
	_, err := ConfigCheck(context.Background(), stagedContext{newStubContext()}, checkDevice("c1"), checkParams(nil))
	if err == nil || !strings.Contains(err.Error(), "unverified") || time.Since(start) > 5*time.Second {
		t.Fatalf("ConfigCheck() = %v after %v", err, time.Since(start))
	}
}

func TestOneCheckPerDevice_SecondWaitsForTheFirst(t *testing.T) {
	d := checkDevice("serial")
	release, err := oneCheckPerDevice(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := oneCheckPerDevice(short, d); err == nil {
		t.Fatal("a second check of one device did not wait")
	}
	got := make(chan error, 1)
	go func() {
		r, err := oneCheckPerDevice(context.Background(), d)
		if err == nil {
			r()
		}
		got <- err
	}()
	if other, err := oneCheckPerDevice(context.Background(), checkDevice("other")); err != nil {
		t.Fatal(err)
	} else {
		other()
	}
	release()
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting check never got its turn")
	}
}

func TestConfigCheckCall_FromTheCallAlone(t *testing.T) {
	if err := configCheckCall(map[string]any{paramContent: `<a>`}); err != nil {
		t.Errorf("a malformed call = %v, want it left to the run", err)
	}
	if err := configCheckCall(map[string]any{paramContent: `<a/>`}); !cannotCheck(err, "candidate_check") {
		t.Errorf("without candidate_check = %v", err)
	}
	if err := configCheckCall(map[string]any{paramContent: `<a/>`, paramCandidateCheck: true}); err != nil {
		t.Errorf("with candidate_check = %v", err)
	}
}
