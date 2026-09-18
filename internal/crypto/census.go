// The census: counting, for every column sealed under the master key, how many
// rows are sealed and which candidate key holds each one.
package crypto

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// KeyOpens reports whether key is the master key an envelope was sealed
// under, by unwrapping the envelope's data key with it.
//
// It deliberately ignores two things an EnvelopeService relies on. The
// version tag is a label an operator chose (MASTER_ENCRYPTION_KEY_VERSION),
// not a property of the key, so two different keys can carry the same tag
// and one key can carry any tag: a census that trusted it would call a row
// written under "v2" unreadable by a key it is in fact under. And the row
// binding is checked only when the data itself is opened, which this does
// not do, because the question here is which key holds a row, not whether
// the row is intact.
//
// An envelope that cannot be parsed is an error rather than false, so a
// caller can tell "sealed under some other key" from "not a sealed value I
// understand". A wrong key is false with no error, which is the ordinary
// answer.
func KeyOpens(envelope string, key []byte) (bool, error) {
	kek, err := NewAESService(key)
	if err != nil {
		return false, err
	}
	return kekOpens(envelope, kek)
}

// kekOpens is KeyOpens with the key's cipher already built, so the census
// builds each candidate's cipher once rather than once per row.
func kekOpens(envelope string, kek Service) (bool, error) {
	parts := strings.SplitN(envelope, "$", 4)
	if len(parts) != 4 {
		return false, fmt.Errorf("%w: expected 4 fields separated by %q, got %d", ErrMalformedEnvelope, "$", len(parts))
	}
	if parts[1] != algorithmTag && parts[1] != boundAlgorithmTag {
		return false, fmt.Errorf("%w: %q", ErrUnknownAlgorithm, parts[1])
	}
	wrappedDEK, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, fmt.Errorf("%w: wrapped key is not valid base64: %v", ErrMalformedEnvelope, err)
	}

	dek, err := kek.Decrypt(wrappedDEK)
	if err != nil {
		// GCM authentication failed: this key did not wrap this data key.
		return false, nil
	}
	zeroBytes(dek)
	return true, nil
}

// CandidateKey is a key the census tests every sealed row against.
type CandidateKey struct {
	// Name says where the key came from, in words a refusal can print as
	// they stand: "the key in /srv/pleiades/.env".
	Name string

	// Key is the raw 32-byte key.
	Key []byte
}

// ColumnCensus is what one encrypted column holds.
type ColumnCensus struct {
	// Noun names the rows in plain words: "credentials".
	Noun string

	// Sealed is how many rows hold a ciphertext at all.
	Sealed int

	// Opens counts, per candidate name, the sealed rows that candidate is
	// the key for.
	Opens map[string]int

	// Unknown is how many sealed rows no candidate opens, counting a
	// ciphertext that cannot be parsed. These are held by some key the
	// census was not given.
	Unknown int
}

// Census is what every encrypted column in one database holds.
type Census struct {
	// Columns lists every encrypted column, in encryptedColumns order,
	// including those that hold nothing.
	Columns []ColumnCensus
}

// Sealed is how many rows across every column hold a ciphertext.
func (c Census) Sealed() int {
	n := 0
	for _, col := range c.Columns {
		n += col.Sealed
	}
	return n
}

// Opens is how many rows across every column the named candidate opens.
func (c Census) Opens(name string) int {
	n := 0
	for _, col := range c.Columns {
		n += col.Opens[name]
	}
	return n
}

// Unknown is how many sealed rows across every column no candidate opens.
func (c Census) Unknown() int {
	n := 0
	for _, col := range c.Columns {
		n += col.Unknown
	}
	return n
}

// StoredValueReader reads a column's raw stored text. internal/ent's
// ExistingDatabase is the production implementation: it reads with plain
// SQL, so no decrypting interceptor can stand between the census and the
// ciphertext, and a table an older schema does not have reads as empty.
type StoredValueReader interface {
	StoredValues(ctx context.Context, table, column string) ([]ent.StoredValue, error)
}

