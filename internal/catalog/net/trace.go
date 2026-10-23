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
		Description: "Runs tracepath, which needs no raw socket and so no privilege, and returns each hop's address. Without container it runs on the device, a Linux machine reached over SSH. With container it runs inside that container on the device, a container host, through the engine's own exec as container.exec does, so a lab host with no SSH of its own (a containerlab end host) can be traced from; the container needs tracepath (iputils) and /bin/sh. pleiades validate refuses a call whose device cannot take the shape it chose: container on a device that is not a container host, or none on one that is not a Linux SSH device. With expect, it fails the task when the destination was not reached, or a hop named in includes is missing, or one named in avoids is on the path. It changes nothing.",
		Params: []collection.Param{
			{Name: "dest", Type: "string", Required: true, Description: "The address to trace to.", Format: collection.ParamFormatCommand},
			{Name: "max_hops", Type: "int", Default: "30", Description: "The most hops to try."},
			{Name: "expect", Type: "dict", Description: "reached (bool), includes (list of addresses that must be hops) and avoids (list that must not be)."},
			{Name: traceContainer, Type: "string", Description: "Trace from this container on the device, a container host, rather than from the device itself. The device then needs a container engine, not SSH and Linux."},
			{Name: containerengine.ParamEngine, Type: "string", Description: "With container, the engine on a device that runs both Docker and Podman: docker or podman. Never inferred."},
			{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: "hops", Type: "list of string", Returned: "always", Description: "Each hop's address, in order, the destination last when reached."},
			{Name: "reached", Type: "bool", Returned: "always", Description: "Whether the destination answered."},
		},
		Examples: []collection.Example{
			{Name: "Expect a path through r2", RunbookYAML: "- name: h1's LAN reaches h2 through r2\n  net.trace:\n    dest: 10.3.3.10\n    expect:\n      reached: true\n      includes: [10.0.12.1]\n"},
			{Name: "Trace from a lab host container", RunbookYAML: "- name: h1 reaches h2 through r1\n  net.trace:\n    target: dockerhost\n    container: clab-routing-h1\n    dest: 10.3.3.10\n    expect:\n      reached: true\n      includes: [10.1.1.1]\n"},
		},
		SeeAlso: []string{"wait.routes", "net.batfish.check", "container.exec"},
	}
}

// traceExpect is what the path must be.
type traceExpect struct {
	reached         *bool
	includes, avoid []string
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
	hops, reached := parseTracepath(res.Stdout)
	if len(hops) == 0 && !reached {
		return collection.Result{}, fmt.Errorf("%s: tracepath exited %d with no hops: %s", traceFQCN, res.ExitCode, deviceText(res.Stderr+res.Stdout))
	}
	if err := rc.SetStat("hops", hops); err != nil {
		return collection.Result{}, err
	}
	if err := rc.SetStat("reached", reached); err != nil {
		return collection.Result{}, err
	}
	if problems := expect.check(hops, reached); len(problems) > 0 {
		return collection.Result{}, fmt.Errorf("%s: the path to %s is not the one expected: %s (hops: %s)", traceFQCN, dest, strings.Join(problems, "; "), strings.Join(hops, " "))
	}
	return collection.Result{}, nil
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
		if k != "reached" && k != "includes" && k != "avoids" {
			return e, fmt.Errorf("%s: expect has no %q; it takes reached, includes and avoids", traceFQCN, k)
		}
	}
	if r, ok := m["reached"]; ok {
		b, ok := r.(bool)
		if !ok {
			return e, fmt.Errorf("%s: expect.reached must be true or false", traceFQCN)
		}
		e.reached = &b
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
func (e traceExpect) check(hops []string, reached bool) []string {
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
	return problems
}

// parseTracepath reads iputils tracepath -n's answer: each hop's address
// once, in order ("no reply" kept as "*", so a hop's position holds), and
// whether a line said reached.
func parseTracepath(out string) (hops []string, reached bool) {
	seen := map[int]bool{}
	for _, line := range strings.Split(out, "\n") {
		m := traceHop.FindStringSubmatch(line)
		if m == nil || m[2] == "[LOCALHOST]" {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		if strings.HasSuffix(strings.TrimSpace(line), "reached") {
			reached = true
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		addr := m[2]
		if addr == "no" {
			addr = "*"
		}
		hops = append(hops, addr)
	}
	return hops, reached
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
