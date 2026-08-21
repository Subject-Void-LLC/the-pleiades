package engine

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TransportBinding is one fqcn's binding to a real transport.Transport:
// which capability a target device must declare, which Transport actually
// runs the command, and how to turn a resolved device into the
// transport.Target that Transport.Exec needs. This is the literal
// "Strategy keyed by capability" Phase W6's own Pattern Entry Gate names,
// and it mirrors validate.actionCapability's own established shape
// (internal/validate/capability_rule.go): "data, not a type switch, so a
// new action is a new map entry, never a change to [the executor]
// itself." A second protocol (NETCONF, WinRM, ...) is a new
// TransportBinding entry plus a new transport.Transport implementation,
// never a change to TransportActionExecutor.
type TransportBinding struct {
	// Capability is the capability.Name a resolved device must declare
	// (via InventoryItem.HasCapability) before TransportActionExecutor
	// will dispatch to Transport at all.
	Capability capability.Name

	// Transport actually runs the command once Target and a credential
	// are resolved.
	Transport transport.Transport

	// Target turns a resolved device into the transport.Target Transport
	// needs to connect. It reports false if device does not carry
	// whatever concrete accessor this binding's protocol requires (SSH's
	// own Target function, SSHTarget below, type-asserts against
	// capability.SSHTransportCapable); TransportActionExecutor treats a
	// false result as a defense-in-depth failure, since the capability
	// check above should already have ruled this out for any device that
	// reached this point honestly.
	Target func(inventory.InventoryItem) (transport.Target, bool)
}

// SSHTarget is the TransportBinding.Target function for any fqcn bound to
// capability.NameSSHTransport: it extracts the host and port a
// capability.SSHTransportCapable device advertises. Exported so the
// composition root (cmd/pleiades/run.go) can wire it into a
// TransportBinding without redefining the same type assertion itself.
func SSHTarget(item inventory.InventoryItem) (transport.Target, bool) {
	sshDev, ok := item.(capability.SSHTransportCapable)
	if !ok {
		return transport.Target{}, false
	}
	return transport.Target{Host: sshDev.SSHHost(), Port: sshDev.SSHPort()}, true
}

// transportActionExecutor is the ActionExecutor that dispatches a task to
// a real transport.Transport, keyed by fqcn via bindings, falling back to
// fallback for any fqcn bindings does not cover. This is how "noop" keeps
// working unchanged (NewBuiltinActionExecutor is passed as fallback) even
// though this executor is what the composition root actually hands to
// engine.Executor from Phase W6 onward.
type transportActionExecutor struct {
	bindings    map[string]TransportBinding
	credentials credential.Store
	inventory   hopChainInventory
	fallback    ActionExecutor
}

// NewTransportActionExecutor returns an ActionExecutor that dispatches
// each task's fqcn through bindings to a real transport.Transport,
// looking up credentials by device name via credentials, resolving each
// dispatched device's own configured hop chain (if any) via inventoryRepo
// (Phase 72: ResolveRoute, hopChainInventory), and delegating any fqcn
// not present in bindings to fallback unchanged. This is the seam
// action.go's own doc comment names Phase W6 as replacing: dispatch over
// a device's transport capability, once the device is resolved and its
// capability checked.
//
// inventoryRepo may be nil, in which case hop-chain resolution is
// skipped entirely and every dispatch behaves exactly as it did before
// Phase 72: a nil route is exactly a direct connection.
// internal/adapters/native's per-task subprocess composition root passes
// nil today, stated once here rather than at that call site, for two
// compounding reasons rather than one. First, structurally: that
// Adapter has no inventory.Repository at all (internal/adapters/native/
// adapter.go's own Adapter struct holds bus, runbooks, bindings, ipc and
// logger, nothing that reaches a real database), because a Collection
// method there runs inside a per-task subprocess reached only by the
// wire.DispatchPayload the Controller sent over NATS, with no live
// connection of its own to resolve against. Second, even if it had one,
// its credential.Store is a StaticStore scoped to the one target
// device's own secret from that same payload (payload.Secrets), so a
// configured hop's own credential.Store.Lookup would fail regardless of
// whether the route resolved. Wiring the Controller to also resolve and
// attach each hop's credential to the dispatch payload (widening the
// JetStream exposure this module's own docs already flag for the single
// target credential) is real, undone work, not a defect in this
// constructor.
func NewTransportActionExecutor(bindings map[string]TransportBinding, credentials credential.Store, inventoryRepo hopChainInventory, fallback ActionExecutor) ActionExecutor {
	return &transportActionExecutor{bindings: bindings, credentials: credentials, inventory: inventoryRepo, fallback: fallback}
}

