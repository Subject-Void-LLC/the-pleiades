// The harness the snapshot methods' tests share: a model VirtualBox host in
// place of a real one, and a context that keeps what a method records.
// Each test calls a method through its registered descriptor, so what it
// exercises is what the dispatcher calls.
package snapshot

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// recorder is a RunbookContext keeping each stat a method sets.
type recorder struct {
	stats  map[string]any
	failOn string
}

func newRecorder() *recorder { return &recorder{stats: map[string]any{}} }

func (r *recorder) InjectSecrets() map[string]string { return nil }

func (r *recorder) SetStat(key string, value any) error {
	if key == r.failOn {
		return fmt.Errorf("recorder: refusing %q", key)
	}
	r.stats[key] = value
	return nil
}

func (r *recorder) EmitFact(key string, value any) error { return r.SetStat(key, value) }

// diff returns the recorded diff's two halves.
func (r *recorder) diff(t *testing.T) (before, after map[string]any) {
	t.Helper()
	d, ok := r.stats[sdk.StatDiff].(map[string]any)
	if !ok {
		t.Fatalf("no diff recorded: %v", r.stats)
	}
	return d[sdk.DiffBefore].(map[string]any), d[sdk.DiffAfter].(map[string]any)
}

// host is the VirtualBox host every test targets.
var host = &inventorytest.Stub{StubName: "vengeance", Caps: []capability.Name{capability.NameVirtualBox, capability.NameWinRM}}

// onModel makes each method reach model instead of a real host.
func onModel(t *testing.T, model *vboxmanagetest.Host) {
	t.Helper()
	old := hostFunc
	hostFunc = func(sdk.RunbookContext, inventory.InventoryItem) (vboxmanage.Host, error) {
		return model.VBoxHost(), nil
	}
	t.Cleanup(func() { hostFunc = old })
}

// UUIDs the tests' snapshots have.
const (
	cleanUUID  = "5732a952-0b85-4e3a-820a-1a568f31166b"
	secondUUID = "fe443bb2-c464-4ccc-8df7-91445f718b63"
)

// lab is a stopped VM with one snapshot, clean, which is current.
func lab() *vboxmanagetest.VM {
	return &vboxmanagetest.VM{
		Name: "ubuntu-lab", UUID: "9883f6c3-b17d-4077-aa52-d9c49a6912a5", State: vboxmanage.StatePoweroff, MemoryMB: 1024, CPUs: 2,
		Snapshots: []vboxmanagetest.Snapshot{{Name: "clean", UUID: cleanUUID, Description: "fresh install"}},
		Current:   cleanUUID,
	}
}

// twoClean is lab with a second snapshot also named clean, as VirtualBox
// itself allows.
func twoClean() *vboxmanagetest.VM {
	vm := lab()
	vm.Snapshots = append(vm.Snapshots, vboxmanagetest.Snapshot{Name: "clean", UUID: secondUUID, Parent: cleanUUID})
	vm.Current = secondUUID
	return vm
}

// call runs fqcn's Invoke, or its Check when check is true, through the
// registered descriptor.
func call(t *testing.T, fqcn string, check bool, rc sdk.RunbookContext, params map[string]any) (collection.Result, error) {
	t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	if check {
		return d.Check(context.Background(), rc, host, params)
	}
	return d.Invoke(context.Background(), rc, host, params)
}

// changes returns the calls a method made on the model other than the
// reads, which a check must never make.
func changes(model *vboxmanagetest.Host) []string {
	var changed []string
	for _, c := range model.Calls() {
		if !strings.HasPrefix(c, "VBoxManage.exe showvminfo ") && !strings.HasPrefix(c, "VBoxManage.exe list ") {
			changed = append(changed, c)
		}
	}
	return changed
}
