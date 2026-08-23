package remoteexec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// hostKeyCallback builds the ssh.HostKeyCallback one connection uses to
// verify its target's host key, per opts.
//
// Host key verification is what stops a machine in the middle answering
// for the device, so every branch here fails closed.
// InsecureSkipHostKeyVerify is the only way to bypass it, and it must be
// set explicitly and loudly by the caller. A missing known_hosts file is
// never treated as trust on first use: it is a hard error, mirroring
// `ssh -o StrictHostKeyChecking=yes` against an unpopulated file. A host
// whose key does not match its entry, or that has no entry at all in a
// file that does exist, is rejected by the knownhosts callback itself
// with a clear, actionable error.
//
// This runs fresh for every connection rather than once when a Runner is
// built. That keeps New free of an error return, means a known_hosts
// file populated after construction still works, and is what lets
// Shared memoize a Runner without also freezing a $HOME that a later
// caller may have changed. The repeated stat and parse cost is nothing
// next to a real network round trip.
func hostKeyCallback(opts Options) (ssh.HostKeyCallback, error) {
	return hostKeyCallbackFor(opts, opts.InsecureSkipHostKeyVerify)
}

// hostKeyCallbackFor is hostKeyCallback with the skip-verification
// decision taken as an explicit parameter rather than always read from
// opts.InsecureSkipHostKeyVerify, so a Hop's own
// InsecureSkipHostKeyVerify can override it for just that hop's leg of a
// chain without disturbing the Runner-wide default (or, in the more
// dangerous direction, without a lab bastion's own opt-out silently
// reaching the production device tunneled through it: each leg of
// Connect's loop calls this with its own leg's decision, never the
// Runner's).
func hostKeyCallbackFor(opts Options, insecureSkipHostKeyVerify bool) (ssh.HostKeyCallback, error) {
	if insecureSkipHostKeyVerify {
		// Reachable only through this explicit, loud opt-in. Never the
		// default, and never silently substituted for a broken
		// known_hosts source below.
		return ssh.InsecureIgnoreHostKey(), nil // #nosec G106 -- explicit opt-in only, see above
	}

	path, err := knownHostsPath(opts)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Fail closed: a missing known_hosts file must never become
			// "trust everything."
			//
			// Both fixes are named, and the order they are named in is the
			// point. The secure one comes first, because "point me at the
			// right file" is what an operator whose mount landed elsewhere
			// actually needs, and an error that offers only the bypass
			// teaches every reader to reach for the bypass.
			return nil, fmt.Errorf("known_hosts file %q not found, host key verification cannot proceed (set %s to the right file, or set InsecureSkipHostKeyVerify to explicitly bypass verification)", path, KnownHostsEnv)
		}
		return nil, fmt.Errorf("stat known_hosts file %q: %w", path, err)
	}

	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("parse known_hosts file %q: %w", path, err)
	}
	return cb, nil
}

// KnownHostsEnv names the environment variable a DEPLOYMENT sets to
// point every SSH connection this process makes at one known_hosts file.
//
// It exists because there was no way at all to configure this. Every
// caller in this repository builds its Options with an empty
// KnownHostsPath: both Collection methods construct one from task
// parameters, which carry no path, and both composition roots that build
// a transport pass a bare Options{}. So every SSH connection resolved
// $HOME/.ssh/known_hosts, and the published runner image sets no HOME and
// ships no such file. Verification could not succeed there at all, which
// left insecure_skip_host_key_verify as the only working path on that
// tier: a security control whose off switch was the only thing that
// worked. FAILURE_PATTERNS.md #150.
//
// A package reading its own environment variable is usually a smell, and
// it is the right answer here for one specific reason: this is the only
// channel that reaches the code that needs it. Under the Walk tier a
// Collection method runs inside a per-task subprocess with no composition
// root of its own and no argument it controls, so a value wired at
// startup cannot reach it. The subprocess inherits the environment
// (internal/adapters/native's exec.CommandContext sets no Env), so this
// does, and one variable read in one place configures all four call sites
// at once rather than each of them growing a parameter.
//
// Deliberately a PATH and never a POLICY. There is no environment
// variable that turns host key verification off, and there must not be
// one: an operator who sets a variable once forgets it, while a task
// parameter is written in the runbook next to the command it applies to
// and shows up in review. The only way to skip verification stays the
// loud, per-task opt-in.
const KnownHostsEnv = "PLEIADES_KNOWN_HOSTS"

// knownHostsPath returns the known_hosts file to verify against, from the
// first of three sources that names one:
//
//  1. the caller's explicit Options.KnownHostsPath, which one call site
//     chose for this one connection;
//  2. the KnownHostsEnv environment variable, which the deployment chose
//     for this whole process;
//  3. "$HOME/.ssh/known_hosts", which is where the person running this
//     already keeps theirs.
//
// That is OpenSSH's own layering (-o UserKnownHostsFile beats
// GlobalKnownHostsFile beats the default), so an operator who knows ssh
// already knows this. Most specific wins, which is also AGENTS.md's
// hierarchical policy principle.
//
// All three resolve here, per connection, rather than once when a Runner
// is built. That keeps New free of an error return, means a file written
// or a variable exported after construction still counts, and is what
// lets Shared memoize a Runner without also freezing the environment of
// whichever caller happened to build it first.
func knownHostsPath(opts Options) (string, error) {
	if opts.KnownHostsPath != "" {
		return opts.KnownHostsPath, nil
	}

	// An empty value counts as unset, matching how every other environment
	// variable in this repository is read. A deployment that exports the
	// name with no value has configured nothing, and silently treating ""
	// as a path would report a missing file named "" instead.
	if fromEnv := os.Getenv(KnownHostsEnv); fromEnv != "" {
		return fromEnv, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		// Name the variable, because this is the error a container hits and
		// setting it is the fix. Without that the message describes an
		// environment problem the reader cannot act on, which is exactly how
		// #150 stayed invisible.
		return "", fmt.Errorf("no known_hosts path configured and the home directory could not be determined, host key verification cannot proceed: set %s to a known_hosts file: %w", KnownHostsEnv, err)
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}
