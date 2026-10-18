// How the model answers for a registration VirtualBox cannot read.
package vboxmanagetest

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// TestInaccessible_AnswersAsVirtualBox72Does: list vms names it
// <inaccessible> under its UUID and runningvms leaves it out; showvminfo
// and unregistervm --delete are refused with E_ACCESSDENIED; unregistervm
// by UUID forgets it; forgetting what is not registered is not found.
func TestInaccessible_AnswersAsVirtualBox72Does(t *testing.T) {
	const ghost = "9883f6c3-b17d-4077-aa52-d9c49a6912a5"
	h := New(&VM{Name: "gone", UUID: ghost, State: vboxmanage.StatePoweroff, Inaccessible: true})
	if out := run(t, h, "list", "vms"); !strings.Contains(out.Stdout, `"<inaccessible>" {`+ghost+`}`) {
		t.Errorf("list vms = %q", out.Stdout)
	}
	if out := run(t, h, "list", "runningvms"); out.Stdout != "" {
		t.Errorf("runningvms = %q", out.Stdout)
	}
	for _, args := range [][]string{{"showvminfo", ghost, "--machinereadable"}, {"unregistervm", ghost, "--delete"}} {
		if out := run(t, h, args...); out.ExitCode == 0 || !strings.Contains(out.Stderr, "E_ACCESSDENIED") {
			t.Errorf("%v = %+v", args, out)
		}
	}
	if out := run(t, h, "unregistervm", "11111111-1111-4111-8111-111111111111"); out.ExitCode == 0 || !strings.Contains(out.Stderr, "Could not find") {
		t.Errorf("forgetting what is not there = %+v", out)
	}
	if out := run(t, h, "unregistervm", ghost); out.ExitCode != 0 || h.VM(ghost) != nil {
		t.Errorf("forgetting it = %+v, still %v", out, h.VM(ghost))
	}
}
