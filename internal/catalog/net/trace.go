// Package net: net.trace, the path from a Linux device, or from a
// container on a container host, to an address.
//
// The method has two shapes. Without container it runs tracepath on a
// Linux device over SSH; with container it runs tracepath inside that
// container through the host's engine, the exec container.exec sends
// (pkg/containerengine's Session.Exec), so a host in a lab that has no SSH
// of its own (containerlab's end hosts) can be traced from. The two need
// different capabilities and transports, declared as the manifest's own
// shape and a call shape selected by container (collection.CallShape),
// which validation, the engine and admission hold each call to.
package net

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// traceFQCN is net.trace's name.
const traceFQCN = "net.trace"

// traceContainer is the parameter that traces from a container on a
// container host rather than from the device itself.
const traceContainer = "container"

// traceHost is a destination that is not an address: a host name, never
// anything tracepath could read as an option.
var traceHost = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)

// traceHop is one line of tracepath's answer: the hop number, an
// optional "?" for the first probe, and the address or "no reply".
var traceHop = regexp.MustCompile(`^\s*(\d+)\??:\s+(\S+)`)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: traceFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportSSH},
			RequiredCapabilities: []capability.Name{capability.NameLinux, capability.NameCommandExec},
			// Given container, the call reaches a container host's engine
			// instead, and needs only that.
			Shapes: []collection.CallShape{{
				Param:                traceContainer,
				SupportedTransports:  capability.EngineTransports(),
				RequiredCapabilities: []capability.Name{capability.NameContainerEngine},
			}},
			ExecutionContext: collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:    ">=0.2.0",
			Status:           collection.StatusImplemented,
			NoExistsReason:   collection.NoExistsProbe,
			Reversibility:    collection.Reversibility{ReadOnly: true, Notes: "It sends probes and reads their answers, changing nothing."},
			SupportsCheck:    true,
			Doc:              traceDoc(),
		},
		Invoke: Trace,
		Check:  Trace,
	})
}

