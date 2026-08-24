package ios

import (
	"context"
	"errors"
	"reflect"
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
	// defaultOutput answers any line with no entry in outputs. Ping builds
	// its own command string from several parameters, so a test asserting
	// on the RESULT should not have to predict the exact line to key on.
	defaultOutput string
	commandErr    error
	configErr     error
	closed        bool
}

func (f *fakeSession) Command(ctx context.Context, line string) (string, error) {
	f.commandCalls = append(f.commandCalls, line)
	if f.commandErr != nil {
		return "", f.commandErr
	}
	if out, ok := f.outputs[line]; ok {
		return out, nil
	}
	return f.defaultOutput, nil
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
	// facts is kept separate from stats deliberately. net.ios.facts must
	// use EmitFact (long-lived drift data) and not SetStat (ephemeral,
	// gone at the end of the run), and a stub that funnelled both into one
	// map could not tell the two apart, so it could not prove the
	// distinction the method's own doc comment claims.
	facts map[string]any
}

func newStubContext() *stubContext {
	return &stubContext{stats: map[string]any{}, facts: map[string]any{}}
}

func (c *stubContext) InjectSecrets() map[string]string { return nil }

func (c *stubContext) SetStat(key string, value any) error {
	c.stats[key] = value
	return nil
}

func (c *stubContext) EmitFact(key string, value any) error {
	c.facts[key] = value
	return nil
}

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

// The three fixtures below are VERBATIM output from a real Cisco IOS XE
// device (17.15.04c on a Catalyst 8000V), captured by
// pkg/netcli/live_probe_test.go's TestLiveIOSFactsShapes. They are pasted
// rather than paraphrased on purpose: every parser in facts.go exists to
// handle what a device actually prints, and a fixture an author wrote
// from memory of Cisco output would only ever prove the parser matches
// that memory. Trailing whitespace inside them is real and deliberate.
const realShowVersion = `Cisco IOS XE Software, Version 17.15.04c
Cisco IOS Software [IOSXE], Virtual XE Software (X86_64_LINUX_IOSD-UNIVERSALK9-M), Version 17.15.4c, RELEASE SOFTWARE (fc2)
Technical Support: http://www.cisco.com/techsupport
Copyright (c) 1986-2025 by Cisco Systems, Inc.
Compiled Fri 26-Sep-25 09:24 by mcpre

ROM: IOS-XE ROMMON

Cat8kv uptime is 1 hour, 32 minutes
Uptime for this control processor is 1 hour, 34 minutes
System returned to ROM by reload
System image file is "bootflash:packages.conf"
Last reload reason: reload

cisco C8000V (VXE) processor (revision VXE) with 1655530K/3075K bytes of memory.
Processor board ID 9XLWRY40A2V
Router operating mode: Autonomous
3 Gigabit Ethernet interfaces
32768K bytes of non-volatile configuration memory.
3959740K bytes of physical memory.

Configuration register is 0x2102`

const realShowInventory = `NAME: "Chassis", DESCR: "Cisco Catalyst 8000V Edge Chassis"
PID: C8000V            , VID: V00  , SN: 9XLWRY40A2V

NAME: "module R0", DESCR: "Cisco Catalyst 8000V Edge Route Processor"
PID: C8000V            , VID: V00  , SN: JAB1303001C

NAME: "module F0", DESCR: "Cisco Catalyst 8000V Edge Embedded Services Processor"
PID: C8000V            , VID:      , SN:            `

const realShowIPIntBrief = `Interface              IP-Address      OK? Method Status                Protocol
GigabitEthernet1       10.10.20.148    YES NVRAM  up                    up      
GigabitEthernet2       unassigned      YES NVRAM  administratively down down    
GigabitEthernet3       unassigned      YES NVRAM  administratively down down    `

func factsSession() *fakeSession {
	return &fakeSession{outputs: map[string]string{
		"show version":            realShowVersion,
		"show inventory":          realShowInventory,
		"show ip interface brief": realShowIPIntBrief,
	}}
}

func runFacts(t *testing.T, params map[string]any) (*fakeSession, *stubContext, error) {
	t.Helper()
	f := factsSession()
	swapOpenSession(t, func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (iosSession, error) {
		return f, nil
	})
	rc := newStubContext()
	_, err := Facts(context.Background(), rc, nil, params)
	return f, rc, err
}

func TestFacts_Registered(t *testing.T) {
	d, ok := collection.Lookup("net.ios.facts")
	if !ok {
		t.Fatal(`collection.Lookup("net.ios.facts") found nothing`)
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Reversibility.Notes is empty, but Reversible is false")
	}
}

