// Package netconf: fetching a module's YANG source from the server itself,
// RFC 6022's <get-schema>. A server offers it when it implements
// ietf-netconf-monitoring, which it says in its hello (a YANG 1.0 server's
// module capability) or its YANG library (a YANG 1.1 server's), so a session
// asks only a server that said so. The answer's text is returned byte for byte
// as the server encoded it in its reply, after the XML's own escaping is read:
// it is schema, not configuration, and nothing here masks or reformats it.
package netconf

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// MonitoringNamespace is ietf-netconf-monitoring's namespace (RFC 6022),
// which get-schema and its answer are in.
const MonitoringNamespace = "urn:ietf:params:xml:ns:yang:ietf-netconf-monitoring"

// monitoringModule is the module whose implementation offers get-schema.
const monitoringModule = "ietf-netconf-monitoring"

// ErrNoGetSchema is a server that advertises no ietf-netconf-monitoring, so
// offers no get-schema to ask.
var ErrNoGetSchema = errors.New("netconf: the server does not advertise ietf-netconf-monitoring (RFC 6022), so it offers no get-schema")

// CanGetSchema reports whether the server advertises ietf-netconf-monitoring:
// as a module its hello or YANG library lists as implemented, or as a hello
// capability in the module's namespace.
func (s *Session) CanGetSchema() bool {
	for _, m := range s.discovery.Modules {
		if m.Name == monitoringModule {
			return true
		}
	}
	for _, c := range s.serverCapabilities {
		if base, _, _ := strings.Cut(strings.TrimSpace(c), "?"); base == MonitoringNamespace {
			return true
		}
	}
	return false
}

// GetSchema fetches the YANG source of the module or submodule named
// identifier at version (a revision date; empty asks for the one version the
// server holds, which a server holding more refuses), and returns its text,
// refusing one longer than limit bytes. identifier must be a YANG identifier
// and version a date, both checked before anything is sent. A refusal by the
// server is its RPCErrors; an answer that is not text (its data element holds
// an element) is an error naming that.
func (s *Session) GetSchema(ctx context.Context, identifier, version string, limit int) ([]byte, error) {
	if !yanglib.IsIdentifier(identifier) {
		return nil, fmt.Errorf("netconf: get-schema: %q is not a YANG identifier", identifier)
	}
	if version != "" && !yanglib.IsRevision(version) {
		return nil, fmt.Errorf("netconf: get-schema: %q is not a revision date", version)
	}
	if !s.CanGetSchema() {
		return nil, ErrNoGetSchema
	}
	var body strings.Builder
	body.WriteString(`<get-schema xmlns="` + MonitoringNamespace + `"><identifier>` + identifier + `</identifier>`)
	if version != "" {
		body.WriteString(`<version>` + version + `</version>`)
	}
	// RFC 6022's format is an identityref; unprefixed, it names the yang
	// identity of the element's own (the monitoring) namespace.
	body.WriteString(`<format>yang</format></get-schema>`)
	reply, err := s.do(ctx, body.String())
	if err != nil {
		return nil, err
	}
	text, err := schemaText(reply.raw, limit)
	if err != nil {
		return nil, fmt.Errorf("netconf: get-schema of %s: %w", yanglib.Schema{Name: identifier, Revision: version}.Key(), err)
	}
	return text, nil
}

// schemaText is the character data of the data element of raw, an
// rpc-reply, as the XML decoder reads it (entities and CDATA sections
// resolved), refusing a reply with no data element, a data element holding an
// element, or text longer than limit bytes.
func schemaText(raw []byte, limit int) ([]byte, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	depth, inData := 0, false
	var text bytes.Buffer
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the reply carries no data element")
		}
		if err != nil {
			return nil, fmt.Errorf("the reply is not usable XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch {
			case inData:
				// The element's name is the device's text, so it is not
				// quoted.
				return nil, errors.New("the reply's data holds an element, not the source's text")
			case depth == 2 && t.Name.Local == "data":
				// RFC 6022 puts the data element in the monitoring
				// namespace; a server putting it in the base one is read
				// alike, since the local name is what rpc-reply carries.
				inData = true
			}
		case xml.EndElement:
			if inData {
				return text.Bytes(), nil
			}
			depth--
		case xml.CharData:
			if !inData {
				continue
			}
			if text.Len()+len(t) > limit {
				return nil, fmt.Errorf("the source is larger than %d bytes, which is not read", limit)
			}
			text.Write(t)
		}
	}
}
