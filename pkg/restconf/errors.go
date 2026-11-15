// Package restconf: a server's refusal, RFC 8040 section 7's
// ietf-restconf:errors, read into a structured error. Its text is the
// device's, which may echo configuration, so the whole answer is masked by
// pkg/netcfg before any of it is read, and a message masking cannot vouch
// for is left out rather than shown.
package restconf

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// errorsNamespace is ietf-restconf's namespace, which its errors container
// is in.
const errorsNamespace = "urn:ietf:params:xml:ns:yang:ietf-restconf"

// maxErrors is how many of a server's error entries a StatusError keeps.
const maxErrors = 16

// Error is one entry of a server's ietf-restconf:errors.
type Error struct {
	// Type is the error-type: transport, rpc, protocol or application.
	Type string
	// Tag is the error-tag, RFC 8040 section 7's vocabulary
	// (invalid-value, access-denied, data-missing and the rest).
	Tag string
	// Path is the error-path, the instance the server objected to, as the
	// server wrote it.
	Path string
	// Message is the error-message, the server's own words, masked.
	Message string
}

// StatusError is an answer whose status is not the one asked for.
type StatusError struct {
	// Status is the HTTP status code.
	Status int
	// Errors are the server's ietf-restconf:errors entries, at most
	// sixteen, masked.
	Errors []Error
	// Said is the server's answer as farside.Text quotes it, masked, when it
	// held no errors container; empty when masking could not vouch for it.
	Said string
}

// Error says the status and what the server said, each field quoted, so a
// control character in a device's answer is shown rather than acted on.
func (e *StatusError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "the device answered %d %s", e.Status, http.StatusText(e.Status))
	for _, x := range e.Errors {
		b.WriteString(": ")
		b.WriteString(farside.Text(x.Tag))
		if x.Path != "" {
			b.WriteString(" at " + farside.Text(x.Path))
		}
		if x.Message != "" {
			b.WriteString(", " + farside.Text(x.Message))
		}
	}
	if len(e.Errors) == 0 && e.Said != "" {
		b.WriteString(": " + e.Said)
	}
	return b.String()
}

// HasTag reports whether the server named tag in any of its errors.
func (e *StatusError) HasTag(tag string) bool {
	for _, x := range e.Errors {
		if x.Tag == tag {
			return true
		}
	}
	return false
}

// statusError reads a's body into a StatusError: its errors container in
// either encoding, masked first; or, when it holds none, its text as
// farside quotes it. A body masking refuses is named as withheld, and none
// of it is kept.
func statusError(a answer) *StatusError {
	e := &StatusError{Status: a.status}
	if len(a.body) == 0 {
		return e
	}
	masked, _, err := netcfg.MaskOutput(string(a.body))
	if err != nil {
		e.Said = "(the answer is withheld: masking cannot vouch for it)"
		return e
	}
	if a.isJSON() {
		e.Errors = errorsFromJSON([]byte(masked))
	} else {
		e.Errors = errorsFromXML([]byte(masked))
	}
	if len(e.Errors) == 0 {
		e.Said = farside.Text(masked)
	}
	return e
}

// errorEntry is one error entry in either encoding.
type errorEntry struct {
	Type    string `xml:"error-type" json:"error-type"`
	Tag     string `xml:"error-tag" json:"error-tag"`
	Path    string `xml:"error-path" json:"error-path"`
	Message string `xml:"error-message" json:"error-message"`
}

// entriesOf is entries as Errors, trimmed, at most maxErrors.
func entriesOf(entries []errorEntry) []Error {
	var out []Error
	for _, x := range entries {
		if len(out) == maxErrors {
			break
		}
		out = append(out, Error{
			Type:    strings.TrimSpace(x.Type),
			Tag:     strings.TrimSpace(x.Tag),
			Path:    strings.TrimSpace(x.Path),
			Message: strings.TrimSpace(x.Message),
		})
	}
	return out
}

// errorsFromXML reads an errors container (RFC 8040 section 7.1) whose root
// is in ietf-restconf's namespace; anything else holds none.
func errorsFromXML(body []byte) []Error {
	var doc struct {
		XMLName xml.Name
		Errors  []errorEntry `xml:"error"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil || doc.XMLName.Space != errorsNamespace || doc.XMLName.Local != "errors" {
		return nil
	}
	return entriesOf(doc.Errors)
}

// errorsFromJSON reads the JSON encoding of an errors container.
func errorsFromJSON(body []byte) []Error {
	var doc struct {
		Errors *struct {
			Error []errorEntry `json:"error"`
		} `json:"ietf-restconf:errors"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || doc.Errors == nil {
		return nil
	}
	return entriesOf(doc.Errors.Error)
}
