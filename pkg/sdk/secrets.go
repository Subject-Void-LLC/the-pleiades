// A value a method obtained while it ran, added to the run's masking set.
package sdk

// SecretRegistrar is satisfied by a RunbookContext that can add a value to
// its run's masking set after the method has started.
//
// A run masks the credential it handed a method (InjectSecrets) from the
// method's output, errors and stats. A secret the method obtains later, such
// as a registry account it asks for (RegistryCredentials) or a header value
// built from one, is not in that set unless the method says so, and the
// first place it would then appear unmasked is an error echoing it back.
type SecretRegistrar interface {
	// RegisterSecret adds each non-empty value to the run's masking set.
	RegisterSecret(values ...string)
}

// RegisterSecret adds values to rc's masking set when rc can hold them, and
// does nothing for a context that cannot, which masks only what it handed
// out.
func RegisterSecret(rc RunbookContext, values ...string) {
	if r, ok := rc.(SecretRegistrar); ok {
		r.RegisterSecret(values...)
	}
}
