// The "virt.vbox.vm.addresses" method: the addresses a VM's host-only adapters were given.
package vm

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.vbox.vm.addresses",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"winrm",
			},
			RequiredCapabilities: []capability.Name{
				capability.Name("VirtualBoxCapable"),
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
			},
			// TODO(forge): PlatformTargets narrows this manifest to a
			// specific vendor, model, firmware range, or deployment
			// context (pkg/collection.PlatformTarget). Left nil: thin
			// flag parsing only, per Phase 33's own checklist. Add real
			// entries by hand once this method's platform scope is known.
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "Reading is not changing: this reads the VM and the host's DHCP leases and alters nothing, so there is nothing an undo could restore.",
				ReadOnly:   true,
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Reports the addresses a VM's host-only adapters were given, from VirtualBox's DHCP server and from virt.vbox.vm.clone.",
				Description: "Reports each address a VM's host-only adapters have: the leases VirtualBox's own DHCP server handed them (read from the leases file it keeps for the host's account), and the fixed address virt.vbox.vm.clone gave the VM (the extradata pleiades/address). A VM whose first boot did not apply its fixed address shows the address DHCP gave it too, which is how to reach it anyway. A NAT adapter's address is private to the VM and is not reported. Nothing is changed. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the same as a run.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
				},
				Returns: []collection.ReturnField{
					{Name: "addresses", Type: "list", Returned: "always", Description: "Each address as nic (the adapter's number), mac, adapter (the host's), address, source (dhcp or fixed), and for a DHCP lease its state (acked while held, expired after), issued and expires."},
					{Name: "address", Type: "string", Returned: "when there is one", Description: "The address to reach the VM at: the fixed one, or else a held DHCP lease's. A first boot that applied its fixed address leaves the lease it took before then marked held for a while, so a VM whose fixed address was never applied is known by that lease being the only one it answers at."},
				},
				Examples: []collection.Example{
					{Name: "Find a VM's address", RunbookYAML: "- name: Where the lab VM answers\n  virt.vbox.vm.addresses:\n    name: win-lab\n  register: found\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.clone", "virt.vbox.vm.list"},
			},
		},
		Invoke: Addresses,
		Check:  Addresses,
	})
}

// The addresses method's stats, and where each address came from.
const (
	statAddresses = "addresses"
	sourceDHCP    = "dhcp"
	sourceFixed   = "fixed"
	leaseHeld     = "acked"
)

// Addresses implements "virt.vbox.vm.addresses". It only reads, so it is
// its own check.
func Addresses(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.addresses"
	h, name, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	m, err := mustRead(ctx, h, name, device, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	extra, err := h.ExtraData(ctx, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	found, reach, err := addressesOf(ctx, h, m, extra[vboxmanage.ExtraAddress])
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statAddresses, found); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if reach != "" {
		if err := rc.SetStat(statAddress, reach); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	return collection.Result{}, nil
}

// addressesOf lists each host-only adapter's DHCP leases and, on the first
// one, the fixed address clone gave the VM; and returns the address to
// reach it at: the fixed one, or else a held lease's.
func addressesOf(ctx context.Context, h vboxmanage.Host, m vboxmanage.Machine, fixed string) ([]map[string]any, string, error) {
	numbers := make([]int, 0, len(m.NICs))
	for n := range m.NICs {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	found := []map[string]any{}
	leases := map[string][]vboxmanage.Lease{}
	held := ""
	for _, n := range numbers {
		nic := m.NICs[n]
		if nic.Kind != "hostonly" {
			continue
		}
		if fixed != "" {
			found = append(found, map[string]any{"nic": n, "mac": nic.MAC, "adapter": nic.HostOnlyAdapter, statAddress: fixed, "source": sourceFixed})
			fixed = ""
		}
		if _, read := leases[nic.HostOnlyAdapter]; !read {
			all, err := h.DHCPLeases(ctx, nic.HostOnlyAdapter)
			if err != nil {
				return nil, "", err
			}
			leases[nic.HostOnlyAdapter] = all
		}
		for _, l := range leases[nic.HostOnlyAdapter] {
			if l.MAC != nic.MAC {
				continue
			}
			found = append(found, map[string]any{"nic": n, "mac": nic.MAC, "adapter": nic.HostOnlyAdapter, statAddress: l.Address, "source": sourceDHCP,
				"state": l.State, "issued": l.Issued.Format(time.RFC3339), "expires": l.Expires.Format(time.RFC3339)})
			if l.State == leaseHeld && held == "" {
				held = l.Address
			}
		}
	}
	// The fixed address first: a first boot that applied it leaves the
	// lease it took before then marked held for up to ten minutes.
	for _, a := range found {
		if a["source"] == sourceFixed {
			return found, a[statAddress].(string), nil
		}
	}
	return found, held, nil
}
