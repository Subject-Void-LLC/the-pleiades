package netconf

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// This file proves Config's own decision logic -- parameter validation,
// backup capture, lock policy, the candidate-datastore commit -- with NO
// real I/O at all, through the openSession seam config.go declares. The
// protocol itself is pkg/netconf's responsibility and is proven there
// against real captured device bytes, and end to end against real
// hardware by cmd/pleiades's own Release Gate.

// fakeSession is a canned netconfSession double.
type fakeSession struct {
	getCalls  []datastore.Path
	editCalls []netconf.EditConfigRequest
	locked    int
	unlocked  int
	committed int
	closed    bool

	capabilities map[string]bool
	getPayload   datastore.Payload
	getErr       error
	editErr      error
	lockErr      error
	commitErr    error
}

func (f *fakeSession) GetConfig(ctx context.Context, p datastore.Path) (datastore.Payload, error) {
	f.getCalls = append(f.getCalls, p)
	return f.getPayload, f.getErr
}

func (f *fakeSession) EditConfig(ctx context.Context, req netconf.EditConfigRequest) error {
	f.editCalls = append(f.editCalls, req)
	return f.editErr
}

func (f *fakeSession) Lock(ctx context.Context) error {
	f.locked++
	return f.lockErr
}

func (f *fakeSession) Unlock(ctx context.Context) error {
	f.unlocked++
	return nil
}

func (f *fakeSession) Commit(ctx context.Context) error {
	f.committed++
	return f.commitErr
}

func (f *fakeSession) HasCapability(urn string) bool { return f.capabilities[urn] }

func (f *fakeSession) Close() error {
	f.closed = true
	return nil
}

// swapOpenSession installs fn for the duration of one test, mirroring
// internal/catalog/net/{cli,ios}'s identically-shaped helpers.
func swapOpenSession(t *testing.T, fn func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, target netconf.Datastore) (netconfSession, error)) {
	t.Helper()
	orig := openSession
	openSession = fn
	t.Cleanup(func() { openSession = orig })
}

// useSession installs a fixed double and returns it, along with the
// target Config resolved, so a test can assert on both.
func useSession(t *testing.T, f *fakeSession) *netconf.Datastore {
	t.Helper()
	var seen netconf.Datastore
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, target netconf.Datastore) (netconfSession, error) {
		seen = target
		return f, nil
	})
	return &seen
}

// stubContext is a minimal sdk.RunbookContext recording every stat this
// package writes.
type stubContext struct {
	stats map[string]any
	facts map[string]any

	// failStat names the one stat whose recording fails, so a test can
	// reach the branch where the device answered and recording its answer
	// did not.
	failStat string
}

// errRecording is what a stub context's refusal to record carries.
var errRecording = errors.New("recording the result failed")

func newStubContext() *stubContext {
	return &stubContext{stats: map[string]any{}, facts: map[string]any{}}
}

func (c *stubContext) InjectSecrets() map[string]string { return nil }

func (c *stubContext) SetStat(key string, value any) error {
	if c.failStat != "" && c.failStat == key {
		return errRecording
	}
	c.stats[key] = value
	return nil
}

func (c *stubContext) EmitFact(key string, value any) error {
	c.facts[key] = value
	return nil
}

func TestConfig_Registered(t *testing.T) {
	d, ok := collection.Lookup("net.netconf.config")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "net.netconf.config")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Manifest claims StatusImplemented but Invoke is nil")
	}
	if d.Manifest.Reversibility.Reversible {
		t.Error("Reversibility.Reversible = true, want false")
	}
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Reversibility.Notes is empty; collection.Register would have refused this")
	}

	// The transport string is the one thing about this manifest nothing
	// validates at run time, and it is rendered into the generated
	// reference page, so a scaffold regeneration putting "ssh" back would
	// be invisible without this.
	if len(d.Manifest.SupportedTransports) != 1 || d.Manifest.SupportedTransports[0] != "netconf" {
		t.Errorf("SupportedTransports = %v, want exactly [netconf]", d.Manifest.SupportedTransports)
	}
}

