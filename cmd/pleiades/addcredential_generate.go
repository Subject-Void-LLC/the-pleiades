// Package main: add-credential --generate, a new login for a machine
// Pleiades is about to create: the key type it holds, whether it has a
// password, the stored login it refuses to discard, and what it prints.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// generateKey makes a generated login. It is a variable so a test can count
// the keys a command made: a refused command makes none.
var generateKey = credential.GenerateWith

// generateOnlyFlags are the flags that mean something only with --generate,
// each refused without it before anything is read.
var generateOnlyFlags = []string{"replace", "key-type", "no-password", "json"}

// rsaSignatureNote is what generating an RSA key prints on stderr. The line
// says ssh-rsa because that names the key's format; the signature a device
// sees is another matter, and a device that takes only SHA-1 refuses it.
const rsaSignatureNote = "note: an RSA key's public key line starts with ssh-rsa because that names the key's format, not how it signs: " +
	"Pleiades signs with it using rsa-sha2-256 or rsa-sha2-512, so a device that accepts only SHA-1 ssh-rsa signatures refuses it " +
	"unless that device sets ssh_allow_legacy_user_key_signatures"

// generateRequest is one add-credential --generate call, its flags already
// checked.
type generateRequest struct {
	dir, device, username string
	options               credential.GenerateOptions
	replace, asJSON       bool
	save                  credential.SaveOptions
	// readEnable reads the enable secret, "" when none was asked for.
	readEnable func() (string, error)
	// readSNMP adds the SNMP family to a credential when SNMP flags were
	// given, reading its secrets.
	readSNMP func(credential.Credential) (credential.Credential, error)
}

// checkGenerateFlags refuses a flag that applies only to --generate when
// --generate is absent, and otherwise returns the key type asked for. It runs
// before any secret is read or warning printed, so a typo costs nothing.
func checkGenerateFlags(given func(name string) bool, generate bool, keyType string) (credential.KeyType, error) {
	if !generate {
		for _, name := range generateOnlyFlags {
			if given(name) {
				return "", fmt.Errorf("--%s only applies to --generate", name)
			}
		}
		return "", nil
	}
	parsed, err := credential.ParseKeyType(keyType)
	if err != nil {
		return "", fmt.Errorf("--key-type: %w", err)
	}
	return parsed, nil
}

// generateCredential stores a new random key, and a password unless
// --no-password, for the device in the project's vault, and prints what it
// stored: the key's type and fingerprint, then the public key line, labeled
// user@device, which is what the machine is given so the key may log in.
//
// Everything that can refuse is done first, in this order: the stored login
// it would discard (refused without --replace), then the secrets read from
// standard input or a prompt, then the key is made, then saved, then printed,
// so a refusal reads no input and makes no key, and an interrupt stores
// nothing.
func generateCredential(req generateRequest) error {
	key, err := credential.ResolveMasterKey(vaultDir(req.dir))
	if err != nil {
		return fmt.Errorf("failed to resolve master key: %w", err)
	}
	store, err := credential.NewFileStore(vaultDir(req.dir), key)
	if err != nil {
		return fmt.Errorf("failed to open the credential store: %w", err)
	}
	replaced, err := replaceableLogin(store, req.device, req.replace, req.save.Families)
	if err != nil {
		return err
	}
	enable, err := req.readEnable()
	if err != nil {
		return err
	}
	snmp, err := req.readSNMP(credential.Credential{})
	if err != nil {
		return err
	}

	comment := req.username + "@" + req.device
	cred, err := generateKey(req.username, comment, req.options)
	if err != nil {
		return err
	}
	cred.EnablePassword = enable
	cred = cred.WithSNMP(snmp.SNMP())
	public, err := credential.PublicKey(cred, comment)
	if err != nil {
		return err
	}
	info, err := credential.DescribeKey(cred)
	if err != nil {
		return err
	}
	if err := credential.SaveFileStoreWith(vaultDir(req.dir), key, req.device, cred, req.save); err != nil {
		return fmt.Errorf("failed to save credential: %w", err)
	}

	for _, w := range sameAsUsernameWarnings(cred) {
		fmt.Fprintln(os.Stderr, "WARNING: "+w)
	}
	if info.Type.IsRSA() {
		fmt.Fprintln(os.Stderr, rsaSignatureNote)
	}
	report := clireport.GeneratedCredential{
		SchemaVersion: clireport.GeneratedCredentialVersion, Device: req.device, Username: req.username,
		KeyType: string(info.Type), KeyBits: info.Bits, KeyFingerprint: info.Fingerprint, PublicKey: public,
		PasswordGenerated: cred.Password != "", EnablePasswordStored: enable != "", Unbound: req.save.Unbound,
		ReplacedKeyFingerprint: replaced,
	}
	if req.asJSON {
		return writeJSON(os.Stdout, report)
	}
	return printGenerated(os.Stdout, report, req.save)
}

