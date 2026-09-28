// The properties that make a Server capability.VirtualBoxCapable: whether
// VirtualBox is there, where VBoxManage is, and where new VMs go.
package windows

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The record properties VirtualBox support reads.
const (
	// propVirtualBox, true, declares capability.VirtualBoxCapable.
	propVirtualBox = "virtualbox"
	// propVBoxManagePath overrides where VBoxManage is.
	propVBoxManagePath = "vboxmanage_path"
	// propVMFolder names the folder new VMs are created in.
	propVMFolder = "vm_folder"
)

// defaultVBoxManagePath is where the VirtualBox installer puts VBoxManage.
const defaultVBoxManagePath = `C:\Program Files\Oracle\VirtualBox\VBoxManage.exe`

// absoluteWindowsPath matches a path from a drive root. A relative program
// path would be resolved against the working directory first, which is
// how a planted file of the same name runs instead.
var absoluteWindowsPath = regexp.MustCompile(`^[A-Za-z]:\\`)

// virtualBoxSettings is what a record says about VirtualBox on the host.
type virtualBoxSettings struct {
	declared bool
	path     string
	folder   string
}

// parseVirtualBox reads and checks the VirtualBox properties. A path or a
// folder without virtualbox: true is refused, since it says the record's
// author believes VirtualBox is in use when nothing will use it. A path
// that is not absolute, or holds a quote, '%', '!' or a control character,
// is refused because VBoxManage's path is a command line's program, where
// none of those can be carried safely.
func parseVirtualBox(props inventory.Properties) (virtualBoxSettings, error) {
	raw := props.Raw()
	enabled := false
	if value, set := raw[propVirtualBox]; set {
		b, ok := value.(bool)
		if !ok {
			return virtualBoxSettings{}, fmt.Errorf("property %s must be true or false, got %v", propVirtualBox, value)
		}
		enabled = b
	}
	path, pathSet := props.String(propVBoxManagePath)
	folder, folderSet := props.String(propVMFolder)
	if !enabled {
		for key, set := range map[string]bool{propVBoxManagePath: pathSet, propVMFolder: folderSet} {
			if set {
				return virtualBoxSettings{}, fmt.Errorf("property %s applies only with %s: true", key, propVirtualBox)
			}
		}
		return virtualBoxSettings{}, nil
	}
	if !pathSet || path == "" {
		path = defaultVBoxManagePath
	}
	for key, value := range map[string]string{propVBoxManagePath: path, propVMFolder: folder} {
		if value == "" {
			continue
		}
		if !absoluteWindowsPath.MatchString(value) {
			return virtualBoxSettings{}, fmt.Errorf("property %s must be an absolute path from a drive root, got %q", key, value)
		}
		if strings.ContainsAny(value, "\"%!") || strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return virtualBoxSettings{}, fmt.Errorf("property %s holds a quote, '%%', '!' or a control character, which a Windows command line cannot carry in a path", key)
		}
	}
	return virtualBoxSettings{declared: true, path: path, folder: folder}, nil
}

// capabilities returns the capability these settings declare, if any.
func (s virtualBoxSettings) capabilities() []capability.Name {
	if !s.declared {
		return nil
	}
	return []capability.Name{capability.NameVirtualBox}
}

// VBoxManagePath implements capability.VirtualBoxCapable: the
// vboxmanage_path property, or where the VirtualBox installer puts it.
func (w *Server) VBoxManagePath() string {
	if w.vbox.path != "" {
		return w.vbox.path
	}
	return defaultVBoxManagePath
}

// VMFolder implements capability.VirtualBoxCapable: the vm_folder
// property, or "" for VirtualBox's own default machine folder.
func (w *Server) VMFolder() string { return w.vbox.folder }
