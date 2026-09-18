// The rules for writing a master key, and the refusals that explain them.
package setup

import (
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/keyregistry"
)

// publishedComposeKeyText is the MASTER_ENCRYPTION_KEY that docker-compose.yml
// shipped, in plain text, before this command existed: base64 of 32 "k"
// bytes. It is public, so it protects nothing, and it is here for one
// reason: a developer who ran the trial stack before this command existed
// has a database encrypted under it, and a refusal that can say "these rows
// are under the key the old compose file published" tells them exactly what
// they are looking at. It is only ever tested against stored rows, never
// written anywhere.
const publishedComposeKeyText = "a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s="

// PublishedComposeKeyName is how a refusal names that key.
const PublishedComposeKeyName = "the key earlier versions of docker-compose.yml published"

// PublishedComposeKey returns the published key's raw bytes.
func PublishedComposeKey() []byte {
	key, err := crypto.DecodeKey(publishedComposeKeyText, "the published compose key")
	if err != nil {
		// The constant is fixed at build time; TestPublishedComposeKey
		// proves it decodes, so this cannot happen in a built binary.
		panic(err)
	}
	return key
}

// Refusal is a decision not to write, with the message that explains it.
// The message is the whole of what an operator reads at that moment, so it
// is written for them, in full, rather than wrapped around an internal error.
type Refusal struct {
	// Message is the complete text shown to the operator.
	Message string
}

// Error returns the message.
func (r *Refusal) Error() string { return r.Message }

// refuse builds a Refusal from paragraphs, starting with the sentence every
// refusal opens with.
func refuse(paragraphs ...string) *Refusal {
	return &Refusal{Message: "setup refused, and wrote nothing.\n\n" + strings.Join(paragraphs, "\n\n")}
}

// KeyContext is everything the key rules look at.
type KeyContext struct {
	// File is the file that holds, or will hold, the key, as a message
	// names it.
	File string

	// Database names the database the census read, without credentials.
	// Empty when none is configured.
	Database string

	// Census is what the database holds, or nil when no database is
	// configured.
	Census *crypto.Census

	// HeldKey is the fingerprint of the key the file already holds, or empty
	// on a first write.
	HeldKey string

	// Held names the census candidates that are keys the file holds: its
	// current key and, during a rotation, its previous one.
	Held []string

	// Replacing is true when the operator named the destructive flag.
	Replacing bool
}

// JudgeKey applies the rules for writing a master key and returns nil when
// the write may go ahead, or the Refusal that explains why it may not.
//
// A first write is refused if the database holds any encrypted row: a new
// key opens none of them, and the key that does is somewhere this command
// cannot see. A replacement is refused if any row opens under a key the file
// holds, and refused without a count, because replacing a key without
// knowing what it protects is the one mistake this command exists to make
// impossible. The destructive flag does not override either rule.
func JudgeKey(k KeyContext) error {
	if !k.Replacing {
		if k.Census == nil || k.Census.Sealed() == 0 {
			return nil
		}
		return refuseFirstWrite(k)
	}

	if k.Census == nil {
		return refuse(
			fmt.Sprintf("%s holds a MASTER_ENCRYPTION_KEY (fingerprint %s), and no database is configured here, so setup cannot count what that key protects. Setup does not replace a key without counting first.", k.File, keyregistry.Short(k.HeldKey)),
			"Run setup where the controller runs, with the same DB_DSN or DB_PATH, so it can read the database this key protects.",
		)
	}

	protected := 0
	for _, name := range k.Held {
		protected += k.Census.Opens(name)
	}
	if protected == 0 {
		return nil
	}
	return refuse(
		fmt.Sprintf("The key in %s (fingerprint %s) is the key for this data in %s:\n%s", k.File, keyregistry.Short(k.HeldKey), k.Database, heldCounts(k.Census, k.Held)),
		"Writing a new MASTER_ENCRYPTION_KEY makes every one of these permanently unreadable. Nothing recovers them. --destroy-existing-encryption-key does not override this.",
		rotationAdvice,
	)
}

