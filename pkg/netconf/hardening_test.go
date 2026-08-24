package netconf

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
)

// TestValidateName_RefusesEverythingThatIsNotAnNCName covers this
// package's XML injection surface for the half that cannot be escaped.
// A name becomes a tag, and escaping "<" inside a tag name produces a
// well-formed document addressing something nobody asked for, so the
// only sufficient mechanism is refusal.
func TestValidateName_RefusesEverythingThatIsNotAnNCName(t *testing.T) {
	hostile := []struct {
		name  string
		value string
	}{
		{"closes its own tag", "native><evil"},
		{"opens an attribute", `native operation="delete"`},
		{"introduces a namespace prefix", "nc:operation"},
		{"a bare angle bracket", "<native"},
		{"an ampersand", "nat&ive"},
		{"a quote", `nat"ive`},
		{"a slash", "native/interface"},
		{"whitespace", "native interface"},
		{"a newline", "native\ninterface"},
		{"empty", ""},
		{"starts with a digit", "8990"},
		{"starts with a hyphen", "-native"},
		{"a NUL byte", "nat\x00ive"},
	}
	for _, tc := range hostile {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateName("element", tc.value); err == nil {
				t.Fatalf("validateName(%q) = nil, want a refusal", tc.value)
			}
		})
	}

	for _, ok := range []string{"native", "_private", "Loopback", "ietf-interfaces", "a.b-c_d", "GigabitEthernet1"} {
		if err := validateName("element", ok); err != nil {
			t.Errorf("validateName(%q) = %v, want nil: this is an ordinary YANG node name", ok, err)
		}
	}
}

// TestNestElements_RefusesAHostileNameRatherThanEscapingIt proves the
// refusal reaches the actual construction path, not just the validator
// in isolation: a Path is caller data, and on the Collection side it
// comes from a runbook.
func TestNestElements_RefusesAHostileNameRatherThanEscapingIt(t *testing.T) {
	p := datastore.Path{Elem: []datastore.PathElem{
		{Name: `native><config operation="delete"><native`},
	}}
	if _, err := subtreeFilter(p); err == nil {
		t.Fatal("subtreeFilter() = nil error, want a refusal of a name carrying markup")
	}
	if _, err := wrapInPath(p, "<x/>", datastore.OperationMerge); err == nil {
		t.Fatal("wrapInPath() = nil error, want a refusal of a name carrying markup")
	}
}

// TestNestElements_EscapesKeyValues covers the other half: a key VALUE
// is character data, where escaping genuinely is the complete answer,
// so a hostile value must be neutralized rather than refused. Refusing
// it would be wrong: an interface description legitimately contains
// angle brackets and ampersands.
func TestNestElements_EscapesKeyValues(t *testing.T) {
	p := datastore.Path{Elem: []datastore.PathElem{
		{Name: "Loopback", Keys: map[string]string{"name": `8990</name></Loopback><evil op="delete"/>`}},
	}}
	got, err := subtreeFilter(p)
	if err != nil {
		t.Fatalf("subtreeFilter() error = %v, want nil: a value is escapable and must not be refused", err)
	}
	if strings.Contains(got, "<evil") {
		t.Fatalf("subtreeFilter() = %q, want the hostile value escaped rather than emitted as markup", got)
	}
	if !strings.Contains(got, "&lt;evil") {
		t.Errorf("subtreeFilter() = %q, want the angle bracket escaped", got)
	}
}

// TestNestElements_IsDeterministic pins the stable key ordering. Two
// identical requests that differed on the wire between runs would make
// a captured fixture untestable and a diff of two runs unreadable.
func TestNestElements_IsDeterministic(t *testing.T) {
	p := datastore.Path{Elem: []datastore.PathElem{
		{Name: "entry", Keys: map[string]string{"zeta": "1", "alpha": "2", "mid": "3"}},
	}}
	first, err := subtreeFilter(p)
	if err != nil {
		t.Fatalf("subtreeFilter() error = %v", err)
	}
	for i := 0; i < 50; i++ {
		again, err := subtreeFilter(p)
		if err != nil {
			t.Fatalf("subtreeFilter() error = %v", err)
		}
		if again != first {
			t.Fatalf("subtreeFilter() is not deterministic:\n%s\nvs\n%s", first, again)
		}
	}
	if !strings.Contains(first, "<alpha>2</alpha><mid>3</mid><zeta>1</zeta>") {
		t.Errorf("subtreeFilter() = %q, want keys in sorted order", first)
	}
}

