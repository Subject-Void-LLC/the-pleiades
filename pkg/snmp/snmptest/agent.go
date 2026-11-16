// Package snmptest is an in-process SNMPv1 and SNMPv2c agent over a real
// UDP socket on the loopback interface, for tests that need an agent to
// answer on demand: a store of objects it reads and writes as an agent does,
// or a handler that answers anything at all (an answer out of order, a
// datagram that is not SNMP, silence, an error status).
//
// Every request it answers is decoded by gosnmp and every answer encoded by
// gosnmp, and a client reaches it over UDP exactly as it reaches a device.
// What it proves about a real agent's USM, BER or algorithms is nothing: it
// speaks no SNMPv3. pkg/snmp/snmplive proves those against net-snmp.
package snmptest

import (
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
)

// Answer is what the agent sends back for one request: a packet to encode,
// raw bytes to send as they are, or nothing (Drop).
type Answer struct {
	Packet *gosnmp.SnmpPacket
	Raw    []byte
	Drop   bool
}

// Agent is an SNMPv1/v2c agent answering through its handler.
type Agent struct {
	t      testing.TB
	conn   *net.UDPConn
	handle func(req *gosnmp.SnmpPacket) Answer

	mu       sync.Mutex
	requests []*gosnmp.SnmpPacket
	arrived  []time.Time
}

// Start starts an agent answering through handle, stopped when tb ends.
func Start(tb testing.TB, handle func(req *gosnmp.SnmpPacket) Answer) *Agent {
	tb.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		tb.Fatal(err)
	}
	a := &Agent{t: tb, conn: conn, handle: handle}
	done := make(chan struct{})
	go a.serve(done)
	tb.Cleanup(func() {
		_ = conn.Close()
		<-done
	})
	return a
}

// serve answers requests until the socket closes.
func (a *Agent) serve(done chan struct{}) {
	defer close(done)
	buf := make([]byte, 65535)
	decoder := &gosnmp.GoSNMP{Version: gosnmp.Version2c}
	for {
		n, from, err := a.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		req, err := decoder.SnmpDecodePacket(append([]byte(nil), buf[:n]...))
		if err != nil {
			continue
		}
		a.mu.Lock()
		a.requests = append(a.requests, req)
		a.arrived = append(a.arrived, time.Now())
		a.mu.Unlock()
		ans := a.handle(req)
		out := ans.Raw
		switch {
		case ans.Drop:
			continue
		case ans.Packet != nil:
			p := ans.Packet
			p.Version, p.Community, p.RequestID = req.Version, req.Community, req.RequestID
			if p.PDUType == 0 {
				p.PDUType = gosnmp.GetResponse
			}
			if out, err = p.MarshalMsg(); err != nil {
				a.t.Errorf("encoding the test agent's answer: %v", err)
				continue
			}
		}
		_, _ = a.conn.WriteToUDP(out, from)
	}
}

// Port is the agent's UDP port.
func (a *Agent) Port() int { return a.conn.LocalAddr().(*net.UDPAddr).Port }

// Requests returns the requests the agent decoded.
func (a *Agent) Requests() []*gosnmp.SnmpPacket {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]*gosnmp.SnmpPacket(nil), a.requests...)
}

// Arrived returns when each request arrived.
func (a *Agent) Arrived() []time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]time.Time(nil), a.arrived...)
}

// Store is a set of objects an agent serves by OID (numeric, no leading
// dot), as a real agent does: GET, GETNEXT and GETBULK in OID order, and SET
// of an object it holds with the type it holds, by Community alone. An OID
// in Drops answers a SET as done and keeps nothing.
type Store struct {
	mu sync.Mutex
	// Community is the one community the store answers; any other is
	// dropped without an answer, as an agent drops it.
	Community string
	objects   map[string]gosnmp.SnmpPDU
	// Drops are the OIDs whose SET is answered and not kept.
	Drops map[string]bool
	// ReadOnly are the OIDs a SET is refused for, with notWritable.
	ReadOnly map[string]bool
	// Unanswered are the OIDs whose SET is applied and never answered, as a
	// SET whose answer UDP loses.
	Unanswered map[string]bool
	// Removes are the OIDs a SET removes from the store rather than keeps,
	// as a RowStatus destroy(6) removes its row.
	Removes map[string]bool
}

// NewStore is a store answering community, holding objects.
func NewStore(community string, objects ...gosnmp.SnmpPDU) *Store {
	s := &Store{Community: community, objects: map[string]gosnmp.SnmpPDU{}, Drops: map[string]bool{}, ReadOnly: map[string]bool{},
		Unanswered: map[string]bool{}, Removes: map[string]bool{}}
	for _, o := range objects {
		s.Put(o)
	}
	return s
}

