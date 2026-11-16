// Package gnmi: the text a device sends held to a bound wherever an error
// quotes it, through the decoders Get and Subscribe read every answer with,
// so a megabyte name, origin or path in an answer never becomes a megabyte
// error.
package gnmi

import (
	"strings"
	"testing"
	"unicode/utf8"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

// maxErrorBytes is more than any bounded error here needs: one quoted
// bound, a second one beside it, and the sentence around them.
const maxErrorBytes = 4 * maxQuotedBytes

// TestDecode_ADevicesLongTextIsBoundedInTheError sends, in a notification,
// a long origin, a long name holding control characters, and a long valid
// path whose value breaks a rule, and holds each error to the bound, its
// control characters escaped rather than sent.
func TestDecode_ADevicesLongTextIsBoundedInTheError(t *testing.T) {
	long := strings.Repeat("o", 1<<20)
	hostile := strings.Repeat("a\x1b[2J", 1<<18)
	var elems []*gpb.PathElem
	for range MaxElems {
		elems = append(elems, &gpb.PathElem{Name: strings.Repeat("n", 200), Key: map[string]string{"k": strings.Repeat("v", MaxKeyBytes)}})
	}
	for name, n := range map[string]*gpb.Notification{
		"origin":     {Prefix: &gpb.Path{Origin: long}},
		"name":       {Update: []*gpb.Update{{Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: hostile}}}}}},
		"key name":   {Update: []*gpb.Update{{Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "a", Key: map[string]string{hostile: "v"}}}}}}},
		"value path": {Update: []*gpb.Update{{Path: &gpb.Path{Elem: elems}, Value: &gpb.Value{Value: []byte("x")}}}},
		"bad value":  {Update: []*gpb.Update{{Path: &gpb.Path{Elem: elems}, Val: &gpb.TypedValue{Value: &gpb.TypedValue_JsonVal{JsonVal: []byte("{")}}}}},
	} {
		_, err := decodeNotification(n)
		if err == nil {
			t.Errorf("%s: decoded, want it refused", name)
			continue
		}
		msg := err.Error()
		if len(msg) > maxErrorBytes || !utf8.ValidString(msg) || strings.ContainsRune(msg, 0x1b) {
			t.Errorf("%s: the error is %d bytes (bound %d), or holds an unescaped control character", name, len(msg), maxErrorBytes)
		}
		if !strings.Contains(msg, "...") {
			t.Errorf("%s: %.200s, want the cut marked", name, msg)
		}
	}
}

// TestQuoted_KeepsShortTextWhole quotes short text as strconv.Quote does and
// cuts long text on a character boundary.
func TestQuoted_KeepsShortTextWhole(t *testing.T) {
	if got := quoted("eth\x00"); got != `"eth\x00"` {
		t.Errorf("quoted = %s", got)
	}
	long := strings.Repeat("é", maxQuotedBytes)
	if got := quoted(long); !strings.HasSuffix(got, `"...`) || !utf8.ValidString(got) || len(got) > maxQuotedBytes+5 {
		t.Errorf("quoted of %d bytes = %d bytes ending %q", len(long), len(got), got[len(got)-6:])
	}
	if got := shown(Path{Elems: []Elem{{Name: "a"}}}); got != "/a" {
		t.Errorf("shown = %s", got)
	}
}
