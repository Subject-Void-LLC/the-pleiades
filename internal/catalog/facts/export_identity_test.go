// Package facts: the identity reads' exact commands, exported to the external
// check test only, which holds a check's commands to the fixed reads.
package facts

// IdentityReadCommands are the identity facts' read commands, each guarded so it
// skips a container (gather_identity.go).
var IdentityReadCommands = []string{gatherMachineIDCommand, gatherSystemUUIDCommand, gatherSerialCommand, gatherVirtualizationCommand}
