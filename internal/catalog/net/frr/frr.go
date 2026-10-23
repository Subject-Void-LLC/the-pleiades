// Package frr implements the net.frr Collection methods, which manage an
// FRRouting router through vtysh and FRR's own frr-reload.py over SSH, as
// the unprivileged account onboarding proved may use them
// (capability.FRRCapable). This file holds what every one of them shares.
package frr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// reloadScript is FRR's own reload tool, where FRR's packages put it.
const reloadScript = "/usr/lib/frr/frr-reload.py"

// configStart is the first line of a configuration file: everything
// show running-config prints before it ("Building configuration...",
// "Current configuration:") is not configuration, and vtysh refuses it as a
// file (Phase 127b's spikes).
const configStart = "frr version"

// session is one SSH connection to a router, for one method's run.
type session struct {
	conn *remoteexec.Conn
	fqcn string
}

// open connects to device for fqcn, with the task's credential.
func open(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (*session, error) {
	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return nil, err
	}
	return &session{conn: conn, fqcn: fqcn}, nil
}

// close closes the connection.
func (s *session) close() { _ = s.conn.Close() }

// run runs argv on the router and returns its standard output, failing on
// a non-zero exit with what the router said.
func (s *session) run(ctx context.Context, argv ...string) (string, error) {
	res, err := s.conn.Run(ctx, remoteexec.QuoteCommand(argv))
	if err != nil {
		return "", fmt.Errorf("%s: %s: %w", s.fqcn, argv[0], err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("%s: %s exited %d: %s", s.fqcn, argv[0], res.ExitCode, firstLine(res.Stderr+res.Stdout))
	}
	return res.Stdout, nil
}

// vtysh runs each command through vtysh, in order, in one invocation. A
// line vtysh answers with "% " (an unknown command, a refused value) fails
// the task, since vtysh can exit 0 after one.
func (s *session) vtysh(ctx context.Context, commands ...string) (string, error) {
	argv := []string{"vtysh"}
	for _, c := range commands {
		argv = append(argv, "-c", c)
	}
	out, err := s.run(ctx, argv...)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "% ") {
			return "", fmt.Errorf("%s: vtysh refused %q: %s", s.fqcn, strings.Join(commands, "; "), strings.TrimSpace(line))
		}
	}
	return out, nil
}

// vtyshJSON runs one show command ending in json and decodes it. An empty
// answer (a daemon that is not running) is an empty object.
func (s *session) vtyshJSON(ctx context.Context, command string) (map[string]any, error) {
	out, err := s.vtysh(ctx, command)
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return map[string]any{}, nil
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, fmt.Errorf("%s: %q did not answer JSON: %w", s.fqcn, command, err)
	}
	return v, nil
}

// runningConfig returns the running configuration from its first line on.
func (s *session) runningConfig(ctx context.Context) (string, error) {
	out, err := s.vtysh(ctx, "show running-config")
	if err != nil {
		return "", err
	}
	return configFrom(out), nil
}

// configFrom returns text from its "frr version" line on, or all of it
// when there is none.
func configFrom(text string) string {
	if i := strings.Index(text, configStart); i >= 0 {
		return text[i:]
	}
	return text
}

// writeTemp writes content to a new file under /tmp on the router and
// returns its path and a function that removes it.
func (s *session) writeTemp(ctx context.Context, content string) (string, func(), error) {
	res, err := s.conn.RunWithStdin(ctx, `set -e; f=$(mktemp /tmp/pleiades-frr.XXXXXX); cat > "$f"; printf '%s' "$f"`, strings.NewReader(content))
	if err != nil {
		return "", nil, fmt.Errorf("%s: writing the candidate: %w", s.fqcn, err)
	}
	path := strings.TrimSpace(res.Stdout)
	if res.ExitCode != 0 || !strings.HasPrefix(path, "/tmp/pleiades-frr.") {
		return "", nil, fmt.Errorf("%s: writing the candidate: exited %d: %s", s.fqcn, res.ExitCode, firstLine(res.Stderr))
	}
	cleanup := func() {
		_, _ = s.conn.Run(context.WithoutCancel(ctx), remoteexec.QuoteCommand([]string{"rm", "-f", "--", path}))
	}
	return path, cleanup, nil
}

