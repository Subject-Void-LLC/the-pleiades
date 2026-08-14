package archtest

import (
	"sort"
	"strings"
	"testing"
)

// The structural enforcement of two rules internal/localauth's package doc
// states and cannot enforce about itself.
//
// The first is that a password hash is not envelope encryption. The second
// is that the set of packages allowed to accept a plaintext password stays
// small enough to review. Both are the kind of rule that survives exactly
// as long as everybody remembers it, which is why they are tests.

// localAuthPackage is the one package that accepts a plaintext password.
const localAuthPackage = modulePath + "/internal/localauth"

// reversibleCryptoPackage is the envelope encryption service.
const reversibleCryptoPackage = modulePath + "/internal/crypto"

// localAuthConsumerAllowlist is every package permitted to import
// internal/localauth.
//
// It is empty at the stage that introduces the package, and that is
// correct rather than an oversight: nothing calls a password store until
// the login handler and the controller subcommands land, and listing them
// ahead of their own wiring would be an allowlist entry with nothing behind
// it. TestLocalAuthAllowlistHasNoStaleEntries reports that shape rather
// than failing on it, the same treatment the credential resolver's own
// allowlist already gets.
//
// cmd/ composition roots are exempt below rather than listed here, because
// constructing a concrete implementation and handing it to whoever needs it
// is what a composition root is for.
//
// Adding an entry is a real decision. The question to answer first is not
// "does this package need to check a password" but "can it take a proven
// localauth.Account instead", which for anything downstream of the login
// handler is yes.
var localAuthConsumerAllowlist = map[string]bool{}

// TestLocalAuthNeverImportsReversibleCrypto is the assertion the phase that
// built internal/localauth cared most about.
//
// internal/crypto is reversible BY DESIGN: Service declares Encrypt and
// Decrypt as a pair, EnvelopeService.Decrypt is public, one process-wide
// KEK from MASTER_ENCRYPTION_KEY covers every row, and a previousKEK is
// retained so old ciphertext still decrypts. Every one of those properties
// is correct for a credential a runbook has to replay and wrong for a
// password: a reversible password store is a plaintext password store with
// extra steps, and one key compromise exposes every account at once rather
// than forcing a per-account attack.
//
// It is a test rather than a comment because internal/crypto is the
// NEAREST EXISTING PRIMITIVE. Reaching for it is the predictable mistake
// here, not an unlikely one, and a comment saying "do not" is read only by
// people who already went looking.
func TestLocalAuthNeverImportsReversibleCrypto(t *testing.T) {
	// Checked over Deps rather than Imports, so the hash path cannot reach
	// the envelope service through an intermediary either.
	for _, pkg := range goList(t, true, localAuthPackage+"/...") {
		for _, dep := range pkg.Deps {
			if dep == reversibleCryptoPackage {
				t.Errorf(
					"%s depends on %s, which is reversible encryption.\n"+
						"A password must be verified, never decrypted: internal/crypto's Decrypt is public, one "+
						"process-wide KEK covers every row, and a previousKEK is deliberately retained so old "+
						"ciphertext still decrypts. Storing a password through it would mean a single key "+
						"compromise exposes every account at once. Use internal/localauth's own Argon2id path, "+
						"which is one-way and carries a per-password salt and work factor.",
					pkg.ImportPath, dep)
			}
		}
	}
}

// TestOnlyDesignatedConsumersImportLocalAuth keeps the plaintext-accepting
// surface small.
//
// internal/localauth never RETURNS a password or a hash, so this is a
// narrower rule than the credential resolver's: the risk is not that a
// consumer leaks a secret it was handed, it is that plaintext passwords
// start flowing through packages nobody audited for it, and that a second
// notion of "is this person who they say they are" grows somewhere other
// than the one place that decides it.
func TestOnlyDesignatedConsumersImportLocalAuth(t *testing.T) {
	offenders := make([]string, 0)

	for _, pkg := range goList(t, false, modulePath+"/...") {
		switch {
		case pkg.ImportPath == localAuthPackage:
			continue
		case localAuthConsumerAllowlist[pkg.ImportPath]:
			continue
		case strings.HasPrefix(pkg.ImportPath, modulePath+"/cmd/"):
			// A composition root wires concrete implementations into
			// interfaces, the same carve-out the credential resolver and
			// the concrete-driver allowlists already make.
			continue
		}

		for _, imp := range pkg.Imports {
			if imp == localAuthPackage {
				offenders = append(offenders, pkg.ImportPath)
			}
		}
	}

	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf(
			"these packages import internal/localauth, which accepts plaintext passwords: %v\n"+
				"Ask whether the package can take an already-proven localauth.Account instead. For anything "+
				"downstream of the login handler it can, and authenticating in a second place is how two "+
				"answers to \"who is this caller\" start to drift apart.",
			offenders)
	}
}

// TestLocalAuthAllowlistHasNoStaleEntries keeps the allowlist a true
// record.
//
// A stale entry is a standing, unexamined permission to accept passwords,
// and the next package to occupy that import path inherits it silently.
func TestLocalAuthAllowlistHasNoStaleEntries(t *testing.T) {
	seen := make(map[string]bool, len(localAuthConsumerAllowlist))
	for _, pkg := range goList(t, false, modulePath+"/...") {
		for _, imp := range pkg.Imports {
			if imp == localAuthPackage {
				seen[pkg.ImportPath] = true
			}
		}
	}

	stale := make([]string, 0)
	for pkg := range localAuthConsumerAllowlist {
		if !seen[pkg] {
			stale = append(stale, pkg)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Logf(
			"localAuthConsumerAllowlist entries that do not yet import internal/localauth: %v\n"+
				"A log rather than a failure while the login handler and the controller subcommands are still "+
				"being wired. Once they land, promote this to an error so the allowlist cannot rot.",
			stale)
	}
}

// TestLocalAuthDependsOnAPortRatherThanReachingIntoStorage documents the
// direction that keeps the verification logic testable without a database.
//
// The rule is not "never import internal/ent": the ent adapter in this
// package obviously does, exactly as internal/auth's own ent-backed
// RoleBindingRepository does. The rule is that internal/ent must not import
// BACK, because that is a cycle, and the usual way a cycle gets resolved is
// by collapsing the two, which would put password verification inside the
// generated storage layer where no boundary could contain it.
func TestPersistenceNeverImportsLocalAuth(t *testing.T) {
	for _, pkg := range goList(t, true, modulePath+"/internal/ent/...") {
		for _, dep := range pkg.Deps {
			if dep == localAuthPackage {
				t.Errorf(
					"%s depends on %s. internal/localauth's adapter imports internal/ent, so this direction is "+
						"an import cycle, and the usual fix for a cycle is to merge the two, which would put "+
						"password verification inside generated storage code.",
					pkg.ImportPath, dep)
			}
		}
	}
}
