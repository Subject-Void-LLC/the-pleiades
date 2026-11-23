// Tests that a Conn a Pool lends carries every field that belongs to the
// connection and resets every field that belongs to one borrow. Every Conn
// field is listed here as one or the other, so a field added to Conn fails
// TestLend_EveryConnFieldIsPlaced until someone decides which it is: a field
// lend once dropped by listing fields by hand (the host keys a dial verified)
// would otherwise vanish again for every borrowed connection, silently.
package remoteexec

import (
	"context"
	"reflect"
	"testing"

	"golang.org/x/crypto/ssh"
)

// connectionFields are the Conn fields that describe the connection itself,
// which a borrowed Conn must carry unchanged.
// userKey is what the stored key did when the connection logged in, which a
// borrow does not repeat.
var connectionFields = []string{"client", "chain", "addr", "redial", "severVia", "legacy", "userKey", "hopLegacy", "hostKeys"}

// borrowFields are the Conn fields that belong to one borrow, which lend
// resets.
var borrowFields = []string{"lease", "become", "raw", "extra", "redialRefused"}

// TestLend_EveryConnFieldIsPlaced: each Conn field is in exactly one list.
func TestLend_EveryConnFieldIsPlaced(t *testing.T) {
	placed := map[string]int{}
	for _, f := range append(append([]string(nil), connectionFields...), borrowFields...) {
		placed[f]++
	}
	typ := reflect.TypeOf(Conn{})
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		switch placed[name] {
		case 0:
			t.Errorf("Conn.%s is in neither connectionFields nor borrowFields: decide whether a borrowed connection carries it (pool.go's lend)", name)
		case 1:
		default:
			t.Errorf("Conn.%s is listed twice", name)
		}
		delete(placed, name)
	}
	for name := range placed {
		t.Errorf("%s is listed but Conn has no such field", name)
	}
}

// TestLend_CarriesTheConnectionAndResetsTheBorrow: with every field of the
// pooled Conn set, the lent Conn holds each connection field's value and the
// zero value of each borrow field but its new lease.
func TestLend_CarriesTheConnectionAndResetsTheBorrow(t *testing.T) {
	log := newHostKeyLog(true)
	log.add(generateTestHostKey(t).PublicKey())
	pooledConn := &Conn{
		client:        &ssh.Client{},
		chain:         []*ssh.Client{{}},
		addr:          "web1:22",
		lease:         &lease{},
		become:        &Become{User: "root"},
		raw:           true,
		redial:        func(context.Context) (*Conn, error) { return nil, nil },
		extra:         []*ssh.Client{{}},
		redialRefused: true,
		severVia:      func() {},
		legacy:        Legacy{HostKeys: true},
		userKey:       userKeyOutcome{key: "RSA key", signed: ssh.KeyAlgoRSASHA512},
		hopLegacy:     []hopLegacy{{addr: "bastion:22"}},
		hostKeys:      log,
	}
	p := NewPool(0)
	e := &pooled{conn: pooledConn}
	lent := p.lend("web1", e)

	// Funcs cannot be compared by value, so each is held to the same code
	// pointer; every other connection field is compared by DeepEqual on
	// copies with the funcs and the borrow fields cleared.
	orig, got := reflect.ValueOf(pooledConn).Elem(), reflect.ValueOf(lent).Elem()
	for _, name := range connectionFields {
		if a, b := orig.FieldByName(name), got.FieldByName(name); a.Kind() == reflect.Func && a.Pointer() != b.Pointer() {
			t.Errorf("lent Conn.%s differs from the pooled one", name)
		}
	}
	want, have := *pooledConn, *lent
	want.redial, want.severVia, have.redial, have.severVia = nil, nil, nil, nil
	want.lease, want.become, want.raw, want.extra, want.redialRefused = nil, nil, false, nil, false
	have.lease = nil
	if !reflect.DeepEqual(want, have) {
		t.Errorf("lent Conn's connection fields differ from the pooled connection's:\n got %+v\nwant %+v", have, want)
	}
	if lent.lease == nil || lent.lease.entry != e || lent.lease.pool != p || lent.lease.device != "web1" {
		t.Errorf("lent Conn's lease = %+v, want a new lease on the entry", lent.lease)
	}
	if lent.become != nil || lent.raw || lent.extra != nil || lent.redialRefused {
		t.Errorf("lent Conn kept a borrow's state: become %v, raw %v, extra %v, redialRefused %v", lent.become, lent.raw, lent.extra, lent.redialRefused)
	}
	if lent.hostKeys != log {
		t.Error("lent Conn does not share the pooled connection's host key record")
	}
}
