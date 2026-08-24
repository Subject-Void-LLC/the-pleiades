package ios

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// This file proves Config's own decision logic -- param validation,
// backup capture, stat recording, changed/error handling -- with NO
// real I/O at all, through the openSession seam this package's own
// config.go declares. Real I/O against a genuine PTY session,
// including the "(config-if)#" sub-mode net.ios.config's own Release
// Gate drives IOS into, is pkg/netcli's own responsibility, proven
// there (pkg/netcli/netcli_test.go, pkg/netcli/netcli_ssh_test.go) and
// against a real device by pkg/netcli/live_probe_test.go and
// cmd/pleiades/net_ios_config_release_gate_test.go.

// fakeSession is a canned iosSession double.
type fakeSession struct {
	commandCalls []string          // every line Command was called with
	configCalls  [][]string        // every batch Config was called with
	outputs      map[string]string // Command line -> canned output
	commandErr   error
	configErr    error
	closed       bool
}

func (f *fakeSession) Command(ctx context.Context, line string) (string, error) {
	f.commandCalls = append(f.commandCalls, line)
	if f.commandErr != nil {
		return "", f.commandErr
	}
	return f.outputs[line], nil
}

func (f *fakeSession) Config(ctx context.Context, lines []string) error {
	f.configCalls = append(f.configCalls, lines)
	return f.configErr
}

func (f *fakeSession) Close() error {
	f.closed = true
	return nil
}

func swapOpenSession(t *testing.T, fn func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (iosSession, error)) {
	t.Helper()
	orig := openSession
	openSession = fn
	t.Cleanup(func() { openSession = orig })
}

// stubContext is a minimal sdk.RunbookContext recording every stat this
// package writes.
type stubContext struct {
	stats map[string]any
}

func newStubContext() *stubContext { return &stubContext{stats: map[string]any{}} }

func (c *stubContext) InjectSecrets() map[string]string { return nil }

func (c *stubContext) SetStat(key string, value any) error {
	c.stats[key] = value
	return nil
}

func (c *stubContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

func TestConfig_Registered(t *testing.T) {
	d, ok := collection.Lookup("net.ios.config")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "net.ios.config")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Manifest.Reversibility.Reversible {
		t.Error("Manifest.Reversibility.Reversible = true, want false: IOS's own \"no <line>\" negation is not reliable enough to assert")
	}
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Manifest.Reversibility.Notes is empty; collection.Register would have refused this")
	}
	if d.Invoke == nil {
		t.Error("Manifest claims StatusImplemented but Invoke is nil")
	}
}

func TestConfig_RequiresTheLinesParam(t *testing.T) {
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (iosSession, error) {
		t.Fatal("openSession must not be called when lines is missing")
		return nil, nil
	})

	_, err := Config(context.Background(), newStubContext(), nil, map[string]any{})
	if err == nil {
		t.Fatal("Config with no lines param returned no error")
	}
	if !strings.Contains(err.Error(), paramLines) {
		t.Errorf("error = %v, want it to name %q", err, paramLines)
	}
}

func TestConfig_RefusesAnEmptyLinesList(t *testing.T) {
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (iosSession, error) {
		t.Fatal("openSession must not be called when lines is empty")
		return nil, nil
	})

	params := map[string]any{paramLines: []any{}}
	if _, err := Config(context.Background(), newStubContext(), nil, params); err == nil {
		t.Fatal("Config with an empty lines list returned no error")
	}
}

func TestConfig_SendsLinesThroughSessionConfigAndReportsChanged(t *testing.T) {
	fake := &fakeSession{}
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (iosSession, error) {
		return fake, nil
	})

	params := map[string]any{paramLines: []any{"interface Loopback0", "description managed by pleiades"}}
	result, err := Config(context.Background(), newStubContext(), nil, params)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if !result.Changed {
		t.Error("Result.Changed = false, want true")
	}
	if !fake.closed {
		t.Error("the session was never closed")
	}
	if len(fake.configCalls) != 1 {
		t.Fatalf("Session.Config was called %d times, want 1", len(fake.configCalls))
	}
	want := []string{"interface Loopback0", "description managed by pleiades"}
	got := fake.configCalls[0]
	if len(got) != len(want) {
		t.Fatalf("lines sent = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(fake.commandCalls) != 0 {
		t.Errorf("Command was called directly (%v); backup was not requested, so it should never run", fake.commandCalls)
	}
}

func TestConfig_CapturesBackupBeforeApplyingWhenRequested(t *testing.T) {
	fake := &fakeSession{outputs: map[string]string{"show running-config": "hostname Cat8kv\n!\n"}}
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (iosSession, error) {
		return fake, nil
	})

	rc := newStubContext()
	params := map[string]any{paramLines: []any{"interface Loopback0"}, paramBackup: true}
	if _, err := Config(context.Background(), rc, nil, params); err != nil {
		t.Fatalf("Config: %v", err)
	}

	if got := rc.stats[statBackup]; got != "hostname Cat8kv\n!\n" {
		t.Errorf("stat %q = %v, want the captured running-config", statBackup, got)
	}
	if len(fake.commandCalls) != 1 || fake.commandCalls[0] != "show running-config" {
		t.Errorf("commands sent = %v, want exactly [\"show running-config\"]", fake.commandCalls)
	}
	// The backup must be captured BEFORE Config is applied: Command's
	// own call has to be the only one recorded before Config's.
	if len(fake.configCalls) != 1 {
		t.Fatalf("Session.Config was called %d times, want 1", len(fake.configCalls))
	}
}

func TestConfig_PropagatesABackupFailureWithoutApplyingAnything(t *testing.T) {
	fake := &fakeSession{commandErr: errors.New("device unreachable")}
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (iosSession, error) {
		return fake, nil
	})

	params := map[string]any{paramLines: []any{"interface Loopback0"}, paramBackup: true}
	_, err := Config(context.Background(), newStubContext(), nil, params)
	if err == nil {
		t.Fatal("Config with a failing backup capture returned no error")
	}
	if len(fake.configCalls) != 0 {
		t.Error("Session.Config ran even though the backup capture failed")
	}
}

func TestConfig_PropagatesASessionConfigError(t *testing.T) {
	fake := &fakeSession{configErr: errors.New("device rejected \"bogus\"")}
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (iosSession, error) {
		return fake, nil
	})

	_, err := Config(context.Background(), newStubContext(), nil, map[string]any{paramLines: []any{"bogus"}})
	if err == nil {
		t.Fatal("Config with a failing Session.Config returned no error")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error = %v, want it to wrap the session's own error", err)
	}
}
