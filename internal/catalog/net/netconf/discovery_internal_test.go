// Package netconf: tests of how net.netconf.config acts on what its session
// discovered: the datastore, the candidate path, the namespace refusal and the
// facts it records.
package netconf

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// nativeDiscovery is a complete discovery of a device implementing
// Cisco-IOS-XE-native alone.
func nativeDiscovery() netconf.Discovery {
	return netconf.Discovery{
		Protocol:     []string{netconf.CapabilityBase11, netconf.CapabilityCandidate},
		Modules:      []netconf.Module{{Name: "Cisco-IOS-XE-native", Namespace: "http://cisco.com/ns/yang/Cisco-IOS-XE-native", Revision: "2024-07-01"}},
		Source:       "yang-library:1.1",
		Completeness: netconf.Complete,
		Datastores:   []string{"candidate", "running"},
		ContentID:    "c1",
	}
}

const nativeDoc = `<native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native"><hostname>r1</hostname></native>`

// candidateSession is a fake whose session writes the candidate datastore.
func candidateSession(validate bool) *fakeSession {
	caps := map[string]bool{netconf.CapabilityCandidate: true}
	if validate {
		caps[netconf.CapabilityValidate11] = true
	}
	return &fakeSession{target: netconf.Candidate, capabilities: caps, discovery: nativeDiscovery()}
}

func TestConfig_TheCandidateIsLockedValidatedAndCommitted(t *testing.T) {
	f := candidateSession(true)
	useSession(t, f)
	res, err := Config(context.Background(), newStubContext(), nil, map[string]any{paramContent: nativeDoc})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || f.locked != 1 || f.validated != 1 || f.committed != 1 || f.discarded != 0 || f.unlocked != 1 {
		t.Errorf("changed=%v locked=%d validated=%d committed=%d discarded=%d unlocked=%d; want a locked, validated commit", res.Changed, f.locked, f.validated, f.committed, f.discarded, f.unlocked)
	}
}

func TestConfig_LockNeverIsRefusedOnTheCandidate(t *testing.T) {
	f := candidateSession(true)
	useSession(t, f)
	_, err := Config(context.Background(), newStubContext(), nil, map[string]any{paramContent: nativeDoc, paramLock: "never"})
	if err == nil || !strings.Contains(err.Error(), "never is refused on the candidate") {
		t.Fatalf("err = %v, want lock: never refused on the candidate", err)
	}
	if len(f.editCalls) != 0 {
		t.Error("the edit was sent")
	}
}

// TestConfig_ADeniedLockDiscardsNothing holds that a candidate holding
// another client's changes (which is why the lock is denied) is left as it
// is.
func TestConfig_ADeniedLockDiscardsNothing(t *testing.T) {
	f := candidateSession(true)
	f.lockErr = netconf.RPCErrors{{Tag: "lock-denied", Severity: "error"}}
	useSession(t, f)
	if _, err := Config(context.Background(), newStubContext(), nil, map[string]any{paramContent: nativeDoc}); err == nil {
		t.Fatal("err = nil, want the denied lock")
	}
	if f.discarded != 0 || len(f.editCalls) != 0 {
		t.Errorf("discarded=%d edits=%d after a denied lock, want neither", f.discarded, len(f.editCalls))
	}
}

func TestConfig_AFailedCommitDiscardsTheOwnEdit(t *testing.T) {
	f := candidateSession(false)
	f.commitErr = netconf.RPCErrors{{Tag: "operation-failed", Severity: "error"}}
	useSession(t, f)
	if _, err := Config(context.Background(), newStubContext(), nil, map[string]any{paramContent: nativeDoc}); err == nil {
		t.Fatal("err = nil, want the failed commit")
	}
	if f.discarded != 1 || f.validated != 0 {
		t.Errorf("discarded=%d validated=%d, want the edit discarded, and no validate on a device without :validate", f.discarded, f.validated)
	}
}

func TestConfig_CommitFalseValidatesDiscardsAndChangesNothing(t *testing.T) {
	for _, validate := range []bool{true, false} {
		f := candidateSession(validate)
		useSession(t, f)
		rc := newStubContext()
		res, err := Config(context.Background(), rc, nil, map[string]any{paramContent: nativeDoc, paramCommit: false})
		if err != nil {
			t.Fatal(err)
		}
		if res.Changed || f.committed != 0 || f.discarded != 1 {
			t.Errorf("validate=%v: changed=%v committed=%d discarded=%d, want discarded, uncommitted, unchanged", validate, res.Changed, f.committed, f.discarded)
		}
		if rc.stats[statValidated] != validate || rc.stats[statApplied] != false {
			t.Errorf("validate=%v: validated=%v applied=%v", validate, rc.stats[statValidated], rc.stats[statApplied])
		}
	}
}

func TestConfig_CommitFalseIsRefusedOnRunning(t *testing.T) {
	f := &fakeSession{target: netconf.Running, discovery: nativeDiscovery()}
	useSession(t, f)
	_, err := Config(context.Background(), newStubContext(), nil, map[string]any{paramContent: nativeDoc, paramCommit: false})
	if err == nil || !strings.Contains(err.Error(), "where an edit is live at once") {
		t.Fatalf("err = %v, want commit: false refused on running", err)
	}
	if len(f.editCalls) != 0 {
		t.Error("the edit was sent")
	}
}

