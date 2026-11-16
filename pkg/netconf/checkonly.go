// Package netconf: a session limited to what a check through the candidate
// datastore may send (owner decision 2026-11-14), and the session holding a
// lock a server denied.
package netconf

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// checkRPCs are the RPCs a session limited to a check sends, each as the
// start of the body this package writes for it: reads, the candidate's lock
// and unlock, an edit, a validate and a discard of the candidate, and the
// polite close. Nothing that can change running, startup or another
// session (commit, cancel-commit, copy-config, delete-config, kill-session,
// an edit of any other datastore) is among them.
var checkRPCs = []string{
	"<get>",
	"<get-config>",
	"<lock><target><candidate/></target></lock>",
	"<unlock><target><candidate/></target></unlock>",
	"<edit-config><target><candidate/></target>",
	"<validate><source><candidate/></source></validate>",
	"<discard-changes/>",
	"<close-session/>",
}

// LimitToCheck limits the session, from now on, to the RPCs a check through
// the candidate datastore sends. Any other RPC is refused before a byte of it
// is written, so a mistake in a check (a commit, a write to running) reaches
// no device. It cannot be undone on the session.
func (s *Session) LimitToCheck() { s.checkOnly = true }

// refuseInCheck is the refusal of body on a session limited to a check, or
// nil when body is one a check sends.
func (s *Session) refuseInCheck(body string) error {
	if !s.checkOnly {
		return nil
	}
	for _, allowed := range checkRPCs {
		if strings.HasPrefix(body, allowed) {
			return nil
		}
	}
	return fmt.Errorf("netconf: %s is refused on a session limited to a check through the candidate datastore, which sends only reads, the candidate's lock, edit, validate and discard, and close-session; nothing was sent", rpcName(body))
}

// rpcName is the first element name of body, the RPC it is.
func rpcName(body string) string {
	name := strings.TrimPrefix(body, "<")
	if i := strings.IndexAny(name, " />"); i >= 0 {
		name = name[:i]
	}
	return name
}

// lockHolder matches error-info's session-id, RFC 6241 section 7.5's answer
// to a lock held by another session, with or without a prefix.
var lockHolder = regexp.MustCompile(`<(?:[A-Za-z_][A-Za-z0-9_.-]*:)?session-id(?:\s[^>]*)?>\s*([0-9]+)\s*</`)

// LockHolder reports whether err is a server's lock-denied, and the session
// holding the lock when the server named it: "0" is RFC 6241's answer for a
// lock held by something other than a NETCONF session, and "" means the
// server named none.
func LockHolder(err error) (string, bool) {
	var errs RPCErrors
	if !errors.As(err, &errs) {
		return "", false
	}
	for _, e := range errs {
		if e.Tag != "lock-denied" {
			continue
		}
		if m := lockHolder.FindStringSubmatch(e.Info); m != nil {
			return m[1], true
		}
		return "", true
	}
	return "", false
}
