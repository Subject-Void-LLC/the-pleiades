// Tests of the failures the read-only VM methods share (virt.vbox.vm.addresses,
// log and host_keys): the host refusing to describe the VM, and the context
// refusing the exists stat, for a VM the host has and for one it does not.
package vm

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// TestReadOnlyMethods_FailWhenTheReadOrTheExistsStatFails holds each method
// to failing the task, never to answering, when VBoxManage refuses
// showvminfo, or when the exists stat cannot be recorded whether the VM is
// there or not.
func TestReadOnlyMethods_FailWhenTheReadOrTheExistsStatFails(t *testing.T) {
	refused := vboxmanage.Output{ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}
	for _, fqcn := range []string{"virt.vbox.vm.addresses", "virt.vbox.vm.log", "virt.vbox.vm.host_keys"} {
		t.Run(fqcn, func(t *testing.T) {
			broken := vboxmanagetest.New(windowsVM())
			broken.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo": refused}
			onModel(t, broken)
			if _, err := call(t, fqcn, false, newRecorder(), map[string]any{"name": "win-lab"}); err == nil || !strings.Contains(err.Error(), "refused") {
				t.Errorf("a VM the host refused to describe: %v, want the host's refusal", err)
			}

			onModel(t, vboxmanagetest.New(windowsVM()))
			for _, name := range []string{"win-lab", "gone"} {
				failing := newRecorder()
				failing.failOn = statExists
				if _, err := call(t, fqcn, false, failing, map[string]any{"name": name}); err == nil || !strings.Contains(err.Error(), "recorder: refusing") {
					t.Errorf("%s: recording exists failed and the task said %v", name, err)
				}
			}
		})
	}
}
