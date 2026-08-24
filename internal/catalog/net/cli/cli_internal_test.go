package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// This file proves Command and Config's own decision logic -- param
// validation, stat recording, changed/error handling -- with NO real
// I/O at all, through the openSession seam this package's own cli.go
// declares (mirroring internal/catalog/svc/windows's statusFunc/
// startFunc seam). Real I/O against a genuine PTY session is
// pkg/netcli's own responsibility, proven there
// (pkg/netcli/netcli_test.go, pkg/netcli/netcli_ssh_test.go) and against
// a real device by pkg/netcli/live_probe_test.go and
// cmd/pleiades/net_ios_config_release_gate_test.go.

// fakeSession is a canned cliSession double.
type fakeSession struct {
	lines   []string          // every line Command was called with, in order
	outputs map[string]string // line -> the canned output to return
	err     error             // when set, every Command call returns this instead
	closed  bool

	// deadlines records whether each Command call carried a ctx
	// deadline at all, so TestRunCommand_BoundsTheContext can prove
	// runCommand actually applies one rather than trusting the caller's
	// own ctx.
	deadlines []bool
}

func (f *fakeSession) Command(ctx context.Context, line string) (string, error) {
	f.lines = append(f.lines, line)
	_, hasDeadline := ctx.Deadline()
	f.deadlines = append(f.deadlines, hasDeadline)
	if f.err != nil {
		return "", f.err
	}
	return f.outputs[line], nil
}

func (f *fakeSession) Close() error {
	f.closed = true
	return nil
}

// swapOpenSession replaces this package's openSession seam for the
// remainder of the calling test, restoring the real one afterward.
func swapOpenSession(t *testing.T, fn func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (cliSession, error)) {
	t.Helper()
	orig := openSession
	openSession = fn
	t.Cleanup(func() { openSession = orig })
}

// stubContext is a minimal sdk.RunbookContext recording every stat this
// package writes, so a test can read back exactly what a real runbook's
// register: would see.
type stubContext struct {
	stats   map[string]any
	statErr error
}

func newStubContext() *stubContext { return &stubContext{stats: map[string]any{}} }

func (c *stubContext) InjectSecrets() map[string]string { return nil }

func (c *stubContext) SetStat(key string, value any) error {
	if c.statErr != nil {
		return c.statErr
	}
	c.stats[key] = value
	return nil
}

func (c *stubContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

func TestCommand_Registered(t *testing.T) {
	d, ok := collection.Lookup("net.cli.command")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "net.cli.command")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Manifest.Reversibility.Reversible {
		t.Error("Manifest.Reversibility.Reversible = true, want false: a CLI line's effect cannot be inspected")
	}
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Manifest.Reversibility.Notes is empty; collection.Register would have refused this")
	}
	if d.Invoke == nil {
		t.Error("Manifest claims StatusImplemented but Invoke is nil")
	}
}

func TestConfig_Registered(t *testing.T) {
	d, ok := collection.Lookup("net.cli.config")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "net.cli.config")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Manifest claims StatusImplemented but Invoke is nil")
	}
}

func TestCommand_RequiresTheCommandParam(t *testing.T) {
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (cliSession, error) {
		t.Fatal("openSession must not be called when the command param is missing")
		return nil, nil
	})

	_, err := Command(context.Background(), newStubContext(), nil, map[string]any{})
	if err == nil {
		t.Fatal("Command with no command param returned no error")
	}
	if !strings.Contains(err.Error(), paramCommand) {
		t.Errorf("error = %v, want it to name %q", err, paramCommand)
	}
}

func TestCommand_RecordsStdoutAndReportsChanged(t *testing.T) {
	fake := &fakeSession{outputs: map[string]string{"show version": "Cisco IOS XE Software, Version 17.15.04c"}}
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (cliSession, error) {
		return fake, nil
	})

	rc := newStubContext()
	result, err := Command(context.Background(), rc, nil, map[string]any{paramCommand: "show version"})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if !result.Changed {
		t.Error("Result.Changed = false, want true: reaching the device without error is this method's own definition of changed")
	}
	if !fake.closed {
		t.Error("the session was never closed")
	}
	if got := rc.stats[statStdout]; got != "Cisco IOS XE Software, Version 17.15.04c" {
		t.Errorf("stat %q = %v, want the session's own output", statStdout, got)
	}
	if len(fake.lines) != 1 || fake.lines[0] != "show version" {
		t.Errorf("commands sent = %v, want exactly [\"show version\"]", fake.lines)
	}
}

