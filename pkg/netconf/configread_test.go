// Package netconf: unit tests of the reads an edit is judged by and of a session limited to
// a check, against the scripted device session_test.go defines, which reads
// the client's real framing.
package netconf

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
)

// helloWith is realHelloPrelude with its with-defaults capability replaced.
func helloWith(withDefaults string) string {
	const wd = `<capability>urn:ietf:params:netconf:capability:with-defaults:1.0?basic-mode=explicit&amp;also-supported=report-all-tagged,report-all</capability>`
	return strings.Replace(realHelloPrelude, wd, withDefaults, 1)
}

// candidateHello is realHelloPrelude advertising :candidate as well.
var candidateHello = strings.Replace(realHelloPrelude, "</capabilities>",
	"<capability>urn:ietf:params:netconf:capability:candidate:1.0</capability></capabilities>", 1)

func TestWithDefaults_ReadsTheServersModes(t *testing.T) {
	for name, tc := range map[string]struct {
		capability, want string
		asks             bool
	}{
		"explicit basic":     {`<capability>urn:ietf:params:netconf:capability:with-defaults:1.0?basic-mode=explicit</capability>`, WithDefaultsExplicit, false},
		"explicit also":      {`<capability>urn:ietf:params:netconf:capability:with-defaults:1.0?basic-mode=report-all&amp;also-supported=trim, explicit</capability>`, WithDefaultsExplicit, true},
		"report-all only":    {`<capability>urn:ietf:params:netconf:capability:with-defaults:1.0?basic-mode=report-all</capability>`, "report-all", false},
		"none":               {"", "", false},
		"unreadable query":   {`<capability>urn:ietf:params:netconf:capability:with-defaults:1.0?basic-mode=%zz</capability>`, "", false},
		"another capability": {`<capability>urn:ietf:params:netconf:capability:url:1.0?scheme=file</capability>`, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			d := &fakeDevice{hello: helloWith(tc.capability), respond: func(req string) string {
				return `<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="` + messageIDOf(req) + `"><data/></rpc-reply>`
			}}
			s := openAgainst(t, d, Options{})
			if got := s.WithDefaults(); got != tc.want {
				t.Errorf("WithDefaults() = %q, want %q", got, tc.want)
			}
			if _, err := s.ReadConfig(context.Background(), Running, ""); err != nil {
				t.Fatal(err)
			}
			if asked := strings.Contains(d.lastRequest(t), "<with-defaults"); asked != tc.asks {
				t.Errorf("asked for with-defaults = %v, want %v: %s", asked, tc.asks, d.lastRequest(t))
			}
		})
	}
}

func TestReadConfig_ReturnsDataReadWholeWithTheRepliesPrefixes(t *testing.T) {
	d := &fakeDevice{hello: realHelloPrelude, respond: func(req string) string {
		return `<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" xmlns:t="urn:iana" message-id="` + messageIDOf(req) +
			`"><data><interfaces xmlns="urn:if"><interface><name>lo1</name><type>t:softwareLoopback</type></interface></interfaces></data></rpc-reply>`
	}}
	s := openAgainst(t, d, Options{})
	els, err := s.ReadConfig(context.Background(), Candidate, `<interfaces xmlns="urn:if"/>`)
	if err != nil {
		t.Fatal(err)
	}
	req := d.lastRequest(t)
	if !strings.Contains(req, `<get-config><source><candidate/></source><filter type="subtree"><interfaces xmlns="urn:if"/></filter>`) {
		t.Errorf("request %s", req)
	}
	if len(els) != 1 || els[0].Name.Local != "interfaces" {
		t.Fatalf("elements %+v", els)
	}
	typ := els[0].Children[0].Children[1]
	if _, uri, ok := typ.ValuePrefix(); !ok || uri != "urn:iana" {
		t.Errorf("a prefix the reply declared outside data did not resolve: %q %v", uri, ok)
	}
}

func TestReadConfig_RefusesAReplyWithNoUsableData(t *testing.T) {
	for name, reply := range map[string]string{
		"no data":            `<ok/>`,
		"data in another ns": `<data xmlns="urn:other"/>`,
	} {
		t.Run(name, func(t *testing.T) {
			d := &fakeDevice{hello: realHelloPrelude, respond: func(req string) string {
				return `<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="` + messageIDOf(req) + `">` + reply + `</rpc-reply>`
			}}
			if _, err := openAgainst(t, d, Options{}).ReadConfig(context.Background(), Running, ""); err == nil {
				t.Error("accepted")
			}
		})
	}
	d := &fakeDevice{hello: realHelloPrelude, respond: func(req string) string {
		return `<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="` + messageIDOf(req) + `"><data><a>x<b/></a></data></rpc-reply>`
	}}
	if _, err := openAgainst(t, d, Options{}).ReadConfig(context.Background(), Running, ""); err == nil {
		t.Error("mixed content accepted")
	}
	failing := &fakeDevice{hello: realHelloPrelude, respond: func(req string) string { return fmtError(req, "access-denied") }}
	if _, err := openAgainst(t, failing, Options{}).ReadConfig(context.Background(), Running, ""); err == nil {
		t.Error("an rpc-error accepted")
	}
}

// fmtError is an rpc-error reply to req carrying tag.
func fmtError(req, tag string) string {
	return `<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="` + messageIDOf(req) +
		`"><rpc-error><error-type>protocol</error-type><error-tag>` + tag + `</error-tag><error-severity>error</error-severity></rpc-error></rpc-reply>`
}

