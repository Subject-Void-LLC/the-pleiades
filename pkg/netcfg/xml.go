// Package netcfg: masking a configuration a device returns as XML (a
// NETCONF get-config reply's <data>, as net.netconf.config's backup records
// it). The text masker reads configuration lines; this reads elements, so a
// secret is found by the YANG leaf that holds it (IOS XE native, Junos,
// OpenConfig and the IETF models name them alike), and every byte that is not
// a secret stays where it was.
package netcfg

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// MaxXMLBytes is the largest document MaskXML reads. A device's whole
// configuration is a few megabytes; a document past this is refused rather
// than read without bound.
const MaxXMLBytes = 64 << 20

// MaxXMLDepth is the deepest element nesting MaskXML follows. YANG models
// nest a few tens of levels; anything deeper is refused, which keeps the
// walk's stack bounded whatever a device sends.
const MaxXMLDepth = 256

// withheldComment replaces every XML comment and processing instruction
// (other than the XML declaration): their text is free-form, so nothing can
// vouch that a secret is not in it, and neither is configuration.
const withheldComment = "<!-- withheld by pleiades: comments are not recorded -->"

// secretTokens are the words that make a leaf's name a secret's name
// wherever they appear in it: enable-secret, encrypted-password, secret-key,
// auth-password, pre-shared-key-string, passphrase.
var secretTokens = map[string]bool{
	"password": true, "passwd": true, "secret": true, "passphrase": true, "psk": true,
}

// secretKeyNames are the leaf names ending in "key" that do not hold a
// secret: an identifier or a reference to one. Every other name whose last
// word is "key" (key, auth-key, md5-key, pre-shared-key, key-string's
// parent) is read as holding one.
var secretKeyNames = map[string]bool{
	"key-id": true, "keyid": true, "key-type": true, "public-key": true, "host-key": true,
}

// secretExactNames are the names that hold a key but match no token and do
// not end in "key": RFC 8177 ietf-key-chain's key-string container and its
// keystring and hexadecimal-string leaves (the IOS XE native model's
// key-string too), and OpenConfig MACsec's key-clear-text.
var secretExactNames = map[string]bool{
	"key-string": true, "keystring": true, "hexadecimal-string": true, "key-clear-text": true,
}

// metadataNames are the elements a secret's container also holds that
// describe the secret rather than hold it: its encryption type, a key's id,
// its algorithm, its lifetime. They stay readable, so a backup still says
// how a key is stored, and secrecy does not pass into one (a lifetime
// container's dates are not a key).
var metadataNames = map[string]bool{
	"encryption": true, "encryption-type": true, "enctype": true, "encrypt": true, "type": true,
	"id": true, "key-id": true, "keyid": true, "algorithm": true, "crypto-algorithm": true,
	"cryptographic-algorithm": true, "hash-algorithm": true, "hash-algo": true, "level": true,
	"mode": true, "format": true, "auth-type": true, "enc-type": true,
	"start-time": true, "end-time": true, "lifetime": true, "send-lifetime": true,
	"accept-lifetime": true, "send-accept-lifetime": true, "valid-date-time": true,
	"expiration-date-time": true,
}

// identifierNames are the leaves that name or address something rather
// than hold a secret, kept readable when a secret-named element is a
// grandparent or further up (IOS XE native's key chain name under <key>, an
// IKE peer's address under <key>). Directly under a secret-named element,
// or anywhere in an SNMP subtree, they are masked as before: an SNMP
// community list is keyed by <name>, and that name is the community string.
var identifierNames = map[string]bool{
	"name": true, "address": true, "mask": true, "vrf": true,
}

// cryptShaped matches a value in crypt's $id$ form: a Linux or Junos hash
// ($1$, $5$, $6$, $y$) and Junos' reversible $9$ encoding, which is a
// secret wherever it appears whatever the leaf is called.
var cryptShaped = regexp.MustCompile(`^\$[0-9A-Za-z]{1,2}\$`)

