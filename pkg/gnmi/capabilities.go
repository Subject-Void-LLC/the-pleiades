// Package gnmi: the encodings and data types a call names, and the
// Capabilities RPC, which says what a server supports: its gNMI version, the
// encodings it answers in and the models it implements. It is not a YANG
// library: a model's version is the model's own (an OpenConfig semantic
// version, or a date), there is no namespace, and nothing makes the list
// complete, so a reader never takes it for every module the device has.
package gnmi

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

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

// Capabilities asks the server what it supports. An entry that is not
// printable text within yanglib.MaxNamespaceBytes, and an encoding this
// client does not know, is left out and counted rather than kept; past
// yanglib.MaxModules every further model is.
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
			drop("encoding " + strconv.Itoa(int(e)))
			continue
		}
		if !seenEncoding[Encoding(e)] {
			seenEncoding[Encoding(e)] = true
			out.Encodings = append(out.Encodings, Encoding(e))
		}
	}
	sort.Slice(out.Encodings, func(i, j int) bool { return out.Encodings[i] < out.Encodings[j] })
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

// keepable reports whether s may be kept as it is: printable, and no longer
// than a namespace may be.
func keepable(s string) bool {
	return len(s) <= yanglib.MaxNamespaceBytes && yanglib.Printable(s)
}