func TestCommand_PropagatesASessionError(t *testing.T) {
	fake := &fakeSession{err: errors.New("device unreachable")}
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (cliSession, error) {
		return fake, nil
	})

	_, err := Command(context.Background(), newStubContext(), nil, map[string]any{paramCommand: "show version"})
	if err == nil {
		t.Fatal("Command with a failing session returned no error")
	}
	if !strings.Contains(err.Error(), "device unreachable") {
		t.Errorf("error = %v, want it to wrap the session's own error", err)
	}
}

func TestConfig_RequiresTheConfigParam(t *testing.T) {
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (cliSession, error) {
		t.Fatal("openSession must not be called when the config param is missing")
		return nil, nil
	})

	_, err := Config(context.Background(), newStubContext(), nil, map[string]any{})
	if err == nil {
		t.Fatal("Config with no config param returned no error")
	}
}

func TestConfig_SplitsIntoNonBlankLinesAndSendsEachThroughCommand(t *testing.T) {
	fake := &fakeSession{outputs: map[string]string{}}
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (cliSession, error) {
		return fake, nil
	})

	params := map[string]any{paramConfig: "configure terminal\n\nbanner motd ^Chello^C\n  \nend\n"}
	result, err := Config(context.Background(), newStubContext(), nil, params)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if !result.Changed {
		t.Error("Result.Changed = false, want true")
	}

	want := []string{"configure terminal", "banner motd ^Chello^C", "end"}
	if len(fake.lines) != len(want) {
		t.Fatalf("commands sent = %v, want %v (blank lines must be skipped)", fake.lines, want)
	}
	for i, line := range want {
		if fake.lines[i] != line {
			t.Errorf("command[%d] = %q, want %q", i, fake.lines[i], line)
		}
	}
}

func TestConfig_RefusesAllBlankConfig(t *testing.T) {
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (cliSession, error) {
		t.Fatal("openSession must not be called when config has no non-blank lines")
		return nil, nil
	})

	_, err := Config(context.Background(), newStubContext(), nil, map[string]any{paramConfig: "\n  \n\n"})
	if err == nil {
		t.Fatal("Config with only blank lines returned no error")
	}
}

func TestConfig_AbortsOnTheFirstFailingLine(t *testing.T) {
	fake := &fakeSession{err: errors.New("% Invalid input detected")}
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (cliSession, error) {
		return fake, nil
	})

	_, err := Config(context.Background(), newStubContext(), nil, map[string]any{paramConfig: "bogus line\nsecond line\n"})
	if err == nil {
		t.Fatal("Config with a failing line returned no error")
	}
	if len(fake.lines) != 1 {
		t.Errorf("commands sent = %v, want the batch to stop after the first failure", fake.lines)
	}
}

func TestCliPrompt_ReturnsEmptyForADeviceWithNoNetworkCLICapability(t *testing.T) {
	if got := cliPrompt(nil); got != "" {
		t.Errorf("cliPrompt(nil) = %q, want empty", got)
	}
}

// TestRunCommand_BoundsTheContextEvenWhenTheCallerGivesNone is the
// regression test for a real hang found by running net.cli.command
// against a real device: "show version" against a device with default
// terminal settings paused on a pager prompt this method has no way to
// answer, and nothing bounded the wait, because cmd/pleiades's own run
// path calls this with context.Background(). runCommand's own doc
// comment has the full story; this proves the fix rather than trusting
// the comment.
func TestRunCommand_BoundsTheContextEvenWhenTheCallerGivesNone(t *testing.T) {
	fake := &fakeSession{outputs: map[string]string{"show version": "ok"}}

	if _, err := runCommand(context.Background(), fake, "show version"); err != nil {
		t.Fatalf("runCommand: %v", err)
	}

	if len(fake.deadlines) != 1 || !fake.deadlines[0] {
		t.Fatal("Session.Command was called with no ctx deadline at all, even though the caller's own context.Background() carried none: a pager pause this Dialect cannot answer would hang forever")
	}
}
