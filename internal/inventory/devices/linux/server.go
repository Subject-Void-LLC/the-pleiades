// Package linux holds the concrete device type implementation for
// standard Linux servers. This package's init registers NewServer into the
// shared record.Types registry under "linux_server";
// internal/inventory/factory.go's NewItemFactory draws its
// batteries-included set from that registry rather than importing this
// package by name (internal/inventory/builtins.go blank-imports it purely
// to trigger this init). This package itself never imports
// internal/inventory, only the leaf internal/inventory/record package plus
// pkg/inventory and pkg/capability, so internal/inventory can depend on
// this package (via the blank import) without a cycle back.
package linux

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/policy"
)

func init() {
	record.RegisterType("linux_server", NewServer)
}

// Server implements InventoryItem plus, structurally,
// capability.SSHTransportCapable, capability.LinuxCapable,
// capability.ShellExecCapable (which embeds
// capability.CommandExecCapable) and capability.SystemdCapable (which
// embeds capability.ServiceManagerCapable).
type Server struct {
	*record.Base
}

// NewServer builds a Server from rec. It matches the
// func(record.Record) (inventory.InventoryItem, error) shape ItemFactory's
// registry expects.
//
// The capability set is the vendor baseline (SSHTransportCapable,
// LinuxCapable, ShellExecCapable) unioned with rec.Capabilities, per
// Phase 32's capability granularity decision: classification-derived data
// can only add to what this type already asserts about itself, never
// replace it -- a Record hydrated with no Classify path (an explicit
// Type) still gets the same baseline this constructor always granted,
// while a classified one can gain more (policy.UnionSlices, Section 25's
// shared primitive, rather than a bespoke dedup loop here).
//
// ShellExecCapable is declared rather than CommandExecCapable, and the
// difference is not cosmetic. It is the narrower of the two, and
// capability.Resolves walks upward, so declaring it satisfies a method
// requiring either one; declaring only the parent would satisfy
// exec.command and refuse exec.shell on a device that plainly has a
// shell. It is also the honest claim: a Linux server does have /bin/sh,
// which is exactly what ShellPath reports.
func NewServer(rec record.Record) (inventory.InventoryItem, error) {
	caps := policy.UnionSlices(
		[]capability.Name{
			capability.NameSSHTransport,
			capability.NameLinux,
			capability.NameShellExec,
			capability.NameSystemd,
			capability.NamePOSIXFileSystem,
			capability.NameFactGatherer,
			capability.NameNetworkAddressable,
		},
		rec.Capabilities,
	)
	base := record.NewBase(rec, caps)
	return &Server{Base: base}, nil
}

// HasCapability checks the declared classification AND the structural
// registry assertion, so a true result is a guarantee, not a hope.
func (l *Server) HasCapability(name capability.Name) bool {
	return l.Declares(name) && capability.Implements(l, name)
}

// SSHHost returns the configured management host for this server.
func (l *Server) SSHHost() string {
	host, _ := l.Properties().String("host")
	return host
}

// SSHPort returns the configured SSH port, defaulting to 22.
func (l *Server) SSHPort() int {
	if port, ok := l.Properties().Int("port"); ok && port != 0 {
		return port
	}
	return 22
}

// IPAddress returns this server's reachable network address, the same
// value SSHHost reports: a Linux server managed over SSH has exactly one
// address this platform knows about today. Phase 73 added this accessor
// (capability.NetworkAddressableCapable) so pleiades.builtin.wait.port,
// StatusImplemented since before this Part, could dispatch against a
// real device at all -- see FAILURE_PATTERNS.md.
func (l *Server) IPAddress() string {
	return l.SSHHost()
}

// RootPath returns the filesystem root.
//
// This and FactSourceName below are not new claims about what a Linux
// server can do. Every implemented file.* method, plus wait.path and
// wait.search, already required POSIXFileSystemCapable, and facts.gather
// already required FactGathererCapable; they ran anyway because nothing
// compared a manifest's RequiredCapabilities against the target device.
// Once engine.checkMethodCapabilities started making that comparison,
// this type's silence became a refusal of fifteen methods that have been
// working against real Linux hosts all along. Declaring what was already
// true is the fix; loosening the methods would have been the wrong one.
func (l *Server) RootPath() string { return "/" }

// FactSourceName identifies which backend gathered this device's facts.
//
// "setup" is Ansible's own name for the module that does this, kept
// deliberately rather than invented fresh, for the same reason the
// runbook schema reuses Ansible's parameter vocabulary: an operator
// reading a fact source should recognize the word.
func (l *Server) FactSourceName() string { return "setup" }

// ServiceManagerName returns which service manager this device runs,
// defaulting to systemd.
//
// The default is a claim about the mainstream case rather than about
// every Linux system, and the property is how the exceptions say so: an
// Alpine or OpenRC host sets service_manager to its own manager, and the
// svc methods then refuse it by name instead of running systemctl and
// failing with "command not found".
//
// SystemdCapable is declared in the baseline rather than gated on this
// property because a capability is what a device CAN do and the union in
// NewServer can only add. Declaring the parent alone would leave the
// concrete systemd methods unreachable on every stock linux_server,
// which is the case they exist for. The property is what keeps the
// declaration from overreaching: it names the manager, so a method can
// check it and stop.
func (l *Server) ServiceManagerName() string {
	if name, ok := l.Properties().String("service_manager"); ok && name != "" {
		return name
	}
	return "systemd"
}

// SystemdUnitPath returns the directory systemd unit files live in,
// defaulting to the standard location for administrator-provided units.
//
// /etc/systemd/system is deliberately the default rather than
// /lib/systemd/system or /usr/lib/systemd/system: those hold units the
// distribution package manager owns, and anything this platform writes
// belongs in the administrator's directory, which also takes precedence.
func (l *Server) SystemdUnitPath() string {
	if path, ok := l.Properties().String("systemd_unit_path"); ok && path != "" {
		return path
	}
	return "/etc/systemd/system"
}

// WorkingDirectory returns the directory a command runs in when the task
// names none of its own, satisfying capability.CommandExecCapable.
//
// It reads the free-form "working_directory" property and returns an
// empty string when that is unset, which is deliberate rather than a
// missing default. An empty answer means "wherever this account lands on
// login," which is what a person running the same command by hand would
// get, and it is the only answer that is correct for every account: a
// hardcoded /root or /home/<user> would be wrong for most of them and
// would silently move where a relative path resolves.
func (l *Server) WorkingDirectory() string {
	dir, _ := l.Properties().String("working_directory")
	return dir
}

// ShellPath returns the shell a command runs through, satisfying
// capability.ShellExecCapable.
//
// It reads the free-form "shell" property and falls back to /bin/sh.
// /bin/sh is the POSIX-guaranteed path and the one every Linux
// distribution ships, so it is a real default rather than a guess; a
// device that wants bash-specific behavior sets the property.
func (l *Server) ShellPath() string {
	if shell, ok := l.Properties().String("shell"); ok && shell != "" {
		return shell
	}
	return "/bin/sh"
}

// KernelVersion returns the detected Linux kernel version.
func (l *Server) KernelVersion() string {
	v, _ := l.Properties().String("kernel_version")
	return v
}

// Distribution returns the detected Linux distribution name.
func (l *Server) Distribution() string {
	v, _ := l.Properties().String("distribution")
	return v
}
