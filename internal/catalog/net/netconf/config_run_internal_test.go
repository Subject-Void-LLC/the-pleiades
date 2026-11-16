// Package netconf: tests of the run's reads before and after an edit and of
// when its undo is recorded, over the test double netconf_internal_test.go
// defines, whose datastore an edit replaces.
package netconf

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// recordedUndo is the undo rc holds, nil when none.
func recordedUndo(t *testing.T, rc *stubContext) map[string]any {
	t.Helper()
	record, _ := rc.stats[sdk.StatInverse].(map[string]any)
	return record
}

// undoParams are the parameters of rc's undo.
func undoParams(t *testing.T, rc *stubContext) map[string]any {
	t.Helper()
	record := recordedUndo(t, rc)
	if record == nil {
		t.Fatalf("no undo is recorded; stats %v", rc.stats)
	}
	params, _ := record[sdk.InverseParamsKey].(map[string]any)
	return params
}

// warned reports whether rc's warnings hold text.
func warned(rc *stubContext, text string) bool {
	ws, _ := rc.stats[sdk.StatWarnings].([]string)
	for _, w := range ws {
		if strings.Contains(w, text) {
			return true
		}
	}
	return false
}

const twoGroups = `<nacm xmlns="` + nacmNS + `"><groups><group><name>a</name></group><group><name>b</name></group></groups></nacm>`

func TestConfig_RecordsAnExactUndoBeforeTheEditIsSent(t *testing.T) {
	f := &fakeSession{running: twoGroups}
	useSession(t, f)
	rc := newStubContext()
	recordedFirst := false
	f.onEdit = func() { recordedFirst = recordedUndo(t, rc) != nil }
	res, err := Config(context.Background(), rc, nil, map[string]any{paramContent: groups(`<group><name>ops</name></group>`)})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || !recordedFirst {
		t.Fatalf("changed %v, undo recorded before the edit %v", res.Changed, recordedFirst)
	}
	params := undoParams(t, rc)
	want := groups(`<group xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0" nc:operation="remove"><name>ops</name></group>`)
	if params[paramContent] != want || params[paramDefaultOperation] != netconf.DefaultOperationNone || params[paramDatastore] != nil {
		t.Errorf("undo params %v, want the removal under none", params)
	}
	if kept, _ := recordedUndo(t, rc)[sdk.InverseKeepOnFailureKey].(bool); !kept {
		t.Error("the undo does not stand if the task fails")
	}
	if len(f.filters) != 2 || f.filters[0] != `<nacm xmlns="`+nacmNS+`"><groups><group/></groups></nacm>` {
		t.Errorf("filters %v, want the groups read twice", f.filters)
	}
}

func TestConfig_ALostAnswerStillLeavesTheUndo(t *testing.T) {
	f := &fakeSession{running: twoGroups, lostAnswer: true}
	useSession(t, f)
	rc := newStubContext()
	if _, err := Config(context.Background(), rc, nil, map[string]any{paramContent: groups(`<group><name>ops</name></group>`)}); err == nil {
		t.Fatal("a lost answer was taken as success")
	}
	if !strings.Contains(undoParams(t, rc)[paramContent].(string), `nc:operation="remove"><name>ops</name>`) {
		t.Errorf("the undo left is %v", undoParams(t, rc))
	}
}

func TestConfig_AReRunThatChangesNothingReportsUnchanged(t *testing.T) {
	content := groups(`<group><name>a</name></group>`)
	f := &fakeSession{running: twoGroups, afterEdit: ptr(twoGroups)}
	useSession(t, f)
	rc := newStubContext()
	res, err := Config(context.Background(), rc, nil, map[string]any{paramContent: content})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Error("an edit that left the configuration as it was reported a change")
	}
	if _, recorded := rc.stats[sdk.StatInverse]; recorded {
		t.Errorf("a run that changed nothing recorded an undo: %v", rc.stats[sdk.StatInverse])
	}
}