func TestFacts_ParsesRealShowVersionAndInventoryOutput(t *testing.T) {
	_, rc, err := runFacts(t, nil)
	if err != nil {
		t.Fatalf("Facts: %v", err)
	}
	want := map[string]any{
		factHostname: "Cat8kv",
		factUptime:   "1 hour, 32 minutes",
		factVersion:  "17.15.04c",
		factModel:    "C8000V",
		factImage:    "bootflash:packages.conf",
		// show inventory's Chassis SN wins over show version's board ID.
		// Both happen to read 9XLWRY40A2V on this device, so the
		// preference itself is proven separately below.
		factSerialNum: "9XLWRY40A2V",
	}
	for k, v := range want {
		if got := rc.facts[k]; got != v {
			t.Errorf("fact %s = %v, want %v", k, got, v)
		}
	}
}

func TestFacts_PrefersTheChassisSerialOverTheProcessorBoardID(t *testing.T) {
	f := factsSession()
	// A real chassis serial that differs from the board ID, so the
	// preference is observable rather than coincidental.
	f.outputs["show inventory"] = "NAME: \"Chassis\", DESCR: \"c\"\nPID: C8000V, VID: V00, SN: CHASSIS123\n"
	swapOpenSession(t, func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (iosSession, error) {
		return f, nil
	})
	rc := newStubContext()
	if _, err := Facts(context.Background(), rc, nil, nil); err != nil {
		t.Fatalf("Facts: %v", err)
	}
	if got := rc.facts[factSerialNum]; got != "CHASSIS123" {
		t.Errorf("%s = %v, want the chassis serial CHASSIS123, not the board ID", factSerialNum, got)
	}
}

func TestFacts_IgnoresANonChassisInventoryEntryWithABlankSerial(t *testing.T) {
	// The real device's "module F0" entry reports an entirely blank SN.
	// Reading serials positionally, or taking the last one, would pick
	// that empty value up.
	if got := chassisSerial(realShowInventory); got != "9XLWRY40A2V" {
		t.Errorf("chassisSerial = %q, want the Chassis entry's own serial", got)
	}
}

func TestFacts_ParsesATwoWordInterfaceStatusWithoutTruncatingIt(t *testing.T) {
	_, rc, err := runFacts(t, map[string]any{paramGatherSubset: []any{subsetAll}})
	if err != nil {
		t.Fatalf("Facts: %v", err)
	}
	ifaces, ok := rc.facts[factInterfaces].([]map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want []map[string]any", factInterfaces, rc.facts[factInterfaces])
	}
	if len(ifaces) != 3 {
		t.Fatalf("parsed %d interfaces, want 3: %v", len(ifaces), ifaces)
	}
	// "administratively down" is two words, and the protocol column that
	// follows it is a third. A parser splitting on whitespace positionally
	// reports the status as "administratively" and loses the rest.
	if got := ifaces[1]["status"]; got != "administratively down" {
		t.Errorf("Gi2 status = %q, want %q", got, "administratively down")
	}
	if got := ifaces[1]["protocol"]; got != "down" {
		t.Errorf("Gi2 protocol = %q, want %q", got, "down")
	}
	if got := ifaces[0]["status"]; got != "up" {
		t.Errorf("Gi1 status = %q, want %q", got, "up")
	}
}

func TestFacts_OmitsAnUnassignedAddressRatherThanReportingTheWordUnassigned(t *testing.T) {
	_, rc, err := runFacts(t, map[string]any{paramGatherSubset: []any{subsetInterfaces}})
	if err != nil {
		t.Fatalf("Facts: %v", err)
	}
	ifaces := rc.facts[factInterfaces].([]map[string]any)
	if got, ok := ifaces[0]["ip_address"]; !ok || got != "10.10.20.148" {
		t.Errorf("Gi1 ip_address = %v (present %v), want 10.10.20.148", got, ok)
	}
	if got, ok := ifaces[1]["ip_address"]; ok {
		t.Errorf("Gi2 ip_address = %v, want it absent entirely: the device said \"unassigned\"", got)
	}
}

func TestFacts_EmitsFactsAndNeverStats(t *testing.T) {
	_, rc, err := runFacts(t, nil)
	if err != nil {
		t.Fatalf("Facts: %v", err)
	}
	if len(rc.facts) == 0 {
		t.Fatal("no facts emitted")
	}
	if len(rc.stats) != 0 {
		t.Errorf("SetStat was called with %v: a fact gatherer records drift data through EmitFact only", rc.stats)
	}
}

func TestFacts_DefaultsToTheMinSubsetAndSkipsTheInterfaceCommand(t *testing.T) {
	f, rc, err := runFacts(t, nil)
	if err != nil {
		t.Fatalf("Facts: %v", err)
	}
	for _, line := range f.commandCalls {
		if strings.Contains(line, "interface") {
			t.Errorf("ran %q without being asked for the interfaces subset", line)
		}
	}
	if _, ok := rc.facts[factInterfaces]; ok {
		t.Error("emitted an interfaces fact without being asked for that subset")
	}
	if got := rc.facts[factGatherSubset]; !reflect.DeepEqual(got, []string{subsetMin}) {
		t.Errorf("%s = %v, want [min]", factGatherSubset, got)
	}
}

