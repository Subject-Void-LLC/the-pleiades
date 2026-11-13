// Package main: whether add-credential's name is a device the inventory has
// (decided 2026-11-11, the 0.3.x half). A name the inventory does not have is
// still stored, with a warning naming it and its near matches, and marked
// unbound; --no-device says that is meant and silences the warning. From
// 0.4.0 the name is refused without the flag.
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// credentialBinding decides whether a credential for name is stored bound
// to a device or marked unbound, writing any warning to warn. It reads the
// inventory through loadInventory, the loader run resolves its targets with,
// so a name run would find is never warned about and one it would not is.
// It runs before any secret is read, so a refusal consumes nothing.
//
//   - name is a device: stored bound; with noDevice, refused, since the flag
//     says there is no device and there is one.
//   - name is not a device: stored unbound; without noDevice, a warning
//     names it, its near matches, the flag, and the 0.4.0 refusal.
//   - there is no inventory file yet: no device has the name.
//   - the inventory cannot be read: a warning says the name was not checked,
//     and the credential is stored as before, unbound only when noDevice
//     says so.
func credentialBinding(dir, name string, noDevice bool, warn io.Writer) (unbound bool, err error) {
	var items []pkginventory.InventoryItem
	// A project with no inventory yet (credentials first, hosts after) has no
	// device of any name, which is not a failure to read one.
	if _, statErr := os.Stat(inventoryFile(dir)); !errors.Is(statErr, fs.ErrNotExist) {
		items, err = loadInventory(dir)
	}
	if err != nil {
		fmt.Fprintf(warn, "warning: could not read the inventory to check that device %q is in it: %v\n", termsafe.EscapeLine(name), err)
		return noDevice, nil
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		if item.Name() == name {
			if noDevice {
				return false, fmt.Errorf("device %q is in the inventory, so --no-device does not apply; drop it to store this device's credential", name)
			}
			return false, nil
		}
		names = append(names, item.Name())
	}
	if noDevice {
		return true, nil
	}
	near := ""
	if matches := engine.NearMatches(name, names, 3); len(matches) > 0 {
		quoted := make([]string, len(matches))
		for i, m := range matches {
			quoted[i] = fmt.Sprintf("%q", termsafe.EscapeLine(m))
		}
		near = " (did you mean " + strings.Join(quoted, ", ") + "?)"
	}
	fmt.Fprintf(warn, "warning: no device named %q in %s%s; the credential is stored and marked unbound, and the first run that uses it will say so. "+
		"Pass --no-device when no device is meant (a sync source's account, a device added later); from 0.4.0 a name the inventory does not have is refused without it\n",
		termsafe.EscapeLine(name), inventory.DefaultInventoryFilename, near)
	return true, nil
}