// TestConfig_AnUndoRecordedBeforeAnEditThatChangedNothingIsTakenBack: the
// read before planned the removal of a leaf the content adds, and the device
// kept what it had.
func TestConfig_AnUndoRecordedBeforeAnEditThatChangedNothingIsTakenBack(t *testing.T) {
	const sys = `<sys xmlns="urn:s"><location>here</location></sys>`
	f := &fakeSession{running: sys, afterEdit: ptr(sys)}
	useSession(t, f)
	rc := newStubContext()
	recordedFirst := false
	f.onEdit = func() { recordedFirst = recordedUndo(t, rc) != nil }
	res, err := Config(context.Background(), rc, nil, map[string]any{paramContent: `<sys xmlns="urn:s"><contact>new</contact></sys>`})
	if err != nil {
		t.Fatal(err)
	}
	value, present := rc.stats[sdk.StatInverse]
	if res.Changed || !recordedFirst || !present || value != nil {
		t.Errorf("changed %v, recorded before %v, undo after %v (present %v), want it withdrawn", res.Changed, recordedFirst, value, present)
	}
}

func TestConfig_AnUndoTheReadBeforeCannotDecideIsRecordedAfterTheReadAfter(t *testing.T) {
	const one = `<nacm xmlns="` + nacmNS + `"><groups><group><name>a</name></group></groups></nacm>`
	const both = `<nacm xmlns="` + nacmNS + `"><groups><group><name>a</name></group><group><name>ops</name></group></groups></nacm>`
	f := &fakeSession{running: one, afterEdit: ptr(both)}
	useSession(t, f)
	rc := newStubContext()
	recordedFirst := true
	f.onEdit = func() { recordedFirst = recordedUndo(t, rc) != nil }
	res, err := Config(context.Background(), rc, nil, map[string]any{paramContent: groups(`<group><name>ops</name></group>`)})
	if err != nil || !res.Changed || recordedFirst {
		t.Fatalf("Config() = %v, %v, recorded before the edit %v", res, err, recordedFirst)
	}
	if got := undoParams(t, rc)[paramContent]; !strings.Contains(got.(string), `nc:operation="remove"><name>ops</name></group>`) {
		t.Errorf("undo %v", got)
	}
}

func TestConfig_AnUndoLeftToALostReadAfterSaysNoneWasRecorded(t *testing.T) {
	const one = `<nacm xmlns="` + nacmNS + `"><groups><group><name>a</name></group></groups></nacm>`
	for name, f := range map[string]*fakeSession{
		"the edit's answer lost": {running: one, lostAnswer: true},
		"the read after failed":  {running: one, readErrAt: 2},
	} {
		t.Run(name, func(t *testing.T) {
			useSession(t, f)
			rc := newStubContext()
			if _, err := Config(context.Background(), rc, nil, map[string]any{paramContent: groups(`<group><name>ops</name></group>`)}); err == nil {
				t.Fatal("accepted")
			}
			if recordedUndo(t, rc) != nil || !warned(rc, "no undo is recorded: the edit's undo could be decided only by reading") {
				t.Errorf("stats %v", rc.stats)
			}
		})
	}
}

