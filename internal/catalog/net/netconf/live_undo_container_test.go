// Package netconf: net.netconf.config's undo against the real Netopeer2 server
// (assurance_container_test.go): each undo recorded and then replayed exactly
// as a rollback replays it (this method again, with the recorded parameters),
// a re-run reporting no change, a put-back recording no journaled undo, an
// edit whose answer is lost still leaving its undo, another client's entry
// never taken for this task's, and a two-key list's new entries each removed
// once.
package netconf

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
)

// TestLive_ACreateIsUndoneByItsRecordedUndo: the undo removes the group the
// run created and nothing else, the two groups beside it surviving (measured
// 2026-11-16: Netopeer2 takes a remove naming no key as every entry of the
// list, so a removal that lost its key would take them too).
func TestLive_ACreateIsUndoneByItsRecordedUndo(t *testing.T) {
	const name = "ac-create"
	dropGroup(t, name)
	setGroup(t, "ac-keep1", "x")
	setGroup(t, "ac-keep2", "y")
	t.Cleanup(func() { dropGroup(t, name); dropGroup(t, "ac-keep1"); dropGroup(t, "ac-keep2") })
	content := groups(`<group><name>` + name + `</name><user-name>alice</user-name></group>`)

	changed, rc, err := call(t, false, map[string]any{paramContent: content})
	if err != nil || !changed || readGroup(t, netconf.Running, name) == "" {
		t.Fatalf("the create: changed %v, %v", changed, err)
	}
	again, rerun, err := call(t, false, map[string]any{paramContent: content})
	if err != nil || again {
		t.Fatalf("a re-run of the create: changed %v, %v", again, err)
	}
	if _, recorded := rerun.stats[sdk.StatInverse]; recorded {
		t.Errorf("a re-run that changed nothing recorded an undo: %v", rerun.stats[sdk.StatInverse])
	}
	if !strings.Contains(undoParams(t, rc.stubContext)[paramContent].(string), `nc:operation="remove"><name>`+name+`</name>`) {
		t.Fatalf("the undo was not decided before the edit: %v", undoParams(t, rc.stubContext))
	}
	if !replay(t, rc) || readGroup(t, netconf.Running, name) != "" {
		t.Fatalf("the undo left the group there: %s", readGroup(t, netconf.Running, name))
	}
	if readGroup(t, netconf.Running, "ac-keep1") == "" || readGroup(t, netconf.Running, "ac-keep2") == "" {
		t.Fatal("the undo removed groups the run did not create")
	}
}

// TestLive_AReplaceRecordsNoJournaledUndo: putting the entry back would keep
// configuration read off the device in the run's journal, which it never
// keeps, so the change records no undo and the run says why.
func TestLive_AReplaceRecordsNoJournaledUndo(t *testing.T) {
	const name = "ac-rep"
	setGroup(t, name, "alice", "carol")
	t.Cleanup(func() { dropGroup(t, name) })
	was := readGroup(t, netconf.Running, name)
	changed, rc, err := call(t, false, map[string]any{paramContent: groups(`<group xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0" nc:operation="replace"><name>` + name + `</name><user-name>bob</user-name></group>`)})
	if err != nil || !changed || readGroup(t, netconf.Running, name) == was {
		t.Fatalf("the replace: changed %v, %v", changed, err)
	}
	if recordedUndo(t, rc.stubContext) != nil || !warned(rc.stubContext, "put back what was read from the device") {
		t.Fatalf("stats %v, want no undo and the reason", rc.stats)
	}
}

// TestLive_AUserAddedToTwoIsRemovedAlone: a user merged into a group of two
// users is a new leaf-list entry, decided before the edit, and its undo
// removes it inside the group, named by its key alone.
func TestLive_AUserAddedToTwoIsRemovedAlone(t *testing.T) {
	const name = "ac-users"
	setGroup(t, name, "alice", "bob")
	t.Cleanup(func() { dropGroup(t, name) })
	was := readGroup(t, netconf.Running, name)
	changed, rc, err := call(t, false, map[string]any{paramContent: groups(`<group><name>` + name + `</name><user-name>carol</user-name></group>`)})
	if err != nil || !changed {
		t.Fatalf("the merge: changed %v, %v", changed, err)
	}
	want := groups(`<group><name>` + name + `</name><user-name xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0" nc:operation="remove">carol</user-name></group>`)
	if got := undoParams(t, rc.stubContext)[paramContent]; got != want {
		t.Fatalf("undo %v, want %s", got, want)
	}
	if !replay(t, rc) || readGroup(t, netconf.Running, name) != was {
		t.Fatalf("after the undo the group reads\n%s\nwant\n%s", readGroup(t, netconf.Running, name), was)
	}
}

// TestLive_AChangeOnlyTheReadAfterCanNameRecordsNoJournaledUndo: a user
// merged into a group of one user is a changed leaf or an added leaf-list
// entry, which the read before cannot tell; the reads before and after show
// the group changed, whose undo would be a put-back, so none is recorded.
func TestLive_AChangeOnlyTheReadAfterCanNameRecordsNoJournaledUndo(t *testing.T) {
	const name = "ac-merge"
	setGroup(t, name, "alice")
	t.Cleanup(func() { dropGroup(t, name) })
	changed, rc, err := call(t, false, map[string]any{paramContent: groups(`<group><name>` + name + `</name><user-name>dave</user-name></group>`)})
	if err != nil || !changed {
		t.Fatalf("the merge: changed %v, %v", changed, err)
	}
	if recordedUndo(t, rc.stubContext) != nil || !warned(rc.stubContext, "put back what was read from the device") {
		t.Fatalf("stats %v, want no undo and the reason", rc.stats)
	}
}

// lostAnswer is a real session whose edit is applied and whose answer is
// then lost, as a dropped connection after the device applied it leaves it.
type lostAnswer struct{ netconfSession }