// traceDoc is net.trace's reference documentation, which
// internal/forge/catalogdata holds an identical copy of.
func traceDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Traces the path to an address from a Linux device or a container on a container host, and fails when it is not the path expected.",
		Description: "Runs tracepath, which needs no raw socket and so no privilege, and returns each hop's address. Without container it runs on the device, a Linux machine reached over SSH. With container it runs inside that container on the device, a container host, through the engine's own exec as container.exec does, so a lab host with no SSH of its own (a containerlab end host) can be traced from; the container needs tracepath (iputils) and /bin/sh. pleiades validate refuses a call whose device cannot take the shape it chose: container on a device that is not a container host, or none on one that is not a Linux SSH device. With expect, it fails the task when the destination was not reached, or a hop named in includes is missing, or one named in avoids is on the path, or the path is not as symmetric as expect.symmetric says. path names what the trace found: reached; loop, when one address answered at two hops with another hop between them, so the probe passed that router twice; unreached, when some hop answered and none shows a loop; or unknown, when no hop answered at all. A run of unanswered hops is never read as a loop, since a filter, an ICMP rate limit and a loop all look the same, and a trace no hop answered fails the task, since it says nothing about the path, unless the only thing expected is reached: false. tracepath reads how many hops each reply came back over from the TTL it arrives with, assuming the sender began at 64, 128 or 255: a hop whose reply came back over a different number of hops carries return_hops in hop_details, and a trace that reached its destination returns the destination's return_hops and whether that differs from the hops out (asymmetric). These count hops and never name routers, so a return path of the same length through other routers reads as symmetric. It changes nothing.",
		Params: []collection.Param{
			{Name: "dest", Type: "string", Required: true, Description: "The address to trace to.", Format: collection.ParamFormatCommand},
			{Name: "max_hops", Type: "int", Default: "30", Description: "The most hops to try."},
			{Name: "expect", Type: "dict", Description: "reached (bool), includes (list of addresses that must be hops), avoids (list that must not be) and symmetric (bool: true fails an asymmetric path, false a symmetric one, and either fails a trace that did not reach its destination or whose reply's hop count tracepath could not read)."},
			{Name: traceContainer, Type: "string", Description: "Trace from this container on the device, a container host, rather than from the device itself. The device then needs a container engine, not SSH and Linux. An empty value is refused rather than read as none: leave container out to trace from the device."},
			{Name: containerengine.ParamEngine, Type: "string", Choices: []string{"docker", "podman"}, Description: "With container, the engine on a device that runs both Docker and Podman: docker or podman. Never inferred."},
			{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: "hops", Type: "list of string", Returned: "always", Description: "Each hop's address, in order, the destination last when reached."},
			{Name: "reached", Type: "bool", Returned: "always", Description: "Whether the destination answered."},
			{Name: "path", Type: "string", Returned: "always", Description: "reached, loop, unreached or unknown, as the description says."},
			{Name: "hop_details", Type: "list of dict", Returned: "always", Description: "Each hop in order: hop (its number), address (\"*\" when no reply came), and return_hops on a hop whose reply came back over a different number of hops than its number (tracepath's asymm, or for the destination its back)."},
			{Name: "return_hops", Type: "int", Returned: "when the destination answered and tracepath read its reply's hop count", Description: "How many hops the destination's reply came back over (tracepath's back)."},
			{Name: "asymmetric", Type: "bool", Returned: "with return_hops", Description: "Whether return_hops differs from the hops the probe went out over. False means only that the counts agree."},
		},
		Examples: []collection.Example{
			{Name: "Expect a path through r2", RunbookYAML: "- name: h1's LAN reaches h2 through r2\n  net.trace:\n    dest: 10.3.3.10\n    expect:\n      reached: true\n      includes: [10.0.12.1]\n"},
			{Name: "Trace from a lab host container", RunbookYAML: "- name: h1 reaches h2 through r1\n  net.trace:\n    target: dockerhost\n    container: clab-routing-h1\n    dest: 10.3.3.10\n    expect:\n      reached: true\n      includes: [10.1.1.1]\n"},
			{Name: "Expect the reply to come back over as many hops", RunbookYAML: "- name: h2 answers h1 over as many hops as h1 reached it\n  net.trace:\n    dest: 10.3.3.10\n    expect:\n      reached: true\n      symmetric: true\n"},
		},
		SeeAlso: []string{"wait.routes", "net.batfish.check", "container.exec"},
	}
}

// traceExpect is what the path must be.
type traceExpect struct {
	// reached and symmetric are nil when not asked.
	reached, symmetric *bool
	includes, avoid    []string
}

// onlyUnreached reports whether the one thing asked is that the destination
// not answer, which a trace no hop answered still shows.
func (e traceExpect) onlyUnreached() bool {
	return e.reached != nil && !*e.reached && e.symmetric == nil && len(e.includes) == 0 && len(e.avoid) == 0
}

