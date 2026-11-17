// The values a run masks from its start, for a method that writes a device's
// text where no output boundary of the run reaches it.
package sdk

// KnownSecrets is implemented by a RunbookContext that can hand a method the
// values its run masks from its start: the project's named secrets, which the
// command line masks in every run whether or not a task resolves one
// (engine.WithKnownSecrets). The run masks its report, journal and facts by
// them itself; a method that writes a device's text into a file of the
// project, which no output boundary reaches, masks that text by these and by
// its own credential before writing it (net.yang.schema). A Runner's context
// and an external Collection's do not implement it, and a method there masks
// by its credential alone.
type KnownSecrets interface {
	// KnownSecretValues returns the values, a copy.
	KnownSecretValues() []string
}

// KnownSecretValues returns the values rc's run masks from its start, nil
// for a context that does not say.
func KnownSecretValues(rc RunbookContext) []string {
	if k, ok := rc.(KnownSecrets); ok {
		return k.KnownSecretValues()
	}
	return nil
}