// refuseFirstWrite explains a database that already holds encrypted data
// when the file holds no key.
func refuseFirstWrite(k KeyContext) *Refusal {
	paragraphs := []string{
		fmt.Sprintf("The database at %s already holds encrypted data:\n%s", k.Database, sealedCounts(k.Census)),
		fmt.Sprintf("%s holds no MASTER_ENCRYPTION_KEY, and a new key opens none of this. Writing one makes every item above permanently unreadable, because the key that reads them is somewhere setup cannot see.", k.File),
	}
	if published := k.Census.Opens(PublishedComposeKeyName); published > 0 {
		paragraphs = append(paragraphs, fmt.Sprintf(
			"%d of these are encrypted under %s. That key is public: anyone who has read that file can decrypt them. To keep this data, set that key as MASTER_ENCRYPTION_KEY_PREVIOUS with MASTER_ENCRYPTION_KEY_PREVIOUS_VERSION=v1, and rotate to a new key. The steps are in %s.",
			published, PublishedComposeKeyName, rotationPointer))
	} else {
		paragraphs = append(paragraphs, fmt.Sprintf(
			"To keep this data, put the key it was written under back in %s as MASTER_ENCRYPTION_KEY.", k.File))
	}
	paragraphs = append(paragraphs,
		"To discard this data instead, delete the database first. For the compose trial stack, `docker compose down --volumes` deletes everything the stack stored, this database included, and that cannot be undone. Then run setup again.")
	return refuse(paragraphs...)
}

// rotationPointer names where the rotation procedure is documented.
const rotationPointer = `the "Rotating the master key" section of the production guide`

// rotationAdvice is the safe way to change a key that protects data.
const rotationAdvice = "To change this key without losing anything, rotate it: keep it as MASTER_ENCRYPTION_KEY_PREVIOUS (with its version tag as MASTER_ENCRYPTION_KEY_PREVIOUS_VERSION), set a new MASTER_ENCRYPTION_KEY with a new MASTER_ENCRYPTION_KEY_VERSION, and start the controller with ROTATE_ENCRYPTION_KEYS=true. Remove the old key only after the controller logs \"key rotation complete\". The steps are in " + rotationPointer + "."

// sealedCounts lists every column that holds encrypted rows, one per line.
func sealedCounts(c *crypto.Census) string {
	var lines []string
	for _, col := range c.Columns {
		if col.Sealed > 0 {
			lines = append(lines, "  "+countPhrase(col.Noun, col.Sealed))
		}
	}
	return strings.Join(lines, "\n")
}

// heldCounts lists, per column, the rows that open under any held key.
func heldCounts(c *crypto.Census, held []string) string {
	var lines []string
	for _, col := range c.Columns {
		n := 0
		for _, name := range held {
			n += col.Opens[name]
		}
		if n > 0 {
			lines = append(lines, "  "+countPhrase(col.Noun, n))
		}
	}
	return strings.Join(lines, "\n")
}

// countPhrase names n rows of one column in words, singular or plural.
func countPhrase(noun string, n int) string {
	plural := n != 1
	switch noun {
	case "credentials":
		return pick(plural, fmt.Sprintf("%d credential", n), fmt.Sprintf("%d credentials", n))
	case "devices":
		return pick(plural, "the stored properties of 1 device", fmt.Sprintf("the stored properties of %d devices", n))
	case "saved launch configurations":
		return pick(plural, "the survey answers of 1 saved launch configuration", fmt.Sprintf("the survey answers of %d saved launch configurations", n))
	case "mesh signing keys":
		return pick(plural, "1 mesh signing key", fmt.Sprintf("%d mesh signing keys", n))
	default:
		return fmt.Sprintf("%d %s", n, noun)
	}
}

// pick returns many when plural is true, and one otherwise.
func pick(plural bool, one, many string) string {
	if plural {
		return many
	}
	return one
}
