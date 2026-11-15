// Package gnmi: the encodings and data types a call names, and the
// Capabilities RPC, which says what a server supports: its gNMI version, the
// encodings it answers in and the models it implements. It is not a YANG
// library: a model's version is the model's own (an OpenConfig semantic
// version, or a date), there is no namespace, and nothing makes the list
// complete, so a reader never takes it for every module the device has.
package gnmi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// Encoding is a value encoding gNMI names, numbered as gNMI numbers them.
type Encoding uint8

const (
	// EncodingJSON is plain JSON (json_val).
	EncodingJSON Encoding = iota
	// EncodingBytes is a server-defined byte string (bytes_val).
	EncodingBytes
	// EncodingProto is gNMI's scalar TypedValue members.
	EncodingProto
	// EncodingASCII is a server-defined text (ascii_val).
	EncodingASCII
	// EncodingJSONIETF is RFC 7951's JSON encoding of YANG data
	// (json_ietf_val): member names qualified by their module.
	EncodingJSONIETF
)

// encodingNames are each Encoding's name, as a runbook and a fact write it.
var encodingNames = []string{"json", "bytes", "proto", "ascii", "json_ietf"}

// String names e as a runbook writes it.
func (e Encoding) String() string {
	if int(e) < len(encodingNames) {
		return encodingNames[e]
	}
	return "unknown(" + strconv.Itoa(int(e)) + ")"
}

// ParseEncoding resolves an encoding's name, in any case, to its Encoding.
func ParseEncoding(s string) (Encoding, error) {
	for i, name := range encodingNames {
		if strings.EqualFold(s, name) {
			return Encoding(i), nil
		}
	}
	return 0, fmt.Errorf("encoding %q is not one of %s", s, strings.Join(encodingNames, ", "))
}

// proto is e on the wire.
func (e Encoding) proto() gpb.Encoding {
	return gpb.Encoding(e)
}

// DataType is which data a Get reads, as gNMI's GetRequest names them.
type DataType uint8

const (
	// DataAll is configuration and state alike.
	DataAll DataType = iota
	// DataConfig is the read-write configuration alone.
	DataConfig
	// DataState is the read-only state alone.
	DataState
	// DataOperational is the state the device's own operation changes.
	DataOperational
)

// Model is one model a server says it implements.
type Model struct {
	// Name is the model's name as the server wrote it, such as
	// openconfig-interfaces; some servers write a URN-qualified name.
	Name string
	// Organization is who publishes the model.
	Organization string
	// Version is the model's own version: an OpenConfig semantic version
	// (2.4.1) or a revision date, as the server wrote it.
	Version string
}

// Capabilities is what a server's Capabilities RPC said.
type Capabilities struct {
	// Version is the gNMI version the server implements, such as 0.7.0.
	Version string
	// Encodings are the encodings the server answers in, sorted by number.
	Encodings []Encoding
	// VendorEncodings are the numbers of encodings the server lists that
	// gNMI does not name (a vendor's own: Nokia SR Linux 26.7.2 lists eight,
	// measured 2026-11-15), sorted, once each, at most maxVendorEncodings.
	// They are kept apart rather than counted as malformed: a vendor may
	// extend the list, and nothing here sends one.
	VendorEncodings []int32
	// Models are the models it implements, sorted by name then version,
	// once each.
	Models []Model
	// Dropped counts the entries refused as malformed or too long; a
	// sample of up to five, quoted, is in DroppedSamples.
	Dropped        int
	DroppedSamples []string
}

// Supports reports whether the server answers in e.
func (c Capabilities) Supports(e Encoding) bool {
	for _, have := range c.Encodings {
		if have == e {
			return true
		}
	}
	return false
}

// maxVendorEncodings bounds the vendor encodings Capabilities keeps.
const maxVendorEncodings = 64

// Capabilities asks the server what it supports. An entry that is not
// printable text within yanglib.MaxNamespaceBytes is left out and counted
// rather than kept, and past yanglib.MaxModules every further model is; an
// encoding gNMI does not name is kept apart, in VendorEncodings.
func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	if err := c.refused(); err != nil {
		return Capabilities{}, err
	}
	resp, err := c.api.Capabilities(ctx, &gpb.CapabilityRequest{})
	if err != nil {
		return Capabilities{}, c.fail(ctx, "Capabilities", err)
	}
	return readCapabilities(resp), nil
}

