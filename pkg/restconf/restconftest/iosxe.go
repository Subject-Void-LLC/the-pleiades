// Package restconftest: the IOS XE mode, a server answering exactly as a
// Cisco Catalyst 8000V running IOS XE 17.15.1a answered on 2026-11-15
// (testdata/iosxe holds its answers, masked by http.request and scanned for
// credentials): host-meta only to a valid login and a 302 to its WebUI's /login.html
// otherwise (which answers 404), 401 with WWW-Authenticate under /restconf to a missing or wrong
// one, the bare capabilities container, its own YANG library in XML and
// JSON, a list entry read back inside its list's array, a post answered 201
// with an empty text/html body and an absolute Location, a second post 409
// data-exists, a read or delete of what is not there 404 invalid-value, and,
// while its RESTCONF starts, Cisco's HTML error page with 502.
package restconftest

import (
	_ "embed" // the captured answers
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

//go:embed testdata/iosxe/host-meta.xml
var iosxeHostMeta []byte

//go:embed testdata/iosxe/capabilities.xml
var iosxeCapabilitiesXML []byte

//go:embed testdata/iosxe/capabilities.json
var iosxeCapabilitiesJSON []byte

//go:embed testdata/iosxe/yang-library.xml.gz
var iosxeLibraryXMLGz []byte

//go:embed testdata/iosxe/yang-library.json.gz
var iosxeLibraryJSONGz []byte

//go:embed testdata/iosxe/modules-state.json.gz
var iosxeModulesStateGz []byte

//go:embed testdata/iosxe/not-found.json
var iosxeNotFound []byte

//go:embed testdata/iosxe/post-exists.json
var iosxePostExists []byte

//go:embed testdata/iosxe/server-going-down.json
var iosxeGoingDown []byte

// iosxeUnauthorized is IOS XE's body for a missing or wrong login under
// /restconf, with WWW-Authenticate: Basic realm="restconf".
const iosxeUnauthorized = "{\n  \"ietf-restconf:errors\": {\n    \"error\": [\n      {\n        \"error-type\": \"protocol\",\n        \"error-tag\": \"access-denied\"\n      }\n    ]\n  }\n}\n"

// openrestyNotFound is openresty's default 404 page, IOS XE's answer at the
// login page host-meta redirects to without a valid login (no
// WWW-Authenticate).
const openrestyNotFound = "<html>\r\n<head><title>404 Not Found</title></head>\r\n<body>\r\n<center><h1>404 Not Found</h1></center>\r\n<hr><center>openresty</center>\r\n</body>\r\n</html>\r\n"

// IOSXEStartingPage is the page IOS XE answers with 502 while its RESTCONF
// starts. Its shape, not its bytes, is what was seen: an HTML comment naming
// Cisco's copyright, then an HTML document titled Error.
const IOSXEStartingPage = "<!--\n   Copyright (c) 2020 by Cisco Systems, Inc.\n   All rights reserved.\n-->\n<!DOCTYPE html>\n<html>\n<head>\n<meta charset=\"utf-8\">\n<title>Error</title>\n</head>\n<body>\n<h1>An error occurred.</h1>\n<p>Sorry, the page you are looking for is currently unavailable.<br/>Please try again later.</p>\n</body>\n</html>\n"

// IOSXELibraryXML returns the captured router's yang-library answer in XML.
func IOSXELibraryXML(t testing.TB) []byte { t.Helper(); return gunzip(t, iosxeLibraryXMLGz) }

// IOSXELibraryJSON returns the captured router's yang-library answer in JSON.
func IOSXELibraryJSON(t testing.TB) []byte { t.Helper(); return gunzip(t, iosxeLibraryJSONGz) }

// IOSXEModulesStateJSON returns the captured router's modules-state answer.
func IOSXEModulesStateJSON(t testing.TB) []byte { t.Helper(); return gunzip(t, iosxeModulesStateGz) }

// serveIOSXE answers r as IOS XE does.
func (s *Server) serveIOSXE(w http.ResponseWriter, r *http.Request, path string) {
	if path == "/.well-known/host-meta" {
		if !s.validLogin(r) {
			// Measured 2026-11-15 with pkg/restconf, which follows no
			// redirect: 302 to the WebUI's login page, whose own answer is
			// openresty's 404 (what a client following it sees).
			w.Header().Set("Location", "/login.html")
			w.WriteHeader(http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/xrd+xml")
		_, _ = w.Write(iosxeHostMeta)
		return
	}
	if path == "/login.html" {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(openrestyNotFound))
		return
	}
	if !strings.HasPrefix(path, "/restconf/") {
		s.iosxeAnswer(w, http.StatusNotFound, iosxeNotFound)
		return
	}
	s.mu.Lock()
	starting := s.opts.Starting > 0
	if starting {
		s.opts.Starting--
	}
	s.mu.Unlock()
	if starting {
		// The web server answers before RESTCONF behind it does, login or
		// none.
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(IOSXEStartingPage))
		return
	}
	if !s.validLogin(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="restconf"`)
		s.iosxeAnswer(w, http.StatusUnauthorized, []byte(iosxeUnauthorized))
		return
	}
	if s.opts.GoingDown && r.Method != http.MethodGet {
		s.iosxeAnswer(w, http.StatusInternalServerError, iosxeGoingDown)
		return
	}
	asXML := strings.Contains(r.Header.Get("Accept"), "yang-data+xml")
	switch path {
	case "/restconf/data/ietf-restconf-monitoring:restconf-state/capabilities":
		s.iosxeDocument(w, asXML, iosxeCapabilitiesXML, iosxeCapabilitiesJSON)
		return
	case "/restconf/data/ietf-yang-library:yang-library":
		s.iosxeDocument(w, asXML, gunzipped(iosxeLibraryXMLGz), gunzipped(iosxeLibraryJSONGz))
		return
	case "/restconf/data/ietf-yang-library:modules-state":
		s.iosxeAnswer(w, http.StatusOK, gunzipped(iosxeModulesStateGz))
		return
	}
	s.iosxeWrite(w, r, path)
}

// validLogin reports whether r carries the login the server asks for.
func (s *Server) validLogin(r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	return ok && user == s.opts.Username && pass == s.opts.Password
}

// iosxeAnswer writes body as JSON with status.
func (s *Server) iosxeAnswer(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/yang-data+json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// iosxeDocument writes the XML or the JSON form of one answer.
func (s *Server) iosxeDocument(w http.ResponseWriter, asXML bool, xmlBody, jsonBody []byte) {
	if asXML {
		w.Header().Set("Content-Type", "application/yang-data+xml")
		_, _ = w.Write(xmlBody)
		return
	}
	s.iosxeAnswer(w, http.StatusOK, jsonBody)
}

// iosxeWrite answers a read or write of a stored resource as IOS XE does.
func (s *Server) iosxeWrite(w http.ResponseWriter, r *http.Request, path string) {
	s.mu.Lock()
	res, exists := s.stored[path]
	s.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		if !exists {
			s.iosxeAnswer(w, http.StatusNotFound, iosxeNotFound)
			return
		}
		w.Header().Set("Content-Type", res.mediaType)
		_, _ = w.Write(res.body)
	case http.MethodPut:
		// 201 for a create and 204 for a replace are RFC 8040 section 4.5's;
		// the router's own answers to a put were not captured (its post
		// answered 201).
		s.Store(path, r.Header.Get("Content-Type"), readBody(r))
		if exists {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusCreated)
		}
	case http.MethodPatch:
		if !exists {
			s.iosxeAnswer(w, http.StatusNotFound, iosxeNotFound)
			return
		}
		s.Store(path, r.Header.Get("Content-Type"), readBody(r))
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if !exists {
			s.iosxeAnswer(w, http.StatusNotFound, iosxeNotFound)
			return
		}
		s.mu.Lock()
		delete(s.stored, path)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPost:
		body := readBody(r)
		child := postChild(body, s.opts.PostChild)
		if child == "" {
			s.iosxeAnswer(w, http.StatusBadRequest, iosxeNotFound)
			return
		}
		created := path + "/" + child
		s.mu.Lock()
		_, there := s.stored[created]
		s.mu.Unlock()
		if there {
			s.iosxeAnswer(w, http.StatusConflict, iosxePostExists)
			return
		}
		// IOS XE reads a created list entry back inside its list's array,
		// which is the shape a post sends it in.
		s.Store(created, r.Header.Get("Content-Type"), body)
		w.Header().Set("Location", s.URL+created)
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		w.WriteHeader(http.StatusCreated)
	default:
		s.iosxeAnswer(w, http.StatusMethodNotAllowed, iosxeNotFound)
	}
}

// readBody is r's body, bounded.
func readBody(r *http.Request) []byte {
	b, _ := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	return b
}

// postChild is the list entry a posted JSON document creates, as IOS XE
// names it in Location: its one member's node and its name key
// ({"Cisco-IOS-XE-native:Loopback": [{"name": 7701}]} is Loopback=7701), or
// fallback when the document is not that shape.
func postChild(body []byte, fallback string) string {
	var doc map[string][]map[string]any
	if json.Unmarshal(body, &doc) != nil || len(doc) != 1 {
		return fallback
	}
	for member, entries := range doc {
		if len(entries) != 1 {
			return fallback
		}
		name, ok := entries[0]["name"]
		if !ok {
			return fallback
		}
		node := member
		if i := strings.IndexByte(node, ':'); i >= 0 {
			node = node[i+1:]
		}
		key, _ := json.Marshal(name)
		return node + "=" + url.PathEscape(strings.Trim(string(key), `"`))
	}
	return fallback
}

// gunzipped is raw decompressed, for a fixture embedded whole.
func gunzipped(raw []byte) []byte {
	out, err := gunzipBytes(raw)
	if err != nil {
		return nil
	}
	return out
}