// nameWords splits an element or attribute name into its lowercase words,
// at hyphens, underscores and dots, as YANG and Junos spell names.
func nameWords(name string) []string {
	return strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	})
}

// secretName reports whether an element or attribute called name holds a
// secret: it is one of secretExactNames, one of secretTokens is a word of
// it, its last word is "key" and it is not one of secretKeyNames, or, inside
// an SNMP subtree, it names a
// community (an SNMP community string is a password).
func secretName(name string, inSNMP bool) bool {
	lower := strings.ToLower(name)
	if secretExactNames[lower] {
		return true
	}
	ws := nameWords(lower)
	if len(ws) == 0 {
		return false
	}
	for _, w := range ws {
		if secretTokens[w] || (inSNMP && strings.HasPrefix(w, "community")) {
			return true
		}
	}
	return ws[len(ws)-1] == "key" && !secretKeyNames[lower]
}

// xmlFrame is one open element during MaskXML's walk.
type xmlFrame struct {
	// local is the element's name without its namespace prefix, lowercase.
	local string
	// secret is set when the element's own name is a secret's name.
	secret bool
	// inSecret is set when a secret-named element encloses this one with no
	// metadata-named element between them: secrecy passes down through every
	// descendant (RFC 8177's key/key-string/keystring), stopping only at
	// metadata such as a lifetime or an algorithm.
	inSecret bool
	// inSNMP is set inside any element whose name mentions snmp.
	inSNMP bool
	// hasChild is set once a child element opens, making the element a
	// container rather than a leaf.
	hasChild bool
	// spans are the byte ranges of the element's own text in the input.
	spans [][2]int
	// text is the element's own text, decoded, in order.
	text strings.Builder
}

// xmlEdit replaces input[start:end] with text.
type xmlEdit struct {
	start, end int
	text       string
}

// MaskXML masks every secret in doc, an XML configuration such as a NETCONF
// get-config reply's <data> content (which may hold several top-level
// elements), and returns each secret it replaced, once each, for the run's
// masking set. Every other byte stays where it was.
//
// A leaf's text is a secret when its name is one (secretName: password,
// secret, key, auth-key, pre-shared-key, encrypted-password, an SNMP
// community, RFC 8177's key-string), when an enclosing element's is and the
// leaf is not one of metadataNames (IOS XE's
// <password><encryption>0</encryption><text>x</text> keeps its encryption
// type and masks x; RFC 8177's key/key-string/keystring masks the keystring
// two levels down), or when it is crypt-shaped. Every
// other leaf is read by the configuration text masker too (MaskChecked), so
// a leaf carrying CLI text (a vendor's text-format configuration) is masked
// line by line, and each value of known, the run's own credential, is masked
// wherever it appears whole. Comments and processing instructions are
// withheld.
//
// It fails closed: an error, and none of the text, when doc is not
// well-formed, holds a DOCTYPE or another directive, nests past MaxXMLDepth,
// exceeds MaxXMLBytes, has an attribute whose name or crypt-shaped value
// marks it as a secret (an attribute cannot be masked in place), carries a
// known secret in an attribute, or holds a leaf the text masker cannot vouch
// for. No error ever quotes the document's text.
func MaskXML(doc string, known ...string) (masked string, found []string, err error) {
	if len(doc) > MaxXMLBytes {
		return "", nil, fmt.Errorf("the document is %d bytes, more than the %d masking reads", len(doc), MaxXMLBytes)
	}
	w := xmlWalk{doc: doc, known: usableKnown(known), seen: map[string]bool{}}
	if err := w.run(); err != nil {
		return "", nil, err
	}
	masked = w.apply()
	// The edits replace only text between markup, so the copy reads as the
	// original does; checked all the same, since a copy that does not parse
	// would be a backup nobody can restore from.
	if err := wellFormed(masked); err != nil {
		return "", nil, fmt.Errorf("masking produced a document that does not read back: %w", err)
	}
	return masked, w.found, nil
}

