// The keys a backup is taken and restored under, and whether a controller
// holding them could read what a database holds.
package backup

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/keyregistry"
	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
)

// keySet is the keys a controller would start with, each with the version
// tag it would give that key, and the words a message names each by.
type keySet struct {
	// key and version are MASTER_ENCRYPTION_KEY and its tag. key is nil
	// when there is none.
	key     []byte
	version string

	// previous and previousVersion are the rotation's previous key and its
	// tag, both empty outside a rotation.
	previous        []byte
	previousVersion string

	// name and previousName say where each key came from.
	name, previousName string
}

// readKeys reads the keys the compose env file in dir holds, the same
// variables the controller reads. A missing file, or one with no key, is a
// set with no key rather than an error; a file setup would refuse to read is
// an error, since a controller given it would not start either.
func readKeys(dir *setup.Dir) (keySet, error) {
	display := dir.Show(setup.ComposeFile)
	data, _, err := dir.ReadExisting(setup.ComposeFile)
	if err != nil {
		return keySet{}, err
	}
	file, err := setup.ParseEnvFile(data)
	if err != nil {
		return keySet{}, fmt.Errorf("%w (in %s)", err, display)
	}
	ks := keySet{version: crypto.DefaultKeyVersion, name: "the key in " + display, previousName: "the previous key in " + display}
	if raw, ok := file.Get(setup.VarMasterKey); ok {
		// ParseEnvFile already refused a value that does not decode.
		ks.key, _ = crypto.DecodeKey(raw, setup.VarMasterKey)
	}
	if v, ok := file.Get(setup.VarMasterKeyVersion); ok {
		ks.version = v
	}
	if raw, ok := file.Get(setup.VarPreviousKey); ok {
		ks.previous, _ = crypto.DecodeKey(raw, setup.VarPreviousKey)
		ks.previousVersion, _ = file.Get(setup.VarPreviousKeyVersion)
	}
	return ks, nil
}

// entered is the key set for a key typed at a restore, whose tag is not yet
// known: it is read off the rows the key opens.
func entered(key []byte) keySet {
	return keySet{key: key, name: "the key you entered"}
}

// candidates lists the set's keys for a census.
func (ks keySet) candidates() []crypto.CandidateKey {
	var out []crypto.CandidateKey
	if ks.key != nil {
		out = append(out, crypto.CandidateKey{Name: ks.name, Key: ks.key})
	}
	if ks.previous != nil {
		out = append(out, crypto.CandidateKey{Name: ks.previousName, Key: ks.previous})
	}
	return out
}

// short is the key's fingerprint as a person reads it, or "none".
func (ks keySet) short() string {
	if ks.key == nil {
		return "none"
	}
	return keyregistry.Short(crypto.Fingerprint(ks.key))
}

// readable decides whether a controller starting with ks could read every
// sealed value census counted: each must open under one of the keys, and
// carry the tag ks gives that key, because a controller finds a value's key
// by its tag. keysOnRecord are the short fingerprints the database's own
// key registry lists, which a refusal names as the keys to look for.
func (ks keySet) readable(census crypto.Census, keysOnRecord []string) error {
	var problems []string
	if n := census.Unknown(); n > 0 {
		problems = append(problems, fmt.Sprintf(
			"%d of them %s under no key in .env (which holds key %s). %s",
			n, pick(n == 1, "is", "are"), ks.short(), recordedKeys(keysOnRecord)))
	}
	for _, k := range []struct {
		name, version string
	}{{ks.name, ks.version}, {ks.previousName, ks.previousVersion}} {
		for tag, n := range census.TagsUnder(k.name) {
			if tag == k.version {
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"%s %s under %s and %s the version tag %s, while .env gives that key the tag %s. A controller finds a value's key by its tag, so it could not read %s.",
				countOf(n), pick(n == 1, "opens", "open"), k.name, pick(n == 1, "carries", "carry"), printable(tag), k.version, pick(n == 1, "it", "them")))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("the backup holds %d sealed %s. %s", census.Sealed(), pick(census.Sealed() == 1, "value", "values"), strings.Join(problems, " "))
}

// tagOf is the one version tag every value key opens carries, for a key
// entered at a restore. No values means the default tag; values under two
// tags cannot be read by one controller, which gives each key one tag.
func tagOf(census crypto.Census, ks keySet) (string, error) {
	tags := census.TagsUnder(ks.name)
	switch len(tags) {
	case 0:
		return crypto.DefaultKeyVersion, nil
	case 1:
		for tag := range tags {
			return tag, nil
		}
	}
	var seen []string
	for tag := range tags {
		seen = append(seen, printable(tag))
	}
	sort.Strings(seen)
	return "", fmt.Errorf("the key you entered opens values carrying %d different version tags (%s), and a controller gives one key one tag, so it could read only some of them", len(tags), strings.Join(seen, ", "))
}

// recordedKeys names the keys the backup's own registry lists.
func recordedKeys(shorts []string) string {
	switch len(shorts) {
	case 0:
		return "The backup's key registry lists no key, so the backup predates it."
	case 1:
		return "The backup's key registry lists key " + shorts[0] + "."
	default:
		return "The backup's key registry lists keys " + strings.Join(shorts, ", ") + "."
	}
}

// countOf is "1 value" or "n values".
func countOf(n int) string {
	return fmt.Sprintf("%d %s", n, pick(n == 1, "value", "values"))
}

// pick returns one when singular, and many otherwise.
func pick(singular bool, one, many string) string {
	if singular {
		return one
	}
	return many
}

// sealedSummary lists what census counted, column by column, in setup's
// words: "3 credentials, the stored properties of 1 device".
func sealedSummary(census crypto.Census) string {
	var parts []string
	for _, col := range census.Columns {
		if col.Sealed > 0 {
			parts = append(parts, setup.CountPhrase(col.Noun, col.Sealed))
		}
	}
	if len(parts) == 0 {
		return "no sealed values"
	}
	return strings.Join(parts, ", ")
}
