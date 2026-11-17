// Package restconftest: IOS XE's cap on RESTCONF sessions, as a Catalyst
// 8000V (IOS XE 17.15) was measured holding to it on 2026-11-17. One
// keep-alive connection carried every request of a fetch, yet the 101st
// source of a fetch of every module was refused with 409 Conflict, error-tag
// in-use, "Too many sessions.", and a run started at once after, on new
// connections, was refused at its first read: the device counts each request
// as a session, whatever connection carries it, and holds it for a while
// after its answer. Options.SessionCap and Options.SessionHold set that cap
// and that while.
package restconftest

import (
	"net/http"
	"time"
)

// IOSXETooManySessions is the body IOS XE answers a request with, 409
// Conflict, while its sessions are at their cap. Its error-tag and
// error-message are the device's own; its error-type was not recorded and is
// the one IOS XE gave its other refusal of a session ("Server is going
// down"), application.
const IOSXETooManySessions = "{\n  \"ietf-restconf:errors\": {\n    \"error\": [\n      {\n        \"error-type\": \"application\",\n        \"error-tag\": \"in-use\",\n        \"error-message\": \"Too many sessions.\"\n      }\n    ]\n  }\n}\n"

// openSession opens a session for r under Options.SessionCap, reporting
// whether one was opened: true for every request when no cap is set and for
// a request carrying no credential, which logs nobody in, and false, counting
// a refusal, while the cap's sessions are all held.
func (s *Server) openSession(r *http.Request) bool {
	if s.opts.SessionCap <= 0 || r.Header.Get("Authorization") == "" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	held := s.sessions[:0]
	for _, expires := range s.sessions {
		if expires.After(now) {
			held = append(held, expires)
		}
	}
	s.sessions = held
	if len(s.sessions) >= s.opts.SessionCap {
		s.sessionRefusals++
		return false
	}
	s.sessions = append(s.sessions, now.Add(s.opts.SessionHold))
	return true
}

// SessionRefusals returns how many requests were refused for too many
// sessions.
func (s *Server) SessionRefusals() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionRefusals
}