// Put adds or replaces an object.
func (s *Store) Put(o gosnmp.SnmpPDU) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o.Name = "." + strings.TrimPrefix(o.Name, ".")
	s.objects[strings.TrimPrefix(o.Name, ".")] = o
}

// Value returns the object at oid.
func (s *Store) Value(oid string) (gosnmp.SnmpPDU, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[oid]
	return o, ok
}

// Handle answers req from the store.
func (s *Store) Handle(req *gosnmp.SnmpPacket) Answer {
	if req.Community != s.Community {
		return Answer{Drop: true}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := &gosnmp.SnmpPacket{}
	switch req.PDUType {
	case gosnmp.GetRequest:
		for i, v := range req.Variables {
			o, ok := s.objects[strings.TrimPrefix(v.Name, ".")]
			if !ok {
				if req.Version == gosnmp.Version1 {
					return Answer{Packet: &gosnmp.SnmpPacket{Error: gosnmp.NoSuchName, ErrorIndex: uint8(i + 1), Variables: req.Variables}} // #nosec G115 -- a test request names few OIDs
				}
				o = gosnmp.SnmpPDU{Name: v.Name, Type: gosnmp.NoSuchObject}
			}
			out.Variables = append(out.Variables, o)
		}
	case gosnmp.GetNextRequest, gosnmp.GetBulkRequest:
		count := 1
		if req.PDUType == gosnmp.GetBulkRequest {
			count = int(req.MaxRepetitions)
		}
		for _, v := range req.Variables {
			from := strings.TrimPrefix(v.Name, ".")
			for range count {
				next, ok := s.after(from)
				if !ok {
					if req.Version == gosnmp.Version1 {
						return Answer{Packet: &gosnmp.SnmpPacket{Error: gosnmp.NoSuchName, ErrorIndex: 1, Variables: req.Variables}}
					}
					out.Variables = append(out.Variables, gosnmp.SnmpPDU{Name: v.Name, Type: gosnmp.EndOfMibView})
					break
				}
				out.Variables = append(out.Variables, next)
				from = strings.TrimPrefix(next.Name, ".")
			}
		}
	case gosnmp.SetRequest:
		for i, v := range req.Variables {
			oid := strings.TrimPrefix(v.Name, ".")
			o, ok := s.objects[oid]
			switch {
			case !ok:
				return Answer{Packet: &gosnmp.SnmpPacket{Error: gosnmp.NoCreation, ErrorIndex: uint8(i + 1), Variables: req.Variables}} // #nosec G115 -- a test request names few OIDs
			case s.ReadOnly[oid]:
				return Answer{Packet: &gosnmp.SnmpPacket{Error: gosnmp.NotWritable, ErrorIndex: uint8(i + 1), Variables: req.Variables}} // #nosec G115 -- a test request names few OIDs
			case o.Type != v.Type:
				return Answer{Packet: &gosnmp.SnmpPacket{Error: gosnmp.WrongType, ErrorIndex: uint8(i + 1), Variables: req.Variables}} // #nosec G115 -- a test request names few OIDs
			}
		}
		unanswered := false
		for _, v := range req.Variables {
			oid := strings.TrimPrefix(v.Name, ".")
			switch {
			case s.Removes[oid]:
				delete(s.objects, oid)
			case !s.Drops[oid]:
				s.objects[oid] = v
			}
			unanswered = unanswered || s.Unanswered[oid]
		}
		if unanswered {
			return Answer{Drop: true}
		}
		out.Variables = req.Variables
	}
	return Answer{Packet: out}
}

// after is the first object after from in OID order.
func (s *Store) after(from string) (gosnmp.SnmpPDU, bool) {
	oids := make([]string, 0, len(s.objects))
	for oid := range s.objects {
		oids = append(oids, oid)
	}
	sort.Slice(oids, func(i, j int) bool { return less(oids[i], oids[j]) })
	for _, oid := range oids {
		if less(from, oid) {
			return s.objects[oid], true
		}
	}
	return gosnmp.SnmpPDU{}, false
}

// less orders two numeric OIDs by sub-identifier.
func less(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, _ := strconv.ParseUint(as[i], 10, 64)
		y, _ := strconv.ParseUint(bs[i], 10, 64)
		if x != y {
			return x < y
		}
	}
	return len(as) < len(bs)
}

