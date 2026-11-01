// A check's preconditions, tested before it runs: an unmet one is a SKIP
// naming it, never a run that fails for want of it.
package gate

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// probeOutputLimit bounds what a precondition probe may answer.
const probeOutputLimit = 4 << 10

// preconditions settles a directory check before it runs, reporting
// whether it did: a SKIP for an unmet precondition, a FAIL for a directory
// that is not there.
func (d *deviceRunner) preconditions(ctx context.Context, conn *remoteexec.Conn, c gate.Check, dir string) (gate.Result, bool) {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	res, err := conn.RunWithStdinBounded(pctx, probeScript(c, dir), nil, probeOutputLimit)
	if err != nil {
		return failed("could not test the check's preconditions on the device: " + err.Error()), true
	}
	line, _, _ := strings.Cut(strings.TrimSpace(res.Stdout), "\n")
	switch {
	case line == "nodir":
		return failed(fmt.Sprintf("its directory %s is not a directory on the device", gate.Escape(dir))), true
	case strings.HasPrefix(line, "needs "):
		return gate.Skipped(c, gate.Escape(line)), true
	case line != "ok":
		return failed(fmt.Sprintf("the precondition probe answered %q (exit %d)", remoteexec.Excerpt(line, 128), res.ExitCode)), true
	}
	return d.devicePreconditions(ctx, conn, c)
}

// probeScript tests, in the shell a check runs in and from its directory,
// every precondition that shell can answer: commands on its PATH,
// variables set (by name; a value is never read), files present. It prints
// ok, nodir, or the first unmet one as "needs ...". Every name was
// validated by pkg/gate, and each is quoted anyway.
func probeScript(c gate.Check, dir string) string {
	q := remoteexec.QuoteArg
	lines := []string{"cd " + q(dir) + " 2>/dev/null || { printf 'nodir\\n'; exit 0; }"}
	for _, cmd := range c.Requires.Commands {
		lines = append(lines, "command -v "+q(cmd)+" >/dev/null 2>&1 || { printf 'needs command %s\\n' "+q(cmd)+"; exit 0; }")
	}
	for _, name := range c.Requires.Env {
		lines = append(lines, "[ -n \"${"+name+"+set}\" ] || { printf 'needs env %s\\n' "+q(name)+"; exit 0; }")
	}
	for _, file := range c.Requires.Files {
		lines = append(lines, "[ -e "+q(file)+" ] || { printf 'needs file %s\\n' "+q(file)+"; exit 0; }")
	}
	lines = append(lines, "printf 'ok\\n'")
	return strings.Join(lines, "\n")
}

// devicePreconditions settles what is tested from outside a check's own
// shell: the operating system, the container engine, and TCP endpoints as
// the device reaches them. conn may be nil, for an image check, and one is
// opened only when a TCP endpoint needs it.
func (d *deviceRunner) devicePreconditions(ctx context.Context, conn *remoteexec.Conn, c gate.Check) (gate.Result, bool) {
	r := c.Requires
	if r.OS != "" && d.osName != r.OS {
		return gate.Skipped(c, fmt.Sprintf("needs os %s, and the device runs %s", r.OS, gate.Escape(d.osName))), true
	}
	if r.Engine {
		if !d.device.HasCapability(capability.NameContainerEngine) {
			return gate.Skipped(c, "needs engine: no container engine was onboarded on the device"), true
		}
		pctx, cancel := context.WithTimeout(ctx, probeTimeout)
		d.dial.Lock()
		session, err := containerengine.Open(pctx, d.rc, d.device, d.params, fqcnRun, containerengine.OpInfo)
		d.dial.Unlock()
		cancel()
		if err != nil {
			return gate.Skipped(c, "needs engine: "+gate.Escape(err.Error())), true
		}
		session.Close()
	}
	if len(r.TCP) == 0 {
		return gate.Result{}, false
	}
	// A TCP probe is a port forward the login opens, which become cannot
	// elevate: refused as a failure, not reported as a missing port.
	if err := sdk.RefuseBecome(ctx, fqcnRun, "a TCP precondition, a port forward the SSH login opens"); err != nil {
		return failed(err.Error()), true
	}
	if conn == nil {
		opened, err := d.connect(ctx)
		if err != nil {
			return failed("could not reach the device to test its TCP preconditions: " + err.Error()), true
		}
		defer func() { _ = opened.Close() }()
		conn = opened
	}
	for _, endpoint := range r.TCP {
		pctx, cancel := context.WithTimeout(ctx, probeTimeout)
		ch, err := conn.DialTCP(pctx, endpoint)
		cancel()
		if err != nil {
			return gate.Skipped(c, fmt.Sprintf("needs tcp %s: %s", endpoint, gate.Escape(err.Error()))), true
		}
		_ = ch.Close()
	}
	return gate.Result{}, false
}