func TestConfig_TheBackupReadsRunningOnTheCandidatePath(t *testing.T) {
	f := candidateSession(true)
	useSession(t, f)
	if _, err := Config(context.Background(), newStubContext(), nil, map[string]any{paramContent: nativeDoc, paramBackup: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.getSources) != 1 || f.getSources[0] != netconf.Running {
		t.Errorf("backup read %v, want running", f.getSources)
	}
}

func TestConfig_ANamespaceTheDeviceDoesNotServeIsRefusedBeforeSending(t *testing.T) {
	f := &fakeSession{target: netconf.Running, discovery: nativeDiscovery()}
	useSession(t, f)
	doc := `<widgets xmlns="urn:example:absent"/>` + nativeDoc
	_, err := Config(context.Background(), newStubContext(), nil, map[string]any{paramContent: doc})
	if err == nil || !strings.Contains(err.Error(), "urn:example:absent") || !strings.Contains(err.Error(), "nothing was sent") {
		t.Fatalf("err = %v, want the namespace named and nothing sent", err)
	}
	if len(f.editCalls) != 0 {
		t.Error("the edit was sent")
	}
}

func TestUnservedNamespaces_OnlyTopLevelAndOnlyWhenComplete(t *testing.T) {
	d := nativeDiscovery()
	doc := `<native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native" xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0">` +
		`<deep xmlns="urn:example:mounted"/></native><unqualified/>`
	if got, err := unservedNamespaces(doc, d); err != nil || len(got) != 0 {
		t.Errorf("unserved = %v, %v; want nothing: deeper and unqualified elements are the device's to judge", got, err)
	}
	d.Completeness = netconf.Partial
	if got, _ := unservedNamespaces(`<x xmlns="urn:example:absent"/>`, d); len(got) != 0 {
		t.Errorf("unserved = %v on a partial list, want nothing refused", got)
	}
	if _, err := unservedNamespaces(`<x>`, nativeDiscovery()); err == nil {
		t.Error("a document that is not well-formed was not refused")
	}
}

func TestRecordDiscovery_RecordsFactsAndStats(t *testing.T) {
	rc := newStubContext()
	warnings, err := recordDiscovery(rc, map[string]any{}, nativeDiscovery())
	if err != nil || len(warnings) != 0 {
		t.Fatalf("recordDiscovery = %v, %v", warnings, err)
	}
	if got := rc.facts[factModules]; strings.Join(got.([]string), ",") != "Cisco-IOS-XE-native@2024-07-01" {
		t.Errorf("yang.modules = %v", got)
	}
	if rc.facts[factModuleCount] != 1 || rc.facts[factCompleteness] != "complete" || rc.facts[factContentID] != "c1" || rc.facts[factModuleDigest] == "" {
		t.Errorf("facts = %v", rc.facts)
	}
	for key := range rc.facts {
		if rc.stats[key] == nil {
			t.Errorf("fact %s has no stat beside it", key)
		}
	}
}

func TestRecordDiscovery_AnUnverifiedHostKeyRecordsNoFact(t *testing.T) {
	rc := newStubContext()
	warnings, err := recordDiscovery(rc, map[string]any{sdk.ParamInsecureSkipHostKeyVerify: true}, nativeDiscovery())
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.facts) != 0 {
		t.Errorf("facts = %v, want none from a session whose host key was not verified", rc.facts)
	}
	if len(rc.stats) == 0 || len(warnings) != 1 {
		t.Errorf("stats = %d, warnings = %v; want the stats kept and the reason said", len(rc.stats), warnings)
	}
}

func TestRecordDiscovery_AModuleListPastTheBoundKeepsCountAndDigest(t *testing.T) {
	d := nativeDiscovery()
	d.Modules = nil
	for i := 0; i < 4000; i++ {
		d.Modules = append(d.Modules, netconf.Module{Name: "module-with-a-long-name-" + strings.Repeat("x", 10) + "-" + string(rune('a'+i%26)) + "-" + itoa(i), Namespace: "urn:m", Revision: "2024-01-01"})
	}
	rc := newStubContext()
	warnings, err := recordDiscovery(rc, map[string]any{}, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rc.facts[factModules]; ok {
		t.Error("yang.modules was recorded past its bound")
	}
	if rc.facts[factModuleCount] != 4000 || rc.facts[factModuleDigest] == "" || len(warnings) != 1 {
		t.Errorf("count=%v digest=%v warnings=%v", rc.facts[factModuleCount], rc.facts[factModuleDigest], warnings)
	}
}

func TestConfig_DeclaresTheDiscoveryFacts(t *testing.T) {
	d, _ := collection.Lookup("net.netconf.config")
	for _, key := range discoveryFactKeys {
		spec, ok := collection.FactFor(d.Manifest.Facts, key)
		if !ok || spec.Type == "" || spec.Description == "" {
			t.Errorf("fact %s = %+v, %v; want it declared with a type and a description", key, spec, ok)
		}
	}
	if spec, _ := collection.FactFor(d.Manifest.Facts, factContentID); spec.Kind != collection.FactVolatile {
		t.Errorf("yang.content_id is %s, want volatile: a device may regenerate it", spec.Kind)
	}
}

// itoa is i in decimal.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}
