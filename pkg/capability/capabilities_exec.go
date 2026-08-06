package capability

const (
	NameCommandExec Name = "CommandExecCapable"
	NameShellExec   Name = "ShellExecCapable"
)

// CommandExecCapable is satisfied by any device that can run an arbitrary
// command outside of a shell (e.g. an exec-style RPC or API call).
type CommandExecCapable interface {
	// WorkingDirectory returns the default directory a command runs in.
	WorkingDirectory() string
}

// ShellExecCapable is satisfied by devices that additionally expose a real
// shell to execute through (e.g. /bin/sh), a narrower capability than
// CommandExecCapable alone.
type ShellExecCapable interface {
	CommandExecCapable

	// ShellPath returns the shell executable commands run through.
	ShellPath() string
}

func init() {
	Register(Descriptor{
		Name:   NameCommandExec,
		Assert: func(item any) bool { _, ok := item.(CommandExecCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameShellExec,
		Parent: NameCommandExec,
		Assert: func(item any) bool { _, ok := item.(ShellExecCapable); return ok },
	})
}
