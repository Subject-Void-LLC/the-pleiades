// Importing an OVA appliance as a VM, and the host paths every file
// operation here names.
package vboxmanage

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// pathPattern is an absolute drive path on a Windows host with no quote,
// wildcard or control character, and no trailing separator. A path is
// refused rather than quoted, as a name is.
var pathPattern = regexp.MustCompile(`^[A-Za-z]:\\[^'"*?<>|\x00-\x1f\x7f]*[^'"*?<>|\\\x00-\x1f\x7f ]$`)

// CheckPath refuses a host path outside pathPattern, or one that climbs
// out of a folder with "..", naming what the path is for.
func CheckPath(what, path string) error {
	if len(path) > 240 || !pathPattern.MatchString(path) || strings.Contains(path, `\..\`) || strings.HasSuffix(path, `\..`) {
		return fmt.Errorf("vboxmanage: %s %q is not an absolute path such as G:\\PleiadesLab\\image.ova, under 240 characters, without quotes, wildcards or a \"..\" segment", what, path)
	}
	return nil
}

// Import imports the OVA at ova as the VM name, into folder when it is
// not empty (VirtualBox's default machine folder otherwise), and removes
// the network adapter the appliance asked for, so the VM is on no
// network until something puts it on one. Ubuntu's cloud image asks for
// a bridged adapter, which would put it on the host's own network.
func (h Host) Import(ctx context.Context, ova, name, folder string) error {
	if err := CheckPath("appliance", ova); err != nil {
		return err
	}
	if err := CheckName("VM", name); err != nil {
		return err
	}
	args := []string{"import", ova, "--vsys", "0", "--vmname", name}
	if folder != "" {
		if err := CheckPath("VM folder", folder); err != nil {
			return err
		}
		args = append(args, "--vsys", "0", "--basefolder", folder)
	}
	if _, err := h.run(ctx, args...); err != nil {
		return err
	}
	_, err := h.run(ctx, "modifyvm", name, "--nic1", "none")
	return err
}
