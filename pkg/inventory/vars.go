// The one property that holds the variables a device brings to a run.
package inventory

// VarsProperty is the device property holding the variables a device
// brings (Phase 87): a map from a variable name to its value, which a
// runbook reads as vars.<name>. It is authored in a static inventory's
// group_vars and host_vars files or under a host's properties in
// hosts.yaml, never through a single-value flag, and it is the same string
// as the engine's DeviceVarsProperty (a test holds the two equal).
const VarsProperty = "vars"
