// Package linux: the accessors behind a Linux server's package manager,
// firewall and account capabilities.
//
// Until these existed no device type implemented them, so AptCapable,
// DnfCapable, PackageManagerCapable, FirewalldCapable and
// PosixAccountCapable could be declared but never satisfied: HasCapability
// ANDs the declaration with the structural check, and the eighteen methods
// requiring them were refused on every real device. That was a disclosed
// gap, held open on purpose because these are per-distribution facts; the
// accessors close it without claiming them for every server, since the
// declaration still comes only from classification or a property.
package linux

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// FirewalldProperty is the property that says a server runs firewalld.
// Nothing about a distribution family guarantees it (minimal and cloud
// images often ship without it), so it is declared per server, the way
// file_transfer_root declares file transfer.
const FirewalldProperty = "firewalld"

// PackageManagerName names the package manager pkg.install, pkg.remove and
// pkg.upgrade route to: the one the server's classification declares, apt
// or dnf. It is empty when neither is declared, and then the server does
// not claim PackageManagerCapable either.
func (l *Server) PackageManagerName() string {
	switch {
	case l.Declares(capability.NameApt):
		return "apt"
	case l.Declares(capability.NameDnf):
		return "dnf"
	}
	return ""
}

// AptSourcesList returns apt's main sources file, satisfying
// capability.AptCapable.
func (l *Server) AptSourcesList() string {
	return l.pathProperty("apt_sources_list", "/etc/apt/sources.list")
}

// DnfRepoDir returns the directory dnf reads repository definitions from,
// satisfying capability.DnfCapable.
func (l *Server) DnfRepoDir() string {
	return l.pathProperty("dnf_repo_dir", "/etc/yum.repos.d")
}

// FirewalldZone returns the zone a firewalld method acts on when its task
// names none, satisfying capability.FirewalldCapable.
func (l *Server) FirewalldZone() string {
	return l.pathProperty("firewalld_zone", "public")
}

// PasswdPath returns the account database, satisfying
// capability.PosixAccountCapable.
func (l *Server) PasswdPath() string {
	return l.pathProperty("passwd_path", "/etc/passwd")
}

// pathProperty returns the named string property, or def when it is unset
// or empty.
func (l *Server) pathProperty(name, def string) string {
	if v, ok := l.Properties().String(name); ok && v != "" {
		return v
	}
	return def
}

// firewalldBaseline adds FirewalldCapable to baseline when rec sets the
// firewalld property to true. A value that is not a boolean is refused,
// so a mistyped "yes" cannot pass for either answer.
func firewalldBaseline(rec record.Record, baseline []capability.Name) ([]capability.Name, error) {
	raw, present := rec.Properties[FirewalldProperty]
	if !present {
		return baseline, nil
	}
	on, ok := raw.(bool)
	if !ok {
		return nil, fmt.Errorf("linux_server %q: property %s must be true or false", rec.Name, FirewalldProperty)
	}
	if !on {
		return baseline, nil
	}
	return append(baseline, capability.NameFirewalld), nil
}
