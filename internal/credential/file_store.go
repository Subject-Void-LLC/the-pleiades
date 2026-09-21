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

// credentialsFileName is the fixed filename NewFileStore reads and
// SaveFileStore (file_store_save.go) writes, always placed in the same
// masterKeyDirName directory the master key file lives in (master_key.go),
// so both concerns share one hidden directory under a caller-supplied
// root. fileStore is the Crawl-tier Store adapter: a single local YAML
// file, encrypted field-by-field with the AES-256-GCM envelope
// encryption internal/crypto already provides
// (crypto.NewAESService/Service, internal/crypto/aes.go). This mirrors
// the split internal/inventory uses between its Repository port and its
// file-backed and ent-backed adapters: fileStore is this port's
// Crawl-tier adapter, a database-backed one is PLAN.md Section 17's
// future Walk-tier work.
const credentialsFileName = "credentials.yaml"

// credentialsDocument is the on-disk shape of the credentials file: one
// entry per device, keyed by device name.
//
//	devices:
//	  router1:
//	    username: admin
//	    password_encrypted: "base64ciphertext..."
//	  server1:
//	    username: deploy
//	    private_key_encrypted: "base64ciphertext..."
//	    passphrase_encrypted: "base64ciphertext..."
type credentialsDocument struct {
	Devices map[string]credentialEntry `yaml:"devices"`
}

// credentialEntry is one device's stored credential. Username is stored
// as plaintext since it is not a secret; every other field holds
// base64(AES-256-GCM ciphertext) produced by crypto.Service.Encrypt, or
// is omitted entirely (via the yaml omitempty tag) if that field was not
// set when the entry was saved. At most one of PasswordEncrypted and
// PrivateKeyEncrypted is expected in practice, since a device is either
// password- or key-authenticated, but a Lookup that finds both populates
// both fields on the returned Credential rather than picking one,
// leaving that choice to the transport layer that actually authenticates.
type credentialEntry struct {
	Username            string `yaml:"username"`
	PasswordEncrypted   string `yaml:"password_encrypted,omitempty"`
	PrivateKeyEncrypted string `yaml:"private_key_encrypted,omitempty"`
	PassphraseEncrypted string `yaml:"passphrase_encrypted,omitempty"`
	// CertificateEncrypted holds a PEM client certificate. It is encrypted
	// like every other field here even though a certificate is not a
	// secret, because this file's format is uniform and an exception would
	// be one more thing to reason about for no gain. Phase 78d.
	CertificateEncrypted string `yaml:"certificate_encrypted,omitempty"`
	// PFXEncrypted holds a base64 PKCS#12 bundle, which IS a secret: it
	// carries a private key, sealed only by a passphrase that may be stored
	// in this same file. Phase 78d.
	PFXEncrypted string `yaml:"pfx_encrypted,omitempty"`
}

// fileStore is the Store implementation backed by credentialsFileName
// under dir's masterKeyDirName subdirectory.
type fileStore struct {
	path string         // full path to the credentials YAML file
	svc  crypto.Service // encrypts and decrypts each secret field
}

// NewFileStore constructs a Store backed by <dir>/.pleiades/credentials.yaml,
// encrypting and decrypting secret fields with key. key must already be
// the resolved 32-byte AES-256 master key: see ResolveMasterKey
// (master_key.go). NewFileStore does not resolve it itself, keeping key
// resolution and file storage independently testable, and letting a
// caller (a test, or the CLI's add-credential command built in a later
// integration step) supply a key from any source.
//
// A missing credentials file is not an error: NewFileStore always
// succeeds if key is exactly 32 bytes, so a runbook with no
// SSH-dependent tasks works with zero credential setup. Lookup is what
// reports a missing file or a missing device entry, via ErrNotFound.
func NewFileStore(dir string, key []byte) (Store, error) {
	svc, err := crypto.NewAESService(key)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize credential encryption: %w", err)
	}
	return &fileStore{
		path: credentialsPath(dir),
		svc:  svc,
	}, nil
}