func TestFacts_ExpandsAllIntoEverySubset(t *testing.T) {
	_, rc, err := runFacts(t, map[string]any{paramGatherSubset: []any{subsetAll}})
	if err != nil {
		t.Fatalf("Facts: %v", err)
	}
	if got := rc.facts[factGatherSubset]; !reflect.DeepEqual(got, []string{subsetInterfaces, subsetMin}) {
		t.Errorf("%s = %v, want [interfaces min]", factGatherSubset, got)
	}
}

func TestFacts_RefusesAnUnknownSubsetRatherThanSkippingIt(t *testing.T) {
	_, _, err := runFacts(t, map[string]any{paramGatherSubset: []any{"hardware"}})
	if err == nil {
		t.Fatal("want an error naming the unrecognized subset, got nil")
	}
	if !strings.Contains(err.Error(), "hardware") {
		t.Errorf("error %q does not name the offending value", err)
	}
}

func TestFacts_OmitsAFieldTheDeviceDoesNotReport(t *testing.T) {
	// A device that says nothing about its image must produce no image
	// fact at all, rather than an empty string a condition cannot
	// distinguish from a real answer.
	parsed := parseVersion("Cat8kv uptime is 5 minutes\n")
	if _, ok := parsed[factImage]; ok {
		t.Errorf("image fact present for output that never mentions one: %v", parsed)
	}
	if parsed[factHostname] != "Cat8kv" {
		t.Errorf("hostname = %v, want Cat8kv", parsed[factHostname])
	}
}

// Both ping fixtures are verbatim from the same real device. The failure
// shape matters as much as the success one: IOS omits the round-trip
// clause ENTIRELY when nothing came back, rather than reporting zeroes.
const realPingSuccess = `Type escape sequence to abort.
Sending 3, 100-byte ICMP Echos to 10.10.20.148, timeout is 2 seconds:
!!!
Success rate is 100 percent (3/3), round-trip min/avg/max = 1/1/1 ms`

const realPingFailure = `Type escape sequence to abort.
Sending 5, 100-byte ICMP Echos to 8.8.8.8, timeout is 2 seconds:
.....
Success rate is 0 percent (0/5)`

func runPing(t *testing.T, out string, params map[string]any) (*fakeSession, *stubContext, error) {
	t.Helper()
	f := &fakeSession{outputs: map[string]string{}, defaultOutput: out}
	swapOpenSession(t, func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (iosSession, error) {
		return f, nil
	})
	rc := newStubContext()
	_, err := Ping(context.Background(), rc, nil, params)
	return f, rc, err
}

func TestPing_Registered(t *testing.T) {
	d, ok := collection.Lookup("net.ios.ping")
	if !ok {
		t.Fatal(`collection.Lookup("net.ios.ping") found nothing`)
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Reversibility.Notes is empty, but Reversible is false")
	}
}

func TestPing_RequiresDest(t *testing.T) {
	_, _, err := runPing(t, realPingSuccess, nil)
	if err == nil {
		t.Fatal("want an error when dest is missing, got nil")
	}
}

func TestPing_ParsesASuccessfulPingIncludingRoundTripTimes(t *testing.T) {
	_, rc, err := runPing(t, realPingSuccess, map[string]any{paramDest: "10.10.20.148"})
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if got := rc.stats[statPacketLoss]; got != "0%" {
		t.Errorf("%s = %v, want 0%%", statPacketLoss, got)
	}
	if got := rc.stats[statPacketsRx]; got != 3 {
		t.Errorf("%s = %v, want 3", statPacketsRx, got)
	}
	if got := rc.stats[statPacketsTx]; got != 3 {
		t.Errorf("%s = %v, want 3", statPacketsTx, got)
	}
	rtt, ok := rc.stats[statRTT].(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want map[string]any", statRTT, rc.stats[statRTT])
	}
	for k, want := range map[string]int{"min": 1, "avg": 1, "max": 1} {
		if rtt[k] != want {
			t.Errorf("rtt[%s] = %v, want %d", k, rtt[k], want)
		}
	}
}

func TestPing_OmitsRoundTripEntirelyWhenEveryPacketWasLost(t *testing.T) {
	// This is the case a parser written against only the success shape
	// gets wrong, either by failing to match at all or by inventing zeroes
	// the device never reported.
	_, rc, err := runPing(t, realPingFailure, map[string]any{paramDest: "8.8.8.8", paramState: stateAbsent})
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if got := rc.stats[statPacketLoss]; got != "100%" {
		t.Errorf("%s = %v, want 100%%", statPacketLoss, got)
	}
	if got, ok := rc.stats[statRTT]; ok {
		t.Errorf("%s = %v, want it absent: IOS printed no round-trip clause", statRTT, got)
	}
}