// TakeCensus counts, for every column this package encrypts, how many rows
// hold a ciphertext and which candidate key each one is under.
//
// It is how the setup command decides whether writing a new master key would
// make stored data unreadable, so it fails in one direction only: any read
// error is returned, and no partial census is. A census that stopped halfway
// and reported what it had would read as a database holding less than it
// does, which is the one wrong answer that permits destroying data.
//
// Candidates holding the same key are counted once, under the first name.
func TakeCensus(ctx context.Context, r StoredValueReader, candidates []CandidateKey) (Census, error) {
	unique, err := prepareCandidates(candidates)
	if err != nil {
		return Census{}, err
	}
	census := Census{Columns: make([]ColumnCensus, 0, len(encryptedColumns))}

	for _, col := range encryptedColumns {
		rows, err := r.StoredValues(ctx, col.table, col.column)
		if err != nil {
			return Census{}, fmt.Errorf("counting %s: %w", col.noun, err)
		}
		cc := ColumnCensus{Noun: col.noun, Opens: map[string]int{}}
		for _, row := range rows {
			envelope, sealed := sealedEnvelope(row.Value, col.bare)
			if !sealed {
				continue
			}
			cc.Sealed++
			if name, ok := whichCandidate(envelope, unique); ok {
				cc.Opens[name]++
			} else {
				cc.Unknown++
			}
		}
		census.Columns = append(census.Columns, cc)
	}
	return census, nil
}

// preparedCandidate is a candidate with its cipher built once.
type preparedCandidate struct {
	name string
	kek  Service
}

// prepareCandidates drops a candidate whose key an earlier one already
// holds, and builds each remaining key's cipher. A key that is not a valid
// AES-256 key is an error: a census that silently skipped it would count
// the rows it holds as unknown rather than refusing to guess.
func prepareCandidates(candidates []CandidateKey) ([]preparedCandidate, error) {
	seen := map[string]bool{}
	var out []preparedCandidate
	for _, c := range candidates {
		fp := Fingerprint(c.Key)
		if seen[fp] {
			continue
		}
		seen[fp] = true
		kek, err := NewAESService(c.Key)
		if err != nil {
			return nil, fmt.Errorf("candidate key %s: %w", c.Name, err)
		}
		out = append(out, preparedCandidate{name: c.Name, kek: kek})
	}
	return out, nil
}

// whichCandidate returns the name of the candidate an envelope is under.
// A ciphertext that cannot be parsed matches none.
func whichCandidate(envelope string, candidates []preparedCandidate) (string, bool) {
	for _, c := range candidates {
		opens, err := kekOpens(envelope, c.kek)
		if err != nil {
			return "", false
		}
		if opens {
			return c.name, true
		}
	}
	return "", false
}

// sealedEnvelope extracts the ciphertext from one stored value, and reports
// whether the value is sealed at all.
//
// A bare column is sealed when the value carries an envelope's algorithm tag
// in its second field; the mesh signing key hook documents why a plaintext
// seed can never look like one. A JSON column is sealed when it holds the
// object the hooks write, whose single key is EncryptedKeyMarker. The test
// is strict for the reason isUndecryptedInputs gives: a map holding a real
// entry named EncryptedKeyMarker alongside others is plaintext, not sealed.
//
// A value that names the marker but does not parse is counted as sealed,
// with an empty envelope that no key opens. Guessing "plaintext" there would
// be the permissive mistake: a damaged row that was sealed is still data a
// key change would cost.
func sealedEnvelope(value string, bare bool) (string, bool) {
	if bare {
		parts := strings.SplitN(value, "$", 4)
		sealed := len(parts) == 4 && (parts[1] == algorithmTag || parts[1] == boundAlgorithmTag)
		return value, sealed
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &fields); err != nil {
		return "", strings.Contains(value, EncryptedKeyMarker)
	}
	raw, marked := fields[EncryptedKeyMarker]
	if !marked || len(fields) != 1 {
		return "", false
	}
	var envelope string
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", true
	}
	return envelope, true
}