func TestConfig_SaysWhyAChangeRecordsNoUndo(t *testing.T) {
	long := `<sys xmlns="urn:s"><contact>` + strings.Repeat("x", 300) + `</contact></sys>`
	for name, tc := range map[string]struct {
		f       *fakeSession
		params  map[string]any
		rc      func() *stubContext
		warning string
	}{
		"too long for the journal":  {&fakeSession{}, map[string]any{paramContent: long}, newStubContext, "bytes"},
		"default_operation replace": {&fakeSession{}, map[string]any{paramContent: `<sys xmlns="urn:s"><contact>x</contact></sys>`, paramDefaultOperation: "replace"}, newStubContext, "whole datastore"},
		"a removal read in report-all": {&fakeSession{withDefaults: "report-all"},
			map[string]any{paramContent: `<sys xmlns="urn:s"><contact>new</contact></sys>`}, newStubContext, "with-defaults explicit"},
		"a put-back": {&fakeSession{running: `<sys xmlns="urn:s"><location>here</location><contact>old</contact></sys>`},
			map[string]any{paramContent: `<sys xmlns="urn:s"><contact>new</contact></sys>`}, newStubContext, "put back what was read from the device"},
		"a value the device stores in another form": {&fakeSession{running: `<sys xmlns="urn:s"><on>true</on></sys>`, afterEdit: ptr(`<sys xmlns="urn:s"><on>1</on></sys>`)},
			map[string]any{paramContent: `<sys xmlns="urn:s"><on>true</on></sys>`}, newStubContext, "another form"},
	} {
		t.Run(name, func(t *testing.T) {
			useSession(t, tc.f)
			rc := tc.rc()
			res, err := Config(context.Background(), rc, nil, tc.params)
			if err != nil || !res.Changed {
				t.Fatalf("Config() = %v, %v", res, err)
			}
			if recordedUndo(t, rc) != nil || !warned(rc, "no undo is recorded: ") || !warned(rc, tc.warning) {
				t.Errorf("stats %v, want no undo and a warning naming %q", rc.stats, tc.warning)
			}
		})
	}
}

func TestConfig_ContentNamingASecretRecordsNoUndo(t *testing.T) {
	f := &fakeSession{target: netconf.Running, discovery: nativeDiscovery()}
	useSession(t, f)
	rc := &secretsContext{stubContext: newStubContext(), secrets: map[string]string{"psk.key": "Gx-0123456789abcd"}}
	res, err := Config(context.Background(), rc, nil, map[string]any{paramContent: keyringDoc})
	if err != nil || !res.Changed {
		t.Fatalf("Config() = %v, %v", res, err)
	}
	if recordedUndo(t, rc.stubContext) != nil || !warned(rc.stubContext, "content names a secret") {
		t.Errorf("stats %v", rc.stats)
	}
}

func TestConfig_TheReadBeforeFailingSendsNothing(t *testing.T) {
	f := &fakeSession{readErrAt: 1}
	useSession(t, f)
	_, err := Config(context.Background(), newStubContext(), nil, map[string]any{paramContent: `<sys xmlns="urn:s"/>`})
	if err == nil || !strings.Contains(err.Error(), "nothing was sent") || len(f.editCalls) != 0 {
		t.Fatalf("Config() = %v with %d edits", err, len(f.editCalls))
	}
}

func TestConfig_TheReadAfterFailingKeepsTheUndo(t *testing.T) {
	f := &fakeSession{running: twoGroups, readErrAt: 2}
	useSession(t, f)
	rc := newStubContext()
	_, err := Config(context.Background(), rc, nil, map[string]any{paramContent: groups(`<group><name>ops</name></group>`)})
	if err == nil || !strings.Contains(err.Error(), "whether it changed is unknown") {
		t.Fatalf("Config() = %v", err)
	}
	if undoParams(t, rc) == nil {
		t.Error("no undo")
	}
}

func TestConfig_TheCandidatePathReadsRunningAndRecordsTheDatastoreNamed(t *testing.T) {
	f := candidateSession(true)
	f.running = twoGroups
	// A library of unknown completeness refuses no namespace.
	f.discovery = netconf.Discovery{}
	useSession(t, f)
	rc := newStubContext()
	res, err := Config(context.Background(), rc, nil, map[string]any{paramContent: groups(`<group><name>ops</name></group>`), paramDatastore: "candidate"})
	if err != nil || !res.Changed || f.committed != 1 {
		t.Fatalf("Config() = %v, %v, %d commits", res, err, f.committed)
	}
	if got := undoParams(t, rc)[paramDatastore]; got != "candidate" {
		t.Errorf("undo datastore %v", got)
	}
}