// Text is an octet string object.
func Text(oid, value string) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Name: oid, Type: gosnmp.OctetString, Value: []byte(value)}
}

// Int is an integer object.
func Int(oid string, value int) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Name: oid, Type: gosnmp.Integer, Value: value}
}

// System is a MIB-II system group and a two-row interface table, as a small
// Linux agent answers them.
func System() []gosnmp.SnmpPDU {
	return []gosnmp.SnmpPDU{
		Text("1.3.6.1.2.1.1.1.0", "Linux test-agent 6.1.0"),
		{Name: "1.3.6.1.2.1.1.2.0", Type: gosnmp.ObjectIdentifier, Value: ".1.3.6.1.4.1.8072.3.2.10"},
		{Name: "1.3.6.1.2.1.1.3.0", Type: gosnmp.TimeTicks, Value: uint32(12345)},
		Text("1.3.6.1.2.1.1.4.0", "ops@example.net"),
		Text("1.3.6.1.2.1.1.5.0", "test-agent"),
		Text("1.3.6.1.2.1.1.6.0", "rack 1"),
		Int("1.3.6.1.2.1.1.7.0", 72),
		Int("1.3.6.1.2.1.2.1.0", 2),
		Int("1.3.6.1.2.1.2.2.1.1.1", 1), Int("1.3.6.1.2.1.2.2.1.1.2", 2),
		Text("1.3.6.1.2.1.2.2.1.2.1", "lo"), Text("1.3.6.1.2.1.2.2.1.2.2", "eth0"),
		Int("1.3.6.1.2.1.2.2.1.3.1", 24), Int("1.3.6.1.2.1.2.2.1.3.2", 6),
		{Name: "1.3.6.1.2.1.2.2.1.5.2", Type: gosnmp.Gauge32, Value: uint32(4294967295)},
		{Name: "1.3.6.1.2.1.2.2.1.6.2", Type: gosnmp.OctetString, Value: []byte{0x02, 0x42, 0xac, 0x11, 0x00, 0x02}},
		Int("1.3.6.1.2.1.2.2.1.7.1", 1), Int("1.3.6.1.2.1.2.2.1.7.2", 1),
		Int("1.3.6.1.2.1.2.2.1.8.1", 1), Int("1.3.6.1.2.1.2.2.1.8.2", 2),
		{Name: "1.3.6.1.2.1.2.2.1.10.2", Type: gosnmp.Counter32, Value: uint32(123456)},
		Text("1.3.6.1.2.1.31.1.1.1.1.2", "eth0"),
		{Name: "1.3.6.1.2.1.31.1.1.1.15.2", Type: gosnmp.Gauge32, Value: uint32(10000)},
		Text("1.3.6.1.6.3.10.2.1.1.0", "\x80\x00\x1f\x88\x80test-engine"),
		Int("1.3.6.1.6.3.10.2.1.2.0", 3),
	}
}

// Rows answers a GETNEXT or GETBULK with a synthetic table of n octet-string
// rows under root (root.1 to root.n), each holding size bytes, and
// endOfMibView past the last, with no store behind it: a table as large as a
// test needs costs nothing to build. Any other request is dropped.
func Rows(community, root string, n, size int) func(req *gosnmp.SnmpPacket) Answer {
	value := []byte(strings.Repeat("r", size))
	return func(req *gosnmp.SnmpPacket) Answer {
		if req.Community != community || (req.PDUType != gosnmp.GetNextRequest && req.PDUType != gosnmp.GetBulkRequest) {
			return Answer{Drop: true}
		}
		count := 1
		if req.PDUType == gosnmp.GetBulkRequest {
			count = int(req.MaxRepetitions)
		}
		from := strings.TrimPrefix(req.Variables[0].Name, ".")
		next := 1
		if strings.HasPrefix(from, root+".") {
			i, _ := strconv.Atoi(strings.SplitN(from[len(root)+1:], ".", 2)[0])
			next = i + 1
		} else if less(root, from) {
			next = n + 1
		}
		out := &gosnmp.SnmpPacket{}
		for ; count > 0; count-- {
			if next > n {
				out.Variables = append(out.Variables, gosnmp.SnmpPDU{Name: req.Variables[0].Name, Type: gosnmp.EndOfMibView})
				break
			}
			out.Variables = append(out.Variables, gosnmp.SnmpPDU{Name: "." + root + "." + strconv.Itoa(next), Type: gosnmp.OctetString, Value: value})
			next++
		}
		return Answer{Packet: out}
	}
}
