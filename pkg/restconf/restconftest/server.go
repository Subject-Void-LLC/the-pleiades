// Package restconftest is a RESTCONF server for tests: an httptest TLS
// server answering what RFC 8040 and RFC 8525 define, in the shapes a Cisco
// Catalyst 8000V (IOS XE 17.15.1a) answers with, so pkg/restconf, onboarding
// and the net.restconf methods are tested through their real HTTP, TLS and
// credential path rather than a stand-in for it.
//
// The YANG library it serves is that router's own, captured on 2026-11-14
// over NETCONF (pkg/netconf/testdata holds the rpc-replies): the yang-library
// and modules-state elements, which are what a RESTCONF server answers GET
// {+restconf}/data/ietf-yang-library:yang-library with in XML. Its JSON
// answer is the same library rendered in RFC 7951's encoding. Its host-meta
// and error answers follow IOS XE's (a Tail-f ConfD server); its capability
// list is IOS XE's shape, written here rather than captured.
package restconftest

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	_ "embed" // the captured library documents
	"encoding/json"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

//go:embed testdata/c8k-17.15.1a-yang-library.xml.gz
var libraryGz []byte

//go:embed testdata/c8k-17.15.1a-modules-state.xml.gz
var modulesStateGz []byte

// LibraryXML returns the captured router's yang-library document, the XML a
// RESTCONF server answers its library resource with.
func LibraryXML(t testing.TB) []byte {
	t.Helper()
	return gunzip(t, libraryGz)
}

// ModulesStateXML returns the captured router's modules-state document.
func ModulesStateXML(t testing.TB) []byte {
	t.Helper()
	return gunzip(t, modulesStateGz)
}

// gunzip is raw decompressed.
func gunzip(t testing.TB, raw []byte) []byte {
	t.Helper()
	out, err := gunzipBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// gunzipBytes is raw decompressed.
func gunzipBytes(raw []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}

// Capabilities is the capability list the server answers with by default:
// IOS XE's shape, RFC 8040's own and Tail-f's.
var Capabilities = []string{
	"urn:ietf:params:restconf:capability:defaults:1.0?basic-mode=explicit",
	"urn:ietf:params:restconf:capability:depth:1.0",
	"urn:ietf:params:restconf:capability:fields:1.0",
	"urn:ietf:params:restconf:capability:filter:1.0",
	"urn:ietf:params:restconf:capability:replay:1.0",
	"urn:ietf:params:restconf:capability:with-defaults:1.0",
	"urn:ietf:params:restconf:capability:yang-patch:1.0",
	"http://tail-f.com/ns/restconf/collection/1.0",
	"http://tail-f.com/ns/restconf/query-api/1.0",
}

// Options sets what a Server answers.
type Options struct {
	// Username and Password are the login basic authentication must carry;
	// both empty asks for none.
	Username, Password string
	// Root is the root host-meta names; "/restconf" when empty.
	Root string
	// HostMeta replaces the host-meta document when set.
	HostMeta string
	// JSON answers data resources in RFC 7951's JSON encoding only, as a
	// server supporting no XML would.
	JSON bool
	// NoYANGLibrary answers the yang-library resource 404, as an RFC 7895
	// server does, so a client falls back to modules-state.
	NoYANGLibrary bool
	// Library replaces the yang-library document (XML) when set.
	Library []byte
	// Capabilities replaces the capability list when set.
	Capabilities []string
	// Override answers the escaped request path it names with its handler,
	// before anything above.
	Override map[string]http.HandlerFunc
	// PostChild is the node a POST creates under the path it is sent to,
	// named in its answer's Location, as "group=ops"; empty refuses POST.
	PostChild string
	// IOSXE answers as IOS XE 17.15.1a did (iosxe.go).
	IOSXE bool
	// Starting is, in IOSXE mode, how many requests under /restconf answer
	// 502 with Cisco's error page, as while RESTCONF starts.
	Starting int
	// GoingDown, in IOSXE mode, answers every write 500 "Server is going
	// down", as while the YANG stack goes down.
	GoingDown bool
	// MaxTLSVersion caps the TLS version the server negotiates, as an older
	// device's does (tls.VersionTLS12); zero is Go's default, TLS 1.3.
	MaxTLSVersion uint16
	// RequireClientCertificate refuses a client that presents no
	// certificate, in the handshake (TLS 1.2) or just after it (TLS 1.3).
	RequireClientCertificate bool
	// Sources are YANG sources the server serves, by name@revision (or the
	// name alone), as IOS XE does (sources.go): modules-state then lists
	// these alone, each naming its source's URL.
	Sources map[string][]byte
	// SourceOrigin, when set, is the origin modules-state's schema leaves
	// name in place of the server's own.
	SourceOrigin string
	// SessionCap, when set, caps the server's sessions as IOS XE's are
	// capped (sessions.go): each request carrying a credential opens a
	// session of its own, whatever connection carries it, held for
	// SessionHold after it arrives, and a request arriving while SessionCap
	// are held is answered 409 in-use "Too many sessions." and opens none.
	SessionCap int
	// SessionHold is how long a session is held; SessionCap needs it.
	SessionHold time.Duration
}

// Server is a RESTCONF server over TLS.
type Server struct {
	*httptest.Server
	opts Options
	mu   sync.Mutex
	// paths are the escaped paths requested, in order.
	paths []string
	// unauthenticated counts requests other than host-meta's that carried no
	// Authorization header.
	unauthenticated int
	// logins counts requests that carried one.
	logins int
	// connections counts the connections clients opened.
	connections int
	// hostMetaLogins counts host-meta requests that carried a credential.
	hostMetaLogins int
	// stored are the resources writes made, by escaped path.
	stored map[string]resource
	// methods are the methods requested, in order, beside paths.
	methods []string
	// sessions are when each held session expires, under SessionCap.
	sessions []time.Time
	// sessionRefusals counts the requests refused for too many sessions.
	sessionRefusals int
}

// resource is one written resource: the document and its media type.
type resource struct {
	mediaType string
	body      []byte
}

// New starts a Server answering as opts says, closed when t ends.
func New(t testing.TB, opts Options) *Server {
	t.Helper()
	if opts.Root == "" {
		opts.Root = "/restconf"
	}
	if opts.Library == nil {
		opts.Library = LibraryXML(t)
	}
	if opts.Capabilities == nil {
		opts.Capabilities = Capabilities
	}
	s := &Server{opts: opts, stored: map[string]resource{}}
	modulesState := ModulesStateXML(t)
	s.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serve(w, r, modulesState)
	}))
	s.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			s.mu.Lock()
			s.connections++
			s.mu.Unlock()
		}
	}
	if opts.MaxTLSVersion != 0 || opts.RequireClientCertificate {
		// StartTLS keeps a configuration set before it, adding its own
		// certificate.
		s.TLS = &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: opts.MaxTLSVersion}
		if opts.RequireClientCertificate {
			s.TLS.ClientAuth = tls.RequireAnyClientCert
		}
	}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