func (l lostAnswer) EditConfig(ctx context.Context, req netconf.EditConfigRequest) error {
	if err := l.netconfSession.EditConfig(ctx, req); err != nil {
		return err
	}
	return errors.New("netconf: reading the reply to message-id 4: the connection was lost")
}

func TestLive_ALostAnswerStillLeavesTheUndo(t *testing.T) {
	const name = "ac-lost"
	dropGroup(t, name)
	t.Cleanup(func() { dropGroup(t, name) })
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, target netconf.Datastore) (netconfSession, error) {
		s, err := realOpenSession(ctx, rc, device, params, fqcn, target)
		if err != nil {
			return nil, err
		}
		return lostAnswer{s}, nil
	})
	_, rc, err := call(t, false, map[string]any{paramContent: groups(`<group><name>` + name + `</name></group>`)})
	if err == nil || readGroup(t, netconf.Running, name) == "" {
		t.Fatalf("the edit: %v, the group there %v", err, readGroup(t, netconf.Running, name) != "")
	}
	if kept, _ := recordedUndo(t, rc.stubContext)[sdk.InverseKeepOnFailureKey].(bool); !kept {
		t.Fatalf("no undo stands for the failed task: %v", rc.stats)
	}
	openSession = realOpenSession
	if !replay(t, rc) || readGroup(t, netconf.Running, name) != "" {
		t.Fatal("the undo left the group there")
	}
}

// pairs is a document of the named list holding the given pair elements.
func pairs(list, inner string) string {
	return `<` + list + ` xmlns="` + assuranceNS + `">` + inner + `</` + list + `>`
}

// pair is one pair entry.
func pair(a, b string) string { return `<pair><a>` + a + `</a><b>` + b + `</b></pair>` }

// setPairs makes list hold exactly the given entries, written on running.
func setPairs(t *testing.T, list string, entries ...string) {
	t.Helper()
	s := session(t, netconf.Running)
	if err := s.EditConfig(context.Background(), netconf.EditConfigRequest{DefaultOperation: netconf.DefaultOperationNone,
		Config: `<` + list + ` xmlns="` + assuranceNS + `" xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0" nc:operation="replace">` + strings.Join(entries, "") + `</` + list + `>`}); err != nil {
		t.Fatal(err)
	}
}

// readPairs reads list from running, canonical.
func readPairs(t *testing.T, list string) string {
	t.Helper()
	els, err := session(t, netconf.Running).ReadConfig(context.Background(), netconf.Running, `<`+list+` xmlns="`+assuranceNS+`"/>`)
	if err != nil {
		t.Fatal(err)
	}
	return yangdoc.CanonicalElements(els)
}

// TestLive_ATwoKeyListsNewEntriesAreEachRemovedOnce: a list holding (a,x) and
// (a,y) gets (b,x), whose first key is new, and (a,z), which the read before
// cannot name; the whole group is left to the read after, so each new entry is
// removed exactly once and the undo is one the device takes.
func TestLive_ATwoKeyListsNewEntriesAreEachRemovedOnce(t *testing.T) {
	setPairs(t, "pairs", pair("a", "x"), pair("a", "y"))
	t.Cleanup(func() { setPairs(t, "pairs") })
	was := readPairs(t, "pairs")
	changed, rc, err := call(t, false, map[string]any{paramContent: pairs("pairs", pair("b", "x")+pair("a", "z"))})
	if err != nil || !changed {
		t.Fatalf("the edit: changed %v, %v", changed, err)
	}
	undo := undoParams(t, rc.stubContext)[paramContent].(string)
	if strings.Count(undo, `<a>b</a><b>x</b>`) != 1 || strings.Count(undo, `<a>a</a><b>z</b>`) != 1 {
		t.Fatalf("undo %s, want each new entry removed once", undo)
	}
	if !replay(t, rc) || readPairs(t, "pairs") != was {
		t.Fatalf("after the undo the list reads\n%s\nwant\n%s", readPairs(t, "pairs"), was)
	}
}

// intruder is a real session that, once its edit is applied, has another
// client add an entry before the read after, as a concurrent writer does.
type intruder struct {
	netconfSession
	t *testing.T
}

func (s intruder) EditConfig(ctx context.Context, req netconf.EditConfigRequest) error {
	if err := s.netconfSession.EditConfig(ctx, req); err != nil {
		return err
	}
	other := session(s.t, netconf.Running)
	return other.EditConfig(ctx, netconf.EditConfigRequest{Config: pairs("others", pair("a", "intruder"))})
}

// TestLive_AnotherClientsEntryIsNeverTakenForThisTasks: an entry another
// client adds between the reads is not attributed to this task's content, so
// no undo is recorded (one would remove the other client's entry) and the run
// says why.
func TestLive_AnotherClientsEntryIsNeverTakenForThisTasks(t *testing.T) {
	setPairs(t, "others", pair("a", "x"))
	t.Cleanup(func() { setPairs(t, "others") })
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, target netconf.Datastore) (netconfSession, error) {
		s, err := realOpenSession(ctx, rc, device, params, fqcn, target)
		if err != nil {
			return nil, err
		}
		return intruder{s, t}, nil
	})
	changed, rc, err := call(t, false, map[string]any{paramContent: pairs("others", pair("a", "w"))})
	if err != nil || !changed {
		t.Fatalf("the edit: changed %v, %v", changed, err)
	}
	if recordedUndo(t, rc.stubContext) != nil || !warned(rc.stubContext, "another client may have added one") {
		t.Fatalf("stats %v, want no undo and the reason", rc.stats)
	}
	if !strings.Contains(readPairs(t, "others"), "intruder") {
		t.Fatal("the other client's entry is gone")
	}
}