func TestConfig_ANewParameterIsReadStrictly(t *testing.T) {
	for name, params := range map[string]map[string]any{
		"candidate_check not a bool":  {paramContent: `<a/>`, paramCandidateCheck: "maybe"},
		"default_operation not known": {paramContent: `<a/>`, paramDefaultOperation: "delete"},
		"error_option not known":      {paramContent: `<a/>`, paramErrorOption: "ignore"},
		"content not well-formed":     {paramContent: `<a>`},
		"content with a DOCTYPE":      {paramContent: `<!DOCTYPE a><a/>`},
		"commit false on running":     {paramContent: `<a/>`, paramDatastore: "running", paramCommit: false},
		"lock never on candidate":     {paramContent: `<a/>`, paramDatastore: "candidate", paramLock: "never"},
		"datastore not text":          {paramContent: `<a/>`, paramDatastore: 7},
		"lock not text":               {paramContent: `<a/>`, paramLock: 7},
		"backup not a bool":           {paramContent: `<a/>`, paramBackup: "x"},
		"commit not a bool":           {paramContent: `<a/>`, paramCommit: "x"},
		"content masked":              {paramContent: `<a>pleiades-masked-0123456789abcdef</a>`},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readRequest(params); err == nil {
				t.Error("accepted")
			}
		})
	}
	if _, err := readRequest(map[string]any{paramContent: `<a/>`, paramDefaultOperation: "none", paramErrorOption: "stop-on-error", paramCandidateCheck: true}); err != nil {
		t.Errorf("a well-formed call refused: %v", err)
	}
}

// ptr is a pointer to s.
func ptr(s string) *string { return &s }

// TestConfig_AFailedDiscardKeepsTheCandidatesLock: when the discard of a
// failed candidate edit fails, the lock is not released (an unlock could
// leave the edit staged for the next commit by any client); the session's
// close ends it instead.
func TestConfig_AFailedDiscardKeepsTheCandidatesLock(t *testing.T) {
	for name, params := range map[string]map[string]any{
		"a refused edit":           {paramContent: `<sys xmlns="urn:s"/>`, paramDatastore: "candidate"},
		"a validated edit, commit": {paramContent: `<sys xmlns="urn:s"/>`, paramDatastore: "candidate", paramCommit: false},
	} {
		t.Run(name, func(t *testing.T) {
			f := candidateSession(true)
			f.discovery = netconf.Discovery{}
			f.discardErr = errors.New("broken pipe")
			if name == "a refused edit" {
				f.editErr = netconf.RPCErrors{{Tag: "invalid-value"}}
			}
			useSession(t, f)
			_, err := Config(context.Background(), newStubContext(), nil, params)
			if err == nil || !strings.Contains(err.Error(), "lock is kept") || f.unlocked != 0 || !f.closed {
				t.Fatalf("Config() = %v; %d unlocks, closed %v", err, f.unlocked, f.closed)
			}
		})
	}
}

// TestConfig_CommitFalseDiscardsOnAContextOfItsOwn: a task canceled while its
// validated edit is staged still discards it on a live context, then unlocks.
func TestConfig_CommitFalseDiscardsOnAContextOfItsOwn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := candidateSession(true)
	f.discovery = netconf.Discovery{}
	f.onValidate = cancel
	useSession(t, f)
	if _, err := Config(ctx, newStubContext(), nil, map[string]any{paramContent: `<sys xmlns="urn:s"/>`, paramDatastore: "candidate", paramCommit: false}); err != nil {
		t.Fatal(err)
	}
	if f.discarded != 1 || f.discardCtxErr != nil || f.unlocked != 1 {
		t.Fatalf("%d discards (on a context ending %v), %d unlocks", f.discarded, f.discardCtxErr, f.unlocked)
	}
}
