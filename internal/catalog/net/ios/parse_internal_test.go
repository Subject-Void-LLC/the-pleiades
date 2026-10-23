// Package ios: tests of net.ios.parse's decisions (refusals before the
// device, masking of configuration differences, an empty parse as a result)
// through the package's session double.
package ios

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// parseBrief is real "show ip interface brief" output from a Catalyst
// 8000V, as genieparser's own fixture holds it.
const parseBrief = "Interface              IP-Address      OK? Method Status                Protocol\n" +
	"GigabitEthernet1       10.10.20.48     YES manual up                    up\n" +
	"Loopback0              unassigned      YES unset  administratively down down\n"

// parseDiffWithSecrets is show archive config differences output whose
// changed lines hold an enable secret, a local user's secret, an SNMP
// community and a TACACS key.
const parseDiffWithSecrets = "!Contextual Config Diffs:\n" +
	"+enable secret 9 $9$hunter2enablehash\n" +
	"-username admin privilege 15 secret 9 $9$oldadminhash\n" +
	"+snmp-server community s3cr3tcommunity RO\n" +
	"tacacs server T1\n" +
	" +key 7 0822455D0A16\n" +
	"+hostname R1\n"

// parseSecrets are the secret values parseDiffWithSecrets holds.
var parseSecrets = []string{"$9$hunter2enablehash", "$9$oldadminhash", "s3cr3tcommunity", "0822455D0A16"}

// openWith swaps the session seam for one answering with session, and
// reports whether it was opened.
func openWith(t *testing.T, session *fakeSession) *bool {
	t.Helper()
	opened := false
	swapOpenSession(t, func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (iosSession, error) {
		opened = true
		return session, nil
	})
	return &opened
}

