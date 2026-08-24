package netconf

import (
	"fmt"
	"strings"
)

// RPCError is one <rpc-error> element from a server's reply, decoded
// into its RFC 6241 section 4.3 fields rather than flattened into a
// string. An operator needs to know WHICH subtree the device rejected
// and why, and a device that reports "/native/interface/Loopback[8990]"
// has told them something a message alone has not.
//
// # Every field here is optional, and one of them being optional is a trap
//
// RFC 6241 marks error-message, error-path and error-info as optional,
// and a real Cisco IOS XE device exercises that: an unknown-element
// error carries NO error-message at all, only the tag, the type, a path
// and a bad-element. That was observed directly, not inferred from the
// RFC (pkg/remoteexec/live_subsystem_probe_test.go). A client that
// rendered only error-message would print an empty reason for a real
// failure, which is why Error below is built to degrade through the
// fields that are present rather than to assume any single one is.
type RPCError struct {
	// Type is the protocol layer the error came from: transport, rpc,
	// protocol or application.
	Type string

	// Tag is the machine-readable error condition, such as
	// "unknown-element", "invalid-value" or "access-denied". This is
	// the field to branch on; Message is for a human.
	Tag string

	// Severity is "error" or "warning".
	Severity string

	// Path is the absolute XPath of the element that caused the error,
	// whitespace-trimmed. A real device pretty-prints this element,
	// arriving as "\n    /rpc\n  ", so it is trimmed on decode rather
	// than at every use.
	Path string

	// Message is the human-readable description, empty when the server
	// sent none.
	Message string

	// BadElement is error-info's bad-element child, the single most
	// useful part of error-info in practice and the reason it is lifted
	// out of Info rather than left for a caller to re-parse.
	BadElement string

	// Info is error-info's raw inner XML, carried verbatim because its
	// contents are protocol- and vendor-extensible: RFC 6241 defines
	// several children per error-tag, and vendors add their own. Parsing
	// it into a fixed struct would silently discard whatever this
	// package had not anticipated.
	Info string
}

// Error renders the error, degrading through whichever fields the
// server actually sent. The tag always leads, because it is the part
// that is always present and the part worth searching for.
func (e RPCError) Error() string {
	var b strings.Builder
	b.WriteString("netconf: rpc-error")
	if e.Severity != "" && e.Severity != "error" {
		fmt.Fprintf(&b, " (%s)", e.Severity)
	}
	b.WriteString(": ")

	if e.Tag != "" {
		b.WriteString(e.Tag)
	} else {
		b.WriteString("unspecified error")
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}

	var detail []string
	if e.Type != "" {
		detail = append(detail, "type="+e.Type)
	}
	if e.Path != "" {
		detail = append(detail, "path="+e.Path)
	}
	if e.BadElement != "" {
		detail = append(detail, "bad-element="+e.BadElement)
	}
	if len(detail) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(detail, ", "))
	}
	return b.String()
}

// RPCErrors is every <rpc-error> in one reply. A single reply may carry
// several, one per rejected element, and reporting only the first would
// send an operator round a fix-one-rerun loop for a batch the device
// already told them everything about.
type RPCErrors []RPCError

// Error renders every error in the reply, numbered when there is more
// than one so a reader can tell "three separate rejections" from "one
// long message".
func (e RPCErrors) Error() string {
	switch len(e) {
	case 0:
		// Not reachable through this package, which never builds an
		// empty RPCErrors, but a zero value that renders as an empty
		// string would be a genuinely confusing thing to log.
		return "netconf: rpc-error: the server reported an error with no detail"
	case 1:
		return e[0].Error()
	}
	parts := make([]string, 0, len(e))
	for i, err := range e {
		parts = append(parts, fmt.Sprintf("[%d/%d] %s", i+1, len(e), err.Error()))
	}
	return fmt.Sprintf("netconf: the server reported %d errors: %s", len(e), strings.Join(parts, "; "))
}

// Unwrap exposes the individual errors to errors.Is and errors.As, so a
// caller can ask whether any single rejection in a batch matched
// without knowing how many there were.
func (e RPCErrors) Unwrap() []error {
	out := make([]error, 0, len(e))
	for _, err := range e {
		out = append(out, err)
	}
	return out
}

// HasTag reports whether any error in the reply carries tag. It is the
// branch a caller actually wants: "did this fail because the datastore
// is locked" is a question about the set, not about the first element.
func (e RPCErrors) HasTag(tag string) bool {
	for _, err := range e {
		if err.Tag == tag {
			return true
		}
	}
	return false
}