// TestConfig_DeclaresBothCapabilitiesItActuallyNeeds pins the plan-time
// refusal. sdk.Connect asserts SSHTransportCapable at run time whatever
// the manifest says; declaring it is what lets "pleiades validate"
// report the mismatch before anything dials.
func TestConfig_DeclaresBothCapabilitiesItActuallyNeeds(t *testing.T) {
	d, _ := collection.Lookup("net.netconf.config")
	want := map[string]bool{"NetconfCapable": false, "SSHTransportCapable": false}
	for _, name := range d.Manifest.RequiredCapabilities {
		want[string(name)] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("RequiredCapabilities does not include %s", name)
		}
	}
}

func TestConfig_RequiresContent(t *testing.T) {
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, target netconf.Datastore) (netconfSession, error) {
		t.Fatal("openSession must not be called when content is missing: a session opened only to discover a missing parameter is a connection to a real device nobody needed")
		return nil, nil
	})

	for _, params := range []map[string]any{
		{},
		{paramContent: ""},
		{paramContent: "   \n  "},
	} {
		if _, err := Config(context.Background(), newStubContext(), nil, params); err == nil {
			t.Errorf("Config(%v) error = nil, want a refusal", params)
		}
	}
}

func TestConfig_AppliesContentAndReportsChanged(t *testing.T) {
	f := &fakeSession{}
	target := useSession(t, f)

	res, err := Config(context.Background(), newStubContext(), nil, map[string]any{
		paramContent: "<native/>",
	})
	if err != nil {
		t.Fatalf("Config() error = %v, want nil", err)
	}
	if !res.Changed {
		t.Error("Result.Changed = false, want true")
	}
	if *target != netconf.Running {
		t.Errorf("target = %q, want running by default", *target)
	}
	if len(f.editCalls) != 1 {
		t.Fatalf("EditConfig called %d times, want 1", len(f.editCalls))
	}
	if f.editCalls[0].Config != "<native/>" {
		t.Errorf("EditConfig content = %q, want the caller's own document verbatim", f.editCalls[0].Config)
	}
	if !f.closed {
		t.Error("the session was not closed")
	}
}

func TestConfig_PassesTheProtocolVocabularyThrough(t *testing.T) {
	f := &fakeSession{}
	useSession(t, f)

	_, err := Config(context.Background(), newStubContext(), nil, map[string]any{
		paramContent:          "<native/>",
		paramDefaultOperation: "none",
		paramErrorOption:      "continue-on-error",
	})
	if err != nil {
		t.Fatalf("Config() error = %v, want nil", err)
	}
	got := f.editCalls[0]
	if got.DefaultOperation != "none" || got.ErrorOption != "continue-on-error" {
		t.Errorf("EditConfigRequest = %+v, want the caller's default_operation and error_option carried through unchanged", got)
	}
}

func TestConfig_RefusesUnknownParameterValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"an unknown datastore", map[string]any{paramContent: "<a/>", paramDatastore: "operational"}, "running, candidate and startup"},
		{"an unknown lock policy", map[string]any{paramContent: "<a/>", paramLock: "maybe"}, "never, always and if_supported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, target netconf.Datastore) (netconfSession, error) {
				t.Fatal("openSession must not be called for an invalid parameter")
				return nil, nil
			})
			_, err := Config(context.Background(), newStubContext(), nil, tc.params)
			if err == nil {
				t.Fatalf("Config() error = nil, want one containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to list the valid values (%q)", err, tc.want)
			}
		})
	}
}

