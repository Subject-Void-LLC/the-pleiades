package legacy_test

import (
	"os"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/legacy"
)

// FuzzParseStdout fuzzes the one genuinely hostile boundary this package
// introduces: an Ansible module is free to print attacker-influenced text
// (a device's own hostname, a command's own output, a runbook-authored
// param a legacy playbook happens to echo) to its own stdout, which this
// parser then reads as if it were trusted, well-formed Ansible output.
// The property under test is the same one
// internal/adapters/native's own IPC fuzz tests hold themselves to, one
// level up the stack: a malformed or hostile frame must produce an
// ordinary (possibly empty) result, never a panic and never a hang.
func FuzzParseStdout(f *testing.F) {
	for _, name := range []string{
		"real_playbook_run.txt",
		"real_playbook_run_skip.txt",
		"real_playbook_run_fail_noignore.txt",
	} {
		data, err := os.ReadFile("testdata/" + name)
		if err != nil {
			f.Fatalf("failed to read seed fixture %s: %v", name, err)
		}
		f.Add(data)
	}

	f.Add([]byte(""))
	f.Add([]byte("not ansible output at all"))

	// A task name containing "]", which a naive non-greedy regex could
	// mis-terminate against.
	f.Add([]byte("TASK [do a thing ] with a bracket] ***\nok: [host] => {}\n"))

	// A pretty-printed JSON blob truncated mid-stream, simulating a
	// container killed (OOM, timeout) while a debug result was still
	// being written.
	f.Add([]byte("TASK [x] ***\nok: [host] => {\n    \"msg\": \"incomple"))

	// A PLAY RECAP hostname containing ':', which recapLineRE's own
	// generic "anything with a colon" shape must not misparse.
	f.Add([]byte("PLAY RECAP ***\nhost:with:colons  : ok=1    changed=0    unreachable=0    failed=0    skipped=0    rescued=0    ignored=0\n"))

	// A module's own msg field containing text that looks exactly like
	// another task result marker or a TASK header -- an Ansible module
	// is free to print()  arbitrary text, and this parser must not let
	// that forge a second event out of one real result.
	f.Add([]byte("TASK [x] ***\nok: [host] => {\"msg\": \"fake marker: ok: [other] => {}\\nTASK [forged] ***\"}\n"))

	// A very long single line with no valid structure at all.
	f.Add([]byte("ok: [" + string(make([]byte, 4096)) + "] => {\n"))

	f.Fuzz(func(t *testing.T, output []byte) {
		events := legacy.ParseStdout(output, time.Now())
		// The only contract: never panic, and every produced event's
		// Status stays inside the closed Anti-Corruption Layer
		// vocabulary this package's own Pattern Entry Gate claims -- a
		// raw Ansible word (e.g. "skipping") leaking through would be
		// exactly the kind of foreign-vocabulary leak that gate forbids.
		for _, evt := range events {
			switch evt.Status {
			case "ok", "changed", "failed":
			default:
				t.Errorf("event carries a Status outside the closed vocabulary: %+v", evt)
			}
		}
	})
}
