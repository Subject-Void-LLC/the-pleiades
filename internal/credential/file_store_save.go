package credential

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"go.yaml.in/yaml/v3"
)

// SaveFileStore adds or updates exactly one device's credential in the
// credentials file under dir, encrypting each of cred's secret fields that
// is non-empty with the master key key. Every other device already stored
// in the file is preserved unchanged.
//
// It replaces each family cred sets a field of (a login, its SNMP fields,
// or both; families.go) with a fresh encoding of cred, and keeps the other
// family as stored. Within a family it does not merge cred's fields onto
// whatever was stored before. This matters when switching a device from
// password to key authentication (or back): calling SaveFileStore with only
// PrivateKeyPEM set clears any previously stored password for that device,
// rather than leaving stale, no-longer-intended credential material on disk
// next to the new key, while the device's SNMP fields stay. SaveFileStoreWith
// names the families outright.
//
// The file is written atomically (temp file in the same directory, then
// os.Rename over the target) with mode 0o600, mirroring atomicWriteFile
// in internal/inventory/file_repository_save.go, so a crash mid-write
// never leaves a torn or half-written credentials file. That helper is
// duplicated here (as atomicWriteCredentialsFile) rather than imported,
// since internal/inventory does not export it and this package should
// not depend on an unrelated package purely for one small write helper.
func SaveFileStore(dir string, key []byte, deviceName string, cred Credential) error {
	return SaveFileStoreWith(dir, key, deviceName, cred, SaveOptions{})
}

// updateCredentialsDocument reads the project's credential file, applies
// change, and writes it back atomically into a directory only the owner may
// open. Every writer goes through it, so device and registry credentials are
// saved one way.
//
// It holds the credential file lock (save_lock.go) from before the read
// until the new file is in place, so writers started together take turns and
// none writes the file back without another's entry. The directory is made
// first because the lock file lives in it; a change that is then refused
// leaves the directory and the empty lock file, and no credential file.
// ctx bounds only the wait for the lock (lockCredentials); once the lock is
// held the save runs to its end.
func updateCredentialsDocument(ctx context.Context, dir string, change func(doc *credentialsDocument) error) error {
	path := credentialsPath(dir)
	dirPath := filepath.Dir(path)
	if err := os.MkdirAll(dirPath, 0o700); err != nil {
		return fmt.Errorf("failed to create %s: %w", dirPath, err)
	}
	// os.MkdirAll only applies its mode to directories it actually
	// creates: if dirPath already exists (with, for example, a looser
	// permission left by an unrelated process or a permissive umask), the
	// call above silently leaves it as-is. credentials.yaml itself is
	// still written 0o600 below regardless, so this is defense in depth
	// rather than the only thing standing between a secret and another
	// local user, but it is cheap and this package's whole purpose is
	// secrets at rest, so it is worth doing explicitly rather than
	// trusting MkdirAll's create-only semantics (found by Phase W6's own
	// Schema/Injection Hardening audit, FAILURE_PATTERNS.md #22).
	if err := os.Chmod(dirPath, 0o700); err != nil { // #nosec G302 -- 0o700 is a directory permission (owner rwx), not a file permission; gosec's 0600 threshold does not distinguish the two
		return fmt.Errorf("failed to set permissions on %s: %w", dirPath, err)
	}

	unlock, err := lockCredentials(ctx, dir)
	if err != nil {
		return err
	}
	defer unlock()

	doc, err := readCredentialsDocument(path)
	if err != nil {
		return err
	}
	if err := change(&doc); err != nil {
		return err
	}

	data, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal credential file: %w", err)
	}

	return atomicWriteCredentialsFile(path, data)
}

// buildCredentialEntry encrypts both families of cred into one whole entry,
// with no mark: what a registry account, which has no families of its own,
// is stored as.
func buildCredentialEntry(svc crypto.Service, cred Credential) (credentialEntry, error) {
	login, err := buildLoginEntry(svc, cred)
	if err != nil {
		return credentialEntry{}, err
	}
	snmp, err := buildSNMPEntry(svc, cred, false)
	if err != nil {
		return credentialEntry{}, err
	}
	return credentialEntry{loginEntry: login, snmpEntry: snmp}, nil
}