func TestEditFilter_SelectsBySuperSetAndNeverByValue(t *testing.T) {
	for name, tc := range map[string]struct{ content, want string }{
		"a leaf under a container selects the container whole": {
			`<native xmlns="urn:n"><hostname>edge</hostname></native>`, `<native xmlns="urn:n"/>`},
		"containers followed by name to the list": {
			`<native xmlns="urn:n"><interface><Loopback><name>1</name></Loopback><Loopback><name>2</name></Loopback></interface></native>`,
			`<native xmlns="urn:n"><interface><Loopback/></interface></native>`},
		"an operation stops the descent": {
			`<native xmlns="urn:n"><interface xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0" nc:operation="replace"><Loopback><name>1</name></Loopback></interface></native>`,
			`<native xmlns="urn:n"><interface/></native>`},
		"siblings and an augment's namespace": {
			`<a xmlns="urn:a"><b><c>1</c></b><d><e xmlns="urn:x"><f>2</f></e></d><b><g>3</g></b></a><z xmlns="urn:z"/>`,
			`<a xmlns="urn:a"><b/><d><e xmlns="urn:x"/></d></a><z xmlns="urn:z"/>`},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := EditFilter(tc.content, false)
			if err != nil || got != tc.want {
				t.Errorf("EditFilter() = %s, %v\nwant %s", got, err, tc.want)
			}
		})
	}
	if got, err := EditFilter(`<a xmlns="urn:a"><b>1</b></a>`, true); got != "" || err != nil {
		t.Errorf("default-operation replace = %q, %v, want the whole datastore", got, err)
	}
	for _, bad := range []string{`<a>`, ``, `  `} {
		if _, err := EditFilter(bad, false); err == nil {
			t.Errorf("EditFilter(%q) accepted", bad)
		}
	}
}

func TestOperation_ReadsTheBaseNamespacesAttribute(t *testing.T) {
	els, err := yangdoc.Parse([]byte(`<a xmlns="urn:a" xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0" xmlns:o="urn:other" o:operation="x"><b nc:operation="delete"/></a>`))
	if err != nil {
		t.Fatal(err)
	}
	if got := Operation(els[0]); got != "" {
		t.Errorf("an operation attribute in another namespace read as %q", got)
	}
	if got := Operation(els[0].Children[0]); got != "delete" {
		t.Errorf("Operation() = %q", got)
	}
}

// TestLimitToCheck_RefusesWithNothingWritten: the scripted device records
// every request it reads, so a refused RPC leaving none proves no byte of it
// was sent.
func TestLimitToCheck_RefusesWithNothingWritten(t *testing.T) {
	d := okDevice(candidateHello)
	s := openAgainst(t, d, Options{})
	s.LimitToCheck()
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"commit":       func() error { return s.Commit(ctx) },
		"edit running": func() error { return s.EditConfig(ctx, EditConfigRequest{Config: `<a xmlns="urn:a"/>`}) },
		"lock running": func() error { return s.Lock(ctx) },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "nothing was sent") {
			t.Errorf("%s = %v", name, err)
		}
	}
	d.mu.Lock()
	sent := len(d.requests)
	d.mu.Unlock()
	if sent != 0 {
		t.Fatalf("%d requests reached the device: %v", sent, d.requests)
	}
	if got := rpcName("<commit/>"); got != "commit" {
		t.Errorf("rpcName = %q", got)
	}
	c := openAgainst(t, okDevice(candidateHello), Options{Target: Candidate})
	c.LimitToCheck()
	for name, call := range map[string]func() error{
		"lock":     func() error { return c.Lock(ctx) },
		"edit":     func() error { return c.EditConfig(ctx, EditConfigRequest{Config: `<a xmlns="urn:a"/>`}) },
		"validate": func() error { return c.Validate(ctx) },
		"discard":  func() error { return c.DiscardChanges(ctx) },
		"unlock":   func() error { return c.Unlock(ctx) },
		"close":    func() error { return c.Close(ctx) },
	} {
		if err := call(); err != nil && strings.Contains(err.Error(), "nothing was sent") {
			t.Errorf("%s was refused on a check session: %v", name, err)
		}
	}
}

func TestLockHolder(t *testing.T) {
	held := RPCErrors{{Tag: "lock-denied", Info: `<session-id xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">42</session-id>`}}
	if id, ok := LockHolder(held); !ok || id != "42" {
		t.Errorf("LockHolder(held) = %q, %v", id, ok)
	}
	prefixed := RPCErrors{{Tag: "operation-failed"}, {Tag: "lock-denied", Info: `<nc:session-id>0</nc:session-id>`}}
	if id, ok := LockHolder(prefixed); !ok || id != "0" {
		t.Errorf("LockHolder(prefixed) = %q, %v", id, ok)
	}
	if id, ok := LockHolder(RPCErrors{{Tag: "lock-denied"}}); !ok || id != "" {
		t.Errorf("LockHolder(no info) = %q, %v", id, ok)
	}
	if _, ok := LockHolder(RPCErrors{{Tag: "in-use"}}); ok {
		t.Error("in-use read as lock-denied")
	}
	if _, ok := LockHolder(errors.New("lost")); ok {
		t.Error("a broken stream read as lock-denied")
	}
}
