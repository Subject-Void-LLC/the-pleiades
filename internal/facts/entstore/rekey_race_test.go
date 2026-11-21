// Two re-keys of one device at once, and the pass that finishes a change of
// the facts key (rekey.go, rekey_all.go), on SQLite and on PostgreSQL.
package entstore_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	entfact "github.com/Subject-Void-LLC/the-pleiades/internal/ent/fact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
)

// lineUp has each of n re-keys wait, once it has read its device and before
// it writes, until all n have read it or three seconds pass: so every one of
// them that the database lets read at once works from the device as it was
// under the previous key. The device's hold (ent.Tx.LockFactsOfDevice) lets
// only one in at a time, so the first waits out the three seconds alone and
// the second reads what it committed; without the hold both would read the
// previous key and meet at their writes.
func lineUp(n int) func() {
	var mu sync.Mutex
	arrived := 0
	all := make(chan struct{})
	return func() {
		mu.Lock()
		arrived++
		if arrived == n {
			close(all)
		}
		mu.Unlock()
		select {
		case <-all:
		case <-time.After(3 * time.Second):
		}
	}
}

// danglingObservations counts the observations that name a coverage their
// device does not hold: confirmations a coverage rewritten without them
// would lose.
func danglingObservations(t *testing.T, db *sql.DB) string {
	t.Helper()
	return rawQuery(t, db, "SELECT count(*) FROM fact_observations o WHERE NOT EXISTS (SELECT 1 FROM fact_coverages c WHERE c.digest = o.coverage AND c.device_fact_coverages = o.device_id)")
}

// twoRekeysAtOnce is the proof both dialects run: two stores, the second
// replica holding either the same new key or another one, both with the same
// previous key, read a device under the previous key and then both write.
// The device ends wholly under one store's keys, its history whole: with the
// same keys both reads succeed, and with different ones the loser is refused
// (ErrKeysChanged), never left half rewritten.
func twoRekeysAtOnce(t *testing.T, client *ent.Client, device string, db *sql.DB, sameKeys bool) {
	t.Helper()
	ctx := context.Background()
	old := factstest.Keys()
	gatherRotationHistory(t, entstore.New(client, old, factstest.Envelope()), device)
	before := snapshotOf(t, entstore.New(client, old, factstest.Envelope()), device)
	second := byte(8)
	if sameKeys {
		second = 7
	}
	stores := []*entstore.Store{
		entstore.New(client, newFactKeys(t, 7), factstest.Envelope(), entstore.WithPreviousKeys(old)),
		entstore.New(client, newFactKeys(t, second), factstest.Envelope(), entstore.WithPreviousKeys(old)),
	}
	wait := lineUp(len(stores))
	for _, s := range stores {
		entstore.SetBeforeRewrite(s, wait)
	}
	errs := make([]error, len(stores))
	var wg sync.WaitGroup
	for i, s := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.Current(ctx, device)
		}()
	}
	wg.Wait()
	winner := -1
	for i, err := range errs {
		switch {
		case err == nil:
			if winner < 0 || sameKeys {
				winner = i
			} else {
				t.Errorf("both re-keys under different keys succeeded")
			}
		case !sameKeys && errors.Is(err, entstore.ErrKeysChanged):
		default:
			t.Errorf("re-key %d: %v", i, err)
		}
	}
	if winner < 0 {
		t.Fatalf("no re-key succeeded: %v", errs)
	}
	keys := newFactKeys(t, 7)
	if !sameKeys && winner == 1 {
		keys = newFactKeys(t, second)
	}
	if got := rawQuery(t, db, "SELECT DISTINCT keyed_by FROM facts"); got != keys.ID()+"|\n" {
		t.Errorf("the device's rows name %q, want the winner's keys alone", got)
	}
	if got := danglingObservations(t, db); got != "0|\n" {
		t.Errorf("%s observation(s) name a coverage the device does not hold", strings.TrimSuffix(got, "|\n"))
	}
	sameSnapshot(t, "the device after two re-keys at once", snapshotOf(t, entstore.New(client, keys, factstest.Envelope()), device), before)
}

// TestRekey_TwoReplicasWithTheSameKeyAtOnce: on SQLite.
func TestRekey_TwoReplicasWithTheSameKeyAtOnce(t *testing.T) {
	client, device, db, _ := sqliteClientDB(t)
	twoRekeysAtOnce(t, client, device, db, true)
}