func TestConfig_BackupCapturesBeforeApplying(t *testing.T) {
	f := &fakeSession{getPayload: datastore.Payload{Encoding: datastore.EncodingXML, Bytes: []byte("<native><hostname>Cat8kv</hostname></native>")}}
	useSession(t, f)
	rc := newStubContext()

	if _, err := Config(context.Background(), rc, nil, map[string]any{
		paramContent: "<native/>",
		paramBackup:  true,
	}); err != nil {
		t.Fatalf("Config() error = %v, want nil", err)
	}

	if len(f.getCalls) != 1 {
		t.Fatalf("GetConfig called %d times, want 1", len(f.getCalls))
	}
	if !f.getCalls[0].IsRoot() {
		t.Errorf("GetConfig path = %s, want the whole datastore", f.getCalls[0])
	}
	got, ok := rc.stats[statBackup]
	if !ok {
		t.Fatalf("no %q stat was recorded", statBackup)
	}
	if !strings.Contains(got.(string), "Cat8kv") {
		t.Errorf("%s stat = %q, want the device's configuration", statBackup, got)
	}
}

// TestConfig_BackupFailureStopsBeforeApplying is the ordering that
// makes backup worth setting at all: a backup captured after the change
// documents the wrong state, and a backup that failed while the change
// still went ahead documents nothing.
func TestConfig_BackupFailureStopsBeforeApplying(t *testing.T) {
	f := &fakeSession{getErr: errors.New("device refused get-config")}
	useSession(t, f)

	_, err := Config(context.Background(), newStubContext(), nil, map[string]any{
		paramContent: "<native/>",
		paramBackup:  true,
	})
	if err == nil {
		t.Fatal("Config() error = nil, want the backup failure")
	}
	if len(f.editCalls) != 0 {
		t.Fatalf("EditConfig was called %d time(s) after the backup failed: the change went ahead with nothing to restore from", len(f.editCalls))
	}
}

func TestConfig_LockPolicy(t *testing.T) {
	for _, tc := range []struct {
		name         string
		lock         any
		hasCandidate bool
		wantLocks    int
	}{
		{"never by default", nil, true, 0},
		{"explicitly never", lockNever, true, 0},
		{"always", lockAlways, false, 1},
		{"if_supported, and it is not", lockIfSupported, false, 0},
		{"if_supported, and it is", lockIfSupported, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSession{capabilities: map[string]bool{netconf.CapabilityCandidate: tc.hasCandidate}}
			useSession(t, f)

			params := map[string]any{paramContent: "<native/>"}
			if tc.lock != nil {
				params[paramLock] = tc.lock
			}
			if _, err := Config(context.Background(), newStubContext(), nil, params); err != nil {
				t.Fatalf("Config() error = %v, want nil", err)
			}
			if f.locked != tc.wantLocks {
				t.Errorf("Lock called %d times, want %d", f.locked, tc.wantLocks)
			}
			if f.locked != f.unlocked {
				t.Errorf("Lock called %d times but Unlock %d: a lock left held blocks every other client of a shared device", f.locked, f.unlocked)
			}
		})
	}
}

// TestConfig_LockAlwaysFailingIsFatal separates the two lock policies
// by consequence, not just by name: "always" means the caller decided
// an unlocked edit is unacceptable, so failing to take the lock has to
// stop the change rather than proceed without it.
func TestConfig_LockAlwaysFailingIsFatal(t *testing.T) {
	f := &fakeSession{lockErr: errors.New("locked by another session")}
	useSession(t, f)

	_, err := Config(context.Background(), newStubContext(), nil, map[string]any{
		paramContent: "<native/>",
		paramLock:    lockAlways,
	})
	if err == nil {
		t.Fatal("Config() error = nil, want the lock failure")
	}
	if len(f.editCalls) != 0 {
		t.Error("EditConfig ran despite lock: always failing")
	}
}