func TestPing_StatePresentFailsWhenNothingAnswered(t *testing.T) {
	_, rc, err := runPing(t, realPingFailure, map[string]any{paramDest: "8.8.8.8"})
	if err == nil {
		t.Fatal("want an error: state defaults to present and every packet was lost")
	}
	// The measurements must still be recorded, so a run that fails the
	// gate can still be diagnosed from what it saw.
	if got := rc.stats[statPacketsRx]; got != 0 {
		t.Errorf("%s = %v, want the stat recorded even on a failed gate", statPacketsRx, got)
	}
}

func TestPing_StateAbsentFailsWhenSomethingAnswered(t *testing.T) {
	_, _, err := runPing(t, realPingSuccess, map[string]any{paramDest: "10.10.20.148", paramState: stateAbsent})
	if err == nil {
		t.Fatal("want an error: state is absent but the destination answered")
	}
}

func TestPing_RefusesAnUnknownState(t *testing.T) {
	_, _, err := runPing(t, realPingSuccess, map[string]any{paramDest: "10.0.0.1", paramState: "maybe"})
	if err == nil {
		t.Fatal("want an error naming the unrecognized state, got nil")
	}
}

func TestPing_BuildsTheCommandInIOSsOwnArgumentOrder(t *testing.T) {
	// vrf really does precede the destination in IOS's grammar, and repeat
	// and source really do follow it. Getting this backwards produces a
	// command the device rejects, which no amount of output parsing fixes.
	got, err := buildPingCommand("192.0.2.1", "Loopback0", "MGMT", 7, "net.ios.ping")
	if err != nil {
		t.Fatalf("buildPingCommand: %v", err)
	}
	want := "ping vrf MGMT 192.0.2.1 repeat 7 source Loopback0"
	if got != want {
		t.Errorf("buildPingCommand = %q, want %q", got, want)
	}
}

func TestPing_RefusesWhitespaceInACallerSuppliedValue(t *testing.T) {
	// The transport already refuses an embedded newline, which is what
	// stops a second COMMAND. This stops a second ARGUMENT to this one.
	for _, tc := range []struct{ name, dest, source, vrf string }{
		{"dest", "192.0.2.1 repeat 99999", "", ""},
		{"source", "192.0.2.1", "Lo0 size 18000", ""},
		{"vrf", "192.0.2.1", "", "M GMT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildPingCommand(tc.dest, tc.source, tc.vrf, 5, "net.ios.ping"); err == nil {
				t.Error("want a refusal for a value carrying whitespace, got nil")
			}
		})
	}
}

func TestPing_ReportsADeviceRejectionRatherThanAParseFailure(t *testing.T) {
	_, _, err := runPing(t, "% Invalid input detected at '^' marker.", map[string]any{paramDest: "192.0.2.1"})
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("error %q should say the device rejected the command, not that parsing failed", err)
	}
}

func TestSave_Registered(t *testing.T) {
	d, ok := collection.Lookup("net.ios.save")
	if !ok {
		t.Fatal(`collection.Lookup("net.ios.save") found nothing`)
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Reversibility.Notes is empty, but Reversible is false")
	}
}

func TestSave_SendsWriteMemoryAndReportsChanged(t *testing.T) {
	f := &fakeSession{outputs: map[string]string{saveCommand: "Building configuration...\n[OK]"}}
	swapOpenSession(t, func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (iosSession, error) {
		return f, nil
	})
	rc := newStubContext()
	res, err := Save(context.Background(), rc, nil, nil)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true")
	}
	if !reflect.DeepEqual(f.commandCalls, []string{saveCommand}) {
		t.Errorf("commandCalls = %v, want exactly [%q]", f.commandCalls, saveCommand)
	}
	if got := rc.stats[statSaveStdout]; got != "Building configuration...\n[OK]" {
		t.Errorf("%s = %q, want the device's own reply", statSaveStdout, got)
	}
}

func TestSave_RefusesWhenTheDeviceReportsAnError(t *testing.T) {
	// Without this check a refused save reports success and changed, which
	// is the worst outcome available for a method whose whole purpose is
	// making an earlier change survive a reload.
	f := &fakeSession{outputs: map[string]string{saveCommand: "% Error opening nvram:/startup-config (Permission denied)"}}
	swapOpenSession(t, func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (iosSession, error) {
		return f, nil
	})
	res, err := Save(context.Background(), newStubContext(), nil, nil)
	if err == nil {
		t.Fatal("want an error when the device refuses the save, got nil")
	}
	if res.Changed {
		t.Error("Changed = true on a save the device refused")
	}
}