// CAPEM returns the server's certificate as PEM, for a device's tls_ca_pem.
func (s *Server) CAPEM() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}))
}

// Paths returns the escaped paths requested so far, in order.
func (s *Server) Paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

// Logins returns how many requests carried a credential.
func (s *Server) Logins() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins
}

// HostMetaLogins returns how many host-meta requests carried a credential.
func (s *Server) HostMetaLogins() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hostMetaLogins
}

// Connections returns how many connections clients opened.
func (s *Server) Connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connections
}

// Unauthenticated returns how many requests other than host-meta's carried
// no credential: host-meta is read with none, as RFC 8040's client finds the
// root first.
func (s *Server) Unauthenticated() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unauthenticated
}

// serve answers one request.
func (s *Server) serve(w http.ResponseWriter, r *http.Request, modulesState []byte) {
	path := r.URL.EscapedPath()
	s.mu.Lock()
	s.paths = append(s.paths, path)
	s.methods = append(s.methods, r.Method)
	if r.Header.Get("Authorization") != "" {
		s.logins++
	} else if path != "/.well-known/host-meta" {
		s.unauthenticated++
	}
	s.mu.Unlock()
	if path == "/.well-known/host-meta" && r.Header.Get("Authorization") != "" {
		s.mu.Lock()
		s.hostMetaLogins++
		s.mu.Unlock()
	}
	if !s.openSession(r) {
		s.iosxeAnswer(w, http.StatusConflict, []byte(IOSXETooManySessions))
		return
	}
	if h, ok := s.opts.Override[path]; ok {
		h(w, r)
		return
	}
	if s.opts.IOSXE {
		s.serveIOSXE(w, r, path)
		return
	}
	// host-meta is answered with no login, as rousette answers it
	// (measured 2026-11-14), so a refused credential is first met on a data
	// read, after the root is found.
	if path != "/.well-known/host-meta" && (s.opts.Username != "" || s.opts.Password != "") {
		user, pass, ok := r.BasicAuth()
		if !ok || user != s.opts.Username || pass != s.opts.Password {
			// IOS XE's answer to a missing or refused login.
			w.Header().Set("WWW-Authenticate", `Basic realm="restconf"`)
			s.errors(w, r, http.StatusUnauthorized, "protocol", "access-denied", "", "access denied")
			return
		}
	}
	root := s.opts.Root
	if s.serveSource(w, r, path) || s.write(w, r, path) {
		return
	}
	switch path {
	case "/.well-known/host-meta":
		w.Header().Set("Content-Type", "application/xrd+xml")
		if s.opts.HostMeta != "" {
			_, _ = io.WriteString(w, s.opts.HostMeta)
			return
		}
		fmt.Fprintf(w, "<XRD xmlns='http://docs.oasis-open.org/ns/xri/xrd-1.0'>\n    <Link rel='restconf' href='%s'/>\n</XRD>", root)
	case root + "/data/ietf-yang-library:yang-library":
		if s.opts.NoYANGLibrary {
			s.errors(w, r, http.StatusNotFound, "application", "invalid-value", "", "uri keypath not found")
			return
		}
		s.library(w, s.opts.Library)
	case root + "/data/ietf-yang-library:modules-state":
		s.library(w, modulesState)
	case root + "/data/ietf-restconf-monitoring:restconf-state/capabilities":
		s.capabilities(w)
	default:
		s.errors(w, r, http.StatusNotFound, "application", "invalid-value", "", "uri keypath not found")
	}
}