// Trace runs tracepath to dest and holds the path to expect.
func Trace(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	dest, err := sdk.RequiredStringParam(params, "dest")
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", traceFQCN, err)
	}
	if _, err := netip.ParseAddr(dest); err != nil && !traceHost.MatchString(dest) {
		return collection.Result{}, fmt.Errorf("%s: dest must be an address or a host name, got %q", traceFQCN, dest)
	}
	maxHops, ok, err := sdk.IntParam(params, "max_hops")
	if err != nil {
		return collection.Result{}, err
	}
	if !ok {
		maxHops = 30
	}
	if maxHops < 1 || maxHops > 255 {
		return collection.Result{}, fmt.Errorf("%s: max_hops must be 1 to 255, got %d", traceFQCN, maxHops)
	}
	expect, err := traceExpectParam(params)
	if err != nil {
		return collection.Result{}, err
	}

	from, err := sdk.OptionalStringParam(params, traceContainer)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", traceFQCN, err)
	}
	// Validation, the engine and admission held this call to the container
	// shape because container is present (collection.Manifest.For), so an
	// empty one (written so, or rendered so) is refused rather than traced
	// over SSH, a shape nothing checked this device for.
	if v, present := params[traceContainer]; present && v != nil && from == "" {
		return collection.Result{}, fmt.Errorf("%s: container is empty; name the container to trace from, or leave container out to trace from the device itself", traceFQCN)
	}
	cmd := remoteexec.QuoteCommand([]string{"tracepath", "-n", "-m", strconv.Itoa(maxHops), dest})
	var res remoteexec.Result
	where := "the device"
	if from != "" {
		where = "container " + from
		res, err = traceInContainer(ctx, rc, device, params, from, cmd)
	} else {
		res, err = traceOverSSH(ctx, rc, device, params, cmd)
	}
	if err != nil {
		return collection.Result{}, err
	}
	if res.ExitCode == 127 {
		return collection.Result{}, fmt.Errorf("%s: %s has no tracepath (iputils): %s", traceFQCN, where, deviceText(res.Stderr+res.Stdout))
	}
	path := parseTracepath(res.Stdout)
	if len(path.hops) == 0 && !path.reached {
		return collection.Result{}, fmt.Errorf("%s: tracepath exited %d with no hops: %s", traceFQCN, res.ExitCode, deviceText(res.Stderr+res.Stdout))
	}
	if err := setTraceStats(rc, path); err != nil {
		return collection.Result{}, err
	}
	hops := path.addresses()
	if problems := expect.check(path); len(problems) > 0 {
		return collection.Result{}, fmt.Errorf("%s: the path to %s is not the one expected: %s (hops: %s)", traceFQCN, dest, strings.Join(problems, "; "), strings.Join(hops, " "))
	}
	// No hop answered: the trace says nothing about the path, which a
	// success would claim it did. Only "the destination did not answer"
	// is known, so expect.reached: false alone is held to it.
	if path.state() == pathUnknown && !expect.onlyUnreached() {
		return collection.Result{}, fmt.Errorf("%s: no hop on the way to %s answered, so the path is unknown: a path filtered at its first router, a loop whose routers send no ICMP and a missing route all look the same (hops: %s)", traceFQCN, dest, strings.Join(hops, " "))
	}
	return collection.Result{}, nil
}

// setTraceStats records what the trace found: hops and reached as before,
// path's state, each hop's details, and the destination's hop counts out
// and back when tracepath printed them.
func setTraceStats(rc sdk.RunbookContext, path tracedPath) error {
	stats := map[string]any{
		"hops":        path.addresses(),
		"reached":     path.reached,
		"path":        path.state(),
		"hop_details": path.hopDetails(),
	}
	if out, back, ok := path.returnCounts(); ok {
		stats["return_hops"] = back
		stats["asymmetric"] = out != back
	}
	for _, k := range []string{"hops", "reached", "path", "hop_details", "return_hops", "asymmetric"} {
		v, ok := stats[k]
		if !ok {
			continue
		}
		if err := rc.SetStat(k, v); err != nil {
			return err
		}
	}
	return nil
}

// traceOverSSH runs cmd on device over SSH.
func traceOverSSH(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, cmd string) (remoteexec.Result, error) {
	conn, err := sdk.Connect(ctx, rc, device, params, traceFQCN)
	if err != nil {
		return remoteexec.Result{}, err
	}
	defer func() { _ = conn.Close() }()
	res, err := conn.Run(ctx, cmd)
	if err != nil {
		return remoteexec.Result{}, fmt.Errorf("%s: %w", traceFQCN, err)
	}
	return res, nil
}

