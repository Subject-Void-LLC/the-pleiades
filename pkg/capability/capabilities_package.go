package capability

const (
	NamePackageManager Name = "PackageManagerCapable"
	NameApt            Name = "AptCapable"
	NameDnf            Name = "DnfCapable"
)

// PackageManagerCapable is satisfied by any device with some package
// manager, without committing to which one. Collection authors targeting
// this broad level get downward resolution to whichever specific manager
// (apt, dnf, ...) the device actually declares (PLAN.md Section 8).
type PackageManagerCapable interface {
	// PackageManagerName returns the package manager's identifying name
	// (e.g. "apt", "dnf").
	PackageManagerName() string
}

// AptCapable is satisfied by devices managed through APT. This is a floor
// capability (PLAN.md Section 8): no narrower per-distro or per-version
// interface exists below it.
type AptCapable interface {
	PackageManagerCapable

	// AptSourcesList returns the path to the device's sources.list.
	AptSourcesList() string
}

// DnfCapable is satisfied by devices managed through DNF. Also a floor
// capability, sibling to AptCapable under PackageManagerCapable.
type DnfCapable interface {
	PackageManagerCapable

	// DnfRepoDir returns the path to the device's repo definition directory.
	DnfRepoDir() string
}

func init() {
	Register(Descriptor{
		Name:   NamePackageManager,
		Assert: func(item any) bool { _, ok := item.(PackageManagerCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameApt,
		Parent: NamePackageManager,
		Assert: func(item any) bool { _, ok := item.(AptCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameDnf,
		Parent: NamePackageManager,
		Assert: func(item any) bool { _, ok := item.(DnfCapable); return ok },
	})
}