// replaceableLogin refuses to discard a stored login without --replace, and
// with it returns the fingerprint of the key being discarded when that can
// be read, so the operator knows which line to remove from the machine.
//
// Only a stored login is what a new one would put out of reach; a device
// holding SNMP fields alone has none to lose. A stored credential the read
// refuses (credential.UnusableError) may hold a login all the same, so it
// too needs --replace, which then replaces it, as its refusal says to; a
// fault in a family this save keeps is named instead, since a new login
// beside it would leave the credential refused.
func replaceableLogin(store credential.Store, device string, replace bool, families credential.Families) (string, error) {
	stored, err := store.Lookup(context.Background(), device)
	var unusable *credential.UnusableError
	switch {
	case errors.As(err, &unusable) && unusable.Family&families == 0:
		return "", fmt.Errorf("%w; --generate replaces only its %s fields, so store its %s fields again first", err, families, unusable.Family)
	case errors.As(err, &unusable) && !replace:
		return "", fmt.Errorf("%w; with --generate, --replace discards it, and whatever it logs in to is then out of reach", err)
	case errors.As(err, &unusable):
		// --replace, and the save replaces the family at fault.
		return "", nil
	case err == nil && stored.HasLogin() && !replace:
		return "", fmt.Errorf("device %q already has a credential; --replace discards it, and whatever it logs in to is then out of reach", device)
	case err != nil && !errors.Is(err, credential.ErrNotFound):
		return "", fmt.Errorf("failed to read the credential store: %w", err)
	}
	if err != nil || len(stored.PrivateKeyPEM) == 0 {
		return "", nil
	}
	old, err := credential.DescribeKey(stored)
	if err != nil {
		return "", nil
	}
	return old.Fingerprint, nil
}

// printGenerated prints a generated login as text: one line saying what was
// stored, the replaced key's fingerprint when there was one, the key's type
// and fingerprint, and the public key line last, so a script reading
// everything after "public key: " reads the key alone. No line holds a
// secret.
func printGenerated(w io.Writer, r clireport.GeneratedCredential, opts credential.SaveOptions) error {
	parts := []string{"an " + r.KeyType + " key"}
	if r.PasswordGenerated {
		parts = append(parts, fmt.Sprintf("a %d-character password", credential.GeneratedPasswordLength))
	} else {
		parts = append(parts, "no password")
	}
	if r.EnablePasswordStored {
		parts = append(parts, "the enable secret given")
	}
	stored := strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	lines := []string{fmt.Sprintf("stored a generated credential for device %q%s: %s, for %s", r.Device, unboundNote(opts), stored, r.Username)}
	if r.ReplacedKeyFingerprint != "" {
		lines = append(lines, "replaced key fingerprint: "+r.ReplacedKeyFingerprint)
	}
	lines = append(lines, "key type: "+r.KeyType, "fingerprint: "+r.KeyFingerprint, "public key: "+r.PublicKey)
	_, err := fmt.Fprintln(w, strings.Join(lines, "\n"))
	return err
}
