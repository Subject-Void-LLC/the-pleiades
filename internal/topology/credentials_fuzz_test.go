// Fuzzing the one new place this phase reads untrusted bytes.
//
// A credential file is operator-supplied rather than attacker-supplied,
// which is why this is a fuzz over robustness rather than over a trust
// boundary. The properties it holds are still the ones that matter when a
// file is truncated by a full disk, replaced by a Kubernetes Secret
// projection mid-read, or pasted with the wrong half: the process must
// refuse it cleanly at startup, and it must never put what it read into a
// message.
package topology_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// FuzzCredentialsFromEnv asserts three properties over arbitrary file
// contents.
func FuzzCredentialsFromEnv(f *testing.F) {
	// Seeds: the shapes a real mistake produces, rather than random
	// noise alone.
	f.Add("")
	f.Add("hello")
	f.Add("-----BEGIN NATS USER JWT-----\nabc.def.ghi\n------END NATS USER JWT------\n")
	f.Add("-----BEGIN USER NKEY SEED-----\nSUAGWHMVZKSSR4VPRP5HCLMDK5D66C6VITMFZRWCMOSZYED2PLKNL\n------END USER NKEY SEED------\n")
	f.Add(strings.Repeat("A", 4096))
	f.Add("\x00\x00\x00")

	f.Fuzz(func(t *testing.T, body string) {
		dir := t.TempDir()
		path := filepath.Join(dir, "fuzz.creds")
		if err := os.WriteFile(path, []byte(body), 0o400); err != nil {
			t.Skip("could not write this body to a file")
		}

		creds, err := topology.CredentialsFromEnv(path)

		// 1. Never both. A caller appends a dial option when creds is
		//    non-nil, so returning both would dial with something the
		//    function had already judged unusable.
		if err != nil && creds != nil {
			t.Fatalf("returned %d bytes alongside an error", len(creds))
		}

		// 2. An accepted credential must actually be usable. The whole
		//    reason this function parses rather than merely reading is to
		//    turn a corrupt file into a startup error instead of an
		//    authorization failure against the broker later, so anything
		//    it accepts has to survive the same parse the dial path runs.
		if err == nil {
			if creds == nil {
				t.Fatal("accepted the file and returned no credential")
			}
			if verr := topology.ValidateCredentialForTest(creds); verr != nil {
				t.Fatalf("accepted a credential the dial path then refuses: %v", verr)
			}
		}

		// 3. The error must never quote the file. This is the property
		//    that matters most and the one easiest to break by adding a
		//    helpful %q to a message: a startup error reaches a log, and a
		//    log is what somebody pastes into an issue.
		//
		//    Short bodies are skipped rather than excepted, and the
		//    reason is worth stating: an error legitimately contains a
		//    path, punctuation and library text, so a one or two
		//    character body collides with that meaninglessly. The bound
		//    is on length, not on a list of allowed strings, because a
		//    list of exceptions accumulates until somebody deletes the
		//    rule.
		if err != nil && len(body) >= 16 && strings.Contains(err.Error(), body) {
			t.Fatalf("the error quotes the file's contents back: %v", err)
		}
	})
}
