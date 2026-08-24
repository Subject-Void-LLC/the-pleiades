// Package datastore is the shared port through which this platform
// reads and writes a device's structured configuration, independent of
// which protocol carries it: NETCONF (pkg/netconf), and later RESTCONF
// and gNMI.
//
// # "Datastore" means the device's, never this platform's
//
// The word is used here in the RFC 8342 sense: a conceptual place a
// network device keeps configuration, named running, candidate,
// intended or operational. It never refers to this platform's own state
// store, which is inventory.Repository and is never called a datastore
// anywhere. Nothing in this package touches Pleiades' inventory,
// credentials or job state.
//
// # Why a port at all, and what it deliberately does not carry
//
// The three protocols above all fail transport.Transport's
// Exec(ctx, target, cred, command string) (Result, error) shape for the
// same reason pkg/catalystcenter/client.go already recorded for REST:
// that port is command-oriented, and there is no command string, no
// stdout, no stderr and no exit code in an XML RPC against a named
// datastore. Giving them three unrelated bespoke clients with no shared
// vocabulary is the other failure, and it is the one pkg/catalystcenter
// already demonstrates: a Collection author would have to learn each
// one from scratch.
//
// So the port carries exactly what all three genuinely share, and
// nothing more. RFC 8342 names the datastores NETCONF already used;
// RFC 8527 later retrofitted those names onto RESTCONF, which RFC 8040
// as published does not carry; gNMI never adopted RFC 8342 at all and
// qualifies a path with an origin instead. What survives that
// intersection is a path and a payload, which is what Path and Payload
// are. The verb set (Operation) comes from each protocol's own
// operation list, NETCONF edit-config's "operation" attribute,
// RESTCONF's HTTP methods, and gNMI Set's update/replace/delete fields,
// not from RFC 8342, which defines no operation at all. The port adopts
// names that already exist rather than coining new ones.
//
// # There is deliberately no Commit
//
// NETCONF has :candidate and :confirmed-commit. RFC 8040 RESTCONF has
// neither a candidate datastore nor a commit operation, and gNMI's Set
// is atomic per request with no candidate at all. A three-way port
// declaring Commit would be false for two of its three implementations,
// and a false method on an interface is worse than an absent one
// because it type-checks: every caller would have to discover at run
// time, per device, that it does nothing. NETCONF's datastore family
// (Lock, Unlock, Commit, DiscardChanges) therefore lives on the
// concrete netconf.Session and is reached by an optional-interface
// assertion at the call site, the idiom
// internal/engine/collection_action.go already uses for FactCollector.
//
// This package is under pkg/, not internal/, because a Collection
// method may import only pkg/ (internal/archtest's
// TestPkgNeverImportsInternal), which is the constraint a third-party
// Collection will have to satisfy once out-of-tree distribution exists.
// It imports nothing outside the standard library.
package datastore

import (
	"context"
	"fmt"
	"strings"
)

// Encoding names the wire encoding of a Payload's bytes. It is a closed
// set of iota constants rather than a string, per AGENTS.md's rule for
// fixed sets of values: a typo in a string encoding name would be a run
// time surprise on a real device, and there is no reason for it to be
// one.
type Encoding uint8

const (
	// EncodingXML is RFC 6241 NETCONF's encoding and one of RFC 8040
	// RESTCONF's two (application/yang-data+xml).
	EncodingXML Encoding = iota

	// EncodingJSON is plain JSON, as gNMI's TypedValue json_val carries
	// it.
	EncodingJSON

	// EncodingJSONIETF is RFC 7951's JSON encoding of YANG data,
	// RESTCONF's application/yang-data+json and gNMI's json_ietf_val.
	// It is a distinct encoding from EncodingJSON, not a content-type
	// spelling of it: RFC 7951 qualifies top-level member names with
	// their module name, so the same data is not byte-identical between
	// the two.
	EncodingJSONIETF

	// EncodingProtobuf is gNMI's native TypedValue encoding.
	EncodingProtobuf
)

// String renders an Encoding for error messages and logs.
func (e Encoding) String() string {
	switch e {
	case EncodingXML:
		return "xml"
	case EncodingJSON:
		return "json"
	case EncodingJSONIETF:
		return "json_ietf"
	case EncodingProtobuf:
		return "protobuf"
	default:
		return fmt.Sprintf("Encoding(%d)", uint8(e))
	}
}

// Valid reports whether e is one of the declared encodings. An
// implementation validates what it is handed rather than trusting a
// caller's uint8 conversion, since Go does not make an iota type
// closed at the language level.
func (e Encoding) Valid() bool {
	return e <= EncodingProtobuf
}

// Operation is what a SetConfig does to the subtree a Path names.
type Operation uint8

const (
	// OperationMerge merges the payload into whatever is already
	// configured at the path, leaving unmentioned siblings alone. It is
	// the zero value because it is the least destructive of the three,
	// so a caller that forgets to set it gets the safe behavior rather
	// than a surprising one.
	OperationMerge Operation = iota

	// OperationReplace replaces the subtree the path names outright:
	// anything configured there and absent from the payload is removed.
	OperationReplace

	// OperationDelete removes the subtree the path names. A Payload is
	// meaningless alongside it and implementations refuse a non-empty
	// one rather than silently ignoring it.
	OperationDelete
)

