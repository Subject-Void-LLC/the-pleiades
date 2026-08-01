// Package sdk provides the public toolset for developers writing Collections.
package sdk

// RunbookContext bridges the stateless Go Collection to the Controller.
type RunbookContext interface {
	// InjectSecrets securely provides the Just-In-Time credentials required
	// for the runbook. The implementer must zero this memory after use.
	InjectSecrets() map[string]string

	// SetStat bubbles ephemeral variables up to the WorkflowContext for CEL logic.
	SetStat(key string, value interface{}) error

	// EmitFact caches long-term historical drift data in the database.
	EmitFact(key string, value interface{}) error
}
