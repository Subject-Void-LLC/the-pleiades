// What virt.fusion.vm.info reads of a VM beyond its power state: the
// identifiers and hardware its vmx keys name, its snapshots, and VMware
// Tools' state, each one more read on the Mac.
package vm

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vmrun"
)

// The stats info reports beyond exists, state and guest_ip.
const (
	statDisplayName     = "display_name"
	statGuestOS         = "guest_os"
	statMemoryMB        = "memory_mb"
	statCPUs            = "cpus"
	statFirmware        = "firmware"
	statHardwareVersion = "hardware_version"
	statNICs            = "nics"
	statDisks           = "disks"
	statSnapshots       = "snapshots"
	statToolsState      = "tools_state"
)

// detailKeys is every stat detail can report, in the order info records
// them.
var detailKeys = []string{statDisplayName, statGuestOS, statMemoryMB, statCPUs, statFirmware, statHardwareVersion, statNICs, statDisks, statSnapshots, statToolsState}

// maxNICs and maxDisks bound what info lists of a VM: a vmx names at most
// ten Ethernet adapters, and no real VM has more disks than this.
const (
	maxNICs  = 10
	maxDisks = 32
)

// diskKey is a vmx key naming a disk's file: nvme0:0.fileName and the like.
var diskKey = regexp.MustCompile(`^((?:nvme|sata|scsi|ide)[0-9]+:[0-9]+)\.fileName$`)

// detail reads the VM's configuration and snapshots, and VMware Tools'
// state when it is on. A read that fails is named in the warnings and the
// rest is returned, since the power state alone answered whether the VM
// exists, and that is the question a failover plan asks first.
func detail(ctx context.Context, h vmrun.Host, vmx, state string) (map[string]any, []string) {
	out := map[string]any{}
	var warnings []string
	if cfg, err := h.Config(ctx, vmx); err != nil {
		warnings = append(warnings, fmt.Sprintf("the VM's configuration was not read: %v", err))
	} else {
		for k, v := range fromConfig(cfg) {
			out[k] = v
		}
	}
	if snaps, err := h.Snapshots(ctx, vmx); err != nil {
		warnings = append(warnings, fmt.Sprintf("the VM's snapshots were not read: %v", err))
	} else {
		out[statSnapshots] = nonNil(snaps)
	}
	if state == stateOn {
		if tools, err := h.ToolsState(ctx, vmx); err != nil {
			warnings = append(warnings, fmt.Sprintf("VMware Tools' state was not read: %v", err))
		} else if tools != "" {
			out[statToolsState] = tools
		}
	}
	return out, warnings
}

// fromConfig is what info reports of a VM's vmx keys: an allowlist of
// identifiers and hardware, never the keys themselves, since a vmx can
// carry a secret (a guestinfo value handed to the guest, a VNC password).
// A key the vmx leaves out is left out here, rather than guessed at
// VMware's default.
func fromConfig(cfg map[string]string) map[string]any {
	out := map[string]any{}
	for stat, key := range map[string]string{statDisplayName: "displayName", statGuestOS: "guestOS", statFirmware: "firmware"} {
		if v := cfg[key]; v != "" {
			out[stat] = v
		}
	}
	for stat, key := range map[string]string{statMemoryMB: "memsize", statCPUs: "numvcpus", statHardwareVersion: "virtualHW.version"} {
		if n, err := strconv.Atoi(cfg[key]); err == nil {
			out[stat] = n
		}
	}
	nics := []any{}
	for n := 0; n < maxNICs; n++ {
		prefix := fmt.Sprintf("ethernet%d.", n)
		if strings.EqualFold(cfg[prefix+"present"], "FALSE") || !hasPrefix(cfg, prefix) {
			continue
		}
		nic := map[string]any{"number": n}
		for field, key := range map[string]string{"connection_type": "connectionType", "virtual_dev": "virtualDev", "address_type": "addressType"} {
			if v := cfg[prefix+key]; v != "" {
				nic[field] = v
			}
		}
		// A static address is in address; one Fusion made is in
		// generatedAddress.
		if mac := firstNonEmpty(cfg[prefix+"address"], cfg[prefix+"generatedAddress"]); mac != "" {
			nic["mac"] = mac
		}
		nics = append(nics, nic)
	}
	out[statNICs] = nics
	var slots []string
	for k := range cfg {
		if m := diskKey.FindStringSubmatch(k); m != nil && !strings.EqualFold(cfg[m[1]+".present"], "FALSE") {
			slots = append(slots, m[1])
		}
	}
	slices.Sort(slots)
	disks := []any{}
	for _, slot := range slots[:min(len(slots), maxDisks)] {
		disk := map[string]any{"slot": slot, "file": cfg[slot+".fileName"]}
		if t := cfg[slot+".deviceType"]; t != "" {
			disk["device_type"] = t
		}
		disks = append(disks, disk)
	}
	out[statDisks] = disks
	return out
}

// hasPrefix reports whether any of cfg's keys starts with prefix.
func hasPrefix(cfg map[string]string, prefix string) bool {
	for k := range cfg {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// firstNonEmpty is the first of values that is not empty.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// nonNil is list, or an empty list for nil.
func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}