// TestRekey_TwoReplicasWithDifferentKeysAtOnce: on SQLite.
func TestRekey_TwoReplicasWithDifferentKeysAtOnce(t *testing.T) {
	client, device, db, _ := sqliteClientDB(t)
	twoRekeysAtOnce(t, client, device, db, false)
}

// TestRekey_TwoReplicasWithTheSameKeyAtOnceOnPostgres: on PostgreSQL, where
// both reads see the previous key and the second write waits on the first.
func TestRekey_TwoReplicasWithTheSameKeyAtOnceOnPostgres(t *testing.T) {
	client, device, db := postgresClientDB(t)
	twoRekeysAtOnce(t, client, device, db, true)
}

// TestRekey_TwoReplicasWithDifferentKeysAtOnceOnPostgres: on PostgreSQL.
func TestRekey_TwoReplicasWithDifferentKeysAtOnceOnPostgres(t *testing.T) {
	client, device, db := postgresClientDB(t)
	twoRekeysAtOnce(t, client, device, db, false)
}

// aGatherUnderTheOldKeyDuringARekey is the proof both dialects run: a
// replica still holding only the previous key (one not yet restarted) stores
// a gather for a device while another replica re-keys it, the re-key held
// after its read for a second, long enough for a gather nothing holds back
// to commit. The device ends wholly under the new key, its history whole,
// and the gather writes nothing: on PostgreSQL the re-key holds the device's
// facts, so the gather waits for it and then finds the device under a key it
// does not hold (ErrKeysChanged); on SQLite the re-key holds the database's
// write lock, which the gather cannot wait for once it has read, so it fails
// at its first write. Either way its batch is redelivered or dropped, never
// stored beside the re-key. refused says the gather's error is the one its
// dialect gives.
func aGatherUnderTheOldKeyDuringARekey(t *testing.T, client *ent.Client, device string, db *sql.DB, refused func(error) bool) {
	t.Helper()
	ctx := context.Background()
	old := factstest.Keys()
	gatherRotationHistory(t, entstore.New(client, old, factstest.Envelope()), device)
	rotated := entstore.New(client, newFactKeys(t, 7), factstest.Envelope(), entstore.WithPreviousKeys(old))
	paused, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	entstore.SetBeforeRewrite(rotated, func() {
		once.Do(func() {
			close(paused)
			<-release
		})
	})
	rekeyed := make(chan error, 1)
	go func() {
		_, err := rotated.Current(ctx, device)
		rekeyed <- err
	}()
	<-paused
	// Only a new stable value: a gather that deleted a volatile row the
	// re-key read would meet its compare-and-swap, and prove less.
	obs := facts.Observation{Entries: prepared(t, map[string]any{"package.openssh-server": "1:9.9p3-zq7build"}), At: rotationT0.Add(3 * time.Hour),
		By: facts.Provenance{Run: "run5", Order: facts.Order{Source: facts.ClockSource, Token: 40}}}
	gathered := make(chan error, 1)
	go func() {
		gathered <- entstore.New(client, old, factstest.Envelope()).Record(ctx, device, obs)
	}()
	var gatherErr error
	gatherDone := false
	select {
	case gatherErr = <-gathered:
		gatherDone = true
	case <-time.After(time.Second):
	}
	close(release)
	if err := <-rekeyed; err != nil {
		t.Fatalf("the re-key: %v", err)
	}
	if !gatherDone {
		gatherErr = <-gathered
	}
	if !refused(gatherErr) {
		t.Errorf("the gather under the old key: %v, want it refused", gatherErr)
	}
	if got := rawQuery(t, db, "SELECT DISTINCT keyed_by FROM facts"); got != newFactKeys(t, 7).ID()+"|\n" {
		t.Errorf("the device's rows name %q, want the new keys alone", got)
	}
	if got := danglingObservations(t, db); got != "0|\n" {
		t.Errorf("%s observation(s) name a coverage the device does not hold", strings.TrimSuffix(got, "|\n"))
	}
	if h := snapshotOf(t, rotated, device).History["package.openssh-server"]; len(h) != 2 {
		t.Errorf("History(package) = %+v; the gather was refused, so want the two values from before", h)
	}
}

// TestRekey_AGatherUnderTheOldKeyDuringARekey: on SQLite.
func TestRekey_AGatherUnderTheOldKeyDuringARekey(t *testing.T) {
	client, device, db, _ := sqliteClientDB(t)
	aGatherUnderTheOldKeyDuringARekey(t, client, device, db, func(err error) bool {
		return err != nil && strings.Contains(err.Error(), "database is locked")
	})
}