// credentialsPath returns the fixed credentials file path under dir,
// shared by NewFileStore's read path and SaveFileStore's write path so
// the two can never disagree about where the file lives.
func credentialsPath(dir string) string {
	return filepath.Join(dir, masterKeyDirName, credentialsFileName)
}

// Lookup implements Store. See the Store interface's doc comment
// (credential.go) for the general contract. This implementation reads
// and decrypts credentialsFileName fresh on every call rather than
// caching: the file is small and changes rarely, and a stale cached
// secret surviving past a rotation (a fresh add-credential run) would be
// a worse failure mode than the extra disk read.
func (s *fileStore) Lookup(ctx context.Context, deviceName string) (Credential, error) {
	doc, err := readCredentialsDocument(s.path)
	if err != nil {
		return Credential{}, err
	}

	// doc.Devices is nil, not just empty, whenever the file was missing
	// or had no devices section at all; a nil map read below still
	// reports ok == false rather than panicking, so a missing file and a
	// missing device entry fall into this exact same branch and produce
	// the same ErrNotFound-wrapped error.
	entry, ok := doc.Devices[deviceName]
	if !ok {
		return Credential{}, fmt.Errorf(
			"%w: no credential stored for device %q (add one with: pleiades add-credential %s)",
			ErrNotFound, deviceName, deviceName,
		)
	}

	cred := Credential{Username: entry.Username}

	if entry.PasswordEncrypted != "" {
		password, err := s.decryptField(entry.PasswordEncrypted)
		if err != nil {
			return Credential{}, fmt.Errorf("failed to decrypt password for device %s: %w", deviceName, err)
		}
		cred.Password = string(password)
	}
	if entry.PrivateKeyEncrypted != "" {
		key, err := s.decryptField(entry.PrivateKeyEncrypted)
		if err != nil {
			return Credential{}, fmt.Errorf("failed to decrypt private key for device %s: %w", deviceName, err)
		}
		cred.PrivateKeyPEM = key
	}
	if entry.PassphraseEncrypted != "" {
		passphrase, err := s.decryptField(entry.PassphraseEncrypted)
		if err != nil {
			return Credential{}, fmt.Errorf("failed to decrypt passphrase for device %s: %w", deviceName, err)
		}
		cred.Passphrase = string(passphrase)
	}
	if entry.CertificateEncrypted != "" {
		certificate, err := s.decryptField(entry.CertificateEncrypted)
		if err != nil {
			return Credential{}, fmt.Errorf("failed to decrypt certificate for device %s: %w", deviceName, err)
		}
		cred.CertificatePEM = certificate
	}
	if entry.PFXEncrypted != "" {
		bundle, err := s.decryptField(entry.PFXEncrypted)
		if err != nil {
			return Credential{}, fmt.Errorf("failed to decrypt bundle for device %s: %w", deviceName, err)
		}
		cred.PFXBase64 = string(bundle)
	}

	return cred, nil
}

// decryptField base64-decodes an on-disk _encrypted field, then decrypts
// it through s.svc. A failure here (invalid base64, or a GCM
// authentication failure because the field was encrypted under a
// different master key) is returned as-is for the caller to wrap with
// which field and device it belongs to.
func (s *fileStore) decryptField(encoded string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("stored value is not valid base64: %w", err)
	}
	plaintext, err := s.svc.Decrypt(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decryption failed, possibly using the wrong master key: %w", err)
	}
	return plaintext, nil
}

// readCredentialsDocument loads and parses the credentials file at path.
// A missing file is treated the same as an existing-but-empty one
// (Devices left nil): see Lookup's comment above for why unifying these
// two cases into one code path matters. Any other read failure, or a
// parse failure once the file is read, is returned as a real error.
func readCredentialsDocument(path string) (credentialsDocument, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is derived from the CLI's own configured project directory, not untrusted input
	if err != nil {
		if os.IsNotExist(err) {
			return credentialsDocument{}, nil
		}
		return credentialsDocument{}, fmt.Errorf("failed to read credential file %s: %w", path, err)
	}

	var doc credentialsDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return credentialsDocument{}, fmt.Errorf("failed to parse credential file %s: %w", path, err)
	}
	return doc, nil
}
