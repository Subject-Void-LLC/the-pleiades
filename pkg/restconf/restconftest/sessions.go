// Package restconftest: IOS XE's cap on RESTCONF sessions, as a Catalyst
// 8000V (IOS XE 17.15) was measured holding to it on 2026-11-17. One
// keep-alive connection carried every request of a fetch, yet the 101st
// source of a fetch of every module was refused with 409 Conflict, error-tag
// in-use, "Too many sessions.", and a run started at once after, on new
// connections, was refused at its first read: the device counts each request
// as a session, whatever connection carries it, and holds it a while after
// its answer, until it expires.
//
// Options.SessionCap sets the cap. Sessions are released by an event, not by
// a clock, so a test holds the same whatever the machine's speed: every held
// session expires at once after Options.SessionReleaseAfter refusals in a
// row, and never when that is zero.
package restconftest

import (
	"net/http"
	"net/http/httptest"
	"strings"
)

// IOSXETooManySessions is the body IOS XE answers a request with, 409
// Conflict, while its sessions are at their cap. Its error-tag and
// error-message are the device's own; its error-type was not recorded and is
// the one IOS XE gave its other refusal of a session ("Server is going
// down"), application.
const IOSXETooManySessions = "{\n  \"ietf-restconf:errors\": {\n    \"error\": [\n      {\n        \"error-type\": \"application\",\n        \"error-tag\": \"in-use\",\n        \"error-message\": \"Too many sessions.\"\n      }\n    ]\n  }\n}\n"

// TooManySessionsXML is the same refusal in RFC 8040's XML encoding, which a
// server answering errors in the encoding asked for (Accept naming
// application/yang-data+xml first) would send.
const TooManySessionsXML = `<errors xmlns="urn:ietf:params:xml:ns:yang:ietf-restconf"><error><error-type>application</error-type><error-tag>in-use</error-tag><error-message>Too many sessions.</error-message></error></errors>`

// openSession opens a session for r under Options.SessionCap, reporting
// whether one was opened: true for every request when no cap is set and for
// a request carrying no credential, which logs nobody in, and false, counting
// a refusal, while the cap's sessions are all held. The refusal that is the
// SessionReleaseAfter-th in a row releases every held session, as their
// expiry would.
func (s *Server) openSession(r *http.Request) bool {
	if s.opts.SessionCap <= 0 || r.Header.Get("Authorization") == "" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions < s.opts.SessionCap {
		s.sessions++
		s.refusedInARow = 0
		return true
	}
	s.sessionRefusals++
	s.refusedInARow++
	if s.opts.SessionReleaseAfter > 0 && s.refusedInARow >= s.opts.SessionReleaseAfter {
		s.sessions, s.refusedInARow = 0, 0
	}
	return false
}

// refuseSession answers r as a server at its session cap does: in XML when
// Options.SessionRefusalXML is set and r asks for XML first, else in IOS XE's
// JSON. Under Options.SessionApplyRefused a write is applied first, its own
// answer discarded, as a device that applied a write before refusing it
// would (which IOS XE was never seen to do).
func (s *Server) refuseSession(w http.ResponseWriter, r *http.Request, path string, modulesState []byte) {
	if s.opts.SessionApplyRefused && r.Method != http.MethodGet {
		s.route(httptest.NewRecorder(), r, path, modulesState)
	}
	if s.opts.SessionRefusalXML && strings.HasPrefix(r.Header.Get("Accept"), "application/yang-data+xml") {
		w.Header().Set("Content-Type", "application/yang-data+xml")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(TooManySessionsXML))
		return
	}
	s.iosxeAnswer(w, http.StatusConflict, []byte(IOSXETooManySessions))
}

// SessionRefusals returns how many requests were refused for too many
// sessions.
func (s *Server) SessionRefusals() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionRefusals
}
