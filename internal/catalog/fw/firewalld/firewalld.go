// Package firewalld implements the three "fw.firewalld.*" Collection
// methods: allow, deny and reload.
//
// # No new pkg/ primitive
//
// Firewall state is read and changed entirely through firewall-cmd,
// plain SSH commands via pkg/remoteexec, the same tier svc.systemd.*
// ships at. Nothing here needed a shared primitive of its own.
//
// # Permanent and runtime are independent, and both are always read
//
// firewalld keeps two separate rule sets: the permanent configuration
// (survives a reload) and the runtime configuration (in effect right
// now), and they can disagree. allow and deny each read both, always,
// regardless of which the task's own permanent and immediate params ask
// to change, for the same reason internal/catalog/fs reads a mountpoint's
// live state and its fstab entry together: the recorded diff describes
// what the device actually has, not just what the task touched.
//
// # allow and deny converge each half independently
//
// A task naming only permanent: true (immediate: false) changes the
// permanent configuration and leaves the runtime one alone, and the
// reverse for immediate: true (permanent: false). Both default true,
// matching firewall-cmd's own default of touching runtime immediately
// and community.general.firewalld's own default of also persisting.
// reload is the only one of the three that applies a permanent change to
// the runtime configuration; allow and deny never fold that in
// themselves.
//
// # The capability, and the devices that satisfy it
//
// capability.FirewalldCapable (pkg/capability/capabilities_service.go) is
// what RequiredCapabilities below names. linux_server implements its
// accessor (internal/inventory/devices/linux/packages.go) and declares it
// only when the device's firewalld property is true: not every Linux
// server runs firewalld (some run iptables or ufw directly, and minimal
// images run none), so it is a per-server fact rather than part of the
// baseline the way SystemdCapable is.
package firewalld

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameter names. port, protocol, service, zone, permanent and
// immediate are community.general.firewalld's own names.
const (
	paramPort      = "port"
	paramProtocol  = "protocol"
	paramService   = "service"
	paramZone      = "zone"
	paramPermanent = "permanent"
	paramImmediate = "immediate"
)

const (
	defaultZone     = "public"
	defaultProtocol = "tcp"
)

const statZone = "zone"

// ruleTarget is what allow and deny act on: exactly one of a port (with
// its protocol) or a service name, in firewall-cmd's own --query-port /
// --query-service vocabulary.
type ruleTarget struct {
	kind     string // "port" or "service"
	port     int
	protocol string
	service  string
}

func (t ruleTarget) spec() string {
	if t.kind == "service" {
		return t.service
	}
	return fmt.Sprintf("%d/%s", t.port, t.protocol)
}

func (t ruleTarget) queryFlag() string  { return "--query-" + t.kind + "=" + t.spec() }
func (t ruleTarget) addFlag() string    { return "--add-" + t.kind + "=" + t.spec() }
func (t ruleTarget) removeFlag() string { return "--remove-" + t.kind + "=" + t.spec() }

// Map renders the target the way sdk.Diff and an emitted inverse both
// need it: exactly the params a task would have written to name it.
func (t ruleTarget) Map() map[string]any {
	if t.kind == "service" {
		return map[string]any{paramService: t.service}
	}
	return map[string]any{paramPort: t.port, paramProtocol: t.protocol}
}

// parseTarget reads exactly one of port(+protocol) or service from
// params, refusing both given together or neither given: a rule names
// what it applies to, and there is no default that could stand in for a
// task that named nothing.
func parseTarget(params map[string]any) (ruleTarget, error) {
	port, portGiven, err := sdk.IntParam(params, paramPort)
	if err != nil {
		return ruleTarget{}, fmt.Errorf("%s: %w", paramPort, err)
	}
	service := sdk.StringParam(params, paramService)
	if portGiven && service != "" {
		return ruleTarget{}, fmt.Errorf("specify %s or %s, not both", paramPort, paramService)
	}
	if !portGiven && service == "" {
		return ruleTarget{}, fmt.Errorf("specify one of %s or %s", paramPort, paramService)
	}
	if service != "" {
		return ruleTarget{kind: "service", service: service}, nil
	}
	protocol := sdk.StringParam(params, paramProtocol)
	if protocol == "" {
		protocol = defaultProtocol
	}
	return ruleTarget{kind: "port", port: port, protocol: protocol}, nil
}

func zoneParam(params map[string]any) string {
	if z := sdk.StringParam(params, paramZone); z != "" {
		return z
	}
	return defaultZone
}

// ruleState is whether tgt is currently allowed in zone, read
// independently for the permanent configuration and the runtime one.
type ruleState struct {
	permanentAllowed bool
	runtimeAllowed   bool
}

func (s ruleState) Map() map[string]any {
	return map[string]any{
		"permanent_allowed": s.permanentAllowed,
		"runtime_allowed":   s.runtimeAllowed,
	}
}

