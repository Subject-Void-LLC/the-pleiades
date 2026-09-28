// The "virt.vbox.vm.screenshot" method: a picture of a running VM's screen, saved where Pleiades runs.
package vm

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.vbox.vm.screenshot",
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
			Reversibility:   collection.Reversibility{Reversible: false, Notes: "The VM is not changed; the file at dest is replaced by the picture, and what it held before is not kept."},
			SupportsCheck:   true,
			Doc: collection.Doc{
				Summary:     "Saves a picture of a running VM's screen as a PNG file on the machine running Pleiades.",
				Description: "Takes a picture of a running VM's screen on the host, brings it back over the host's own connection, and writes it to dest on the machine running this task, as Ansible's fetch brings a file back; the host keeps no copy. It is how to see a VM with no window, such as one stopped at a first-boot screen or an installer's error. A VM that has not set a display mode yet (still in its firmware, or stopped before it drew anything) has no picture to take, and says so. The VM is not changed; dest is replaced when it exists. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM, takes no picture and writes nothing.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."},
					{Name: "dest", Type: "string", Required: true, Description: "Where to write the PNG on the machine running this task. Its folder must exist."},
				},
				Returns: []collection.ReturnField{
					{Name: "dest", Type: "string", Returned: "always", Description: "Where the picture was written, or would be, in a check."},
					{Name: "width", Type: "int", Returned: "when not a check", Description: "The picture's width, in pixels."},
					{Name: "height", Type: "int", Returned: "when not a check", Description: "The picture's height, in pixels."},
					{Name: "bytes", Type: "int", Returned: "when not a check", Description: "The PNG file's size."},
				},
				Examples: []collection.Example{
					{Name: "See where a Windows VM's first boot stopped", RunbookYAML: "- name: Save a picture of the lab VM's screen\n  virt.vbox.vm.screenshot:\n    name: win-lab\n    dest: ./win-lab.png\n"},
				},
				SeeAlso: []string{"virt.vbox.vm.log", "virt.vbox.vm.send_keys"},
			},
		},
		Invoke: Screenshot,
		Check:  CheckScreenshot,
	})
}

// The screenshot's parameter and stats, and the file it passes through
// in the VM's folder on the host.
const (
	paramDest  = "dest"
	statDest   = "dest"
	statWidth  = "width"
	statHeight = "height"
	statBytes  = "bytes"

	screenFile    = "screenshot.png"
	maxScreenshot = 16 << 20
)

// pngSignature is the eight bytes every PNG file starts with.
const pngSignature = "\x89PNG\r\n\x1a\n"

// Screenshot implements "virt.vbox.vm.screenshot".
func Screenshot(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runScreenshot(ctx, rc, device, params, collection.ModeExecute)
}

// CheckScreenshot is "virt.vbox.vm.screenshot"'s check: it reads the VM,
// takes no picture and writes nothing.
func CheckScreenshot(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runScreenshot(ctx, rc, device, params, collection.ModeCheck)
}

// runScreenshot takes the picture on the host, brings it back and writes
// it to dest.
func runScreenshot(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.vbox.vm.screenshot"
	dest, err := sdk.RequiredStringParam(params, paramDest)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if info, err := os.Stat(filepath.Dir(dest)); err != nil || !info.IsDir() {
		return collection.Result{}, fmt.Errorf("%s: dest %q is not in a folder that exists here", fqcn, dest)
	}
	h, name, err := target(rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	m, err := mustReadFor(ctx, h, name, device, fqcn, mode)
	if err != nil {
		return collection.Result{}, err
	}
	if m.State != vboxmanage.StateRunning {
		return collection.Result{}, fmt.Errorf("%s: %q is %s; only a running VM has a screen to take", fqcn, name, m.State)
	}
	if err := rc.SetStat(statDest, dest); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	picture, err := takePicture(ctx, h, m)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	width, height, err := pngSize(picture)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := writeLocal(dest, picture); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	for key, value := range map[string]int{statWidth: width, statHeight: height, statBytes: len(picture)} {
		if err := rc.SetStat(key, value); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	return collection.Result{Changed: true}, nil
}

// takePicture has the host write m's screen to a file in its folder,
// reads the file back and removes it.
func takePicture(ctx context.Context, h vboxmanage.Host, m vboxmanage.Machine) ([]byte, error) {
	dir, err := vmDir(m)
	if err != nil {
		return nil, err
	}
	path := dir + `\` + screenFile
	if err := h.Screenshot(ctx, m.Name, path); err != nil {
		if strings.Contains(err.Error(), "Unsupported resolution") {
			return nil, fmt.Errorf("%q has no picture on its screen yet: its guest has set no display mode, as one still in its firmware has not (virt.vbox.vm.log shows where it is)", m.Name)
		}
		return nil, err
	}
	picture, err := h.ReadTail(ctx, path, maxScreenshot)
	if err != nil {
		return nil, err
	}
	return picture, h.Remove(ctx, path, false)
}

// pngSize reads a PNG's width and height from its header, refusing bytes
// that are not a PNG.
func pngSize(data []byte) (int, int, error) {
	if len(data) < 24 || string(data[:8]) != pngSignature || string(data[12:16]) != "IHDR" {
		return 0, 0, fmt.Errorf("the host's picture is not a PNG")
	}
	return int(binary.BigEndian.Uint32(data[16:20])), int(binary.BigEndian.Uint32(data[20:24])), nil
}

// writeLocal writes data to path on this machine, readable by its owner
// only: beside path first and then moved into place, so path never holds
// part of a picture.
func writeLocal(path string, data []byte) error {
	part := path + ".pleiades-part"
	if err := os.WriteFile(part, data, 0o600); err != nil {
		return err
	}
	return os.Rename(part, path)
}