// check refuses a configuration file vtysh cannot read whole (vtysh -m -f
// exits non-zero naming the line), which frr-reload would otherwise read
// as an empty configuration and delete everything for.
func (s *session) check(ctx context.Context, path string) error {
	res, err := s.conn.Run(ctx, remoteexec.QuoteCommand([]string{"vtysh", "-m", "-f", path}))
	if err != nil {
		return fmt.Errorf("%s: checking the candidate: %w", s.fqcn, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s: vtysh refuses the candidate, so nothing was sent: %s", s.fqcn, firstLine(res.Stdout+res.Stderr))
	}
	return nil
}

// diff returns the lines frr-reload would delete and add to make the
// running configuration the file at path, from frr-reload.py --test.
func (s *session) diff(ctx context.Context, path string) (deleted, added []string, err error) {
	out, err := s.run(ctx, "python3", reloadScript, "--test", "--stdout", path)
	if err != nil {
		return nil, nil, err
	}
	deleted, added = parseReloadTest(out)
	return deleted, added, nil
}

// runDir is FRR's run directory, where frr-reload writes its reload files.
const runDir = "/var/run/frr"

// canReload refuses, before anything is sent, an account that cannot write
// runDir: frr-reload applies its deletions first and fails on the
// additions, so a reload that cannot write there leaves the router in
// neither configuration (the clean-slate lab's G1 and G2).
func (s *session) canReload(ctx context.Context) error {
	res, err := s.conn.Run(ctx, remoteexec.QuoteCommand([]string{"test", "-w", runDir}))
	if err != nil {
		return fmt.Errorf("%s: checking %s: %w", s.fqcn, runDir, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s: the connecting account cannot write %s, where frr-reload writes its reload files, so a configuration cannot be applied; nothing was changed. Make it writable to the account's group, e.g. chgrp frrvty %s and chmod 2775 %s", s.fqcn, runDir, runDir, runDir)
	}
	return nil
}

// reload makes the running configuration the file at path. A failure names
// what frr-reload said, its secrets masked, not its first log line.
func (s *session) reload(ctx context.Context, path string) error {
	res, err := s.conn.Run(ctx, remoteexec.QuoteCommand([]string{"python3", reloadScript, "--reload", "--stdout", path}))
	if err != nil {
		return fmt.Errorf("%s: frr-reload: %w", s.fqcn, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s: frr-reload exited %d, and the router may already run part of the change (it deletes before it adds): %s", s.fqcn, res.ExitCode, reloadError(res.Stdout+"\n"+res.Stderr))
	}
	return nil
}

// reloadError is what frr-reload said that explains a failure: its last
// lines that are not INFO logging, masked, at most six.
func reloadError(out string) string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.Contains(line, " INFO: ") {
			continue
		}
		lines = append(lines, netcfg.Mask(t))
	}
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	if len(lines) == 0 {
		return "it printed nothing but logging"
	}
	return strings.Join(lines, " | ")
}

// parseReloadTest reads frr-reload.py --test's two sections, "Lines To
// Delete" and "Lines To Add", each a header, a rule of = signs, and its
// lines. The context lines frr-reload prints around a change ("interface
// eth1", "exit") are kept, so a line reads as it would in a file; INFO
// lines are logging and are not.
func parseReloadTest(out string) (deleted, added []string) {
	var into *[]string
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "Lines To Delete":
			into = &deleted
			continue
		case trimmed == "Lines To Add":
			into = &added
			continue
		case trimmed == "" || strings.Trim(trimmed, "=") == "" || strings.Contains(line, " INFO: ") || strings.Contains(line, " WARNING: "):
			continue
		}
		if into != nil && trimmed != "exit" {
			*into = append(*into, strings.TrimRight(line, " "))
		}
	}
	return deleted, added
}

// firstLine returns s's first non-empty line, trimmed.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}