// library answers a library document, as XML or as RFC 7951 JSON.
func (s *Server) library(w http.ResponseWriter, doc []byte) {
	if !s.opts.JSON {
		w.Header().Set("Content-Type", "application/yang-data+xml")
		_, _ = w.Write(doc)
		return
	}
	raw, err := libraryJSON(doc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/yang-data+json")
	_, _ = w.Write(raw)
}

// capabilities answers ietf-restconf-monitoring's capability list.
func (s *Server) capabilities(w http.ResponseWriter) {
	if s.opts.JSON {
		w.Header().Set("Content-Type", "application/yang-data+json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ietf-restconf-monitoring:capabilities": map[string]any{"capability": s.opts.Capabilities}})
		return
	}
	w.Header().Set("Content-Type", "application/yang-data+xml")
	var b strings.Builder
	b.WriteString(`<capabilities xmlns="urn:ietf:params:xml:ns:yang:ietf-restconf-monitoring">`)
	for _, c := range s.opts.Capabilities {
		b.WriteString("<capability>")
		_ = xml.EscapeText(&b, []byte(c))
		b.WriteString("</capability>")
	}
	b.WriteString(`</capabilities>`)
	_, _ = io.WriteString(w, b.String())
}

// errors answers an ietf-restconf:errors container with one error, in the
// encoding the server answers with. Its message echoes what the request
// carried, the Authorization header and the password inside it, as a
// careless server does (FAILURE_PATTERNS 626), so a test can hold the client
// to masking both.
func (s *Server) errors(w http.ResponseWriter, r *http.Request, status int, errType, tag, path, message string) {
	if auth := r.Header.Get("Authorization"); auth != "" {
		message += " (request carried " + auth
		if _, pass, ok := r.BasicAuth(); ok {
			message += ", password " + pass
		}
		message += ")"
	}
	if s.opts.JSON {
		w.Header().Set("Content-Type", "application/yang-data+json")
		w.WriteHeader(status)
		entry := map[string]string{"error-type": errType, "error-tag": tag, "error-message": message}
		if path != "" {
			entry["error-path"] = path
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ietf-restconf:errors": map[string]any{"error": []any{entry}}})
		return
	}
	w.Header().Set("Content-Type", "application/yang-data+xml")
	w.WriteHeader(status)
	var b strings.Builder
	b.WriteString(`<errors xmlns="urn:ietf:params:xml:ns:yang:ietf-restconf"><error><error-type>` + errType + `</error-type><error-tag>` + tag + `</error-tag>`)
	if path != "" {
		b.WriteString("<error-path>")
		_ = xml.EscapeText(&b, []byte(path))
		b.WriteString("</error-path>")
	}
	b.WriteString("<error-message>")
	_ = xml.EscapeText(&b, []byte(message))
	b.WriteString("</error-message></error></errors>")
	_, _ = io.WriteString(w, b.String())
}

// libraryJSON renders a yang-library or modules-state XML document in RFC
// 7951's JSON encoding.
func libraryJSON(doc []byte) ([]byte, error) {
	type module struct {
		Name      string   `xml:"name" json:"name"`
		Revision  string   `xml:"revision" json:"revision,omitempty"`
		Namespace string   `xml:"namespace" json:"namespace"`
		Feature   []string `xml:"feature" json:"feature,omitempty"`
		Deviation []string `xml:"deviation" json:"deviation,omitempty"`
	}
	// stateModule is RFC 7895's entry, whose deviations are a list of name
	// and revision.
	type stateModule struct {
		Name        string   `xml:"name" json:"name"`
		Revision    string   `xml:"revision" json:"revision"`
		Namespace   string   `xml:"namespace" json:"namespace"`
		Feature     []string `xml:"feature" json:"feature,omitempty"`
		Conformance string   `xml:"conformance-type" json:"conformance-type"`
		Deviation   []struct {
			Name     string `xml:"name" json:"name"`
			Revision string `xml:"revision" json:"revision"`
		} `xml:"deviation" json:"deviation,omitempty"`
		// Schema is the URL of the module's source, which IOS XE's JSON
		// carries as its XML does.
		Schema string `xml:"schema" json:"schema,omitempty"`
	}
	var parsed struct {
		XMLName    xml.Name
		ModuleSets []struct {
			Name    string   `xml:"name" json:"name"`
			Modules []module `xml:"module" json:"module"`
		} `xml:"module-set"`
		Modules    []stateModule `xml:"module"`
		Datastores []struct {
			Name string `xml:"name"`
		} `xml:"datastore"`
		ContentID   string `xml:"content-id"`
		ModuleSetID string `xml:"module-set-id"`
	}
	if err := xml.Unmarshal(doc, &parsed); err != nil {
		return nil, err
	}
	if parsed.XMLName.Local == "modules-state" {
		return json.Marshal(map[string]any{"ietf-yang-library:modules-state": map[string]any{
			"module-set-id": parsed.ModuleSetID,
			"module":        parsed.Modules,
		}})
	}
	var datastores []map[string]string
	for _, ds := range parsed.Datastores {
		name := ds.Name
		if i := strings.LastIndexByte(name, ':'); i >= 0 {
			name = name[i+1:]
		}
		datastores = append(datastores, map[string]string{"name": "ietf-datastores:" + name, "schema": "common"})
	}
	return json.Marshal(map[string]any{"ietf-yang-library:yang-library": map[string]any{
		"module-set": parsed.ModuleSets,
		"datastore":  datastores,
		"content-id": parsed.ContentID,
	}})
}

// Methods returns the methods requested so far, in order, beside Paths.
func (s *Server) Methods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.methods...)
}