// TestParse_RecordsTheParse proves a covered command's output is recorded
// as stdout and parsed into genieparser's shape, unchanged.
func TestParse_RecordsTheParse(t *testing.T) {
	session := &fakeSession{outputs: map[string]string{"show ip interface brief": parseBrief}}
	openWith(t, session)
	rc := newStubContext()
	res, err := Parse(context.Background(), rc, nil, map[string]any{paramCommand: "show ip interface brief"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Error("a show command reported changed")
	}
	if rc.stats[statParser] != "ShowIpInterfaceBrief" || rc.stats[statCommandStdout] != parseBrief {
		t.Errorf("stats = %v", rc.stats)
	}
	parsed, _ := rc.stats[statParsed].(map[string]any)
	lo, _ := parsed["interface"].(map[string]any)["Loopback0"].(map[string]any)
	if lo["status"] != "administratively down" || lo["ip_address"] != "unassigned" {
		t.Errorf("parsed = %v", parsed)
	}
	if !session.closed || len(session.commandCalls) != 1 {
		t.Errorf("closed = %v, calls = %v", session.closed, session.commandCalls)
	}
}

// TestParse_MasksArchiveDifferences proves no secret in a configuration
// difference reaches any stat, while the lines around it still parse.
func TestParse_MasksArchiveDifferences(t *testing.T) {
	openWith(t, &fakeSession{defaultOutput: parseDiffWithSecrets})
	rc := newStubContext()
	if _, err := Parse(context.Background(), rc, nil, map[string]any{paramCommand: "show archive config differences"}); err != nil {
		t.Fatal(err)
	}
	all := fmt.Sprint(rc.stats)
	for _, secret := range parseSecrets {
		if strings.Contains(all, secret) {
			t.Errorf("secret %q reached a stat: %s", secret, all)
		}
	}
	diff, _ := rc.stats[statParsed].(map[string]any)["diff"].([]any)
	want := []string{"+enable secret 9 " + netcfg.Placeholder("$9$hunter2enablehash"), "+hostname R1"}
	if len(diff) != 5 || diff[0] != want[0] || diff[4] != want[1] {
		t.Errorf("diff = %q", diff)
	}
	if !strings.Contains(rc.stats[statCommandStdout].(string), " +key 7 "+netcfg.Placeholder("0822455D0A16")) {
		t.Errorf("indented marker not kept: %q", rc.stats[statCommandStdout])
	}
}

// TestParse_Refusals covers each way Parse fails, and that a command no
// parser covers is refused before a session opens.
func TestParse_Refusals(t *testing.T) {
	tests := []struct {
		name     string
		params   map[string]any
		session  *fakeSession
		failStat string
		opens    bool
		want     string
	}{
		{name: "no command", params: map[string]any{}, session: &fakeSession{}, want: "command"},
		{name: "uncovered", params: map[string]any{paramCommand: "show running-config"}, session: &fakeSession{}, want: "show ip route vrf {vrf}"},
		{name: "abbreviated", params: map[string]any{paramCommand: "sh ip int br"}, session: &fakeSession{}, want: "no parser covers"},
		{name: "rejected", params: map[string]any{paramCommand: "show vlan"}, opens: true,
			session: &fakeSession{defaultOutput: "              ^\n% Invalid input detected at '^' marker.\n"}, want: "device rejected"},
		{name: "transport", params: map[string]any{paramCommand: "show vlan"}, opens: true,
			session: &fakeSession{commandErr: errors.New("connection reset")}, want: "connection reset"},
		{name: "empty not recorded", params: map[string]any{paramCommand: "show version"}, opens: true,
			session: &fakeSession{defaultOutput: ""}, failStat: statEmpty, want: errRecording.Error()},
		{name: "stdout not recorded", params: map[string]any{paramCommand: "show ip interface brief"}, opens: true,
			session: &fakeSession{defaultOutput: parseBrief}, failStat: statCommandStdout, want: errRecording.Error()},
		{name: "parser not recorded", params: map[string]any{paramCommand: "show ip interface brief"}, opens: true,
			session: &fakeSession{defaultOutput: parseBrief}, failStat: statParser, want: errRecording.Error()},
		{name: "parsed not recorded", params: map[string]any{paramCommand: "show ip interface brief"}, opens: true,
			session: &fakeSession{defaultOutput: parseBrief}, failStat: statParsed, want: errRecording.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opened := openWith(t, tt.session)
			rc := newStubContext()
			rc.failStat = tt.failStat
			_, err := Parse(context.Background(), rc, nil, tt.params)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.want)
			}
			if *opened != tt.opens {
				t.Errorf("session opened = %v, want %v", *opened, tt.opens)
			}
		})
	}
}

// TestParse_EmptyIsAResultNotAFailure: output the parser finds nothing in
// (no LLDP neighbors yet) is a state a before and after can compare, so it
// is an empty result marked empty, unchanged, not an error.
func TestParse_EmptyIsAResultNotAFailure(t *testing.T) {
	openWith(t, &fakeSession{defaultOutput: "\n"})
	rc := newStubContext()
	res, err := Parse(context.Background(), rc, nil, map[string]any{paramCommand: "show lldp neighbors"})
	if err != nil || res.Changed {
		t.Fatalf("Parse = %+v, %v; want an unchanged result", res, err)
	}
	if parsed, _ := rc.stats[statParsed].(map[string]any); rc.stats[statEmpty] != true || parsed == nil || len(parsed) != 0 {
		t.Errorf("empty %v, parsed %v; want empty true and an empty result", rc.stats[statEmpty], rc.stats[statParsed])
	}
}

// TestParse_SessionFailure proves a session that will not open is the
// method's error.
func TestParse_SessionFailure(t *testing.T) {
	swapOpenSession(t, func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (iosSession, error) {
		return nil, errors.New("no route to host")
	})
	_, err := Parse(context.Background(), newStubContext(), nil, map[string]any{paramCommand: "show version"})
	if err == nil || !strings.Contains(err.Error(), "no route to host") {
		t.Fatalf("err = %v", err)
	}
}
