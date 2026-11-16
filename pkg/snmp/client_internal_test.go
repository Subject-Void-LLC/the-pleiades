// Package snmp: the client against the in-process agent, over a real UDP
// socket: what it reads, what it refuses, and how it paces and bounds itself.
package snmp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp/snmptest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// dialFake dials a at s with the fake agent's community.
func dialFake(t *testing.T, s Settings) *Client {
	t.Helper()
	c, err := Dial("127.0.0.1", s, wire.SNMPCredential{Community: testCommunity})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestGet_ReadsEveryTypeAndSaysWhatIsAbsent: a GET's answer converts each BER
// type to its typed binding, and a missing object reads as an Absent type,
// never as an error, in SNMPv2c.
func TestGet_ReadsEveryTypeAndSaysWhatIsAbsent(t *testing.T) {
	agent := snmptest.Start(t, func(req *gosnmp.SnmpPacket) snmptest.Answer {
		return snmptest.Answer{Packet: &gosnmp.SnmpPacket{Variables: []gosnmp.SnmpPDU{
			{Name: ".1.3.6.1.2.1.1.1.0", Type: gosnmp.OctetString, Value: []byte("Linux lab")},
			{Name: ".1.3.6.1.2.1.1.2.0", Type: gosnmp.ObjectIdentifier, Value: ".1.3.6.1.4.1.8072.3.2.10"},
			{Name: ".1.3.6.1.2.1.1.3.0", Type: gosnmp.TimeTicks, Value: uint32(4242)},
			{Name: ".1.3.6.1.2.1.2.2.1.6.2", Type: gosnmp.OctetString, Value: []byte{0x00, 0x1a, 0x2b, 0x3c, 0x4d, 0x5e}},
			{Name: ".1.3.6.1.2.1.4.20.1.1.1", Type: gosnmp.IPAddress, Value: "10.0.0.1"},
			{Name: ".1.3.6.1.2.1.31.1.1.1.6.1", Type: gosnmp.Counter64, Value: uint64(1) << 40},
			{Name: ".1.3.6.1.2.1.1.7.0", Type: gosnmp.Integer, Value: 72},
			{Name: ".1.3.6.1.2.1.1.9.0", Type: gosnmp.NoSuchObject},
		}}}
	})
	c := dialFake(t, v2cAt(agent))
	got, err := c.Get(context.Background(), []string{"1.3.6.1.2.1.1.1.0", "1.3.6.1.2.1.1.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		typ  Type
		text string
		hex  bool
	}{
		{TypeOctetString, "Linux lab", false},
		{TypeOID, "1.3.6.1.4.1.8072.3.2.10", false},
		{TypeTimeTicks, "4242", false},
		{TypeOctetString, "0x001a2b3c4d5e", true},
		{TypeIPAddress, "10.0.0.1", false},
		{TypeCounter64, "1099511627776", false},
		{TypeInteger, "72", false},
		{TypeNoSuchObject, "", false},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d bindings, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Type != w.typ || got[i].Text != w.text || got[i].Hex != w.hex {
			t.Errorf("binding %d = %+v, want type %s text %q hex %v", i, got[i], w.typ, w.text, w.hex)
		}
	}
	if !got[7].Type.Absent() || got[0].Type.Absent() {
		t.Error("Absent does not tell a missing object from a present one")
	}
	if c.EngineID() != "" || c.Authenticated() || c.Level() != "" || c.Version() != Version2c {
		t.Errorf("a community session reads as engine %q authenticated %v level %q version %s", c.EngineID(), c.Authenticated(), c.Level(), c.Version())
	}
}

// TestSet_AnErrorStatusNamesTheBindingAtFault: a refused SET comes back as an
// *AgentError naming RFC 3416's status and the OID, and a SET's value reaches
// the agent with the type it was given.
func TestSet_AnErrorStatusNamesTheBindingAtFault(t *testing.T) {
	agent := snmptest.Start(t, func(req *gosnmp.SnmpPacket) snmptest.Answer {
		return snmptest.Answer{Packet: &gosnmp.SnmpPacket{Error: gosnmp.NotWritable, ErrorIndex: 1, Variables: req.Variables}}
	})
	c := dialFake(t, v2cAt(agent))
	v, err := Value("sysContact.0", TypeOctetString, "ops@example.net")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Set(context.Background(), []Varbind{v})
	var agentErr *AgentError
	if !errors.As(err, &agentErr) || agentErr.Status != "notWritable" || agentErr.OID != OIDSysContact || agentErr.Index != 1 {
		t.Fatalf("Set = %v, want notWritable for %s", err, OIDSysContact)
	}
	seen := agent.Requests()
	if len(seen) != 1 || seen[0].PDUType != gosnmp.SetRequest || string(seen[0].Variables[0].Value.([]byte)) != "ops@example.net" {
		t.Fatalf("the agent saw %+v, want one SetRequest of the value", seen)
	}
}

// TestWalk_StopsAtTheSubtreeTheLimitAndAnAgentThatLoops: a walk ends at the
// first OID past its root, stops at its limit saying so, and refuses an agent
// that answers an OID not after the one asked from, rather than loop.
func TestWalk_StopsAtTheSubtreeTheLimitAndAnAgentThatLoops(t *testing.T) {
	tb := table{root: "1.3.6.1.2.1.2.2.1.2", n: 25}
	agent := snmptest.Start(t, func(req *gosnmp.SnmpPacket) snmptest.Answer {
		count := 1
		if req.PDUType == gosnmp.GetBulkRequest {
			count = int(req.MaxRepetitions)
		}
		return snmptest.Answer{Packet: &gosnmp.SnmpPacket{Variables: tb.after(strings.TrimPrefix(req.Variables[0].Name, "."), count)}}
	})
	c := dialFake(t, v2cAt(agent))
	res, err := c.Walk(context.Background(), tb.root, MaxWalkResults)
	if err != nil || res.Truncated || len(res.Bindings) != 25 || res.Bindings[24].Text != "row 25" {
		t.Fatalf("Walk = %d bindings, truncated %v, %v; want all 25", len(res.Bindings), res.Truncated, err)
	}
	// GETBULK, max-repetitions from the device's settings: 25 rows in 3.
	if n := len(agent.Requests()); n != 3 || agent.Requests()[0].PDUType != gosnmp.GetBulkRequest {
		t.Errorf("the walk sent %d requests, want 3 GETBULKs", n)
	}

	res, err = c.Walk(context.Background(), tb.root, 7)
	if err != nil || !res.Truncated || len(res.Bindings) != 7 {
		t.Fatalf("a limited walk = %d bindings, truncated %v, %v; want 7 and truncated", len(res.Bindings), res.Truncated, err)
	}

	loop := snmptest.Start(t, func(req *gosnmp.SnmpPacket) snmptest.Answer {
		return snmptest.Answer{Packet: &gosnmp.SnmpPacket{Variables: []gosnmp.SnmpPDU{{Name: "." + tb.root + ".1", Type: gosnmp.Integer, Value: 1}}}}
	})
	lc := dialFake(t, v2cAt(loop))
	if _, err := lc.Walk(context.Background(), tb.root, MaxWalkResults); !errors.Is(err, ErrNotIncreasing) {
		t.Fatalf("a looping agent's walk = %v, want ErrNotIncreasing", err)
	}
	if n := len(loop.Requests()); n != 2 {
		t.Errorf("the walk asked a looping agent %d times, want 2 (it stops at the first repeat)", n)
	}
}

// TestWalk_NoBulkAndSNMPv1UseGetNext: a device whose record turns GETBULK off
// and an SNMPv1 device walk with GETNEXT, and SNMPv1's noSuchName past the
// last object ends the walk.
func TestWalk_NoBulkAndSNMPv1UseGetNext(t *testing.T) {
	tb := table{root: "1.3.6.1.2.1.1", n: 3}
	for _, v1 := range []bool{false, true} {
		agent := snmptest.Start(t, func(req *gosnmp.SnmpPacket) snmptest.Answer {
			vars := tb.after(strings.TrimPrefix(req.Variables[0].Name, "."), 1)
			if v1 && vars[0].Type == gosnmp.EndOfMibView {
				return snmptest.Answer{Packet: &gosnmp.SnmpPacket{Error: gosnmp.NoSuchName, ErrorIndex: 1, Variables: req.Variables}}
			}
			return snmptest.Answer{Packet: &gosnmp.SnmpPacket{Variables: vars}}
		})
		s := v2cAt(agent)
		if v1 {
			s.Version = Version1
		} else {
			s.NoBulk = true
		}
		c := dialFake(t, s)
		res, err := c.Walk(context.Background(), tb.root, MaxWalkResults)
		if err != nil || len(res.Bindings) != 3 {
			t.Fatalf("v1=%v: Walk = %d bindings, %v; want 3", v1, len(res.Bindings), err)
		}
		for _, req := range agent.Requests() {
			if req.PDUType != gosnmp.GetNextRequest {
				t.Fatalf("v1=%v: the walk sent a %v, want only GETNEXT", v1, req.PDUType)
			}
		}
		if _, err := c.GetBulk(context.Background(), []string{tb.root}, 0, 5); err == nil {
			t.Errorf("v1=%v: GetBulk was sent", v1)
		}
	}
}

// TestRequest_SilenceIsATimeoutAfterEveryRetry: an agent that never answers
// (as one does for a community it does not know) is sent the request once
// and then once per retry, and the error says so.
func TestRequest_SilenceIsATimeoutAfterEveryRetry(t *testing.T) {
	agent := snmptest.Start(t, func(*gosnmp.SnmpPacket) snmptest.Answer { return snmptest.Answer{Drop: true} })
	s := v2cAt(agent)
	s.Retries = 2
	c := dialFake(t, s)
	_, err := c.Get(context.Background(), []string{OIDSysName})
	var timeout *TimeoutError
	if !errors.As(err, &timeout) || timeout.Tries != 3 {
		t.Fatalf("Get = %v, want a TimeoutError after 3 tries", err)
	}
	if n := len(agent.Requests()); n != 3 {
		t.Errorf("the agent saw %d requests, want 3", n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Get(ctx, []string{OIDSysName}); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled Get = %v, want context.Canceled", err)
	}
}

// TestRequest_TheMinimumIntervalSpacesEveryRequest: a device's minimum
// interval holds between every two requests, a walk's included. It is
// measured on the client, where the interval is kept: five requests take at
// least four intervals, however the datagrams are delayed on the way, which
// a gap between arrivals at the agent cannot promise on a loaded host.
func TestRequest_TheMinimumIntervalSpacesEveryRequest(t *testing.T) {
	tb := table{root: "1.3.6.1.2.1.1", n: 4}
	agent := snmptest.Start(t, func(req *gosnmp.SnmpPacket) snmptest.Answer {
		return snmptest.Answer{Packet: &gosnmp.SnmpPacket{Variables: tb.after(strings.TrimPrefix(req.Variables[0].Name, "."), 1)}}
	})
	s := v2cAt(agent)
	s.NoBulk, s.MinInterval = true, 60*time.Millisecond
	c := dialFake(t, s)
	started := time.Now()
	if _, err := c.Walk(context.Background(), tb.root, MaxWalkResults); err != nil {
		t.Fatal(err)
	}
	took := time.Since(started)
	if n := len(agent.Arrived()); n != 5 {
		t.Fatalf("the walk sent %d requests, want 5", n)
	}
	if took < 4*s.MinInterval {
		t.Errorf("five requests took %s, under the four %s intervals between them", took, s.MinInterval)
	}
}

// TestWalkWithin_KeepsToItsBytes: a walk handed a byte budget stops inside
// it, saying it was cut short and how much it kept, and a budget past
// MaxWalkBytes is refused; a host that does not resolve is a DialError.
func TestWalkWithin_KeepsToItsBytes(t *testing.T) {
	tb := table{root: "1.3.6.1.2.1.2.2.1.2", n: 25}
	agent := snmptest.Start(t, func(req *gosnmp.SnmpPacket) snmptest.Answer {
		return snmptest.Answer{Packet: &gosnmp.SnmpPacket{Variables: tb.after(strings.TrimPrefix(req.Variables[0].Name, "."), int(req.MaxRepetitions))}}
	})
	c := dialFake(t, v2cAt(agent))
	whole, err := c.Walk(context.Background(), tb.root, MaxWalkResults)
	if err != nil || whole.Truncated || len(whole.Bindings) != 25 {
		t.Fatalf("Walk = %d bindings, %v", len(whole.Bindings), err)
	}
	budget := whole.Bytes / 2
	half, err := c.WalkWithin(context.Background(), tb.root, MaxWalkResults, budget)
	if err != nil || !half.Truncated || half.Bytes > budget || len(half.Bindings) == 0 || len(half.Bindings) >= 25 {
		t.Fatalf("WalkWithin(%d bytes) = %d bindings of %d bytes, truncated %v, %v", budget, len(half.Bindings), half.Bytes, half.Truncated, err)
	}
	if _, err := c.WalkWithin(context.Background(), tb.root, 10, MaxWalkBytes+1); err == nil {
		t.Error("a budget past MaxWalkBytes was accepted")
	}
	var dial *DialError
	if _, err := Dial("no-such-agent.invalid", v2cAt(agent), wire.SNMPCredential{Community: testCommunity}); !errors.As(err, &dial) || dial.Unwrap() == nil {
		t.Errorf("Dial of a name that does not resolve = %v, want a DialError", err)
	}
}

// TestRequest_AMaliciousAnswerIsAnError: an answer that is not SNMP, one
// announcing a length far past its datagram, one nested past any real message
// and one cut off inside a binding are each an error, never a panic or an
// answer. Whether an error can hold a secret is requestError's, which
// TestRequestError_MasksTheCredentialAndDropsAStack holds.
func TestRequest_AMaliciousAnswerIsAnError(t *testing.T) {
	short := "pub1ic"
	for name, raw := range map[string][]byte{
		"not SNMP":            []byte("HTTP/1.1 400 Bad Request\r\n\r\n" + short),
		"a huge length":       {0x30, 0x84, 0x7f, 0xff, 0xff, 0xff, 0x02, 0x01, 0x01},
		"nested past reason":  nested(4000),
		"a truncated varbind": {0x30, 0x10, 0x02, 0x01, 0x01, 0x04, 0x06, 'p', 'u', 'b', '1', 'i', 'c', 0xa2, 0x20, 0x02, 0x04},
	} {
		t.Run(name, func(t *testing.T) {
			agent := snmptest.Start(t, func(*gosnmp.SnmpPacket) snmptest.Answer { return snmptest.Answer{Raw: raw} })
			s := v2cAt(agent)
			c, err := Dial("127.0.0.1", s, wire.SNMPCredential{Community: short})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close() }()
			_, err = c.Get(context.Background(), []string{OIDSysName})
			if err == nil {
				t.Fatal("a malicious answer was accepted")
			}
		})
	}
}

// TestRequestError_MasksTheCredentialAndDropsAStack: an error gosnmp returns
// holding the client's secrets is masked of them, a short community only as
// its own word, and a panic gosnmp recovered from, whose text carries every
// goroutine's stack, is replaced with what it means.
func TestRequestError_MasksTheCredentialAndDropsAStack(t *testing.T) {
	agent := snmptest.Start(t, func(*gosnmp.SnmpPacket) snmptest.Answer { return snmptest.Answer{Drop: true} })
	c, err := Dial("127.0.0.1", v2cAt(agent), wire.SNMPCredential{Community: "pub1ic"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	got := c.requestError("GET", errors.New("the agent said pub1ic, and xpub1icx")).Error()
	if strings.Contains(got, "said pub1ic") || !strings.Contains(got, "xpub1icx") {
		t.Errorf("requestError = %q, want the community masked as a word and nothing else", got)
	}
	got = c.requestError("GET", errors.New("recover: index out of range Stack:goroutine 1 [running]:\nmain.main()")).Error()
	if strings.Contains(got, "goroutine") || !strings.Contains(got, "could not be decoded") {
		t.Errorf("requestError = %q, want the stack replaced", got)
	}
	var refused *RefusedError
	if !errors.As(c.requestError("GET", errors.New("incoming packet is not authentic, discarding")), &refused) {
		t.Error("an unauthenticated answer is not a refusal")
	}
	if !errors.As(c.requestError("GET", gosnmp.ErrWrongDigest), &refused) {
		t.Error("a wrong digest report is not a refusal")
	}
}

// nested is a BER message of depth sequences, each holding the next.
func nested(depth int) []byte {
	out := []byte{0x05, 0x00}
	for i := 0; i < depth && len(out) < 60000; i++ {
		n := len(out)
		switch {
		case n < 0x80:
			out = append([]byte{0x30, byte(n)}, out...)
		case n < 0x100:
			out = append([]byte{0x30, 0x81, byte(n)}, out...)
		default:
			out = append([]byte{0x30, 0x82, byte(n >> 8), byte(n)}, out...)
		}
	}
	return out
}

// TestRequest_RefusesBadOIDsAndAClosedClient: a request names numeric OIDs
// ParseOID would return unchanged, a bounded count of them, through an open
// client.
func TestRequest_RefusesBadOIDsAndAClosedClient(t *testing.T) {
	agent := snmptest.Start(t, func(*gosnmp.SnmpPacket) snmptest.Answer { return snmptest.Answer{Drop: true} })
	c := dialFake(t, v2cAt(agent))
	for _, oids := range [][]string{nil, {"sysName.0"}, {".1.3.6.1"}, make([]string, MaxOIDsPerRequest+1)} {
		if _, err := c.Get(context.Background(), oids); err == nil {
			t.Errorf("Get(%v) was sent", oids)
		}
	}
	if _, err := c.Set(context.Background(), nil); err == nil {
		t.Error("an empty SET was sent")
	}
	if _, err := c.Walk(context.Background(), OIDSystem, 0); err == nil {
		t.Error("a walk of no bindings was sent")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), []string{OIDSysName}); err == nil {
		t.Error("a closed client sent a request")
	}
	if len(agent.Requests()) != 0 {
		t.Errorf("the agent saw %d requests, want none", len(agent.Requests()))
	}
}

// BenchmarkGet_OverLoopback times one GET of the system group's seven
// scalars through the client, gosnmp's codec and a real UDP socket, the cost
// a task pays per request before the network: net-snmp's snmpget pays a
// process start on top of the same round trip.
func BenchmarkGet_OverLoopback(b *testing.B) {
	store := snmptest.NewStore(testCommunity, snmptest.System()...)
	agent := snmptest.Start(b, store.Handle)
	c, err := Dial("127.0.0.1", v2cAt(agent), wire.SNMPCredential{Community: testCommunity})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	oids := []string{OIDSysDescr, OIDSysObjectID, OIDSysUpTime, OIDSysContact, OIDSysName, OIDSysLocation, OIDSysServices}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.Get(context.Background(), oids); err != nil {
			b.Fatal(err)
		}
	}
}

// TestDial_RefusesWhatTheSettingsDoNotAllow: Dial runs Settings.Check, so a
// credential the device's settings refuse never builds a client.
func TestDial_RefusesWhatTheSettingsDoNotAllow(t *testing.T) {
	if _, err := Dial("127.0.0.1", Defaults(), wire.SNMPCredential{Community: testCommunity}); err == nil {
		t.Error("a community was dialed for an SNMPv3 device")
	}
	if _, err := Dial("", Defaults(), wire.SNMPCredential{User: "ops"}); err == nil {
		t.Error("a device with no host was dialed")
	}
	threeDES := wire.SNMPCredential{User: "ops", AuthProtocol: wire.SNMPAuthSHA, AuthPassphrase: "authpassword", PrivProtocol: wire.SNMPPriv3DES, PrivPassphrase: "privpassword"}
	s := Defaults()
	s.AllowLegacyAlgorithms = true
	if _, err := Dial("127.0.0.1", s, threeDES); !errors.Is(err, ErrUnsupportedPrivacy) {
		t.Errorf("Dial with 3des = %v, want ErrUnsupportedPrivacy", err)
	}
}
