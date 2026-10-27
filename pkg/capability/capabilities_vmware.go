// Package capability: VMware's desktop hypervisors, which a device runs and
// drives through vmrun and vmcli.
package capability

// NameVMwareFusion is VMware Fusion on a Mac. Workstation, the Windows and
// Linux product, is a different capability when it is built: the two share
// a command-line tool but not a host, and a method for one never runs on the
// other.
const NameVMwareFusion Name = "VMwareFusionCapable"

// VMwareFusionCapable is satisfied by a Mac running VMware Fusion whose vmrun
// the connecting account may use. Onboarding grants it only when vmrun
// answers "list" as that account on a Darwin kernel, so a grant proves
// permission as well as presence.
type VMwareFusionCapable interface {
	// FusionVersion returns the Fusion version onboarding read from the
	// application, e.g. "26.0.1".
	FusionVersion() string
}

func init() {
	Register(Descriptor{
		Name:   NameVMwareFusion,
		Assert: func(item any) bool { _, ok := item.(VMwareFusionCapable); return ok },
	})
}
