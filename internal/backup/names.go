// The names backup files are written under, and reading one back.
package backup

import (
	"regexp"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/keyregistry"
)

// nameTimeLayout is the UTC timestamp in a backup's name. It sorts in time
// order as plain text, so `ls` lists backups oldest first.
const nameTimeLayout = "20060102T150405Z"

// noKeyMark stands in for the key in the name of a backup taken while .env
// held no key, so the name still says which key it needs: none known.
const noKeyMark = "nokey"

// namePattern is the one shape Name.String writes.
var namePattern = regexp.MustCompile(`^pleiades-(\d{8}T\d{6}Z)-([0-9a-f]{8}|nokey)(-before-restore)?\.dump$`)

// Name is what a backup's file name records: when it was taken, which key
// its sealed rows were under, and whether a restore set it aside.
//
// The key is the first eight hex characters of its fingerprint, never the
// key. It is in the name because a backup is useless without its key, and the
// moment someone needs to know which key a file needs is when they are
// looking at the file, not when they are running a command against it.
type Name struct {
	// Taken is when the backup started, to the second, in UTC.
	Taken time.Time

	// Key is the first eight hex characters of the key's fingerprint, or
	// empty when .env held no key.
	Key string

	// BeforeRestore marks the copy a restore takes of the database it is
	// about to replace.
	BeforeRestore bool
}

// newName names a backup taken now under key, which may be nil.
func newName(now time.Time, key []byte, beforeRestore bool) Name {
	n := Name{Taken: now.UTC().Truncate(time.Second), BeforeRestore: beforeRestore}
	if key != nil {
		n.Key = crypto.Fingerprint(key)[:8]
	}
	return n
}

// String is the file name.
func (n Name) String() string {
	key := n.Key
	if key == "" {
		key = noKeyMark
	}
	s := "pleiades-" + n.Taken.UTC().Format(nameTimeLayout) + "-" + key
	if n.BeforeRestore {
		s += "-before-restore"
	}
	return s + ".dump"
}

// KeyLabel is the key as a person reads it: "3f9a-c21b", the same grouping
// setup and the activity trail use, or "no key".
func (n Name) KeyLabel() string {
	if n.Key == "" {
		return "no key"
	}
	return keyregistry.Short(n.Key)
}

// ParseName reads a name Name.String wrote. Anything else is not a backup
// this command took, and reports false.
func ParseName(s string) (Name, bool) {
	m := namePattern.FindStringSubmatch(s)
	if m == nil {
		return Name{}, false
	}
	taken, err := time.Parse(nameTimeLayout, m[1])
	if err != nil {
		return Name{}, false
	}
	n := Name{Taken: taken, BeforeRestore: m[3] != ""}
	if m[2] != noKeyMark {
		n.Key = m[2]
	}
	return n, true
}
