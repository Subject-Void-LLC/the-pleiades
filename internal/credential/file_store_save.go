package credential

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/SubjectVoidLLC/the-pleiades/internal/crypto"
	"go.yaml.in/yaml/v3"
)

// SaveFileStore adds or updates exactly one device's credential in the
// credentials file under dir, encrypting whichever of cred's
// Password, PrivateKeyPEM, and Passphrase are non-empty with the master
// key key. Every other device already stored in the file is preserved
// unchanged.
//
// A call to SaveFileStore replaces deviceName's entire stored entry with
// a fresh encoding of cred; it does not merge cred's fields onto
// whatever was stored before. This matters when switching a device from
// password to key authentication (or back): calling SaveFileStore with
// only PrivateKeyPEM set clears any previously stored password for that
// device, rather than leaving stale, no-longer-intended credential
// material on disk next to the new key.
//
// The file is written atomically (temp file in the same directory, then
// os.Rename over the target) with mode 0o600, mirroring atomicWriteFile
// in internal/inventory/file_repository_save.go, so a crash mid-write
// never leaves a torn or half-written credentials file. That helper is
// duplicated here (as atomicWriteCredentialsFile) rather than imported,
// since internal/inventory does not export it and this package should
// not depend on an unrelated package purely for one small write helper.
func SaveFileStore(dir string, key []byte, deviceName string, cred Credential) error {
	svc, err := crypto.NewAESService(key)
	if err != nil {
		return fmt.Errorf("failed to initialize credential encryption: %w", err)
	}

	path := credentialsPath(dir)
	doc, err := readCredentialsDocument(path)
	if err != nil {
		return err
	}
	if doc.Devices == nil {
		doc.Devices = make(map[string]credentialEntry)
	}

	entry, err := buildCredentialEntry(svc, cred)
	if err != nil {
		return fmt.Errorf("failed to encrypt credential for device %s: %w", deviceName, err)
	}
	doc.Devices[deviceName] = entry

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

	data, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal credential file: %w", err)
	}

	return atomicWriteCredentialsFile(path, data)
}

// buildCredentialEntry encrypts whichever of cred's secret fields are
// non-empty, leaving the corresponding _encrypted field as its zero
// value (empty string, omitted from the written YAML via omitempty) for
// any field cred does not carry.
func buildCredentialEntry(svc crypto.Service, cred Credential) (credentialEntry, error) {
	entry := credentialEntry{Username: cred.Username}

	if cred.Password != "" {
		encrypted, err := encryptField(svc, []byte(cred.Password))
		if err != nil {
			return credentialEntry{}, fmt.Errorf("password: %w", err)
		}
		entry.PasswordEncrypted = encrypted
	}
	if len(cred.PrivateKeyPEM) != 0 {
		encrypted, err := encryptField(svc, cred.PrivateKeyPEM)
		if err != nil {
			return credentialEntry{}, fmt.Errorf("private key: %w", err)
		}
		entry.PrivateKeyEncrypted = encrypted
	}
	if cred.Passphrase != "" {
		encrypted, err := encryptField(svc, []byte(cred.Passphrase))
		if err != nil {
			return credentialEntry{}, fmt.Errorf("passphrase: %w", err)
		}
		entry.PassphraseEncrypted = encrypted
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
