// Package inventory: removing a host from the static inventory, its row in
// hosts.yaml and its entry in the state sidecar together.
package inventory

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// RemoveHost removes the host named name from the static inventory at
// hostsPath: its row in hosts.yaml, then its entry (version, lifecycle state,
// history, discovery) in the sidecar beside it, matched by ID as every other
// sidecar access is. It returns the row removed, or ErrItemNotFound.
//
// A host another host names in a property (a console node's console_server,
// a route) is refused, naming each, since removing it would leave them
// pointing at nothing; leaveReferences removes it anyway. The journal is not
// touched: a rollback of a run that changed the host then refuses, naming a
// device that has left the inventory, which is what it is for.
func RemoveHost(hostsPath, name string, leaveReferences bool) (HostSpec, error) {
	hosts, err := ReadHosts(hostsPath)
	if err != nil {
		return HostSpec{}, err
	}
	at := slices.IndexFunc(hosts, func(h HostSpec) bool { return h.Name == name })
	if at < 0 {
		return HostSpec{}, fmt.Errorf("host %s: %w", name, ErrItemNotFound)
	}
	removed := hosts[at]
	if !leaveReferences {
		if names := namedBy(hosts, name); len(names) > 0 {
			return HostSpec{}, fmt.Errorf("host %s is named by %s; remove or change those first, or remove it anyway with --leave-references", name, strings.Join(names, ", "))
		}
	}
	// The sidecar is read before anything is removed: a state file that cannot
	// be read leaves the host in place and says so, rather than removing the
	// row first and returning a bare error to a caller that then skips the
	// credential and host-key cleanup for a host that is already gone
	// (FAILURE_PATTERNS 105, LESSONS 294).
	sidecarPath := filepath.Join(filepath.Dir(hostsPath), sidecarFileName)
	doc, err := readSidecar(sidecarPath)
	if err != nil {
		return HostSpec{}, fmt.Errorf("host %s was not removed, because its state in %s could not be read: %w", name, filepath.Base(sidecarPath), err)
	}
	if err := WriteHosts(hostsPath, slices.Delete(slices.Clone(hosts), at, at+1)); err != nil {
		return HostSpec{}, err
	}
	id := removed.ID
	if id == "" {
		id = removed.Name
	}
	kept := slices.DeleteFunc(slices.Clone(doc.Hosts), func(e sidecarEntry) bool { return e.ID == id })
	if len(kept) == len(doc.Hosts) {
		return removed, nil
	}
	doc.Hosts = kept
	if err := writeSidecar(sidecarPath, doc); err != nil {
		return removed, fmt.Errorf("host %s was removed from %s, but not its state: %w", name, filepath.Base(hostsPath), err)
	}
	return removed, nil
}

// namedBy returns, as "host (property)", every other host that names name
// as a property's whole value.
func namedBy(hosts []HostSpec, name string) []string {
	var out []string
	for _, h := range hosts {
		if h.Name == name {
			continue
		}
		for key, v := range h.Properties {
			if s, ok := v.(string); ok && s == name {
				out = append(out, h.Name+" ("+key+")")
			}
		}
	}
	sort.Strings(out)
	return out
}