// traceInContainer runs cmd inside container on device, a container host,
// through the engine's exec: the session container.exec opens, allowed
// only the exec operations.
func traceInContainer(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, container, cmd string) (remoteexec.Result, error) {
	if err := containerengine.ValidName("container", container); err != nil {
		return remoteexec.Result{}, fmt.Errorf("%s: %w", traceFQCN, err)
	}
	s, err := containerengine.Open(ctx, rc, device, params, traceFQCN, containerengine.ExecOps...)
	if err != nil {
		return remoteexec.Result{}, err
	}
	defer s.Close()
	out, err := s.Exec(ctx, container, cmd, containerengine.ExecOptions{})
	if err != nil {
		return remoteexec.Result{}, fmt.Errorf("%s: %w", traceFQCN, err)
	}
	return remoteexec.Result{Stdout: out.Stdout, Stderr: out.Stderr, ExitCode: out.ExitCode}, nil
}

// traceExpectParam reads expect.
func traceExpectParam(params map[string]any) (traceExpect, error) {
	var e traceExpect
	v, ok := params["expect"]
	if !ok || v == nil {
		return e, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return e, fmt.Errorf("%s: expect must be a map of reached, includes and avoids", traceFQCN)
	}
	for k := range m {
		if k != "reached" && k != "includes" && k != "avoids" && k != "symmetric" {
			return e, fmt.Errorf("%s: expect has no %q; it takes reached, includes, avoids and symmetric", traceFQCN, k)
		}
	}
	for key, into := range map[string]**bool{"reached": &e.reached, "symmetric": &e.symmetric} {
		r, ok := m[key]
		if !ok {
			continue
		}
		b, ok := r.(bool)
		if !ok {
			return e, fmt.Errorf("%s: expect.%s must be true or false", traceFQCN, key)
		}
		*into = &b
	}
	for key, into := range map[string]*[]string{"includes": &e.includes, "avoids": &e.avoid} {
		list, _, err := sdk.StringSlice(m, key)
		if err != nil {
			return e, fmt.Errorf("%s: expect.%s: %w", traceFQCN, key, err)
		}
		for _, a := range list {
			addr, err := netip.ParseAddr(a)
			if err != nil {
				return e, fmt.Errorf("%s: expect.%s: %q is not an address", traceFQCN, key, a)
			}
			*into = append(*into, addr.String())
		}
	}
	return e, nil
}

// check returns what about the path differs from what was expected.
func (e traceExpect) check(path tracedPath) []string {
	hops, reached := path.addresses(), path.reached
	var problems []string
	if e.reached != nil && *e.reached != reached {
		if reached {
			problems = append(problems, "the destination answered, and was expected not to")
		} else {
			problems = append(problems, "the destination did not answer")
		}
	}
	for _, a := range e.includes {
		if !slices.Contains(hops, a) {
			problems = append(problems, a+" is not on the path")
		}
	}
	for _, a := range e.avoid {
		if slices.Contains(hops, a) {
			problems = append(problems, a+" is on the path")
		}
	}
	if e.symmetric != nil {
		problems = append(problems, e.checkSymmetric(path)...)
	}
	return problems
}

// checkSymmetric holds the destination's hop counts out and back to
// expect.symmetric. A trace that did not reach its destination, or whose
// reply's hop count tracepath could not read, cannot say either way, and
// fails both.
func (e traceExpect) checkSymmetric(path tracedPath) []string {
	out, back, ok := path.returnCounts()
	switch {
	case !path.reached:
		return []string{"the destination did not answer, so the hops its reply came back over are unknown"}
	case !ok:
		return []string{"tracepath could not read how many hops the destination's reply came back over"}
	case *e.symmetric && out != back:
		return []string{fmt.Sprintf("the path is asymmetric: the probe went out over %d hops and the reply came back over %d", out, back)}
	case !*e.symmetric && out == back:
		return []string{fmt.Sprintf("the path was expected to be asymmetric, and the probe went out and the reply came back over %d hops each", out)}
	}
	return nil
}

// deviceText is a device's words fit for an error: trimmed, at most 300
// bytes, quoted so a control character is shown rather than acted on.
func deviceText(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 300 {
		text = text[:300] + "..."
	}
	return strconv.Quote(text)
}
