// A partial set of master keys (FAILURE_PATTERNS 1341): a history gathered
// partway through a rotation of MASTER_ENCRYPTION_KEY, its older rows sealed
// as v1 under one key and its newer ones as v2 under the next, inspected and
// repaired with only one of the two. A row sealed under a key the command was
// not given does not open, and nothing tells it apart from a damaged one, so
// neither repair may remove such a history without the flag that names it.
package entstore_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factsreport"
)

// nextMasterKey is the master key a rotation moves to, as v2.
var nextMasterKey = bytes.Repeat([]byte{3}, 32)

// firstMasterKey is factstest.Envelope's master key, which sealed
// gatherRotationHistory's rows as v1.
var firstMasterKey = []byte("facts-at-rest-test-master-32byte")

// envelopeOf is an envelope service over current (as currentVersion) and,
// when previous is not nil, previous as previousVersion.
func envelopeOf(t *testing.T, current []byte, currentVersion string, previous []byte, previousVersion string) *crypto.EnvelopeService {
	t.Helper()
	svc, err := crypto.NewEnvelopeService(current, currentVersion, previous, previousVersion)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// midRotation is h's history with two gathers more stored by a controller
// partway through a master key rotation: the new key current as v2, the old
// one beside it as v1. Gather 5 changes the package and the uptime, which
// replaces the uptime's v1 row; gather 6 changes the package again. So six
// stable rows are sealed as v1, and two stable rows and the uptime as v2.
func midRotation(t *testing.T, h *history) {
	t.Helper()
	rotating := entstore.New(h.client, h.old, envelopeOf(t, nextMasterKey, "v2", firstMasterKey, "v1"))
	for i, g := range []struct {
		pkg    string
		uptime any
	}{{"1:9.9p3-zq7build", uptime(5)}, {"1:9.9p4-zq7build", nil}} {
		token := uint64(50 + 10*i)
		if err := recordGather(t, rotating, h.device, fmt.Sprintf("run%d", 5+i), token, rotationT0.Add(time.Duration(5+i)*time.Hour),
			map[string]any{"package.openssh-server": g.pkg}, g.uptime); err != nil {
			t.Fatal(err)
		}
	}
}

// staleMasterKey is a store over h with h's facts keys as the current ones
// and the old master key alone, as v1: a command run with the environment
// from before the rotation.
func staleMasterKey(t *testing.T, h *history) *entstore.Store {
	return entstore.New(h.client, h.old, envelopeOf(t, firstMasterKey, "v1", nil, ""))
}

// newMasterKeyAlone is a store over h with h's facts keys as the current
// ones and the new master key alone, as v2: the previous key left out.
func newMasterKeyAlone(t *testing.T, h *history) *entstore.Store {
	return entstore.New(h.client, h.old, envelopeOf(t, nextMasterKey, "v2", nil, ""))
}

// serversKeys is a store over h with both master keys, as the server runs
// partway through the rotation, under which the whole history verifies.
func serversKeys(t *testing.T, h *history) *entstore.Store {
	return entstore.New(h.client, h.old, envelopeOf(t, nextMasterKey, "v2", firstMasterKey, "v1"))
}

// wantPartlyUnopened fails t unless got is web1 partly unopened under a
// partial master key set: unverifiable, its rows counted by the version they
// are sealed as (six stable rows as v1, two stable rows and the uptime as
// v2), the version it was not given named in its advice, and unopened the
// rows that do not open.
func wantPartlyUnopened(t *testing.T, got factsreport.Device, notGiven string, unopened int) {
	t.Helper()
	if got.State != factsreport.StatePartlyUnopened || !got.Unverifiable || got.Rows.Unopened != unopened {
		t.Errorf("the inspection = %+v; want partly_unopened, unverifiable, %d rows that do not open", got, unopened)
	}
	roles := map[string]string{}
	rows := map[string]int{}
	for _, v := range got.SealedUnder {
		roles[v.Version], rows[v.Version] = v.Role, v.Rows
	}
	if rows["v1"] != 6 || rows["v2"] != 3 || roles[notGiven] != factsreport.RoleOther || len(got.SealedUnder) != 2 {
		t.Errorf("sealed under %+v; want six rows as v1 and three as v2, %s not given", got.SealedUnder, notGiven)
	}
	for _, says := range []string{"master key version " + notGiven, "MASTER_ENCRYPTION_KEY_PREVIOUS", "--discard-unverifiable-history"} {
		if !strings.Contains(got.Advice, says) {
			t.Errorf("the advice does not say %q: %s", says, got.Advice)
		}
	}
	if strings.Contains(got.Advice, "no key makes them verify again") {
		t.Errorf("the advice calls a history it cannot judge beyond repair: %s", got.Advice)
	}
}

// staleMasterKeyIsNotProofOfDamage is case (a): run with the
// master key from before the rotation, the newer rows do not open and the
// older ones verify. A truncate would cut at the last v1 gather and remove it
// and the v2 gathers, every one of them intact; without the flag that names
// an unverifiable history it must refuse, and so must a discard. With it,
// the truncate does what it showed. Both dialects run it.
func staleMasterKeyIsNotProofOfDamage(t *testing.T, d database) {
	ctx := context.Background()
	h := newHistory(t, d)
	midRotation(t, h)
	if got, err := serversKeys(t, h).InspectDevice(ctx, "web1", entstore.InspectOptions{}); err != nil || got.State != factsreport.StateHealthy || got.Unverifiable {
		t.Fatalf("the control failed: under the server's own master keys the history is %+v, %v; want healthy", got, err)
	}
	stale := staleMasterKey(t, h)
	got, err := stale.InspectDevice(ctx, "web1", entstore.InspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wantPartlyUnopened(t, got, "v2", 3)
	if f := got.Under[0].Failure; f == nil || f.Kind != factsreport.FaultDoesNotOpen || f.SealedVersion != "v2" || got.TruncateFrom != 4 || !strings.Contains(got.Advice, "--truncate-unverifiable-history") {
		t.Errorf("the first failure %+v, cut at %d; want a v2 row that does not open, truncate offered with its flag from gather 4", f, got.TruncateFrom)
	}
	raw := rawFactTables(t, h.db)
	confirm := entstore.RepairOptions{Confirm: true, Actor: repairActor}
	if done, err := stale.Truncate(ctx, "web1", confirm); !errors.Is(err, entstore.ErrUnverifiable) || !strings.Contains(err.Error(), "--truncate-unverifiable-history") || !strings.Contains(err.Error(), "version v2") {
		t.Errorf("the truncate = %+v, %v; want ErrUnverifiable naming v2 and the flag, nothing removed", done, err)
	}
	if done, err := stale.Discard(ctx, "web1", confirm); !errors.Is(err, entstore.ErrUnverifiable) || !strings.Contains(err.Error(), "--discard-unverifiable-history") {
		t.Errorf("the discard = %+v, %v; want ErrUnverifiable naming the flag, nothing removed", done, err)
	}
	// The flag for a verified history is not the flag for this one.
	if _, err := stale.Discard(ctx, "web1", entstore.RepairOptions{Confirm: true, Actor: repairActor, DiscardVerified: true}); !errors.Is(err, entstore.ErrUnverifiable) {
		t.Errorf("a discard with --discard-verified-history: %v, want ErrUnverifiable", err)
	}
	if rawFactTables(t, h.db) != raw || len(h.activityOf()) != 0 {
		t.Fatal("a repair refused for an unverifiable history changed the database or recorded itself")
	}
	// Told by name that the newer key is lost, it cuts where it showed:
	// gathers 4 to 6 go, two v1 values among them.
	confirm.TruncateUnverifiable = true
	done, err := stale.Truncate(ctx, "web1", confirm)
	if err != nil || done.Facts != 5 || done.Observations != 3 || done.Kept != 4 || done.Before.State != factsreport.StatePartlyUnopened {
		t.Fatalf("the truncate with its flag = %+v, %v; want five values removed, four kept", done, err)
	}
	if kept, err := serversKeys(t, h).InspectDevice(ctx, "web1", entstore.InspectOptions{}); err != nil || kept.State != factsreport.StateHealthy {
		t.Errorf("what the truncate kept, under the server's keys = %+v, %v; want healthy", kept, err)
	}
}

// missingPreviousMasterKeyIsNotProofOfDamage is case (b): run
// with the new master key and the previous one left out, the oldest rows do
// not open first, so no truncate applies (with its flag or without), and a
// discard of a history every row of which is intact must take the flag that
// names it. Both dialects run it.
func missingPreviousMasterKeyIsNotProofOfDamage(t *testing.T, d database) {
	ctx := context.Background()
	h := newHistory(t, d)
	midRotation(t, h)
	alone := newMasterKeyAlone(t, h)
	got, err := alone.InspectDevice(ctx, "web1", entstore.InspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wantPartlyUnopened(t, got, "v1", 6)
	if len(got.Repairs) != 1 || got.Repairs[0] != factsreport.RepairDiscard || got.TruncateFrom != 0 {
		t.Errorf("repairs %v, cut at %d; want discard alone", got.Repairs, got.TruncateFrom)
	}
	raw := rawFactTables(t, h.db)
	if done, err := alone.Discard(ctx, "web1", entstore.RepairOptions{Confirm: true, Actor: repairActor}); !errors.Is(err, entstore.ErrUnverifiable) {
		t.Errorf("the discard = %+v, %v; want ErrUnverifiable, nothing removed", done, err)
	}
	if _, err := alone.Truncate(ctx, "web1", entstore.RepairOptions{Confirm: true, Actor: repairActor, TruncateUnverifiable: true}); !errors.Is(err, entstore.ErrRepairDoesNotApply) {
		t.Errorf("a truncate with its flag: %v, want ErrRepairDoesNotApply: nothing before the first failure verifies", err)
	}
	if rawFactTables(t, h.db) != raw || len(h.activityOf()) != 0 {
		t.Fatal("a refused repair changed the database or recorded itself")
	}
	done, err := alone.Discard(ctx, "web1", entstore.RepairOptions{Confirm: true, Actor: repairActor, DiscardUnverifiable: true})
	if err != nil || done.Facts != 9 || h.rowsOf(h.dev) != "" {
		t.Fatalf("the discard with its flag = %+v, %v; want the whole history removed", done, err)
	}
}

func TestRepair_AStaleMasterKeyIsNotProofOfDamage(t *testing.T) {
	staleMasterKeyIsNotProofOfDamage(t, onSQLite)
}

func TestRepair_AStaleMasterKeyIsNotProofOfDamageOnPostgres(t *testing.T) {
	staleMasterKeyIsNotProofOfDamage(t, onPostgres)
}

func TestRepair_AMissingPreviousMasterKeyIsNotProofOfDamage(t *testing.T) {
	missingPreviousMasterKeyIsNotProofOfDamage(t, onSQLite)
}

func TestRepair_AMissingPreviousMasterKeyIsNotProofOfDamageOnPostgres(t *testing.T) {
	missingPreviousMasterKeyIsNotProofOfDamage(t, onPostgres)
}

// TestRepair_NoRowThatOpensIsNeverTruncated: a history no row of which
// opens is not truncated even with the flag that names an unverifiable
// history, since nothing of it verifies to keep.
func TestRepair_NoRowThatOpensIsNeverTruncated(t *testing.T) {
	h := newHistory(t, onSQLite)
	raw := rawFactTables(t, h.db)
	_, err := h.store("other master key").Truncate(context.Background(), "web1", entstore.RepairOptions{Confirm: true, Actor: repairActor, TruncateUnverifiable: true})
	if !errors.Is(err, entstore.ErrRepairDoesNotApply) || rawFactTables(t, h.db) != raw {
		t.Errorf("%v, want ErrRepairDoesNotApply and nothing changed", err)
	}
}

// TestInspectAll_NamesTheMasterKeyVersionsNotGiven: a fleet inspection with
// one device partly unopened says, of the fleet, which master key version
// the command was not given and to check the master key settings before
// believing the rows damaged.
func TestInspectAll_NamesTheMasterKeyVersionsNotGiven(t *testing.T) {
	h := newHistory(t, onSQLite)
	midRotation(t, h)
	report, err := staleMasterKey(t, h).InspectAll(context.Background(), entstore.InspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.NeedsRepair != 1 || len(report.Devices) != 1 {
		t.Fatalf("the fleet report = %+v; want web1 alone, needing repair", report)
	}
	for _, says := range []string{"1 of the 1 devices read hold rows that do not open", "master key version v2", "MASTER_ENCRYPTION_KEY_PREVIOUS"} {
		if !strings.Contains(report.Advice, says) {
			t.Errorf("the fleet's advice does not say %q: %s", says, report.Advice)
		}
	}
}