// String renders an Operation for error messages and logs. The names
// match NETCONF edit-config's own "operation" attribute values, which
// is the point: the port adopts names that already exist.
func (o Operation) String() string {
	switch o {
	case OperationMerge:
		return "merge"
	case OperationReplace:
		return "replace"
	case OperationDelete:
		return "delete"
	default:
		return fmt.Sprintf("Operation(%d)", uint8(o))
	}
}

// Valid reports whether o is one of the declared operations.
func (o Operation) Valid() bool {
	return o <= OperationDelete
}

// ParseOperation resolves an operation's name, as a runbook parameter
// spells it, to its Operation. It is the one place a user-supplied
// string becomes a member of the closed set, so a misspelling is
// refused at the boundary with the valid names listed rather than
// defaulting to merge somewhere further in.
func ParseOperation(s string) (Operation, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "merge":
		return OperationMerge, nil
	case "replace":
		return OperationReplace, nil
	case "delete":
		return OperationDelete, nil
	default:
		return 0, fmt.Errorf("datastore: unknown operation %q: valid operations are merge, replace and delete", s)
	}
}

// PathElem is one named step of a Path, optionally qualified by the
// keys that identify a single entry of a YANG list.
//
// Keys are carried as data here rather than pre-formatted into the
// element's name, and that is the whole reason this type exists instead
// of a []string. Each protocol encodes them completely differently:
// NETCONF nests them as child elements of a subtree filter, RFC 8040
// RESTCONF encodes them into the URI where a key value containing "/"
// or "," changes which resource is addressed unless it is escaped, and
// gNMI carries them as a map on the wire. An implementation can only
// escape them correctly if it is handed them separately.
type PathElem struct {
	// Name is the element or node name.
	Name string

	// Namespace is the YANG namespace this element belongs to, empty
	// when it is inherited from the parent element. NETCONF needs it as
	// an xmlns attribute; the other two derive it from the module name.
	Namespace string

	// Keys identifies a single list entry, empty for a container. Key
	// order is not significant: every protocol identifies a list entry
	// by key NAME, so this is a map rather than a slice.
	Keys map[string]string
}

// Path addresses a subtree of a device's configuration.
//
// The zero Path names the whole datastore, which every implementation
// must accept: "read the running configuration" is the most common
// request there is, and requiring a caller to synthesize a
// root-addressing path for it would be ceremony.
type Path struct {
	// Elem is the path's steps from the root, outermost first.
	Elem []PathElem
}

// IsRoot reports whether p addresses the whole datastore.
func (p Path) IsRoot() bool { return len(p.Elem) == 0 }

// String renders a path in a readable, protocol-neutral form for error
// messages and logs. It is deliberately NOT a wire format for any of
// the three protocols and must never be used as one: RESTCONF in
// particular requires per-segment percent-escaping that this rendering
// does not perform, which is exactly the confusion that turns an
// unescaped key value into a request addressing a different resource.
func (p Path) String() string {
	if p.IsRoot() {
		return "/"
	}
	var b strings.Builder
	for _, e := range p.Elem {
		b.WriteByte('/')
		b.WriteString(e.Name)
		if len(e.Keys) == 0 {
			continue
		}
		names := make([]string, 0, len(e.Keys))
		for k := range e.Keys {
			names = append(names, k)
		}
		// Sorted so the rendering of one path is stable across calls;
		// an error message that changed shape between two runs of the
		// same failure would be needlessly hard to search for.
		sortStrings(names)
		b.WriteByte('[')
		for i, k := range names {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(e.Keys[k])
		}
		b.WriteByte(']')
	}
	return b.String()
}

// Payload is a chunk of configuration data plus the encoding its bytes
// are in.
//
// Encoding travels WITH the bytes rather than being a property of the
// session, because a single protocol carries more than one: RESTCONF
// negotiates XML or JSON per request, and gNMI's TypedValue names its
// encoding per value. A session-level encoding would be right for
// NETCONF alone and wrong for the two implementations that follow it.
type Payload struct {
	Encoding Encoding
	Bytes    []byte
}

// IsEmpty reports whether p carries no data.
func (p Payload) IsEmpty() bool { return len(p.Bytes) == 0 }

// Store is the port: two operations against a device's configuration
// datastore, addressed by Path and carrying a Payload.
//
// An implementation is a live session or client, not a stateless
// helper, so a caller owns closing whatever it got the Store from.
// Store deliberately does not embed io.Closer: gNMI's client is
// long-lived across many operations while a NETCONF session is opened
// per task, and putting Close here would assert a shared lifetime the
// three do not have.
type Store interface {
	// GetConfig reads the configuration subtree p addresses, or the
	// whole datastore when p is the zero Path.
	GetConfig(ctx context.Context, p Path) (Payload, error)

	// SetConfig applies payload to the subtree p addresses with the
	// given operation. An OperationDelete takes an empty payload;
	// implementations refuse a non-empty one rather than ignoring it,
	// because silently discarding configuration a caller supplied is
	// the failure that looks like success.
	SetConfig(ctx context.Context, p Path, payload Payload, op Operation) error
}

// sortStrings is an insertion sort over the small key-name slices
// Path.String builds. It avoids importing "sort" for a slice that is
// never longer than a handful of YANG list keys, keeping this package's
// dependency footprint at exactly two standard library packages.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
