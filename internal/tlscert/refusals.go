// The refusals: what this package says when it will not write into a
// directory, and why every one of these sentences names a file.
//
// They live together because they are one decision expressed three ways, and
// because the message IS the fix. A refusal that says "material this
// controller did not write" about a zero-length file, a permission error and
// somebody's real private key sends an operator to the same wrong place three
// times; that was the old message, and the directory it produced could not be
// unblocked by anybody who believed it. So each of these names the exact
// file, quotes what the operating system said when that is the fact, gives
// the identity this process is running as when that is what has to change,
// and states both ways out.
package tlscert

import (
	"fmt"
	"os"
)

// foreignKeyRefusal is the message for the one thing this package will not
// write over.
//
// It names the file, says why the refusal exists, and gives both ways out,
// because an operator who mounted half a secret is the only person who can
// finish the job.
func foreignKeyRefusal(path string) error {
	return fmt.Errorf(
		"%s holds a private key that was not written by this controller, and a private key exists in exactly one place, so it is never replaced; set TLS_CERT_FILE and TLS_KEY_FILE to serve it deliberately, or move it aside to have a self-signed pair provisioned here",
		path)
}

// unprovableKey is the refusal for a real private key whose authorship cannot
// be established, because the records that would answer it cannot be read.
//
// It is a separate message from foreignKeyRefusal because it is a separate
// fact. "This is somebody else's key" and "nothing here can tell me whose key
// this is" send an operator to two different places, and only one of them is
// fixed by pointing TLS_KEY_FILE at the file.
func unprovableKey(keyPath string, cause error) error {
	return fmt.Errorf(
		"%s holds a private key and the record of who wrote it cannot be read (%w), so replacing it could destroy a key that exists nowhere else; %s, or move it aside to have a self-signed pair provisioned here",
		keyPath, cause, readableBy())
}

// unreadableMaterial is the refusal for a file that exists and could not be
// read at all.
//
// The old message called this "material this controller did not write", which
// is a guess dressed as a fact and sends an operator looking for a secret
// nobody put there. The state that produces it in practice is two controllers
// running as different users over one directory: every file this package
// writes is 0600, so the second one gets "permission denied" on its sibling's
// work. So the message names the file, quotes what the operating system said,
// and names the user this process is running as, which is the thing that has
// to change.
func unreadableMaterial(path string, cause error) error {
	return fmt.Errorf(
		"%s exists and could not be read (%w), so this controller cannot tell whether replacing it would destroy a private key; %s, or move it aside to have a self-signed pair provisioned here",
		path, cause, readableBy())
}

// readableBy names the identity a file has to be readable by, in the terms an
// operator changes it with.
func readableBy() string {
	uid := os.Geteuid()
	if uid < 0 {
		// Documented to be -1 on Windows, where a numeric uid would be a
		// wrong answer rather than a missing one.
		return "make it readable by the account this controller runs as"
	}
	return fmt.Sprintf("make it readable by uid %d, which is the user this controller runs as", uid)
}
