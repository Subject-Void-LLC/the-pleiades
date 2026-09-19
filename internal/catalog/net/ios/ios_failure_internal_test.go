// Package ios: what each method does when a parameter is wrong, the
// session cannot be opened, the device refuses a command, or the answer
// cannot be recorded.
//
// These are the paths a real device reaches on a bad day, and the ones a
// method most easily gets wrong: reporting success for work that never
// happened. Like the rest of this package's tests they run through the
// openSession seam, with no real I/O.
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

// errNoSession is a session that could not be opened at all: the device
// is unreachable, or refused this run's credential.
var errNoSession = errors.New("dial tcp 10.0.0.1:22: connect: connection refused")

// withSession points the openSession seam at session, and records the
// fact that it was asked for one.
func withSession(t *testing.T, session *fakeSession) *bool {
	t.Helper()
	asked := false
	swapOpenSession(t, func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (iosSession, error) {
		asked = true
		return session, nil
	})
	return &asked
}

// withNoSession makes every attempt to open a session fail.
func withNoSession(t *testing.T) {
	t.Helper()
	swapOpenSession(t, func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (iosSession, error) {
		return nil, errNoSession
	})
}

// TestMethods_RefuseAParameterTheyCannotRead covers each method's own
// parameter validation, which runs before a session is opened: a value of
// the wrong type, or outside what the parameter accepts, is refused
// naming the method, and the device is never dialed. A method that opened
// the session first would reach a real device to tell an operator they
// made a typo.
func TestMethods_RefuseAParameterTheyCannotRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method collection.Method
		params map[string]any
		says   string
	}{
		{name: "config, lines that are not a list", method: Config, params: map[string]any{"lines": 42}, says: "net.ios.config"},
		{name: "config, a backup flag that is not a bool", method: Config, params: map[string]any{"lines": []any{"hostname r1"}, "backup": "maybe"}, says: "net.ios.config"},
		{name: "facts, a subset list that is not a list", method: Facts, params: map[string]any{"gather_subset": 7}, says: "net.ios.facts"},
		{name: "ping, a count that is not a number", method: Ping, params: map[string]any{"dest": "10.0.0.2", "count": "many"}, says: "net.ios.ping"},
		{name: "ping, a count below one", method: Ping, params: map[string]any{"dest": "10.0.0.2", "count": 0}, says: "count must be at least 1"},
		{name: "ping, a state it does not have", method: Ping, params: map[string]any{"dest": "10.0.0.2", "state": "maybe"}, says: `state must be "present" or "absent"`},
		// Whitespace in any of the three values would put a second IOS
		// command on the line this method builds.
		{name: "ping, a destination carrying a second command", method: Ping, params: map[string]any{"dest": "10.0.0.2 ; reload"}, says: "must not contain whitespace"},
		{name: "ping, a source carrying one", method: Ping, params: map[string]any{"dest": "10.0.0.2", "source": "Loopback0 ; reload"}, says: "must not contain whitespace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked := withSession(t, &fakeSession{})
			_, err := tc.method(context.Background(), newStubContext(), nil, tc.params)
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("err = %v, want one containing %q", err, tc.says)
			}
			if *asked {
				t.Error("the device was dialed to report a parameter this method could have read first")
			}
		})
	}
}

// TestMethods_ReportASessionThatCannotOpen covers the answer every method
// gives when the device cannot be reached or refuses the credential: the
// failure travels out as it came, so an operator reads the dial's own
// reason rather than a method's paraphrase of it.
func TestMethods_ReportASessionThatCannotOpen(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method collection.Method
		params map[string]any
	}{
		{name: "config", method: Config, params: map[string]any{"lines": []any{"hostname r1"}}},
		{name: "facts", method: Facts, params: map[string]any{}},
		{name: "ping", method: Ping, params: map[string]any{"dest": "10.0.0.2"}},
		{name: "save", method: Save, params: map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withNoSession(t)
			if _, err := tc.method(context.Background(), newStubContext(), nil, tc.params); !errors.Is(err, errNoSession) {
				t.Errorf("err = %v, want the dial's own failure", err)
			}
		})
	}
}

// TestConfig_ABackupThatCannotBeRecordedStopsTheChange is the order that
// matters most in this method: the backup exists so an operator can put
// the device back by hand, so a backup the run could not record must stop
// it before any configuration line is sent. Recording it afterwards would
// leave a changed device whose prior configuration nobody has.
func TestConfig_ABackupThatCannotBeRecordedStopsTheChange(t *testing.T) {
	session := &fakeSession{defaultOutput: "hostname r1\n"}
	withSession(t, session)
	rc := newStubContext()
	rc.failStat = statBackup

	_, err := Config(context.Background(), rc, nil, map[string]any{"lines": []any{"hostname r2"}, "backup": true})
	if !errors.Is(err, errRecording) {
		t.Fatalf("err = %v, want the recording failure", err)
	}
	if len(session.configCalls) != 0 {
		t.Errorf("the device was configured anyway: %v", session.configCalls)
	}
}

