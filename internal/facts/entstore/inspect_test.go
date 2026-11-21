// The inspection of a device's stored history (inspect.go): every state and
// every failure kind it tells apart, each built the way the re-key tests
// build a broken history (a real history written through the store, then
// one edit with plain SQL), on SQLite and on PostgreSQL through the real
// migrations; that it changes nothing, a device pending a re-key included;
// and that it names no fact unless asked.
package entstore_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factsreport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
)

// database is one dialect the inspection and repair tests run on.
type database struct {
	// open is a client over a fresh database with one device, web1, in it,
	// the device's ID, and the database handle under the client.
	open func(t *testing.T) (*ent.Client, string, *sql.DB)
	// ph is the dialect's placeholder for the nth argument.
	ph func(n int) string
}

// onSQLite is a real SQLite file in WAL mode, as the Controller runs one.
var onSQLite = database{
	open: func(t *testing.T) (*ent.Client, string, *sql.DB) {
		client, device, db, _ := sqliteClientDB(t)
		return client, device, db
	},
	ph: func(int) string { return "?" },
}

// onPostgres is a fresh PostgreSQL database migrated as the Controller
// migrates one.
var onPostgres = database{open: postgresClientDB, ph: func(n int) string { return fmt.Sprintf("$%d", n) }}

// history is one test's database: web1's history gathered under the old
// keys (gatherRotationHistory), and what an edit needs to find its rows.
type history struct {
	t      *testing.T
	client *ent.Client
	db     *sql.DB
	d      database
	device string
	dev    int
	old    *facts.Keys
}

// newHistory opens d and gathers web1's history under the old keys.
func newHistory(t *testing.T, d database) *history {
	t.Helper()
	client, device, db := d.open(t)
	old := factstest.Keys()
	gatherRotationHistory(t, entstore.New(client, old, factstest.Envelope()), device)
	dev := client.Device.Query().Where(entdevice.DeviceIDEQ(device)).OnlyIDX(context.Background())
	return &history{t: t, client: client, db: db, d: d, device: device, dev: dev, old: old}
}

// exec runs one statement, failing the test when it fails.
func (h *history) exec(query string, args ...any) {
	h.t.Helper()
	if _, err := h.db.ExecContext(context.Background(), query, args...); err != nil {
		h.t.Fatalf("%s: %v", query, err)
	}
}

