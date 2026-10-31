// net.linux.bridge: a Linux bridge, the ports on it, and whether it is up.
package linux

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// readBridgeScript prints what /sys says of a device: absent, notbridge, or
// bridge followed by its flags (hex) and one port per line. It needs no parsing
// of ip's output, so it reads the same on iproute2 and on BusyBox.
const readBridgeScript = `d=/sys/class/net/$1
if [ ! -e "$d" ]; then echo absent
elif [ ! -d "$d/bridge" ]; then echo notbridge
else echo bridge; cat "$d/flags"; ls "$d/brif"; fi`

// bridgeState is a bridge as the host has it.
type bridgeState struct {
	exists, notBridge, up bool
	ports                 []string
}

// readBridge reads name.
func (s *session) readBridge(ctx context.Context, name string) (bridgeState, error) {
	code, out, errOut, err := s.sh(ctx, readBridgeScript, name)
	if err != nil {
		return bridgeState{}, err
	}
	if code != 0 {
		return bridgeState{}, fmt.Errorf("%s: reading %s exited %d: %q", s.fqcn, name, code, truncate(errOut))
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	switch lines[0] {
	case "absent":
		return bridgeState{}, nil
	case "notbridge":
		return bridgeState{exists: true, notBridge: true}, nil
	}
	st := bridgeState{exists: true}
	if len(lines) > 1 {
		flags, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(lines[1]), "0x"), 16, 64)
		if err != nil {
			return bridgeState{}, fmt.Errorf("%s: %s's flags %q are not hex", s.fqcn, name, lines[1])
		}
		st.up = flags&1 != 0
	}
	for _, l := range lines[min(2, len(lines)):] {
		if l = strings.TrimSpace(l); l != "" {
			st.ports = append(st.ports, l)
		}
	}
	slices.Sort(st.ports)
	return st, nil
}

// bridgeRun makes a bridge exist with the ports given, or not exist.
func bridgeRun(ctx context.Context, rc sdk.RunbookContext, s *session, p map[string]any, check bool) (bool, error) {
	name, err := iface(p, "name")
	if err != nil {
		return false, fmt.Errorf("%s: %w", s.fqcn, err)
	}
	present, err := wantPresent(p)
	if err != nil {
		return false, fmt.Errorf("%s: %w", s.fqcn, err)
	}
	ports, err := listParam(p, "ports")
	if err != nil {
		return false, fmt.Errorf("%s: %w", s.fqcn, err)
	}
	detach, err := listParam(p, "detach_ports")
	if err != nil {
		return false, fmt.Errorf("%s: %w", s.fqcn, err)
	}
	wantUp, upSet := true, false
	if v, ok := p["up"]; ok && v != nil {
		if wantUp, ok = v.(bool); !ok {
			return false, fmt.Errorf("%s: up must be true or false, not %T", s.fqcn, v)
		}
		upSet = true
	}
	removeOthers := false
	if v, ok := p["remove_other_ports"].(bool); ok {
		removeOthers = v
	}
	if !present && (len(ports) > 0 || len(detach) > 0 || upSet) {
		return false, fmt.Errorf("%s: ports, detach_ports and up describe a bridge that state absent removes", s.fqcn)
	}
	for _, d := range detach {
		if slices.Contains(ports, d) {
			return false, fmt.Errorf("%s: %s is in both ports and detach_ports", s.fqcn, d)
		}
	}

	st, err := s.readBridge(ctx, name)
	if err != nil {
		return false, err
	}
	if st.notBridge {
		return false, fmt.Errorf("%s: %s exists and is not a bridge, so it is not touched", s.fqcn, name)
	}

	if !present {
		if !st.exists {
			return false, s.stats(rc, false, st)
		}
		if check {
			return true, s.stats(rc, false, bridgeState{})
		}
		undo := sdk.Inverse{FQCN: s.fqcn, Params: map[string]any{"name": name, "state": "present", "ports": st.ports, "up": st.up}, Partial: true, KeepOnFailure: true,
			Description: fmt.Sprintf("Makes the bridge %s again with the ports it held (%s); a port's own addresses and settings are not put back.", name, strings.Join(st.ports, ", "))}
		if err := sdk.RecordInverse(rc, undo); err != nil {
			return false, err
		}
		if _, err := s.ip(ctx, "link", "del", name); err != nil {
			return false, err
		}
		return true, s.stats(rc, false, bridgeState{})
	}

	// What a present bridge needs: created, raised, and its ports.
	var attach, detached []string
	for _, port := range ports {
		if !slices.Contains(st.ports, port) {
			attach = append(attach, port)
		}
	}
	var drop []string
	for _, port := range st.ports {
		if slices.Contains(detach, port) || (removeOthers && !slices.Contains(ports, port)) {
			drop = append(drop, port)
		}
	}
	detached = drop
	create := !st.exists
	raise := (create || !st.up) && wantUp
	lower := st.exists && st.up && upSet && !wantUp
	if !create && !raise && !lower && len(attach) == 0 && len(detached) == 0 {
		return false, s.stats(rc, true, st)
	}
	if check {
		after := st
		after.exists, after.up = true, (st.up && !lower) || raise
		after.ports = slices.Concat(slices.DeleteFunc(slices.Clone(st.ports), func(x string) bool { return slices.Contains(detached, x) }), attach)
		slices.Sort(after.ports)
		return true, s.stats(rc, true, after)
	}

	// The undo is recorded before the first change and kept through a
	// failure: undoing a change that did not happen reads the state and does
	// nothing.
	var undo sdk.Inverse
	if create {
		undo = sdk.Inverse{FQCN: s.fqcn, Params: map[string]any{"name": name, "state": "absent"}, KeepOnFailure: true, Description: "Removes the bridge " + name + ", which this run made."}
	} else {
		params := map[string]any{"name": name}
		if len(attach) > 0 {
			params["detach_ports"] = attach
		}
		if len(detached) > 0 {
			params["ports"] = detached
		}
		if raise || lower {
			params["up"] = st.up
		}
		undo = sdk.Inverse{FQCN: s.fqcn, Params: params, KeepOnFailure: true, Description: "Puts " + name + " back as it was: ports, and whether it was up."}
	}
	if err := sdk.RecordInverse(rc, undo); err != nil {
		return false, err
	}
	steps := [][]string{}
	if create {
		steps = append(steps, []string{"link", "add", "name", name, "type", "bridge"})
	}
	if raise {
		steps = append(steps, []string{"link", "set", name, "up"})
	}
	if lower {
		steps = append(steps, []string{"link", "set", name, "down"})
	}
	for _, port := range attach {
		steps = append(steps, []string{"link", "set", port, "master", name})
	}
	for _, port := range detached {
		steps = append(steps, []string{"link", "set", port, "nomaster"})
	}
	for _, step := range steps {
		if _, err := s.ip(ctx, step...); err != nil {
			return true, err
		}
	}
	after, err := s.readBridge(ctx, name)
	if err != nil {
		return true, err
	}
	return true, s.stats(rc, true, after)
}

// stats records the bridge as the host has it now.
func (s *session) stats(rc sdk.RunbookContext, exists bool, st bridgeState) error {
	if err := rc.SetStat("exists", exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	ports := st.ports
	if ports == nil {
		ports = []string{}
	}
	if err := rc.SetStat("ports", ports); err != nil {
		return err
	}
	return rc.SetStat("up", st.up)
}
