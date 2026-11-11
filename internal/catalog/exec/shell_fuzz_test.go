// FuzzShellParams is exec.shell's parameter fuzz target, which Phase 38
// requires before a status flips, in the shape of FuzzCommandParams.
//
// The boundary is the same real one: a shell command, a chdir and the two
// guards (creates, removes) come out of a runbook file and are parsed
// before anything connects. The device is nil, so any input that parses
// stops at the target check, which keeps every iteration to microseconds
// and off the network. The property is that no bytes ever panic and that
// a result never claims a change beside an error.
package exec_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/exec"
)

func FuzzShellParams(f *testing.F) {
	f.Add("echo hi", "", "", "")
	f.Add("echo hi; rm -rf /", "/tmp", "/tmp/made", "/tmp/gone")
	f.Add("", "", "", "")
	f.Add("\\", "\x00", "\n", "'")
	f.Add("$(whoami) `id` | &&", "-chdir", "-creates", "-removes")
	f.Add("while true; do :; done", "*", "?", "[a-z]")

	f.Fuzz(func(t *testing.T, cmd, chdir, creates, removes string) {
		params := map[string]any{
			"cmd":                           cmd,
			"chdir":                         chdir,
			"creates":                       creates,
			"removes":                       removes,
			"insecure_skip_host_key_verify": true,
		}
		result, err := exec.Shell(context.Background(), &stubContext{}, nil, params)
		if err == nil {
			t.Fatalf("Shell with no target device returned no error for params %+v", params)
		}
		if result.Changed {
			t.Fatalf("Shell reported a change alongside an error for params %+v", params)
		}
	})
}