// ids is web1's row IDs in the order they were stored, of one kind or of
// both when kind is "".
func (h *history) ids(kind string) []int {
	h.t.Helper()
	query := "SELECT id FROM facts WHERE device_facts = " + h.d.ph(1)
	args := []any{h.dev}
	if kind != "" {
		query += " AND kind = " + h.d.ph(2)
		args = append(args, kind)
	}
	rows, err := h.db.QueryContext(context.Background(), query+" ORDER BY id", args...)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

// stable is the id of web1's nth stable row, from 0.
func (h *history) stable(n int) int { return h.ids("stable")[n] }

// copyPayload puts the payload of row from onto row to.
func (h *history) copyPayload(from, to int) {
	h.exec("UPDATE facts SET payload = (SELECT payload FROM facts WHERE id = "+h.d.ph(1)+") WHERE id = "+h.d.ph(2), from, to)
}

// userRows are the IDs of web1's user.zq9operator rows, the one key with
// three stored values (gatherRotationHistory's late change), oldest first.
func (h *history) userRows() []int {
	h.t.Helper()
	var token string
	q := "SELECT key_token FROM facts WHERE device_facts = " + h.d.ph(1) + " GROUP BY key_token HAVING count(*) = 3"
	if err := h.db.QueryRowContext(context.Background(), q, h.dev).Scan(&token); err != nil {
		h.t.Fatal(err)
	}
	var out []int
	for _, id := range h.ids("") {
		var t string
		if err := h.db.QueryRowContext(context.Background(), "SELECT key_token FROM facts WHERE id = "+h.d.ph(1), id).Scan(&t); err != nil {
			h.t.Fatal(err)
		}
		if t == token {
			out = append(out, id)
		}
	}
	return out
}

// stores are the stores a case inspects through, by name.
func (h *history) store(name string) *entstore.Store {
	env := factstest.Envelope()
	switch name {
	case "old":
		return entstore.New(h.client, h.old, env)
	case "previous only":
		return entstore.New(h.client, nil, env, entstore.WithPreviousKeys(h.old))
	case "third":
		return entstore.New(h.client, newFactKeys(h.t, 7), env, entstore.WithPreviousKeys(newFactKeys(h.t, 2)))
	case "other master key":
		// The right facts keys and another MASTER_ENCRYPTION_KEY.
		return entstore.New(h.client, newFactKeys(h.t, 7), otherMaster(h.t), entstore.WithPreviousKeys(h.old))
	}
	return entstore.New(h.client, newFactKeys(h.t, 7), env, entstore.WithPreviousKeys(h.old))
}

// otherMaster is an envelope service under a master key the history was
// not sealed under.
func otherMaster(t *testing.T) *crypto.EnvelopeService {
	t.Helper()
	svc, err := crypto.NewEnvelopeService(bytes.Repeat([]byte{9}, 32), "v1", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// inspectCase is one state an inspection must tell apart: the store it
// reads through, the edit that makes it, and what it must find.
type inspectCase struct {
	name, store string
	// edit changes web1's history and returns the row the first failure
	// must name, 0 when none fails.
	edit  func(h *history) int
	state factsreport.State
	// role is the check whose first failure must be kind; "" asserts no
	// failure.
	role string
	kind factsreport.FailureKind
}

// inspectCases are every state and every failure kind.
var inspectCases = []inspectCase{
	{name: "healthy under the current key", store: "old", edit: func(*history) int { return 0 }, state: factsreport.StateHealthy},
	{name: "pending a re-key", edit: func(*history) int { return 0 }, state: factsreport.StateRekeyPending},
	{name: "pending a re-key, with only the previous key", store: "previous only", edit: func(*history) int { return 0 }, state: factsreport.StateRekeyPending},
	{name: "an edited time", role: factsreport.RolePrevious, kind: factsreport.FaultChainHash, state: factsreport.StateRefused,
		edit: func(h *history) int {
			id := h.stable(1)
			h.exec("UPDATE facts SET created_at = "+h.d.ph(1)+" WHERE id = "+h.d.ph(2), rotationT0.Add(-time.Hour), id)
			return id
		}},
	{name: "an edited time, under the current key", store: "old", role: factsreport.RoleCurrent, kind: factsreport.FaultChainHash, state: factsreport.StateBroken,
		edit: func(h *history) int {
			id := h.stable(1)
			h.exec("UPDATE facts SET created_at = "+h.d.ph(1)+" WHERE id = "+h.d.ph(2), rotationT0.Add(-time.Hour), id)
			return id
		}},
	// A payload that does not open is never by itself proof of damage: the
	// history is partly unopened, not refused.
	{name: "another row's payload", role: factsreport.RolePrevious, kind: factsreport.FaultDoesNotOpen, state: factsreport.StatePartlyUnopened,
		edit: func(h *history) int { h.copyPayload(h.stable(0), h.stable(1)); return h.stable(1) }},
	{name: "a payload never sealed", role: factsreport.RolePrevious, kind: factsreport.FaultUnsealed, state: factsreport.StateRefused,
		edit: func(h *history) int {
			id := h.stable(2)
			h.exec(`UPDATE facts SET payload = '{"json":"1"}' WHERE id = `+h.d.ph(1), id)
			return id
		}},
	{name: "an older value put back", role: factsreport.RolePrevious, kind: factsreport.FaultHashMismatch, state: factsreport.StateRefused,
		// The late value put where the value resumed after it was: the
		// first and last rows hold the same value, the middle one another.
		edit: func(h *history) int { users := h.userRows(); h.copyPayload(users[1], users[2]); return users[2] }},
	{name: "a row moved from another device", role: factsreport.RolePrevious, kind: factsreport.FaultTokenMismatch, state: factsreport.StateRefused,
		edit: func(h *history) int {
			other := h.client.Device.Create().SetName("web2").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.2"}).SaveX(context.Background())
			gatherRotationHistory(h.t, entstore.New(h.client, h.old, factstest.Envelope()), other.DeviceID)
			var id int
			q := "SELECT max(id) FROM facts WHERE kind = 'stable' AND device_facts = " + h.d.ph(1)
			if err := h.db.QueryRowContext(context.Background(), q, other.ID).Scan(&id); err != nil {
				h.t.Fatal(err)
			}
			h.exec("UPDATE facts SET device_facts = "+h.d.ph(1)+" WHERE id = "+h.d.ph(2), h.dev, id)
			return id
		}},
	{name: "a gap", role: factsreport.RolePrevious, kind: factsreport.FaultChainLink, state: factsreport.StateRefused,
		edit: func(h *history) int {
			next := h.stable(2)
			h.exec("DELETE FROM facts WHERE id = "+h.d.ph(1), h.stable(1))
			return next
		}},
	{name: "a stable row with no chain", role: factsreport.RolePrevious, kind: factsreport.FaultChainMissing, state: factsreport.StateRefused,
		edit: func(h *history) int {
			id := h.stable(3)
			h.exec("UPDATE facts SET chain = '' WHERE id = "+h.d.ph(1), id)
			return id
		}},
	{name: "a volatile row that does not open", role: factsreport.RolePrevious, kind: factsreport.FaultDoesNotOpen, state: factsreport.StatePartlyUnopened,
		edit: func(h *history) int { v := h.ids("volatile")[0]; h.copyPayload(h.stable(0), v); return v }},
	{name: "both keys", state: factsreport.StateMixedKeys,
		edit: func(h *history) int {
			if _, err := h.store("").Current(context.Background(), h.device); err != nil {
				h.t.Fatal(err)
			}
			h.exec("UPDATE facts SET keyed_by = "+h.d.ph(1)+" WHERE id = "+h.d.ph(2), h.old.ID(), h.ids("")[0])
			return 0
		}},
	{name: "a third key", store: "third", edit: func(*history) int { return 0 }, state: factsreport.StateOtherKeys},
	{name: "a wrong master key", store: "other master key", role: factsreport.RolePrevious, kind: factsreport.FaultDoesNotOpen, state: factsreport.StateUnopened,
		edit: func(h *history) int { return h.ids("")[0] }},
}

// inspectsEveryState runs every case on d: the state, the first failure's
// row, kind, place and chain index, the repairs offered, that the database
// is exactly as it was, and that nothing the history holds is named.
func inspectsEveryState(t *testing.T, d database) {
	for _, tc := range inspectCases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHistory(t, d)
			want := tc.edit(h)
			raw := rawFactTables(t, h.db)
			got, err := h.store(tc.store).InspectDevice(context.Background(), "web1", entstore.InspectOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if after := rawFactTables(t, h.db); after != raw {
				t.Fatalf("the inspection changed the database:\nbefore\n%s\nafter\n%s", raw, after)
			}
			if got.State != tc.state {
				t.Fatalf("state %s, want %s: %+v", got.State, tc.state, got)
			}
			checkInspection(t, h, got, tc, want)
		})
	}
	t.Run("nothing stored", func(t *testing.T) {
		h := newHistory(t, d)
		h.client.Device.Create().SetName("web3").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.3"}).SaveX(context.Background())
		got, err := h.store("").InspectDevice(context.Background(), "web3", entstore.InspectOptions{})
		if err != nil || got.State != factsreport.StateEmpty || got.Rows.Total != 0 || len(got.Repairs) != 0 {
			t.Fatalf("an empty device: %+v, %v", got, err)
		}
	})
}

// checkInspection holds got, the inspection of tc's edit, to what tc and
// the edited row want.
func checkInspection(t *testing.T, h *history, got factsreport.Device, tc inspectCase, want int) {
	t.Helper()
	var keyed int
	for _, k := range got.KeyedBy {
		keyed += k.Rows
	}
	if keyed != got.Rows.Total || got.Rows.Total != got.Rows.Stable+got.Rows.Volatile || got.Advice == "" {
		t.Errorf("the counts do not add up, or there is no advice: %+v", got)
	}
	if tc.state.NeedsRepair() != slices.Contains(got.Repairs, factsreport.RepairDiscard) {
		t.Errorf("state %s offers repairs %v", got.State, got.Repairs)
	}
	// Every sealed row names the one master key version the history was
	// sealed as; only a payload never sealed names none.
	var sealed int
	for _, v := range got.SealedUnder {
		sealed += v.Rows
		if v.Version != "v1" || v.Role != factsreport.RoleCurrent {
			t.Errorf("sealed under %+v, want v1, the current version", v)
		}
	}
	if want := got.Rows.Total - boolCount(tc.kind == factsreport.FaultUnsealed); sealed != want {
		t.Errorf("%d rows counted by the version they are sealed as, want %d", sealed, want)
	}
	// Rows that do not open are counted, and make the history unverifiable
	// whatever its state.
	if (got.Rows.Unopened > 0) != (tc.kind == factsreport.FaultDoesNotOpen) {
		t.Errorf("%d rows counted as not opening, for a failure of kind %q", got.Rows.Unopened, tc.kind)
	}
	if want := got.Rows.Unopened > 0 || got.State == factsreport.StateOtherKeys || got.State == factsreport.StateMixedKeys; got.Unverifiable != want {
		t.Errorf("unverifiable %v in state %s with %d rows that do not open", got.Unverifiable, got.State, got.Rows.Unopened)
	}
	if tc.role != "" {
		i := slices.IndexFunc(got.Under, func(c factsreport.KeyCheck) bool { return c.Role == tc.role })
		if i < 0 || got.Under[i].Verifies || got.Under[i].Failure == nil {
			t.Fatalf("no failure under the %s key: %+v", tc.role, got.Under)
		}
		f := got.Under[i].Failure
		all, stable := h.ids(""), h.ids("stable")
		if f.Kind != tc.kind || f.Row != want || f.Position != slices.Index(all, want) || got.Under[i].Checked != f.Position {
			t.Errorf("first failure %+v (checked %d), want %s at row %d, position %d", f, got.Under[i].Checked, tc.kind, want, slices.Index(all, want))
		}
		if index := slices.Index(stable, want); (index >= 0) != f.Stable || (f.ChainIndex == nil) == f.Stable || (f.Stable && *f.ChainIndex != index) {
			t.Errorf("failure %+v: want stable %v at chain index %d", f, index >= 0, index)
		}
		if f.Fact != "" {
			t.Errorf("a fact is named without --show-fact-names: %+v", f)
		}
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range append(nonHexPlaintexts(), "zq9late", "zq9shadow") {
		if strings.Contains(string(encoded), plain) {
			t.Errorf("the inspection names %q: %s", plain, encoded)
		}
	}
}

// boolCount is 1 for true and 0 for false.
func boolCount(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nonHexPlaintexts is plaintexts without the plain hash, which is hex: an
// inspection prints key identities in hex, so a hex needle proves nothing.
func nonHexPlaintexts() []string {
	var out []string
	for _, p := range plaintexts() {
		if strings.Trim(p, "0123456789abcdef") != "" {
			out = append(out, p)
		}
	}
	return out
}

// TestInspect_TellsEveryStateApart: on SQLite.
func TestInspect_TellsEveryStateApart(t *testing.T) {
	inspectsEveryState(t, onSQLite)
}

// TestInspect_TellsEveryStateApartOnPostgres: on PostgreSQL.
func TestInspect_TellsEveryStateApartOnPostgres(t *testing.T) {
	inspectsEveryState(t, onPostgres)
}