// Stored returns the document a write left at path, and whether one is there.
func (s *Server) Stored(path string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, ok := s.stored[path]
	return res.body, ok
}

// Store puts a document at path, as a write would have.
func (s *Server) Store(path, mediaType string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stored[path] = resource{mediaType: mediaType, body: body}
}

// write answers a request under the root's /data or /ds/ietf-datastores:
// running against the stored resources, reporting whether it did: a GET of a
// stored resource returns it; PUT stores (201 new, 204 replaced); PATCH and
// DELETE need the resource (404 otherwise; PATCH replaces it whole, which is
// enough to tell a change from none); POST creates PostChild under the path
// (409 resource-denied, RFC 8040 section 4.4.1, when it is there) and names it in Location.
func (s *Server) write(w http.ResponseWriter, r *http.Request, path string) bool {
	root := s.opts.Root
	if !strings.HasPrefix(path, root+"/data/") && !strings.HasPrefix(path, root+"/ds/ietf-datastores:running/") {
		return false
	}
	s.mu.Lock()
	res, exists := s.stored[path]
	s.mu.Unlock()
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	mediaType := r.Header.Get("Content-Type")
	switch r.Method {
	case http.MethodGet:
		if !exists {
			return false
		}
		w.Header().Set("Content-Type", res.mediaType)
		_, _ = w.Write(res.body)
	case http.MethodPut:
		s.Store(path, mediaType, body)
		if exists {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusCreated)
		}
	case http.MethodPatch:
		if !exists {
			s.errors(w, r, http.StatusNotFound, "application", "data-missing", "", "the resource does not exist")
			return true
		}
		s.Store(path, mediaType, body)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if !exists {
			s.errors(w, r, http.StatusNotFound, "application", "data-missing", "", "the resource does not exist")
			return true
		}
		s.mu.Lock()
		delete(s.stored, path)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPost:
		if s.opts.PostChild == "" {
			s.errors(w, r, http.StatusMethodNotAllowed, "protocol", "operation-not-supported", "", "post is not supported here")
			return true
		}
		child := path + "/" + s.opts.PostChild
		s.mu.Lock()
		_, there := s.stored[child]
		s.mu.Unlock()
		if there {
			s.errors(w, r, http.StatusConflict, "application", "resource-denied", "", "the resource already exists")
			return true
		}
		s.Store(child, mediaType, body)
		w.Header().Set("Location", s.URL+child)
		w.WriteHeader(http.StatusCreated)
	default:
		return false
	}
	return true
}
