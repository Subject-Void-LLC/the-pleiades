// Package snmplive: the client against net-snmp, every version and every
// USM algorithm the agent implements, each proven by an answer that only the
// right key could have produced.
package snmplive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// settingsFor is the settings of a device at agent speaking version, quick
// to give up so a refusal that reads as silence does not hold the test.
func settingsFor(agent *testsupport.SNMPD, version snmp.Version, legacy bool) snmp.Settings {
	s := snmp.Defaults()
	s.Port, s.Version = agent.Port, version
	s.AllowCleartextCommunity = version.Community()
	s.AllowLegacyAlgorithms = legacy
	s.Timeout, s.Retries = 2*time.Second, 1
	return s
}

// dial builds a client for agent, failing the test on a refusal.
func dial(t *testing.T, agent *testsupport.SNMPD, s snmp.Settings, cred wire.SNMPCredential) *snmp.Client {
	t.Helper()
	c, err := snmp.Dial(agent.Host, s, cred)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// readSystem reads sysDescr.0 and sysName.0 and fails the test unless both
// come back as octet strings.
func readSystem(t *testing.T, c *snmp.Client) []snmp.Varbind {
	t.Helper()
	got, err := c.Get(context.Background(), []string{snmp.OIDSysDescr, snmp.OIDSysName})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Type != snmp.TypeOctetString || !strings.HasPrefix(got[0].Text, "Linux") || got[1].Type != snmp.TypeOctetString || got[1].Text == "" {
		t.Fatalf("the agent answered %+v, want its sysDescr and sysName", got)
	}
	return got
}

// legacyCred reports whether cred needs the legacy algorithm opt-in.
func legacyCred(cred wire.SNMPCredential) bool { return len(snmp.LegacyAlgorithms(cred)) > 0 }

// TestEveryVersionAndEveryAlgorithmAgainstNetSNMP: SNMPv1 and SNMPv2c with
// the community, and SNMPv3 as every user the agent was configured with, one
// per authentication protocol and one per privacy protocol, each read the
// agent's system group; each SNMPv3 session reports the level it ran at and
// the engine ID USM discovered, which is the agent's own snmpEngineID.0.
func TestEveryVersionAndEveryAlgorithmAgainstNetSNMP(t *testing.T) {
	agent := testsupport.StartSNMPD(t)
	for _, v := range []snmp.Version{snmp.Version1, snmp.Version2c} {
		t.Run("community over SNMPv"+string(v), func(t *testing.T) {
			c := dial(t, agent, settingsFor(agent, v, false), wire.SNMPCredential{Community: agent.ReadCommunity})
			readSystem(t, c)
			if c.Authenticated() || c.EngineID() != "" {
				t.Errorf("a community session reads as authenticated %v with engine %q", c.Authenticated(), c.EngineID())
			}
		})
	}
	// The agent's own engine ID, read over SNMPv2c, which every SNMPv3
	// session's discovery must agree with.
	v2 := dial(t, agent, settingsFor(agent, snmp.Version2c, false), wire.SNMPCredential{Community: agent.ReadCommunity})
	own, err := v2.Get(context.Background(), []string{snmp.OIDEngineID})
	if err != nil || len(own) != 1 || len(own[0].Bytes) == 0 {
		t.Fatalf("snmpEngineID.0 = %+v, %v", own, err)
	}
	engine := fmt.Sprintf("%x", own[0].Bytes)

	tried := map[string]bool{}
	for name, user := range agent.Users {
		t.Run("SNMPv3 "+name, func(t *testing.T) {
			c := dial(t, agent, settingsFor(agent, snmp.Version3, legacyCred(user.Cred)), user.Cred)
			readSystem(t, c)
			if c.Level() != user.Cred.Level() || c.EngineID() != engine {
				t.Errorf("level %s, engine %s; want %s and the agent's %s", c.Level(), c.EngineID(), user.Cred.Level(), engine)
			}
		})
		tried[string(user.Cred.AuthProtocol)+"/"+string(user.Cred.PrivProtocol)] = true
	}
	// Every protocol of the credential model but 3DES was exercised.
	for _, a := range wire.SNMPAuthProtocols() {
		if !tried[string(a)+"/"] {
			t.Errorf("authentication protocol %s was not proven", a)
		}
	}
	for _, p := range wire.SNMPPrivProtocols() {
		if p != wire.SNMPPriv3DES && !tried[string(wire.SNMPAuthSHA)+"/"+string(p)] {
			t.Errorf("privacy protocol %s was not proven", p)
		}
	}
}

// TestTheKeyExtensionSchemesAreNotInterchangeable: a user net-snmp keyed with
// Blumenthal's AES-192 and AES-256 extension answers nothing to a client
// using Reeder's, and the reverse, so the two names this platform keeps
// apart are two different protocols on the wire and not two spellings of one.
func TestTheKeyExtensionSchemesAreNotInterchangeable(t *testing.T) {
	agent := testsupport.StartSNMPD(t)
	for configured, sent := range map[wire.SNMPPrivProtocol]wire.SNMPPrivProtocol{
		wire.SNMPPrivAES192:       wire.SNMPPrivAES192Reeder,
		wire.SNMPPrivAES256:       wire.SNMPPrivAES256Reeder,
		wire.SNMPPrivAES192Reeder: wire.SNMPPrivAES192,
		wire.SNMPPrivAES256Reeder: wire.SNMPPrivAES256,
	} {
		t.Run(string(configured)+" read as "+string(sent), func(t *testing.T) {
			cred := agent.Users[testsupport.SNMPDUserName(wire.SNMPAuthSHA, configured)].Cred
			cred.PrivProtocol = sent
			s := settingsFor(agent, snmp.Version3, false)
			s.Timeout, s.Retries = 700*time.Millisecond, 0
			c := dial(t, agent, s, cred)
			if got, err := c.Get(context.Background(), []string{snmp.OIDSysName}); err == nil {
				t.Fatalf("the agent answered %+v to the other scheme's key", got)
			}
		})
	}
}

// TestRefusalsAreTypedAndHoldNoSecret: a wrong authentication passphrase and
// an unknown user come back as a *snmp.RefusedError, never retried into a
// timeout, and a wrong community as a timeout; no error text holds a
// passphrase or a community.
func TestRefusalsAreTypedAndHoldNoSecret(t *testing.T) {
	agent := testsupport.StartSNMPD(t)
	good := agent.Users[testsupport.SNMPDUserName(wire.SNMPAuthSHA256, "")].Cred
	wrong := good
	wrong.AuthPassphrase = "not-the-passphrase"
	unknown := good
	unknown.User = "nobody-here"
	for name, cred := range map[string]wire.SNMPCredential{"a wrong passphrase": wrong, "an unknown user": unknown} {
		t.Run(name, func(t *testing.T) {
			c := dial(t, agent, settingsFor(agent, snmp.Version3, false), cred)
			_, err := c.Get(context.Background(), []string{snmp.OIDSysName})
			var refused *snmp.RefusedError
			if !errors.As(err, &refused) {
				t.Fatalf("Get = %v, want a RefusedError", err)
			}
			if strings.Contains(err.Error(), cred.AuthPassphrase) || strings.Contains(err.Error(), good.AuthPassphrase) {
				t.Fatalf("the error holds a passphrase: %v", err)
			}
		})
	}
	s := settingsFor(agent, snmp.Version2c, false)
	s.Timeout, s.Retries = 500*time.Millisecond, 0
	c := dial(t, agent, s, wire.SNMPCredential{Community: "not-the-community-at-all"})
	_, err := c.Get(context.Background(), []string{snmp.OIDSysName})
	var timeout *snmp.TimeoutError
	if !errors.As(err, &timeout) || strings.Contains(err.Error(), "not-the-community") {
		t.Fatalf("a wrong community = %v, want a TimeoutError that holds no community", err)
	}
}

// TestWalkAndSetAgainstNetSNMP: a GETBULK walk and a GETNEXT walk of the
// interface table agree; a SET by the user that may write changes
// sysLocation.0, which reads back as written over SNMPv2c too; a SET by a
// read-only community is refused by the agent; and the agent's drop object
// answers a SET as done and keeps the old value, the case net.snmp.set's
// read back exists for.
func TestWalkAndSetAgainstNetSNMP(t *testing.T) {
	agent := testsupport.StartSNMPD(t)
	writer := agent.Users[testsupport.SNMPDUserName(wire.SNMPAuthSHA512, wire.SNMPPrivAES256)].Cred
	bulk := dial(t, agent, settingsFor(agent, snmp.Version3, false), writer)
	noBulkSettings := settingsFor(agent, snmp.Version3, false)
	noBulkSettings.NoBulk = true
	next := dial(t, agent, noBulkSettings, writer)
	a, err := bulk.Walk(context.Background(), snmp.OIDIfTable, snmp.MaxWalkResults)
	if err != nil || len(a.Bindings) == 0 || a.Truncated {
		t.Fatalf("GETBULK walk = %d bindings, %v", len(a.Bindings), err)
	}
	b, err := next.Walk(context.Background(), snmp.OIDIfTable, snmp.MaxWalkResults)
	if err != nil || len(b.Bindings) != len(a.Bindings) {
		t.Fatalf("GETNEXT walk = %d bindings, %v; want %d", len(b.Bindings), err, len(a.Bindings))
	}
	for i := range a.Bindings {
		// Counters move between the walks; the OIDs and types do not.
		if a.Bindings[i].OID != b.Bindings[i].OID || a.Bindings[i].Type != b.Bindings[i].Type {
			t.Fatalf("binding %d: %+v by GETBULK, %+v by GETNEXT", i, a.Bindings[i], b.Bindings[i])
		}
	}

	want, err := snmp.Value("sysLocation.0", snmp.TypeOctetString, "rack 9, written by the live test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bulk.Set(context.Background(), []snmp.Varbind{want}); err != nil {
		t.Fatal(err)
	}
	v2 := dial(t, agent, settingsFor(agent, snmp.Version2c, false), wire.SNMPCredential{Community: agent.ReadCommunity})
	got, err := v2.Get(context.Background(), []string{snmp.OIDSysLocation})
	if err != nil || !snmp.Same(got[0], want) {
		t.Fatalf("sysLocation.0 read back = %+v, %v", got, err)
	}
	if _, err := v2.Set(context.Background(), []snmp.Varbind{want}); err == nil {
		t.Error("a read-only community's SET was accepted")
	}

	dropped, err := snmp.Value(testsupport.SNMPDDropOID, snmp.TypeOctetString, "a value nobody keeps")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bulk.Set(context.Background(), []snmp.Varbind{dropped}); err != nil {
		t.Fatalf("the drop object's SET = %v, want it answered as done", err)
	}
	after, err := bulk.Get(context.Background(), []string{testsupport.SNMPDDropOID})
	if err != nil || snmp.Same(after[0], dropped) {
		t.Fatalf("the drop object read back %+v, %v; want the value it kept", after, err)
	}
}
