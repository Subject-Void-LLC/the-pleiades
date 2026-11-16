// Tests for wait.snmp against a real net-snmp agent (testsupport.StartSNMPD)
// over SNMPv3: a user it knows ends the wait at the first try, and a wrong
// passphrase, which the agent refuses with a USM report, ends it at once
// rather than being sent at every try until the timeout.
package wait_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/wait"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

func TestSNMP_NetSNMPAnswersAUserAndRefusesAWrongPassphraseOnce(t *testing.T) {
	agent := testsupport.StartSNMPD(t)
	item, err := generic.NewSNMP(record.Record{Name: "lab-agent", Type: generic.TypeSNMP, Properties: map[string]any{
		generic.HostProperty: agent.Host, snmp.PortProperty: agent.Port,
	}})
	if err != nil {
		t.Fatal(err)
	}
	user := agent.Users[testsupport.SNMPDUserName(wire.SNMPAuthSHA, wire.SNMPPrivAES192)].Cred
	if user.User == "" {
		t.Fatal("the test agent has no such user")
	}
	rc := snmpContext(user)
	if _, err := wait.SNMP(context.Background(), rc, item, map[string]any{"timeout": 60, "connect_timeout": 15}); err != nil {
		t.Fatal(err)
	}
	if rc.stats["version"] != "3" || rc.stats["tries"] != 1 {
		t.Errorf("stats = %v", rc.stats)
	}
	wrong := user
	wrong.AuthPassphrase = "not-the-passphrase"
	_, err = wait.SNMP(context.Background(), snmpContext(wrong), item, map[string]any{"timeout": 60, "sleep": 1, "connect_timeout": 15})
	if err == nil || !strings.Contains(err.Error(), "stopped at try 1") || !strings.Contains(err.Error(), "refused this credential") {
		t.Fatalf("wait.snmp with a wrong passphrase = %v, want it stopped at the first try", err)
	}
	if strings.Contains(err.Error(), user.AuthPassphrase) || strings.Contains(err.Error(), "not-the-passphrase") {
		t.Errorf("the error holds a passphrase: %v", err)
	}
}
