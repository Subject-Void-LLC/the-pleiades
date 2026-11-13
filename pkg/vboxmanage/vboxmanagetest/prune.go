// The model's answers for what a prune reads and removes: the default
// machine folder, the folders inside a folder, removing a folder, and
// forgetting a registered disk no machine uses, by UUID.
package vboxmanagetest

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// Orphan is a disk VirtualBox has registered that no machine in the model
// uses, as a failed delete or an unregister without --delete leaves one.
type Orphan struct {
	UUID     string
	Location string
	// Parent is the UUID of the disk it descends from, or "".
	Parent string
	// Gone says its file is gone, so list hdds calls it inaccessible.
	Gone bool
}

// defaultMachineFolder is the model's default machine folder, as list
// systemproperties names it on the lab host's account.
const defaultMachineFolder = `C:\Users\pleiades-gate\VirtualBox VMs`

// systemProperties answers list systemproperties with the one line a
// prune reads.
func (h *Host) systemProperties() vboxmanage.Output {
	return vboxmanage.Output{Stdout: "API version:                     7_2\r\nDefault machine folder:          " + defaultMachineFolder + "\r\nRaw-mode Supported:              no\r\n"}
}

// folders lists the machine folders directly inside root: each folder a
// registered machine is in, or a .vbox or .vbox-prev file the model holds.
func (h *Host) folders(root string) vboxmanage.Output {
	prefix := strings.ToLower(strings.TrimRight(root, `\`)) + `\`
	seen := map[string]string{}
	note := func(p string) {
		p = strings.ReplaceAll(p, "/", `\`)
		if !strings.HasPrefix(strings.ToLower(p), prefix) {
			return
		}
		rest := p[len(prefix):]
		if i := strings.Index(rest, `\`); i > 0 {
			seen[strings.ToLower(rest[:i])] = p[:len(prefix)+i]
		}
	}
	for path := range h.Files {
		if lower := strings.ToLower(path); strings.HasSuffix(lower, ".vbox") || strings.HasSuffix(lower, ".vbox-prev") {
			note(path)
		}
	}
	for _, vm := range h.vms {
		note(vm.Folder + `\x.vbox`)
	}
	if len(seen) == 0 {
		return vboxmanage.Output{ExitCode: 3}
	}
	names := make([]string, 0, len(seen))
	for _, full := range seen {
		names = append(names, full)
	}
	sort.Strings(names)
	return vboxmanage.Output{Stdout: strings.Join(names, "\r\n") + "\r\n"}
}

// removeFolder deletes every file the model holds under path, and every
// orphan disk there; a machine's folder is the caller's to refuse.
func (h *Host) removeFolder(path string) vboxmanage.Output {
	prefix := strings.ToLower(strings.TrimRight(path, `\`)) + `\`
	for p := range h.Files {
		if strings.HasPrefix(strings.ToLower(strings.ReplaceAll(p, "/", `\`)), prefix) {
			delete(h.Files, p)
		}
	}
	return vboxmanage.Output{Stdout: "removed\r\n"}
}

// closeOrphan forgets the orphan disk with uuid, deleting its file when
// asked, as closemedium disk does; one with a child is refused.
func (h *Host) closeOrphan(uuid string, args []string) (vboxmanage.Output, bool) {
	for i, o := range h.Orphans {
		if !strings.EqualFold(o.UUID, uuid) {
			continue
		}
		for _, other := range h.Orphans {
			if strings.EqualFold(other.Parent, o.UUID) {
				return errorOutput(fmt.Sprintf("Cannot close medium '%s' because it has 1 child media", o.Location)), true
			}
		}
		h.Orphans = append(h.Orphans[:i], h.Orphans[i+1:]...)
		if len(args) == 1 && args[0] == "--delete" {
			delete(h.Files, o.Location)
		}
		return vboxmanage.Output{}, true
	}
	return vboxmanage.Output{}, false
}
