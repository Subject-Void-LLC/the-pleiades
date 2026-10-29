// Package farside: finding the far side's words inside a structured answer,
// a JSON error object or an XML fault, before falling back to the whole body.
package farside

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"slices"
	"strings"
)

// jsonKeys are the fields an API's JSON error puts its reason in, most
// specific first. Each is the documented field of a real API: description
// (Cisco CML), detail (RFC 9457 problem details, FastAPI, Catalyst Center),
// message (Kubernetes Status, Docker and Podman, Cloudflare's errors[],
// Spring), error_description (OAuth 2.0), msg (FastAPI's validation list),
// error (a string, or an object holding message), errors (HashiCorp Vault's
// list of strings, Cloudflare's list of objects) and response (Catalyst
// Center's error wrapper).
var jsonKeys = []string{"description", "detail", "message", "error_description", "msg", "error", "errors", "response"}

// maxJSONDepth bounds how far into nested objects and lists jsonWords looks:
// deep enough for errors[0].message and response.message, and no deeper, so
// a field named message in some unrelated nested record is not taken for
// the reason.
const maxJSONDepth = 3

// jsonWords returns the first non-blank text under jsonKeys in body, a JSON
// object or list, and false when body is not JSON or names no reason.
func jsonWords(body string) (string, bool) {
	var v any
	if json.Unmarshal([]byte(body), &v) != nil {
		return "", false
	}
	w := findJSON(v, 0)
	return w, w != ""
}

// findJSON looks for a reason in v at the given depth: in an object, the
// first of jsonKeys holding a string or a value holding one; in a list, its
// first element.
func findJSON(v any, depth int) string {
	if depth > maxJSONDepth {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case []any:
		if len(t) > 0 {
			return findJSON(t[0], depth+1)
		}
	case map[string]any:
		for _, k := range jsonKeys {
			if w := findJSON(field(t, k), depth+1); w != "" {
				return w
			}
		}
	}
	return ""
}

// field returns obj's value under key, matched exactly first and then
// without regard to case (ASP.NET and AWS write "Message").
func field(obj map[string]any, key string) any {
	if v, ok := obj[key]; ok {
		return v
	}
	for k, v := range obj {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return nil
}

// xmlFields are the elements an XML fault puts its reason in, most specific
// first: a NETCONF rpc-error's error-message (RFC 6241 section 4.3), a
// WS-Management fault's Message (the WSManFault detail WinRM sends), a SOAP
// 1.2 fault's Reason Text, and a SOAP 1.1 fault's faultstring.
var xmlFields = []string{"error-message", "Message", "Text", "faultstring"}

// skipped are HTML elements whose text is code, never words.
var skipped = map[string]bool{"script": true, "style": true}

// xmlWords returns the reason in body, an XML or HTML document: the text of
// the first of xmlFields it holds, or else all of its text (an HTML error
// page's title and heading), and false when no text could be read from it.
// It reads leniently, as HTML is written, so a proxy's error page reads as
// its words rather than as its markup.
func xmlWords(body string) (string, bool) {
	d := xml.NewDecoder(strings.NewReader(body))
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	found := map[string]*strings.Builder{}
	// done holds each reason element already closed, so only its first
	// occurrence is read: a SOAP Reason's first Text, not every language's.
	done := map[string]bool{}
	var all strings.Builder
	var stack []string
	for {
		tok, err := d.Token()
		if err != nil {
			// The end of the document, or markup this reader cannot follow:
			// what was read so far is still the far side's words.
			if !errors.Is(err, io.EOF) && all.Len() == 0 {
				return "", false
			}
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			if _, seen := found[t.Name.Local]; !seen && isField(t.Name.Local) {
				found[t.Name.Local] = &strings.Builder{}
			}
		case xml.EndElement:
			if _, ok := found[t.Name.Local]; ok {
				done[t.Name.Local] = true
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if inSkipped(stack) {
				continue
			}
			all.WriteString(" ")
			all.Write(t)
			// Text counts toward every reason element it is inside, so a
			// Message holding nested markup is read whole.
			for _, name := range stack {
				if b, ok := found[name]; ok && !done[name] {
					b.WriteString(" ")
					b.Write(t)
				}
			}
		}
	}
	for _, name := range xmlFields {
		if b, ok := found[name]; ok && strings.TrimSpace(b.String()) != "" {
			return b.String(), true
		}
	}
	text := strings.TrimSpace(all.String())
	return text, text != ""
}

// isField reports whether name is one of xmlFields.
func isField(name string) bool {
	return slices.Contains(xmlFields, name)
}

// inSkipped reports whether the innermost open elements include one whose
// text is code.
func inSkipped(stack []string) bool {
	for _, name := range stack {
		if skipped[strings.ToLower(name)] {
			return true
		}
	}
	return false
}