// usableKnown keeps the known secrets long enough to mask by value
// (MinLearnedLength), as the engine's own masking set does, longest first so
// a secret containing another is replaced whole.
func usableKnown(known []string) []string {
	var out []string
	for _, k := range known {
		if len(k) >= MinLearnedLength {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// xmlWalk is one MaskXML call's state.
type xmlWalk struct {
	doc   string
	known []string
	stack []*xmlFrame
	edits []xmlEdit
	found []string
	seen  map[string]bool
}

// run reads doc token by token, recording an edit for each secret.
func (w *xmlWalk) run() error {
	d := newDecoder(w.doc)
	for {
		start := int(d.InputOffset())
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return xmlError(err)
		}
		end := int(d.InputOffset())
		if err := w.token(tok, start, end); err != nil {
			return err
		}
	}
	if len(w.stack) != 0 {
		return errors.New("the document ends inside an element")
	}
	return nil
}

// record adds secret to found once.
func (w *xmlWalk) record(secret string) {
	if !w.seen[secret] {
		w.seen[secret] = true
		w.found = append(w.found, secret)
	}
}

// replaceText records edits writing text over a frame's text spans: the
// first span takes text, escaped, and the rest are emptied.
func (w *xmlWalk) replaceText(f *xmlFrame, text string) {
	for i, s := range f.spans {
		t := ""
		if i == 0 {
			t = escapeXMLText(text)
		}
		w.edits = append(w.edits, xmlEdit{start: s[0], end: s[1], text: t})
	}
}

// leafText decides what a closed element's own text becomes, given its
// parent (nil at the top level), and records the edit and secrets found.
func (w *xmlWalk) leafText(f, parent *xmlFrame) error {
	text := f.text.String()
	value := strings.TrimFunc(text, unicode.IsSpace)
	if value == "" || strings.HasPrefix(value, MaskPrefix) {
		return nil
	}
	// A secret-named element's own text is masked even beside children;
	// the other two rules read leaves only.
	secretLeaf := f.secret || (!f.hasChild && (cryptShaped.MatchString(value) || underSecret(f, parent)))
	if secretLeaf {
		w.record(value)
		w.replaceText(f, Placeholder(value))
		return nil
	}
	// Any other text: the run's own secrets by value, then the
	// configuration text masker, which a leaf holding CLI lines needs.
	out := text
	for _, k := range w.known {
		if strings.Contains(out, k) {
			w.record(k)
			out = strings.ReplaceAll(out, k, Placeholder(k))
		}
	}
	lines, more, err := MaskChecked(out)
	if err != nil {
		return fmt.Errorf("the text of a <%s> element: %w", f.local, err)
	}
	for _, m := range more {
		w.record(m)
	}
	if lines != text {
		w.replaceText(f, lines)
	}
	return nil
}

// underSecret reports whether leaf f, whose parent is parent, is masked for
// lying inside a secret-named element: any leaf that is not metadata, except
// an identifier (identifierNames) whose secret-named ancestor is further up
// than its parent, outside an SNMP subtree.
func underSecret(f, parent *xmlFrame) bool {
	if !f.inSecret {
		return false
	}
	// inSecret implies a parent: a top-level element encloses nothing above.
	if parent.secret || f.inSNMP {
		return true
	}
	return !identifierNames[f.local]
}

// apply writes the edits into doc.
func (w *xmlWalk) apply() string {
	sort.Slice(w.edits, func(i, j int) bool { return w.edits[i].start < w.edits[j].start })
	var b strings.Builder
	at := 0
	for _, e := range w.edits {
		b.WriteString(w.doc[at:e.start])
		b.WriteString(e.text)
		at = e.end
	}
	b.WriteString(w.doc[at:])
	return b.String()
}