// TestFacts_ReportWhichCommandTheDeviceCouldNotAnswer covers the three
// reads net.ios.facts makes: each failure names the command it came from,
// since "show inventory" failing on a device that answered "show version"
// is a different problem from the session dying.
func TestFacts_ReportWhichCommandTheDeviceCouldNotAnswer(t *testing.T) {
	refused := errors.New("% Invalid input detected")
	for _, command := range []string{"show version", "show inventory", "show ip interface brief"} {
		t.Run(command, func(t *testing.T) {
			withSession(t, &fakeSession{commandErrs: map[string]error{command: refused}})
			_, err := Facts(context.Background(), newStubContext(), nil, map[string]any{"gather_subset": []any{"all"}})
			if err == nil || !strings.Contains(err.Error(), command) || !errors.Is(err, refused) {
				t.Errorf("err = %v, want it to name %q and carry the device's own refusal", err, command)
			}
		})
	}
}

// TestFacts_AFactThatCannotBeEmittedFailsTheTask covers the last step:
// facts are the point of this method, so one that could not be stored
// fails the task rather than reporting a gather that recorded nothing.
func TestFacts_AFactThatCannotBeEmittedFailsTheTask(t *testing.T) {
	withSession(t, factsSession())
	rc := newStubContext()
	rc.failFact = factGatherSubset

	if _, err := Facts(context.Background(), rc, nil, nil); !errors.Is(err, errRecording) {
		t.Errorf("err = %v, want the recording failure", err)
	}
}

// TestPing_WhatItDoesWithAReplyItCannotUse covers the two answers that are
// not a ping result: a command the device refused, which prints its own
// "%" line and no summary, and a reply with no success-rate line at all.
// Both fail, because reporting zero loss for a ping that never ran would
// be read as the device being reachable.
func TestPing_WhatItDoesWithAReplyItCannotUse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		says   string
	}{
		{name: "the device refused the command", output: "% Unrecognized host or address, or protocol not running.\n", says: "device rejected"},
		{name: "a reply with no summary", output: "Type escape sequence to abort.\n", says: "could not find a success-rate line"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withSession(t, &fakeSession{defaultOutput: tc.output})
			rc := newStubContext()
			_, err := Ping(context.Background(), rc, nil, map[string]any{"dest": "10.0.0.2"})
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("err = %v, want one containing %q", err, tc.says)
			}
			if len(rc.stats) != 0 {
				t.Errorf("a ping with no result recorded %v", rc.stats)
			}
		})
	}
}

// TestPing_AResultThatCannotBeRecordedFails covers both recording steps:
// the packet counts every ping records, and the round-trip times only a
// ping that got something back has. Either failing fails the task, since
// a later task reading a stat that was never written would compare
// against nothing.
func TestPing_AResultThatCannotBeRecordedFails(t *testing.T) {
	const replied = "Success rate is 100 percent (5/5), round-trip min/avg/max = 1/2/9 ms"
	for _, key := range []string{statPacketLoss, statRTT} {
		t.Run(key, func(t *testing.T) {
			withSession(t, &fakeSession{defaultOutput: replied})
			rc := newStubContext()
			rc.failStat = key
			if _, err := Ping(context.Background(), rc, nil, map[string]any{"dest": "10.0.0.2"}); !errors.Is(err, errRecording) {
				t.Errorf("err = %v, want the recording failure", err)
			}
		})
	}
}

// TestSave_WhatItDoesWhenTheDeviceOrTheRecordFails covers the two ways a
// save that reached the device still fails: the command itself failing,
// and the device's reply not being recordable. A save reports a change
// only when the device took it, so neither may pass for success.
func TestSave_WhatItDoesWhenTheDeviceOrTheRecordFails(t *testing.T) {
	refused := errors.New("session closed")
	withSession(t, &fakeSession{commandErr: refused})
	if _, err := Save(context.Background(), newStubContext(), nil, nil); !errors.Is(err, refused) {
		t.Errorf("a failed save = %v, want the device's own failure", err)
	}

	withSession(t, &fakeSession{defaultOutput: "Building configuration...\n[OK]\n"})
	rc := newStubContext()
	rc.failStat = statSaveStdout
	if _, err := Save(context.Background(), rc, nil, nil); !errors.Is(err, errRecording) {
		t.Errorf("an unrecordable reply = %v, want the recording failure", err)
	}
}

// TestParsers_ReadWhatARealDevicePrints covers the two parsers against
// the shapes real output has and a hand-written fixture usually does not:
// "show inventory" lists several entries, only one of which is the
// chassis, and one of the others can carry a blank serial; "show ip
// interface brief" has a header line and can carry a status of two words.
func TestParsers_ReadWhatARealDevicePrints(t *testing.T) {
	const inventory = `NAME: "Power Supply 1", DESCR: "Power Supply"
PID: PWR-C1-350WAC     , VID: V02, SN:

NAME: "Chassis", DESCR: "Cisco Catalyst 9300 Series"
PID: C9300-24T         , VID: V02, SN: FCW2140L0GF
`
	if got := chassisSerial(inventory); got != "FCW2140L0GF" {
		t.Errorf("chassisSerial = %q, want the chassis entry's serial, not the power supply's blank one", got)
	}

	const brief = `Interface              IP-Address      OK? Method Status                Protocol
GigabitEthernet0/0     10.0.0.1        YES NVRAM  up                    up
GigabitEthernet0/1     unassigned      YES NVRAM  administratively down down
short line
`
	got := parseInterfaces(brief)
	if len(got) != 2 {
		t.Fatalf("parseInterfaces returned %d interfaces, want 2 (the header and the short line are not interfaces): %v", len(got), got)
	}
	if got[1]["status"] != "administratively down" || got[1]["protocol"] != "down" {
		t.Errorf("a two-word status was read as %v", got[1])
	}
}
