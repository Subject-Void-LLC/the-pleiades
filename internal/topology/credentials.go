// Reading the mesh identity this process authenticates as.
//
// This is the client half of what internal/meshid mints. That package
// turns keys and a permission set into a credential; this reads one from
// wherever the deployment put it and hands it to Connect. Neither knows
// about the other, which is why the Runner can hold a credential it has no
// way to have issued.
package topology

import (
	"fmt"
	"os"

	"github.com/nats-io/nkeys"
)

// CredentialsEnv names the variable a deployment puts its credential path
// in.
//
// Named here rather than written as a literal in each composition root,
// because it appears in three binaries, a compose file, a Helm chart and
// an error message, and a variable whose name is spelled six times is
// spelled wrong once.
const CredentialsEnv = "NATS_CREDS_FILE"

// CredentialsFromEnv reads a NATS credential from the file path names.
//
// It returns nil for an empty path, which the caller passes straight to
// Connect as no option at all, so an unset variable is byte-identically
// today's unauthenticated dial. That is what lets enforcement default off.
//
// A PATH rather than the credential body, even though WithCredentials
// takes bytes and an environment variable would have been less code. The
// reason is specific to this platform rather than general hygiene:
// cmd/runner execs subprocesses, an ssh client and a container running an
// unconverted Ansible playbook among them, and a process environment is
// inherited by every child and readable from /proc/<pid>/environ. A seed
// in the environment is therefore a seed every job the Runner runs can
// read. A file at 0400 is not. The bytes never reach the environment: this
// reads them, hands them to WithCredentials, and internal/topology keeps
// them in memory for the life of the connection.
//
// The credential is PARSED here rather than only read, so a truncated or
// mangled file is a startup error naming the file, instead of an
// authorization failure against the broker at some later moment that names
// nothing. Neither error ever carries the file's contents.
func CredentialsFromEnv(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}

	// The same guard TLSFromEnv carries, for the same reason: a directory,
	// a device or a named pipe here is a configuration mistake rather than
	// a credential, and reading one would hang or produce a confusing
	// parse error instead of naming the real problem.
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s %q: %w", CredentialsEnv, path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %q is not a regular file", CredentialsEnv, path)
	}

	// #nosec G304 -- the path is this deployment's own identity material,
	// named by an operator in NATS_CREDS_FILE and read at startup. It is
	// never a value from a request. This is the identical justification
	// TLSFromEnv records for reading a configured certificate path, and
	// the alternative is not authenticating to the broker at all.
	creds, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s %q: %w", CredentialsEnv, path, err)
	}

	if err := validateCredential(creds); err != nil {
		return nil, fmt.Errorf("%s %q: %w", CredentialsEnv, path, err)
	}
	return creds, nil
}

// validateCredential reports whether creds holds both halves a NATS
// credential needs.
//
// A .creds file is one file carrying two things, a user JWT and the seed
// that signs the server's nonce, and a file holding only one of them is
// the likeliest way to get this wrong: a copy that lost its tail, or a JWT
// pasted without its key. Both halves are checked by the same functions
// the dial path uses, so a file that passes here cannot fail there.
//
// The parsed values are deliberately discarded. This answers "is this a
// credential", and nothing else in this function's caller is entitled to
// hold key material any longer than the connection does.
func validateCredential(creds []byte) error {
	if _, err := nkeys.ParseDecoratedJWT(creds); err != nil {
		return fmt.Errorf("no user jwt could be read from it: %w", err)
	}
	kp, err := nkeys.ParseDecoratedUserNKey(creds)
	if err != nil {
		return fmt.Errorf("no user key could be read from it: %w", err)
	}
	// Wiping rather than letting it fall out of scope, because nkeys holds
	// the seed in memory until told otherwise and this copy is about to
	// have no owner.
	kp.Wipe()
	return nil
}
