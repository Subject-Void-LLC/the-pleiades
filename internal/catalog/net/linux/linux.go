// Package linux implements the net.linux Collection methods, which manage a
// Linux host's bridges, addresses and routes through iproute2's ip over SSH,
// as the account the task connects as (or, with become, as root). This file
// holds what the three share: the connection, running ip, and registration.
package linux

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/linuxnet"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// ifaceName is what an interface may be called: what Linux allows (15 bytes),
// and nothing ip could read as an option or a second word.
var ifaceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,14}$`)

// session is one method run's SSH connection to the host.
type session struct {
	conn *remoteexec.Conn
	fqcn string
}

// handler is a method's body; check is true for a check, which reads what a
// run reads and changes nothing. It returns whether the host changed (or, in a
// check, would).
type handler func(ctx context.Context, rc sdk.RunbookContext, s *session, p map[string]any, check bool) (bool, error)

// behavior is a method's body and its reversibility declaration.
type behavior struct {
	run handler
	rev collection.Reversibility
}

// inverse names one undo a method may record.
func inverse(fqcn string, partial bool, keys ...string) sdk.InverseSpec {
	return sdk.InverseSpec{FQCN: fqcn, Record: keys, MayBePartial: partial, MayKeepOnFailure: true}
}

// behaviors is each method's behavior; a test holds it equal to pkg/linuxnet's
// documented methods.
var behaviors = map[string]behavior{
	"net.linux.bridge": {run: bridgeRun, rev: collection.Reversibility{Reversible: true,
		Notes:    "Its undo puts back what it found: a created bridge is removed, attached ports are detached, detached ports are attached again, and a removed bridge is made again with the ports it held, which is partial: a port's own addresses and settings are not recorded.",
		Inverses: []sdk.InverseSpec{inverse("net.linux.bridge", true, "name", "state", "ports", "up", "detach_ports")}}},
	"net.linux.address": {run: addressRun, rev: collection.Reversibility{Reversible: true,
		Notes:    "Its undo is the other state of the same address.",
		Inverses: []sdk.InverseSpec{inverse("net.linux.address", false, "interface", "address", "state")}}},
	"net.linux.route": {run: routeRun, rev: collection.Reversibility{Reversible: true,
		Notes:    "Its undo puts back what it found: a new route is removed, a replaced route is replaced by the old one, a removed route is added again.",
		Inverses: []sdk.InverseSpec{inverse("net.linux.route", false, "destination", "via", "dev", "metric", "state")}}},
}

func init() {
	for _, spec := range linuxnet.Specs() {
		b, ok := behaviors[spec.Name]
		if !ok {
			panic(fmt.Sprintf("net.linux: %s is documented and has no behavior", spec.Name))
		}
		register(spec, b)
	}
}

// register makes one method from its text and its behavior.
func register(spec linuxnet.Spec, b behavior) {
	call := func(check bool) collection.Method {
		return func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
			if err := onlyDeclared(spec, params); err != nil {
				return collection.Result{}, err
			}
			conn, err := sdk.Connect(ctx, rc, device, params, spec.Name)
			if err != nil {
				return collection.Result{}, err
			}
			defer func() { _ = conn.Close() }()
			changed, err := b.run(ctx, rc, &session{conn: conn, fqcn: spec.Name}, params, check)
			return collection.Result{Changed: changed}, err
		}
	}
	collection.MustRegister(collection.Descriptor{
		Name: spec.Name,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportSSH},
			RequiredCapabilities: []capability.Name{capability.NameLinux, capability.NameCommandExec},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.3.0",
			Status:               collection.StatusImplemented,
			Reversibility:        b.rev,
			SupportsCheck:        true,
			Doc:                  spec.Doc,
		},
		Invoke: call(false),
		Check:  call(true),
	})
}

// onlyDeclared refuses a parameter the method does not declare.
func onlyDeclared(spec linuxnet.Spec, params map[string]any) error {
	known := map[string]bool{"target": true, "within": true}
	for _, p := range spec.Doc.Params {
		known[p.Name] = true
	}
	for k := range params {
		if !known[k] {
			return fmt.Errorf("%s: no parameter %q", spec.Name, k)
		}
	}
	return nil
}

// sh runs a shell script on the host with its arguments, returning the exit
// code and the two streams.
func (s *session) sh(ctx context.Context, script string, args ...string) (int, string, string, error) {
	argv := append([]string{"sh", "-c", script, "sh"}, args...)
	res, err := s.conn.Run(ctx, remoteexec.QuoteCommand(argv))
	if err != nil {
		return 0, "", "", fmt.Errorf("%s: %w", s.fqcn, err)
	}
	return res.ExitCode, res.Stdout, res.Stderr, nil
}

// ip runs ip with args and fails on a non-zero exit, naming ip's own words and
// what to do when they are a refusal for rights.
func (s *session) ip(ctx context.Context, args ...string) (string, error) {
	res, err := s.conn.Run(ctx, remoteexec.QuoteCommand(append([]string{"ip"}, args...)))
	if err != nil {
		return "", fmt.Errorf("%s: ip %s: %w", s.fqcn, args[0], err)
	}
	if res.ExitCode != 0 {
		said := strings.TrimSpace(res.Stderr + " " + res.Stdout)
		hint := ""
		if strings.Contains(said, "not permitted") || strings.Contains(said, "ermission denied") {
			hint = ": changing the network needs root or CAP_NET_ADMIN, so connect as an account that has it, or set become: true on the task"
		}
		return "", fmt.Errorf("%s: ip %s exited %d: %q%s", s.fqcn, strings.Join(args, " "), res.ExitCode, truncate(said), hint)
	}
	return res.Stdout, nil
}

// truncate cuts device text to 300 bytes for an error.
func truncate(s string) string {
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// textParam returns a text parameter, "" when absent.
func textParam(p map[string]any, name string) (string, error) {
	v, ok := p[name]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be text, not %T", name, v)
	}
	return s, nil
}

// wantPresent reads state: present (the default) or absent.
func wantPresent(p map[string]any) (bool, error) {
	state, err := textParam(p, "state")
	if err != nil {
		return false, err
	}
	switch state {
	case "", "present":
		return true, nil
	case "absent":
		return false, nil
	}
	return false, fmt.Errorf("state must be present or absent, got %q", state)
}

// iface returns an interface-name parameter, held to ifaceName.
func iface(p map[string]any, name string) (string, error) {
	s, err := textParam(p, name)
	if err != nil {
		return "", err
	}
	if !ifaceName.MatchString(s) {
		return "", fmt.Errorf("%s %q is not an interface name", name, s)
	}
	return s, nil
}

// prefix returns an address-with-prefix parameter.
func prefix(p map[string]any, name string) (netip.Prefix, error) {
	s, err := textParam(p, name)
	if err != nil {
		return netip.Prefix{}, err
	}
	pre, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%s %q is not an address with a prefix length (10.1.1.1/24)", name, s)
	}
	return pre, nil
}

// intParam returns a whole-number parameter and whether it was given.
func intParam(p map[string]any, name string) (int, bool, error) {
	v, ok := p[name]
	if !ok || v == nil {
		return 0, false, nil
	}
	switch n := v.(type) {
	case int:
		return n, true, nil
	case int64:
		return int(n), true, nil
	case float64:
		if n == float64(int(n)) {
			return int(n), true, nil
		}
	case string:
		if i, err := strconv.Atoi(n); err == nil {
			return i, true, nil
		}
	}
	return 0, false, fmt.Errorf("%s must be a whole number, not %v", name, v)
}

// listParam returns a list-of-text parameter, each item an interface name.
func listParam(p map[string]any, name string) ([]string, error) {
	v, ok := p[name]
	if !ok || v == nil {
		return nil, nil
	}
	var out []string
	switch items := v.(type) {
	case []string:
		out = items
	case []any:
		for _, it := range items {
			s, isText := it.(string)
			if !isText {
				return nil, fmt.Errorf("%s must be a list of interface names, and holds %T", name, it)
			}
			out = append(out, s)
		}
	default:
		return nil, fmt.Errorf("%s must be a list of interface names, not %T", name, v)
	}
	for _, s := range out {
		if !ifaceName.MatchString(s) {
			return nil, fmt.Errorf("%s holds %q, which is not an interface name", name, s)
		}
	}
	return out, nil
}