// Execute implements ActionExecutor.
func (e *transportActionExecutor) Execute(ctx context.Context, task *Task, device inventory.InventoryItem) (ActionResult, error) {
	binding, ok := e.bindings[task.FQCN]
	if !ok {
		return e.fallback.Execute(ctx, task, device)
	}

	// Defense in depth: validate.CapabilityRule already rejects, before
	// execution ever starts, any task whose target does not declare the
	// required capability (internal/validate/capability_rule.go). This
	// check exists so a bug or a future caller that skips validation
	// fails loudly here instead of dialing a device with an unproven
	// capability.
	if device == nil || !device.HasCapability(binding.Capability) {
		return ActionResult{}, fmt.Errorf("fqcn %q requires a target device declaring capability %s", task.FQCN, binding.Capability)
	}

	target, ok := binding.Target(device)
	if !ok {
		return ActionResult{}, fmt.Errorf("fqcn %q: device %q declares capability %s but does not implement its transport target accessor", task.FQCN, device.Name(), binding.Capability)
	}

	command, ok := task.Params["command"].(string)
	if !ok || command == "" {
		return ActionResult{}, fmt.Errorf("fqcn %q requires a non-empty string params.command", task.FQCN)
	}

	cred, err := e.credentials.Lookup(ctx, device.Name())
	if err != nil {
		return ActionResult{}, fmt.Errorf("fqcn %q: %w", task.FQCN, err)
	}

	// Every secret this credential actually carries, computed once, up
	// front, so it can mask BOTH ways Exec can fail: a transport-level
	// error below (dial/auth rejected/connection lost) and, further down,
	// a non-zero exit code's captured stdout/stderr. This phase's own
	// Schema/Injection Hardening audit found masking applied only to the
	// success path in an earlier draft: a golang.org/x/crypto/ssh error
	// does not embed credential material today, but the error text this
	// method returns is not otherwise guaranteed to stay that way forever
	// (a future transport.Transport implementation, or a future SSH
	// library version, could start including connection details in an
	// error), and this executor is the one place that can guarantee no
	// secret from THIS credential ever rides through in the error path
	// this codebase actually surfaces (event.Bus, cmd/pleiades/run.go's
	// printed output). FAILURE_PATTERNS.md #22.
	secrets := []string{cred.Password, string(cred.PrivateKeyPEM), cred.Passphrase}

	// A device's configured hop chain, if any (Phase 72: nil e.inventory
	// skips this entirely, exactly a direct connection, per
	// NewTransportActionExecutor's own doc comment). Every hop's own
	// secrets join the masking set below alongside the target's: a route
	// is new attacker-influenceable structure, but the credential
	// material flowing through it is exactly as sensitive as the
	// target's own, and an error path that masked only the target's
	// would leak a bastion's password the moment that hop's own dial or
	// auth failed.
	if e.inventory != nil {
		hops, err := ResolveRoute(ctx, e.inventory, e.credentials, device)
		if err != nil {
			return ActionResult{}, fmt.Errorf("fqcn %q: %w", task.FQCN, err)
		}
		target.Route = hops
		for _, hop := range hops {
			secrets = append(secrets, hop.Credential.Password, string(hop.Credential.PrivateKeyPEM), hop.Credential.Passphrase)
		}
	}

	result, err := binding.Transport.Exec(ctx, target, cred, command)
	if err != nil {
		return ActionResult{}, fmt.Errorf("fqcn %q on device %q: %s", task.FQCN, device.Name(), redact.Text(secrets, err.Error()))
	}

	// Mask every secret value this credential actually carries out of the
	// command's captured output before it goes anywhere near Stats, an
	// error message, a printed plan, or an event on the bus. This is this
	// phase's masking-ruleset requirement: a command that happens to echo
	// its own password or key material back must never leak it past this
	// point.
	stdout := redact.Text(secrets, result.Stdout)
	stderr := redact.Text(secrets, result.Stderr)

	if result.ExitCode != 0 {
		return ActionResult{}, fmt.Errorf(
			"fqcn %q on device %q: command exited %d\nstdout: %s\nstderr: %s",
			task.FQCN, device.Name(), result.ExitCode, stdout, stderr,
		)
	}

	// Changed defaults to true, the opposite of noop's default: a raw
	// remote command is not provably idempotent (this codebase cannot
	// know whether it altered device state), mirroring Ansible's own
	// command/shell module default rather than ok/unchanged. An author
	// who knows a specific command is read-only overrides it explicitly
	// with the same params.changed convention noop already established
	// (action.go's builtinActionExecutor.Execute).
	changed := true
	if explicit, ok := task.Params["changed"].(bool); ok {
		changed = explicit
	}

	return ActionResult{
		Changed: changed,
		Stats: map[string]interface{}{
			"stdout":    stdout,
			"stderr":    stderr,
			"exit_code": result.ExitCode,
		},
	}, nil
}