// readCapabilities holds a CapabilityResponse to the rules above.
func readCapabilities(resp *gpb.CapabilityResponse) Capabilities {
	var out Capabilities
	drop := func(entry string) {
		out.Dropped++
		if len(out.DroppedSamples) < 5 {
			if len(entry) > 120 {
				entry = entry[:120]
			}
			out.DroppedSamples = append(out.DroppedSamples, strconv.Quote(entry))
		}
	}
	if v := resp.GetGNMIVersion(); keepable(v) && len(v) <= 64 {
		out.Version = v
	} else if v != "" {
		drop(v)
	}
	seenEncoding := map[Encoding]bool{}
	for _, e := range resp.GetSupportedEncodings() {
		if e < 0 || int(e) >= len(encodingNames) {
			if !slices.Contains(out.VendorEncodings, int32(e)) && len(out.VendorEncodings) < maxVendorEncodings {
				out.VendorEncodings = append(out.VendorEncodings, int32(e))
			}
			continue
		}
		if !seenEncoding[Encoding(e)] {
			seenEncoding[Encoding(e)] = true
			out.Encodings = append(out.Encodings, Encoding(e))
		}
	}
	sort.Slice(out.Encodings, func(i, j int) bool { return out.Encodings[i] < out.Encodings[j] })
	slices.Sort(out.VendorEncodings)
	seen := map[Model]bool{}
	for _, m := range resp.GetSupportedModels() {
		model := Model{Name: m.GetName(), Organization: m.GetOrganization(), Version: m.GetVersion()}
		switch {
		case len(out.Models) >= yanglib.MaxModules:
			drop(model.Name)
		case !keepable(model.Name) || model.Name == "" || !keepable(model.Organization) || !keepable(model.Version):
			drop(model.Name)
		case !seen[model]:
			seen[model] = true
			out.Models = append(out.Models, model)
		}
	}
	sort.Slice(out.Models, func(i, j int) bool {
		if out.Models[i].Name != out.Models[j].Name {
			return out.Models[i].Name < out.Models[j].Name
		}
		return out.Models[i].Version < out.Models[j].Version
	})
	return out
}

// YANGModules returns the models as YANG modules, for the protocol-neutral
// yang.* facts: each model named by a YANG identifier (or a name ending in
// ":" and one, as a URN-qualified name does) by that identifier, its revision
// the model's version when the version is a date (YYYY-MM-DD) and none
// otherwise, since an OpenConfig semantic version is no YANG revision. A
// model whose name holds no identifier is returned in unread instead.
// Modules are sorted by name then revision, once each, and carry no
// namespace: gNMI names none.
func (c Capabilities) YANGModules() (modules []yanglib.Module, unread []string) {
	seen := map[string]bool{}
	for _, m := range c.Models {
		name := m.Name
		if i := strings.LastIndexByte(name, ':'); i >= 0 {
			name = name[i+1:]
		}
		if !yanglib.IsIdentifier(name) {
			unread = append(unread, m.Name)
			continue
		}
		module := yanglib.Module{Name: name}
		if _, err := time.Parse(time.DateOnly, m.Version); err == nil {
			module.Revision = m.Version
		}
		if key := module.Name + "@" + module.Revision; !seen[key] {
			seen[key] = true
			modules = append(modules, module)
		}
	}
	sort.Slice(modules, func(i, j int) bool {
		if modules[i].Name != modules[j].Name {
			return modules[i].Name < modules[j].Name
		}
		return modules[i].Revision < modules[j].Revision
	})
	return modules, unread
}

// ModelsDigest is the SHA-256 (hex) of the model list as Capabilities kept
// it (sorted, once each), each model's name, organization and version quoted
// on a line of its own, so any change to what the server says it implements
// changes the digest and an unchanged list never does. Onboarding and
// net.gnmi.facts both record it, so the two agree.
func (c Capabilities) ModelsDigest() string {
	h := sha256.New()
	for _, m := range c.Models {
		_, _ = fmt.Fprintf(h, "%q %q %q\n", m.Name, m.Organization, m.Version)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// keepable reports whether s may be kept as it is: printable, and no longer
// than a namespace may be.
func keepable(s string) bool {
	return len(s) <= yanglib.MaxNamespaceBytes && yanglib.Printable(s)
}