// TestRekey_AGatherUnderTheOldKeyDuringARekeyOnPostgres: on PostgreSQL,
// where only the device's lock keeps the gather from landing beside the
// re-key.
func TestRekey_AGatherUnderTheOldKeyDuringARekeyOnPostgres(t *testing.T) {
	client, device, db := postgresClientDB(t)
	aGatherUnderTheOldKeyDuringARekey(t, client, device, db, func(err error) bool {
		return errors.Is(err, entstore.ErrKeysChanged)
	})
}

// aPassFinishesTheRotation is the proof both dialects run: RekeyAll re-keys
// every device still under the previous key, leaves one whose history does
// not verify exactly as it was and names it, writes nothing on a second
// pass, and once that device verifies again reports none remaining.
func aPassFinishesTheRotation(t *testing.T, client *ent.Client, db *sql.DB, placeholder func(n int) string) {
	t.Helper()
	ctx := context.Background()
	old := factstest.Keys()
	var devices []*ent.Device
	for i := 1; i <= 3; i++ {
		devices = append(devices, client.Device.Create().SetName(fmt.Sprintf("fleet%d", i)).SetType("linux_server").SetProperties(map[string]any{"host": fmt.Sprintf("10.0.1.%d", i)}).SaveX(ctx))
	}
	before := map[string]rotationSnapshot{}
	for _, d := range devices {
		gatherRotationHistory(t, entstore.New(client, old, factstest.Envelope()), d.DeviceID)
		before[d.DeviceID] = snapshotOf(t, entstore.New(client, old, factstest.Envelope()), d.DeviceID)
	}
	// The third device's first stored value has its time edited.
	edited := devices[2]
	var id int
	var at time.Time
	pick := "SELECT id, created_at FROM facts WHERE id = (SELECT min(id) FROM facts WHERE kind = 'stable' AND device_facts = " + placeholder(1) + ")"
	if err := db.QueryRowContext(ctx, pick, edited.ID).Scan(&id, &at); err != nil {
		t.Fatal(err)
	}
	setAt := func(when time.Time) {
		t.Helper()
		if _, err := db.ExecContext(ctx, "UPDATE facts SET created_at = "+placeholder(1)+" WHERE id = "+placeholder(2), when, id); err != nil {
			t.Fatal(err)
		}
	}
	setAt(at.Add(-time.Hour))

	rotated := entstore.New(client, newFactKeys(t, 7), factstest.Envelope(), entstore.WithPreviousKeys(old))
	first, err := rotated.RekeyAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := entstore.RekeyCount{Devices: 2, Facts: 14, Coverages: 6, Observations: 8, Refused: 1, RefusedDevices: []string{edited.DeviceID}, Remaining: 1}
	if fmt.Sprint(first) != fmt.Sprint(want) || first.Complete() {
		t.Fatalf("the first pass = %+v, want %+v", first, want)
	}
	for _, d := range devices[:2] {
		sameSnapshot(t, d.Name+" after the pass", snapshotOf(t, rotated, d.DeviceID), before[d.DeviceID])
	}
	raw := rawFactTables(t, db)
	again, err := rotated.RekeyAll(ctx)
	if err != nil || again.Devices != 0 || again.Facts != 0 || again.Refused != 1 || again.Remaining != 1 {
		t.Fatalf("the second pass = %+v, %v; want nothing re-keyed and the refused device still named", again, err)
	}
	if got := rawFactTables(t, db); got != raw {
		t.Error("a second pass changed the database")
	}
	// The edit put back: the device verifies, and the pass finishes.
	setAt(at)
	last, err := rotated.RekeyAll(ctx)
	if err != nil || last.Devices != 1 || last.Refused != 0 || !last.Complete() {
		t.Fatalf("the last pass = %+v, %v; want the third device re-keyed and none remaining", last, err)
	}
	sameSnapshot(t, edited.Name+" after the last pass", snapshotOf(t, rotated, edited.DeviceID), before[edited.DeviceID])
	if n, err := entstore.New(client, newFactKeys(t, 7), factstest.Envelope()).RekeyAll(ctx); err != nil || n.Devices != 0 || n.Remaining != 0 {
		t.Errorf("a store with no previous key: %+v, %v; want nothing to do", n, err)
	}
}

