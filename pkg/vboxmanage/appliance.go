// Importing an OVA appliance as a VM, and the host paths every file
// operation here names.
package vboxmanage

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// pathPattern is an absolute drive path on a Windows host with no quote,
// wildcard or control character, and no trailing separator. A path is
// refused rather than quoted, as a name is.
var pathPattern = regexp.MustCompile(`^[A-Za-z]:\\[^'"*?<>|\x00-\x1f\x7f]*[^'"*?<>|\\\x00-\x1f\x7f ]$`)

// CheckPath refuses a host path outside pathPattern, or one that climbs
// out of a folder with "..", or holds what a WS-Man message cannot carry
// (invalid UTF-8, U+FFFE, U+FFFF), naming what the path is for. Windows reads "/"
// as a separator as well as "\", so a segment between either is checked: a
// segment of only dots and spaces (".", "..", "..." and the like) is
// refused, since Windows reads one as "this folder" or "the folder above"
// rather than as a name. Until 2026-11-13 only "\..\" and a final "\.."
// were refused, so G:\Lab/.. passed, and named G:\.
func CheckPath(what, path string) error {
	if len(path) > 240 || !pathPattern.MatchString(path) || hasDotSegment(path[3:]) || winrmexec.CheckText(what, path) != nil {
		return fmt.Errorf("vboxmanage: %s %q is not an absolute path such as G:\\PleiadesLab\\image.ova, under 240 characters, without quotes, wildcards or a \"..\" segment", what, path)
	}
	return nil
}

// hasDotSegment reports whether a path, after its drive, holds a segment of
// only dots and spaces, or an empty one, between either separator.
func hasDotSegment(rest string) bool {
	for _, s := range strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' || r == '/' }) {
		if strings.Trim(s, ". ") == "" {
			return true
		}
	}
	return strings.Contains(rest, `\\`) || strings.Contains(rest, `//`) || strings.Contains(rest, `\/`) || strings.Contains(rest, `/\`)
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