func queryRuleState(ctx context.Context, conn *remoteexec.Conn, zone string, tgt ruleTarget) (ruleState, error) {
	permanent, err := queryAllowed(ctx, conn, zone, tgt, true)
	if err != nil {
		return ruleState{}, err
	}
	runtime, err := queryAllowed(ctx, conn, zone, tgt, false)
	if err != nil {
		return ruleState{}, err
	}
	return ruleState{permanentAllowed: permanent, runtimeAllowed: runtime}, nil
}

// queryAllowed asks firewall-cmd whether tgt is allowed in zone, in
// either the permanent configuration or the runtime one. firewall-cmd's
// own --query-* convention is exit 0 for yes and exit 1 for no; any
// other exit is a real failure (an unknown zone, a malformed spec, a
// firewalld that is not running).
func queryAllowed(ctx context.Context, conn *remoteexec.Conn, zone string, tgt ruleTarget, permanent bool) (bool, error) {
	args := []string{"firewall-cmd", "--zone=" + zone, tgt.queryFlag()}
	if permanent {
		args = append(args, "--permanent")
	}
	result, err := conn.Run(ctx, remoteexec.QuoteCommand(args))
	if err != nil {
		return false, err
	}
	switch result.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		command := remoteexec.QuoteCommand(args)
		return false, fmt.Errorf("%s exited %d: %s", command, result.ExitCode, failureDetail(result))
	}
}

// convergeRule makes tgt allowed (add=true) or not allowed (add=false) in
// zone, in whichever of the permanent configuration and the runtime one
// the task asked for (permanent, immediate), converging each
// independently against before and reporting exactly which one(s) it
// actually changed. A half already in the requested state is left
// alone, the same "read before acting" discipline every method in this
// batch follows.
func convergeRule(ctx context.Context, conn *remoteexec.Conn, zone string, tgt ruleTarget, before ruleState, permanent, immediate, add bool) (permanentChanged, runtimeChanged bool, err error) {
	flag := tgt.addFlag()
	if !add {
		flag = tgt.removeFlag()
	}
	changePermanent, changeRuntime := ruleChanges(before, permanent, immediate, add)

	if changePermanent {
		if err := runFirewallCmd(ctx, conn, []string{"firewall-cmd", "--permanent", "--zone=" + zone, flag}); err != nil {
			return false, false, err
		}
		permanentChanged = true
	}
	if changeRuntime {
		if err := runFirewallCmd(ctx, conn, []string{"firewall-cmd", "--zone=" + zone, flag}); err != nil {
			return permanentChanged, false, err
		}
		runtimeChanged = true
	}
	return permanentChanged, runtimeChanged, nil
}

// ruleChanges is convergeRule's decision, with nothing run: whether the
// permanent configuration and the runtime one each need changing, which
// is exactly the halves the task asked for (permanent, immediate) that are
// not already in the requested state. A check predicts from it and
// convergeRule acts on it, so the two cannot disagree.
func ruleChanges(before ruleState, permanent, immediate, add bool) (changePermanent, changeRuntime bool) {
	return permanent && before.permanentAllowed != add, immediate && before.runtimeAllowed != add
}

// checkRule is allow's and deny's check: the halves ruleChanges says would
// change, set to the requested state, recorded as the predicted after.
func checkRule(rc sdk.RunbookContext, zone string, before ruleState, permanent, immediate, add bool) (collection.Result, error) {
	changePermanent, changeRuntime := ruleChanges(before, permanent, immediate, add)
	after := before
	if changePermanent {
		after.permanentAllowed = add
	}
	if changeRuntime {
		after.runtimeAllowed = add
	}
	if err := recordRuleState(rc, zone, before, after); err != nil {
		return collection.Result{}, err
	}
	return collection.Result{Changed: changePermanent || changeRuntime}, nil
}

// runFirewallCmd runs one mutating firewall-cmd invocation (add, remove
// or reload) and treats a non-zero exit as a real error.
func runFirewallCmd(ctx context.Context, conn *remoteexec.Conn, args []string) error {
	command := remoteexec.QuoteCommand(args)
	result, err := conn.Run(ctx, command)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("%s exited %d: %s", command, result.ExitCode, failureDetail(result))
	}
	return nil
}

// failureDetail picks the stream an operator should read after a
// non-zero exit. Mirrors internal/catalog/fs's own copy; firewall-cmd
// almost always explains itself on stderr.
func failureDetail(result remoteexec.Result) string {
	if detail := strings.TrimSpace(result.Stderr); detail != "" {
		return detail
	}
	if detail := strings.TrimSpace(result.Stdout); detail != "" {
		return detail
	}
	return "no output"
}

// recordRuleState writes the zone and the before/after diff, the two
// things both allow and deny report regardless of which one ran.
func recordRuleState(rc sdk.RunbookContext, zone string, before, after ruleState) error {
	if err := rc.SetStat(statZone, zone); err != nil {
		return err
	}
	return sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after.Map()})
}