// TestRekeyAll_FinishesTheRotation: on SQLite.
func TestRekeyAll_FinishesTheRotation(t *testing.T) {
	client, _, db, _ := sqliteClientDB(t)
	aPassFinishesTheRotation(t, client, db, func(int) string { return "?" })
}

// TestRekeyAll_FinishesTheRotationOnPostgres: on PostgreSQL.
func TestRekeyAll_FinishesTheRotationOnPostgres(t *testing.T) {
	client, _, db := postgresClientDB(t)
	aPassFinishesTheRotation(t, client, db, func(n int) string { return fmt.Sprintf("$%d", n) })
}

// TestRekeyFact_IsACompareAndSwap: each write a re-key makes names what it
// read, so a row another transaction changed since (another token, another
// keys' name, another device, another digest) is not written, and the call
// says so (ent.ErrFactRekeyRaced), which is what rolls a losing re-key back
// when nothing else serializes two of them.
func TestRekeyFact_IsACompareAndSwap(t *testing.T) {
	ctx := context.Background()
	client, device, db, _ := sqliteClientDB(t)
	gatherRotationHistory(t, entstore.New(client, factstest.Keys(), factstest.Envelope()), device)
	raw := rawFactTables(t, db)
	dev := client.Device.Query().Where(entdevice.DeviceIDEQ(device)).OnlyIDX(ctx)
	row := client.Fact.Query().Order(ent.Asc(entfact.FieldID)).FirstX(ctx)
	cover := client.FactCoverage.Query().FirstX(ctx)
	was := ent.FactKeyed{KeyToken: row.KeyToken, Hash: row.Hash, KeyedBy: row.KeyedBy}
	now := ent.FactKeyed{KeyToken: strings.Repeat("0", 64), Hash: strings.Repeat("1", 64), KeyedBy: "controller:0000000000000000"}
	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for name, write := range map[string]func() error{
		"another token":  func() error { w := was; w.KeyToken = now.KeyToken; return tx.RekeyFact(ctx, row.ID, dev, w, now) },
		"another hash":   func() error { w := was; w.Hash = now.Hash; return tx.RekeyFact(ctx, row.ID, dev, w, now) },
		"other keys":     func() error { w := was; w.KeyedBy = now.KeyedBy; return tx.RekeyFact(ctx, row.ID, dev, w, now) },
		"another device": func() error { return tx.RekeyFact(ctx, row.ID, dev+1, was, now) },
		"another digest": func() error {
			return tx.RekeyFactCoverage(ctx, cover.ID, dev, strings.Repeat("2", 64), strings.Repeat("3", 64), nil, nil)
		},
	} {
		if err := write(); !errors.Is(err, ent.ErrFactRekeyRaced) {
			t.Errorf("%s: %v, want ent.ErrFactRekeyRaced", name, err)
		}
	}
	// The control: what was read is written.
	if err := tx.RekeyFact(ctx, row.ID, dev, was, now); err != nil {
		t.Fatalf("the control failed: a write naming what it read: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := rawFactTables(t, db); got != raw {
		t.Error("a rolled-back write changed the database")
	}
}

// TestRekey_KeysOfAStoreWithNoPreviousAreUnchanged: with no previous key a
// store behaves as it did before rotation existed: a device under other keys
// is refused, and WithPreviousKeys(nil) is the same as none.
func TestRekey_KeysOfAStoreWithNoPreviousAreUnchanged(t *testing.T) {
	ctx := context.Background()
	client, device, _, _ := sqliteClientDB(t)
	gatherRotationHistory(t, entstore.New(client, factstest.Keys(), factstest.Envelope()), device)
	for name, store := range map[string]*entstore.Store{
		"no option":    entstore.New(client, newFactKeys(t, 7), factstest.Envelope()),
		"nil previous": entstore.New(client, newFactKeys(t, 7), factstest.Envelope(), entstore.WithPreviousKeys(nil)),
		"unreadable previous": entstore.New(client, newFactKeys(t, 7), factstest.Envelope(),
			entstore.WithPreviousKeys(facts.NewLazyKeys(func() ([]byte, error) { return nil, errors.New("no secret") }, "controller"))),
	} {
		if _, err := store.Current(ctx, device); !errors.Is(err, entstore.ErrKeysChanged) {
			t.Errorf("%s: Current = %v, want ErrKeysChanged", name, err)
		}
	}
}
