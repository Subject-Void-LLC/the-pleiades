// Tests for the SNMP probe through the real onboarding path, against the
// in-process agent (pkg/snmp/snmptest) over SNMPv2c; pkg/snmp/snmplive runs
// the same probe against net-snmp over SNMPv3.
package onboard

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp/snmptest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// snmpCommunity is the test agent's throwaway community.
const snmpCommunity = "probe-agent-community"

// snmpRun onboards a generic_snmp device at agent over SNMPv2c with
// community.
func snmpRun(t *testing.T, agent *snmptest.Agent, community string) func() (Result, error) {
	t.Helper()
	repo, _ := newRepo(t, "agent1", generic.TypeSNMP, map[string]inventory.PropertyValue{
		generic.HostProperty: "127.0.0.1", snmp.PortProperty: agent.Port(), snmp.VersionProperty: "2c",
		snmp.AllowCleartextCommunityProperty: true, snmp.TimeoutProperty: 1, snmp.RetriesProperty: 0,
	})
	secrets := staticSecrets(wire.SNMPCredential{Community: community}.Secrets())
	return func() (Result, error) {
		return onboardWith(context.Background(), repo, "agent1", secrets, fixedNow, Lookup, options{})
	}
}

// TestSNMPProbe_OnboardsFromTheAgentsOwnAnswers walks a new generic_snmp to
// active and checks what onboarding recorded and warned, and that a second
// onboarding of the unchanged agent writes nothing.
func TestSNMPProbe_OnboardsFromTheAgentsOwnAnswers(t *testing.T) {
	agent := snmptest.Start(t, snmptest.NewStore(snmpCommunity, snmptest.System()...).Handle)
	run := snmpRun(t, agent, snmpCommunity)
	res, err := run()
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "active" || res.Protocol != "snmp" || !res.Changed || !strings.Contains(strings.Join(res.Capabilities, ","), string(capability.NameSNMP)) {
		t.Fatalf("result %+v, want the device active with SNMPCapable", res)
	}
	if res.Facts["snmp_version"] != "2c" || res.Facts["snmp_security_level"] != "community" || res.Facts["snmp_sys_name"] != "test-agent" ||
		res.Facts["snmp_sys_object_id"] != "1.3.6.1.4.1.8072.3.2.10" || !strings.HasPrefix(res.Facts["snmp_engine_id"].(string), "80001f8880") {
		t.Errorf("facts %v", res.Facts)
	}
	if mibs, _ := res.Facts["snmp_mibs"].([]any); len(mibs) != 4 {
		t.Errorf("snmp_mibs = %v, want system, interfaces, ifXTable and snmpEngine", res.Facts["snmp_mibs"])
	}
	warnings := strings.Join(res.Warnings, " ")
	if !strings.Contains(warnings, "in the clear") || !strings.Contains(warnings, "nothing proves which agent") {
		t.Errorf("warnings %q, want the community and the unauthenticated answer named", warnings)
	}
	again, err := run()
	if err != nil || again.Changed {
		t.Errorf("a second onboarding = %+v, %v; want it unchanged", again, err)
	}
}

// TestSNMPProbe_SilenceOrAnEmptyViewProvesNothing: a wrong community, which
// an agent drops without a word, and an agent holding no system object for
// this credential each leave the device unproven.
func TestSNMPProbe_SilenceOrAnEmptyViewProvesNothing(t *testing.T) {
	silent := snmptest.Start(t, snmptest.NewStore("another-community").Handle)
	if res, err := snmpRun(t, silent, snmpCommunity)(); err == nil || res.State == "active" {
		t.Errorf("a silent agent onboarded: %+v, %v", res, err)
	}
	empty := snmptest.Start(t, snmptest.NewStore(snmpCommunity, snmptest.Int("1.3.6.1.4.1.1.1", 1)).Handle)
	res, err := snmpRun(t, empty, snmpCommunity)()
	if err == nil || res.State == "active" || !strings.Contains(err.Error(), "grants nothing to read") {
		t.Errorf("an empty view = %+v, %v", res, err)
	}
}