func TestCheckDepth_BoundsNesting(t *testing.T) {
	deep := strings.Repeat("<a>", 100) + strings.Repeat("</a>", 100)

	if err := checkDepth([]byte(deep), 200); err != nil {
		t.Errorf("checkDepth(depth 100, max 200) = %v, want nil", err)
	}
	err := checkDepth([]byte(deep), 50)
	if err == nil {
		t.Fatal("checkDepth(depth 100, max 50) = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "nests more than 50") {
		t.Errorf("checkDepth() error = %q, want it to name the bound", err)
	}
}

// TestCheckDepth_IsNotRedundantWithTheByteBound is the measurement that
// justifies having a second bound at all. encoding/xml's tokenizer
// keeps a heap entry per open element, so a document made entirely of
// open tags declares far more nesting than its size suggests; the byte
// bound alone does not bound memory.
func TestCheckDepth_IsNotRedundantWithTheByteBound(t *testing.T) {
	// 3 bytes per element. A modest 30 KiB document nests 10,000 deep,
	// which is already past encoding/xml's own unmarshal limit and would
	// be waved through by any byte bound sized for real configuration.
	const elements = 10000
	doc := strings.Repeat("<a>", elements) + strings.Repeat("</a>", elements)

	if len(doc) > defaultMaxMessageBytes {
		t.Fatalf("the fixture is %d bytes, which the byte bound would catch on its own; this test would then prove nothing", len(doc))
	}
	if err := checkDepth([]byte(doc), defaultMaxDepth); err == nil {
		t.Fatalf("checkDepth() = nil for a %d-byte document nesting %d deep, which is exactly the case the byte bound cannot see", len(doc), elements)
	}
}

// TestEncodingXMLDoesNotExpandEntities verifies by execution, in this
// repository's own toolchain, the claim this package's bounds are built
// around: Go's encoding/xml does not expand DTD-declared entities, so
// billion-laughs and external-entity documents fail at the parser
// rather than needing a mitigation here. Asserting it rather than
// citing it is what keeps the claim true if the standard library ever
// changes.
func TestEncodingXMLDoesNotExpandEntities(t *testing.T) {
	billionLaughs := `<?xml version="1.0"?><!DOCTYPE lolz [` +
		`<!ENTITY lol "lol">` +
		`<!ENTITY lol1 "&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;">` +
		`<!ENTITY lol2 "&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;">` +
		`]><lolz>&lol2;</lolz>`
	externalEntity := `<?xml version="1.0"?><!DOCTYPE foo [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><foo>&xxe;</foo>`

	// Driven through the DECODER directly, not through checkWellFormed:
	// this package's own DOCTYPE refusal would fire first and the claim
	// under test here would go unexercised, which is a test that passes
	// while proving nothing.
	for _, tc := range []struct{ name, doc string }{
		{"billion laughs", billionLaughs},
		{"external entity", externalEntity},
	} {
		dec := xml.NewDecoder(strings.NewReader(tc.doc))
		var chars int
		var err error
		for {
			var tok xml.Token
			tok, err = dec.Token()
			if err != nil {
				break
			}
			if cd, ok := tok.(xml.CharData); ok {
				chars += len(cd)
			}
		}
		if err == io.EOF {
			t.Fatalf("%s: the decoder read the whole document without objecting, so entities WERE expanded and this package needs its own mitigation", tc.name)
		}
		if !strings.Contains(err.Error(), "invalid character entity") {
			t.Errorf("%s: decoder stopped with %v, want an invalid-character-entity error", tc.name, err)
		}
		if chars != 0 {
			t.Errorf("%s: the decoder produced %d bytes of character data before stopping, want 0", tc.name, chars)
		}
		t.Logf("%-16s refused by encoding/xml with: %v", tc.name, err)
	}

	// And this package refuses both anyway, one step earlier, because a
	// DOCTYPE has no legitimate place in a configuration fragment and
	// relying on a standard library behavior that could change is not a
	// mitigation.
	for _, doc := range []string{billionLaughs, externalEntity} {
		if err := checkWellFormed([]byte(doc), defaultMaxDepth); err == nil {
			t.Error("checkWellFormed() = nil, want a refusal")
		}
	}
}

func TestCheckWellFormed_RefusesUnusablePayloads(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"a DOCTYPE declaration", `<!DOCTYPE cfg><native/>`, "DOCTYPE"},
		{"an unclosed element", `<native><hostname>x</hostname>`, "malformed XML"},
		{"a mismatched close", `<native></interface>`, "malformed XML"},
		{"no elements at all", `   `, "no XML elements"},
		{"plain text", `not xml at all`, "no XML elements"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkWellFormed([]byte(tc.input), defaultMaxDepth)
			if err == nil {
				t.Fatalf("checkWellFormed(%q) = nil, want one containing %q", tc.input, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("checkWellFormed(%q) error = %q, want it to contain %q", tc.input, err, tc.want)
			}
		})
	}

	valid := `<native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native"><hostname>Cat8kv</hostname></native>`
	if err := checkWellFormed([]byte(valid), defaultMaxDepth); err != nil {
		t.Errorf("checkWellFormed(a real configuration fragment) = %v, want nil", err)
	}
}
