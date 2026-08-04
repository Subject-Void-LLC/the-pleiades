package engine

// SecretMaskSpec names an earlier task's registered result and which of its
// top-level fields to retroactively mark secret. It is applied once per
// node, across every device present under Register, not just the marking
// task's own device: a secret discovered under one device's result is
// masked everywhere it appears for the rest of the run, not just for that
// one device. See Task.SecretMask and executor_secrets.go's
// (*run).applySecretMask.
//
// Only top-level keys are supported, not dotted/nested paths: every
// ActionResult.Stats shape this codebase produces today (the builtin
// "noop"/"set_metadata" Params echo, "ssh_exec"'s stdout/stderr/exit_code)
// is already flat. A nested-path syntax is a clean additive follow-up if a
// real need for one appears; building it speculatively now would be scope
// this feature was not asked for.
type SecretMaskSpec struct {
	// Register is the earlier task's Register name whose result this spec
	// reads from. A name that no task in the DAG ever registers is a
	// build-time validate.Finding (internal/validate/secret_mask_rule.go)
	// and, if validation is skipped, a hard runtime error naming the
	// register (never a value) rather than a silent no-op.
	Register string `json:"register" yaml:"register"`

	// Fields lists the top-level Stats keys, under Register, to mark
	// secret. A field absent from a specific device's result is a benign
	// skip (not every device necessarily produced every field); a field
	// present but not a long-enough string is a hard runtime error naming
	// the field (never its value), since a short or non-string value would
	// corrupt unrelated output if blindly substring-masked.
	Fields []string `json:"fields" yaml:"fields"`
}
