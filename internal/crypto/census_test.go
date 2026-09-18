// Tests for the census, over real SQLite databases seeded through the real hooks
// and read back through ent.OpenExisting.
package crypto_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// censusKey returns a distinct 32-byte key filled with b.
func censusKey(b byte) []byte { return []byte(strings.Repeat(string(b), 32)) }

// openForCensus opens dbPath the way the setup command does: read-only, with
// no hook and no interceptor anywhere.
func openForCensus(t *testing.T, dbPath string) *ent.ExistingDatabase {
	t.Helper()
	db, err := ent.OpenExisting(context.Background(), "sqlite://"+dbPath)
	if err != nil {
		t.Fatalf("OpenExisting() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestTakeCensus_FindsEveryColumnUnderTheKeyThatSealedIt is the census's
// real-path proof. Every encrypted column is written through the real hooks
// under a key whose version tag is NOT the default, then read back through
// ent.OpenExisting with no interceptor installed.
//
// The unusual tag is the point: a census that looked a key up by its tag
// would call every one of these rows unreadable by the key that wrote them,
// and the setup command would read that as permission to replace it.
func TestTakeCensus_FindsEveryColumnUnderTheKeyThatSealedIt(t *testing.T) {
	held := censusKey('h')
	svc, err := crypto.NewEnvelopeService(held, "v7", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	dbPath, _ := seedEveryColumn(t, svc)

	census, err := crypto.TakeCensus(context.Background(), openForCensus(t, dbPath), []crypto.CandidateKey{
		{Name: "a stranger's key", Key: censusKey('s')},
		{Name: "the key in .env", Key: held},
	})
	if err != nil {
		t.Fatalf("TakeCensus() error = %v", err)
	}

	want := []string{"credentials", "devices", "saved launch configurations", "mesh signing keys"}
	if len(census.Columns) != len(want) {
		t.Fatalf("census has %d columns, want %d: %+v", len(census.Columns), len(want), census.Columns)
	}
	for i, col := range census.Columns {
		if col.Noun != want[i] {
			t.Errorf("column %d is %q, want %q", i, col.Noun, want[i])
		}
		if col.Sealed != 1 || col.Opens["the key in .env"] != 1 || col.Opens["a stranger's key"] != 0 || col.Unknown != 0 {
			t.Errorf("%s: %+v, want the one sealed row opening under the held key only", col.Noun, col)
		}
	}
	if census.Sealed() != 4 || census.Opens("the key in .env") != 4 || census.Unknown() != 0 {
		t.Fatalf("totals: sealed %d, opens %d, unknown %d; want 4, 4, 0", census.Sealed(), census.Opens("the key in .env"), census.Unknown())
	}

	// The tag is not what decided which key opens a row, and it is still
	// reported, because a controller reads a row only under the key its tag
	// names. Every row here was written as v7, in every column's shape.
	if tags := census.TagsUnder("the key in .env"); len(tags) != 1 || tags["v7"] != 4 {
		t.Fatalf("TagsUnder(the key in .env) = %v, want all four rows under v7", tags)
	}
	if tags := census.TagsUnder("a stranger's key"); len(tags) != 0 {
		t.Fatalf("TagsUnder(a stranger's key) = %v, want nothing: it opens no row", tags)
	}
}

// TestTakeCensus_CountsRowsUnderAKeyItWasNotGivenAsUnknown proves a row
// under a key the census does not hold is still counted as sealed. It is the
// case a surviving database from an earlier installation presents.
func TestTakeCensus_CountsRowsUnderAKeyItWasNotGivenAsUnknown(t *testing.T) {
	svc, err := crypto.NewEnvelopeService(censusKey('o'), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	dbPath, _ := seedEveryColumn(t, svc)

	census, err := crypto.TakeCensus(context.Background(), openForCensus(t, dbPath), []crypto.CandidateKey{
		{Name: "a fresh key", Key: censusKey('f')},
	})
	if err != nil {
		t.Fatalf("TakeCensus() error = %v", err)
	}
	if census.Sealed() != 4 || census.Unknown() != 4 || census.Opens("a fresh key") != 0 {
		t.Fatalf("census = %+v; want four sealed rows, all under a key the census was not given", census)
	}
}

// TestTakeCensus_CountsADuplicateCandidateOnce proves two names for one key
// do not double-count a row.
func TestTakeCensus_CountsADuplicateCandidateOnce(t *testing.T) {
	held := censusKey('d')
	svc, err := crypto.NewEnvelopeService(held, "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	dbPath, _ := seedEveryColumn(t, svc)

	census, err := crypto.TakeCensus(context.Background(), openForCensus(t, dbPath), []crypto.CandidateKey{
		{Name: "current", Key: held},
		{Name: "previous", Key: held},
	})
	if err != nil {
		t.Fatalf("TakeCensus() error = %v", err)
	}
	if census.Opens("current") != 4 || census.Opens("previous") != 0 {
		t.Fatalf("current opens %d, previous opens %d; want 4 and 0", census.Opens("current"), census.Opens("previous"))
	}
}

// TestTakeCensus_ReadsANeverMigratedDatabaseAsEmpty proves a database with
// no tables yet counts as holding nothing, rather than as an error.
func TestTakeCensus_ReadsANeverMigratedDatabaseAsEmpty(t *testing.T) {
	census, err := crypto.TakeCensus(context.Background(), emptyReader{}, nil)
	if err != nil {
		t.Fatalf("TakeCensus() error = %v", err)
	}
	if census.Sealed() != 0 || len(census.Columns) != 4 {
		t.Fatalf("census = %+v; want four empty columns", census)
	}
}

// emptyReader is a database with no rows in any table.
type emptyReader struct{}

// StoredValues returns nothing.
func (emptyReader) StoredValues(context.Context, string, string) ([]ent.StoredValue, error) {
	return nil, nil
}

// failingReader succeeds for a number of reads, then fails every later one.
type failingReader struct {
	okReads int
	calls   int
}

// StoredValues returns one sealed-looking row until okReads is used up.
func (r *failingReader) StoredValues(context.Context, string, string) ([]ent.StoredValue, error) {
	r.calls++
	if r.calls > r.okReads {
		return nil, errors.New("connection reset by peer")
	}
	return []ent.StoredValue{{ID: 1, Value: `{"_encrypted":"v1$AES256GCM$AAAA$AAAA"}`}}, nil
}

// TestTakeCensus_AReadErrorIsNeverAPartialCount proves a failure partway
// through returns an error and no census, so the setup command can never
// act on "the first two tables I managed to read".
func TestTakeCensus_AReadErrorIsNeverAPartialCount(t *testing.T) {
	census, err := crypto.TakeCensus(context.Background(), &failingReader{okReads: 2}, nil)
	if err == nil {
		t.Fatal("TakeCensus() returned no error when the third read failed")
	}
	if !strings.Contains(err.Error(), "saved launch configurations") {
		t.Errorf("TakeCensus() error %q does not name what it was counting", err)
	}
	if len(census.Columns) != 0 || census.Sealed() != 0 {
		t.Fatalf("TakeCensus() returned %+v alongside its error; a partial census must not escape", census)
	}
}

// fixedReader returns the same rows for every column.
type fixedReader []ent.StoredValue

// StoredValues returns the fixed rows.
func (r fixedReader) StoredValues(context.Context, string, string) ([]ent.StoredValue, error) {
	return r, nil
}

// TestTakeCensus_ClassifiesStoredShapes pins which stored values count as
// sealed. Plaintext must not, a damaged sealed row must, and an unbound
// envelope from before rows were bound must open under its key.
func TestTakeCensus_ClassifiesStoredShapes(t *testing.T) {
	key := censusKey('k')
	svc, err := crypto.NewEnvelopeService(key, "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	unbound, err := svc.Encrypt([]byte(`{"password":"legacy"}`))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	sealedJSON, err := json.Marshal(map[string]string{crypto.EncryptedKeyMarker: unbound})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	cases := []struct {
		name     string
		value    string
		sealed   bool
		opensKey bool
	}{
		{"an unbound legacy envelope", string(sealedJSON), true, true},
		{"plaintext properties", `{"host":"10.0.0.1","port":22}`, false, false},
		{"a real entry that happens to use the marker name", `{"_encrypted":"x","other":"y"}`, false, false},
		{"a damaged sealed row", `{"_encrypted": "v1$AES256GCM$` + "\x00", true, false},
		{"a marker holding a non-string", `{"_encrypted": 12}`, true, false},
		{"a malformed envelope", `{"_encrypted":"not-an-envelope"}`, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			census, err := crypto.TakeCensus(context.Background(), fixedReader{{ID: 1, Value: tc.value}},
				[]crypto.CandidateKey{{Name: "held", Key: key}})
			if err != nil {
				t.Fatalf("TakeCensus() error = %v", err)
			}
			// Look at a JSON column only; the bare column reads the same
			// value as a seed.
			dev := census.Columns[1]
			if got := dev.Sealed == 1; got != tc.sealed {
				t.Fatalf("sealed = %v, want %v (%+v)", got, tc.sealed, dev)
			}
			if got := dev.Opens["held"] == 1; got != tc.opensKey {
				t.Fatalf("opens under the held key = %v, want %v (%+v)", got, tc.opensKey, dev)
			}
		})
	}
}

// TestKeyOpens_DistinguishesAWrongKeyFromAnUnreadableValue pins KeyOpens'
// three answers.
func TestKeyOpens_DistinguishesAWrongKeyFromAnUnreadableValue(t *testing.T) {
	key := censusKey('r')
	svc, err := crypto.NewEnvelopeService(key, "whatever-tag", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	bound, err := svc.EncryptBound([]byte("secret"), []byte("row-binding"))
	if err != nil {
		t.Fatalf("EncryptBound() error = %v", err)
	}

	if ok, err := crypto.KeyOpens(bound, key); !ok || err != nil {
		t.Fatalf("KeyOpens(right key) = %v, %v; want true, nil", ok, err)
	}
	if ok, err := crypto.KeyOpens(bound, censusKey('w')); ok || err != nil {
		t.Fatalf("KeyOpens(wrong key) = %v, %v; want false, nil", ok, err)
	}
	for _, bad := range []string{"", "a$b", "v1$ROT13$AAAA$AAAA", "v1$AES256GCM$!!!!$AAAA"} {
		if ok, err := crypto.KeyOpens(bad, key); ok || err == nil {
			t.Errorf("KeyOpens(%q) = %v, %v; want false and an error", bad, ok, err)
		}
	}
	if _, err := crypto.KeyOpens(bound, []byte("short")); err == nil {
		t.Error("KeyOpens accepted a key that is not 32 bytes")
	}
}

// BenchmarkTakeCensus measures the census over ten thousand sealed rows per
// column against three candidate keys, the worst case being a row no
// candidate opens, so every candidate is tried.
func BenchmarkTakeCensus(b *testing.B) {
	svc, err := crypto.NewEnvelopeService(censusKey('b'), "v1", nil, "")
	if err != nil {
		b.Fatalf("NewEnvelopeService() error = %v", err)
	}
	rows := make(fixedReader, 10000)
	for i := range rows {
		env, err := svc.EncryptBound([]byte(fmt.Sprintf("secret-%d", i)), []byte("binding"))
		if err != nil {
			b.Fatalf("EncryptBound() error = %v", err)
		}
		sealed, _ := json.Marshal(map[string]string{crypto.EncryptedKeyMarker: env})
		rows[i] = ent.StoredValue{ID: i + 1, Value: string(sealed)}
	}
	candidates := []crypto.CandidateKey{
		{Name: "a", Key: censusKey('1')},
		{Name: "b", Key: censusKey('2')},
		{Name: "c", Key: censusKey('3')},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := crypto.TakeCensus(context.Background(), rows, candidates); err != nil {
			b.Fatal(err)
		}
	}
}
