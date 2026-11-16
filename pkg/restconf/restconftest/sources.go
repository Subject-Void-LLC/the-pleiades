// Package restconftest: YANG sources a Server serves, the way IOS XE 17.15.1a
// was measured serving them (2026-11-15): its modules-state names each
// module's source by a schema leaf at the address the device was reached at,
// the default port written out, under /restconf/tailf/modules/<name>/<revision>,
// and a GET there answers the source. A test sets Options.Sources to the
// sources, and Options.SourceOrigin to have the schema leaves name another
// origin, as a device naming an address other than its base URL's would.
package restconftest

import (
	"encoding/xml"
	"net/http"
	"sort"
	"strings"
)

// sourcePrefix is where IOS XE serves YANG sources.
const sourcePrefix = "/restconf/tailf/modules/"

// serveSource answers the requests Options.Sources makes the server answer,
// reporting whether it answered: modules-state, naming each source's URL, and
// each source.
func (s *Server) serveSource(w http.ResponseWriter, r *http.Request, path string) bool {
	if len(s.opts.Sources) == 0 {
		return false
	}
	switch {
	case path == s.opts.Root+"/data/ietf-yang-library:modules-state":
		s.library(w, s.sourcesState(r))
		return true
	case strings.HasPrefix(path, sourcePrefix) && r.Method == http.MethodGet:
		// /restconf/tailf/modules/<name>/<revision>, or <name> alone for a
		// module with no revision, as IOS XE names ATM-FORUM-TC-MIB.
		key := strings.Replace(strings.TrimPrefix(path, sourcePrefix), "/", "@", 1)
		src, ok := s.opts.Sources[key]
		if !ok {
			s.errors(w, r, http.StatusNotFound, "application", "invalid-value", "", "uri keypath not found")
			return true
		}
		w.Header().Set("Content-Type", "application/yang")
		_, _ = w.Write(src)
		return true
	}
	return false
}

// sourcesState is a modules-state document listing every source as an
// implemented module, with a schema leaf at the origin the request reached
// (or Options.SourceOrigin), the default port written out as IOS XE does.
func (s *Server) sourcesState(r *http.Request) []byte {
	origin := s.opts.SourceOrigin
	if origin == "" {
		origin = "https://" + r.Host
		if !strings.Contains(r.Host, ":") {
			origin += ":443"
		}
	}
	keys := make([]string, 0, len(s.opts.Sources))
	for key := range s.opts.Sources {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(`<modules-state xmlns="urn:ietf:params:xml:ns:yang:ietf-yang-library"><module-set-id>sources</module-set-id>`)
	for _, key := range keys {
		name, revision, _ := strings.Cut(key, "@")
		b.WriteString("<module><name>" + name + "</name><revision>" + revision + "</revision><namespace>urn:test:" + name +
			"</namespace><conformance-type>implement</conformance-type><schema>")
		ref := sourcePrefix + name
		if revision != "" {
			ref += "/" + revision
		}
		_ = xml.EscapeText(&b, []byte(origin+ref))
		b.WriteString("</schema></module>")
	}
	b.WriteString("</modules-state>")
	return []byte(b.String())
}

// SourceRequests returns how many requests asked for a source, of any
// module: a test of a refusal made before contact holds it to zero.
func (s *Server) SourceRequests() int {
	n := 0
	for _, p := range s.Paths() {
		if strings.HasPrefix(p, sourcePrefix) {
			n++
		}
	}
	return n
}