func TestConfig_CommitsOnlyForTheCandidateDatastore(t *testing.T) {
	for _, tc := range []struct {
		name       string
		target     string
		commit     any
		wantCommit int
	}{
		{"running does not commit", "running", nil, 0},
		{"running ignores commit: true", "running", true, 0},
		{"candidate commits by default", "candidate", nil, 1},
		{"candidate honors commit: false", "candidate", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSession{}
			useSession(t, f)

			params := map[string]any{paramContent: "<native/>", paramDatastore: tc.target}
			if tc.commit != nil {
				params[paramCommit] = tc.commit
			}
			if _, err := Config(context.Background(), newStubContext(), nil, params); err != nil {
				t.Fatalf("Config() error = %v, want nil", err)
			}
			if f.committed != tc.wantCommit {
				t.Errorf("Commit called %d times, want %d", f.committed, tc.wantCommit)
			}
		})
	}
}

// TestConfig_AnUncommittedCandidateEditIsNotChanged pins the honesty
// rule: a candidate-datastore edit has changed nothing on the device
// until it is committed, so a failed commit must not report success.
func TestConfig_AnUncommittedCandidateEditIsNotChanged(t *testing.T) {
	f := &fakeSession{commitErr: errors.New("commit failed")}
	useSession(t, f)

	res, err := Config(context.Background(), newStubContext(), nil, map[string]any{
		paramContent:   "<native/>",
		paramDatastore: "candidate",
	})
	if err == nil {
		t.Fatal("Config() error = nil, want the commit failure")
	}
	if res.Changed {
		t.Error("Result.Changed = true after a failed commit: nothing reached the running configuration")
	}
}

func TestConfig_ReportsAnEditFailure(t *testing.T) {
	f := &fakeSession{editErr: errors.New("rpc-error: invalid-value")}
	useSession(t, f)

	res, err := Config(context.Background(), newStubContext(), nil, map[string]any{paramContent: "<native/>"})
	if err == nil {
		t.Fatal("Config() error = nil, want the edit failure")
	}
	if !strings.Contains(err.Error(), "net.netconf.config") {
		t.Errorf("error = %q, want it prefixed with the FQCN so an operator knows which task failed", err)
	}
	if res.Changed {
		t.Error("Result.Changed = true alongside an error")
	}
	if !f.closed {
		t.Error("the session was not closed on the error path")
	}
}

// notNetconfDevice implements just enough of inventory.InventoryItem to
// be passed to realOpenSession, and deliberately does NOT implement
// capability.NetconfCapable.
type notNetconfDevice struct{ inventory.InventoryItem }

func (notNetconfDevice) Name() string { return "not-a-netconf-device" }

// TestRealOpenSession_RefusesADeviceWithNoNetconfPort covers the guard
// that would otherwise be a failed type assertion. The manifest's
// RequiredCapabilities normally guarantees this device never arrives, so
// reaching it means something dispatched around that check; a named
// refusal is what an operator can act on, and a panic is not.
//
// It is also the only part of realOpenSession reachable without a real
// device: everything past it dials. The rest is covered by
// cmd/pleiades's own Release Gate against real hardware, which is the
// division AGENTS.md RULE 0 asks for rather than a mock of the dial.
func TestRealOpenSession_RefusesADeviceWithNoNetconfPort(t *testing.T) {
	_, err := realOpenSession(context.Background(), newStubContext(), notNetconfDevice{}, nil, "net.netconf.config", netconf.Running)
	if err == nil {
		t.Fatal("realOpenSession() error = nil, want a refusal for a device that cannot name a NETCONF port")
	}
	for _, want := range []string{"net.netconf.config", "not-a-netconf-device", "NetconfCapable"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

// TestRealOpenSession_RefusesANilDevice proves the guard order: a nil
// device is caught before anything tries to read a port off it.
func TestRealOpenSession_RefusesANilDevice(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("realOpenSession panicked on a nil device: %v", r)
		}
	}()
	if _, err := realOpenSession(context.Background(), newStubContext(), nil, nil, "net.netconf.config", netconf.Running); err == nil {
		t.Fatal("realOpenSession() error = nil, want a refusal for a nil device")
	}
}