// sealedField is one secret field of a family on its way to the file: what
// to call it in an error, its plaintext, and where its ciphertext goes.
type sealedField struct {
	what      string
	plaintext []byte
	into      *string
}

// sealFields encrypts each field whose plaintext is non-empty into its
// place, leaving an empty one's _encrypted field at its zero value (omitted
// from the written YAML via omitempty).
func sealFields(svc crypto.Service, fields []sealedField) error {
	for _, f := range fields {
		if len(f.plaintext) == 0 {
			continue
		}
		encrypted, err := encryptField(svc, f.plaintext)
		if err != nil {
			return fmt.Errorf("%s: %w", f.what, err)
		}
		*f.into = encrypted
	}
	return nil
}

// buildLoginEntry encrypts the login family of cred.
func buildLoginEntry(svc crypto.Service, cred Credential) (loginEntry, error) {
	entry := loginEntry{Username: cred.Username}
	err := sealFields(svc, []sealedField{
		{"password", []byte(cred.Password), &entry.PasswordEncrypted},
		{"private key", cred.PrivateKeyPEM, &entry.PrivateKeyEncrypted},
		{"passphrase", []byte(cred.Passphrase), &entry.PassphraseEncrypted},
		{"certificate", cred.CertificatePEM, &entry.CertificateEncrypted},
		{"bundle", []byte(cred.PFXBase64), &entry.PFXEncrypted},
		{"token", []byte(cred.Token), &entry.TokenEncrypted},
		{"enable secret", []byte(cred.EnablePassword), &entry.EnablePasswordEncrypted},
	})
	if err != nil {
		return loginEntry{}, err
	}
	return entry, nil
}

// buildSNMPEntry encrypts the SNMP family of cred, marking a short community
// as stored deliberately (shortCommunity, which checkStorable has already
// held to the community it allows).
func buildSNMPEntry(svc crypto.Service, cred Credential, shortCommunity bool) (snmpEntry, error) {
	entry := snmpEntry{
		SNMPUser:         cred.SNMPUser,
		SNMPAuthProtocol: cred.SNMPAuthProtocol,
		SNMPPrivProtocol: cred.SNMPPrivProtocol,
		ShortCommunity:   shortCommunity,
	}
	err := sealFields(svc, []sealedField{
		{"SNMP community", []byte(cred.SNMPCommunity), &entry.SNMPCommunityEncrypted},
		{"SNMP authentication passphrase", []byte(cred.SNMPAuthPassphrase), &entry.SNMPAuthPassphraseEncrypted},
		{"SNMP privacy passphrase", []byte(cred.SNMPPrivPassphrase), &entry.SNMPPrivPassphraseEncrypted},
	})
	if err != nil {
		return snmpEntry{}, err
	}
	return entry, nil
}

// encryptField encrypts plaintext through svc and base64-encodes the
// result: the exact inverse of fileStore.decryptField (file_store.go).
func encryptField(svc crypto.Service, plaintext []byte) (string, error) {
	ciphertext, err := svc.Encrypt(plaintext)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// atomicWriteCredentialsFile writes data to path by first writing a temp
// file in the same directory, then os.Rename-ing it over path. Rename is
// atomic on POSIX filesystems within one directory, so a reader (or a
// crash) never observes a partially written file: it sees either the
// complete old content or the complete new content, never a torn write.
func atomicWriteCredentialsFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file for %s: %w", path, err)
	}
	tmpPath := tmp.Name()

	// Clean up the temp file on any path that does not end in a
	// successful rename, so a failed Save never leaves stray files
	// behind in the credentials directory.
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write temp file for %s: %w", path, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to set permissions on temp file for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file for %s: %w", path, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to atomically replace %s: %w", path, err)
	}
	renamed = true
	return nil
}

// RemoveFileStore removes deviceName's credential from the credentials file
// under dir, reporting whether it had one. Nothing else in the file changes,
// and nothing is decrypted, so no master key is needed.
func RemoveFileStore(dir, deviceName string) (bool, error) {
	removed := false
	err := updateCredentialsDocument(context.Background(), dir, func(doc *credentialsDocument) error {
		if _, ok := doc.Devices[deviceName]; ok {
			delete(doc.Devices, deviceName)
			removed = true
		}
		return nil
	})
	return removed, err
}
