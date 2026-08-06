package capability

const (
	NamePOSIXFileSystem Name = "POSIXFileSystemCapable"
	NamePosixAccount    Name = "PosixAccountCapable"
	NameFactGatherer    Name = "FactGathererCapable"
)

// POSIXFileSystemCapable is satisfied by devices exposing a POSIX-style
// filesystem.
type POSIXFileSystemCapable interface {
	// RootPath returns the filesystem's root mount point (e.g. "/").
	RootPath() string
}

// PosixAccountCapable is satisfied by devices with POSIX user/group
// account management.
type PosixAccountCapable interface {
	// PasswdPath returns the path to the device's account database
	// (e.g. "/etc/passwd").
	PasswdPath() string
}

// FactGathererCapable is satisfied by devices that can report structured
// facts back to the engine.
type FactGathererCapable interface {
	// FactSourceName identifies which fact-gathering backend produced
	// facts for this device (e.g. "setup", "gather_facts").
	FactSourceName() string
}

func init() {
	Register(Descriptor{
		Name:   NamePOSIXFileSystem,
		Assert: func(item any) bool { _, ok := item.(POSIXFileSystemCapable); return ok },
	})
	Register(Descriptor{
		Name:   NamePosixAccount,
		Assert: func(item any) bool { _, ok := item.(PosixAccountCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameFactGatherer,
		Assert: func(item any) bool { _, ok := item.(FactGathererCapable); return ok },
	})
}
