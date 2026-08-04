package ssh

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// hostKeyCallback builds the ssh.HostKeyCallback Exec uses to verify a
// target's host key, per opts.
//
// This is the phase's core MITM-prevention requirement, so it is
// deliberately fail-closed at every branch: InsecureSkipHostKeyVerify is
// the only way to bypass verification, and it must be set explicitly and
// loudly by the caller. A missing known_hosts file is never treated as
// "trust on first use": it is a hard error, mirroring `ssh -o
// StrictHostKeyChecking=yes` against an unpopulated file. A host whose
// key does not match a known_hosts entry, or that has no entry at all in
// a file that does exist, is rejected by the knownhosts callback itself
// with a clear, actionable error (knownhosts.KeyError).
//
// hostKeyCallback is called fresh on every Exec call rather than cached
// once at New time. This keeps New's signature error-free (it has no
// error return to give per the plan) and means a known_hosts file that
// does not exist yet at construction time, but is populated before the
// first real Exec call, still works correctly; the small repeated
// file-stat/parse cost is negligible next to a real network round trip.
func hostKeyCallback(opts Options) (ssh.HostKeyCallback, error) {
	if opts.InsecureSkipHostKeyVerify {
		// Only reachable via this explicit, loud opt-in; never the
		// default, and never silently substituted for a broken
		// known_hosts source below.
		return ssh.InsecureIgnoreHostKey(), nil // #nosec G106 -- explicit opt-in only, see above
	}

	path := opts.KnownHostsPath
	if path == "" {
		return nil, errors.New("no known_hosts path configured and $HOME could not be determined; host key verification cannot proceed")
	}

	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Fail closed: a missing known_hosts file must never be
			// silently treated as "trust everything."
			return nil, fmt.Errorf("known_hosts file %q not found, host key verification cannot proceed (set InsecureSkipHostKeyVerify to explicitly bypass this)", path)
		}
		return nil, fmt.Errorf("stat known_hosts file %q: %w", path, err)
	}

	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("parse known_hosts file %q: %w", path, err)
	}
	return cb, nil
}
