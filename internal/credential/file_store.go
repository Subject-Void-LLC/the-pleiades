package credential

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
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
	// Registries holds container registry accounts, keyed by registry host
	// (wire.RegistryHost). It is a namespace of its own: no device name can
	// reach an entry here, and no registry host can reach a device's.
	Registries map[string]credentialEntry `yaml:"registries,omitempty"`
	// Databases holds named database connections, keyed by name
	// (wire.DatabaseConnection.Name), in a namespace of their own like
	// Registries.
	Databases map[string]databaseEntry `yaml:"databases,omitempty"`
	// Secrets holds named secrets, keyed by name and then by field, each
	// value base64(AES-256-GCM ciphertext) like every other secret here; a
	// namespace of its own like Registries (secret_store.go).
	Secrets map[string]map[string]string `yaml:"secrets,omitempty"`
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
//
// Its fields are two families (families.go), each its own embedded struct
// inlined into the same YAML mapping, so the file's shape is what it always
// was and a save that leaves a family alone copies that struct whole: a
// field added to a family later is carried with it without anyone
// remembering to copy it.
type credentialEntry struct {
	loginEntry `yaml:",inline"`
	snmpEntry  `yaml:",inline"`
	// Unbound marks an entry stored for a name no inventory device had at
	// the time (add-credential without --no-device, or with it). Not a
	// secret; see unbound.go. It is about the name, not a family, so every
	// save sets it.
	Unbound bool `yaml:"unbound,omitempty"`
}

// loginEntry is the login family of a stored credential.
type loginEntry struct {
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
	// TokenEncrypted holds a bearer token. Phase 119c.
	TokenEncrypted string `yaml:"token_encrypted,omitempty"`
	// EnablePasswordEncrypted holds a network device's enable secret.
	// Phase 127c.
	EnablePasswordEncrypted string `yaml:"enable_password_encrypted,omitempty"`
}

// snmpEntry is the SNMP family of a stored credential (Phase 97). The user
// and the two protocol names are plaintext, as Username is, since none is a
// secret and a listing reads them with no master key; the community and the
// passphrases are ciphertext.
type snmpEntry struct {
	SNMPCommunityEncrypted      string                `yaml:"snmp_community_encrypted,omitempty"`
	SNMPUser                    string                `yaml:"snmp_user,omitempty"`
	SNMPAuthProtocol            wire.SNMPAuthProtocol `yaml:"snmp_auth_protocol,omitempty"`
	SNMPAuthPassphraseEncrypted string                `yaml:"snmp_auth_passphrase_encrypted,omitempty"`
	SNMPPrivProtocol            wire.SNMPPrivProtocol `yaml:"snmp_priv_protocol,omitempty"`
	SNMPPrivPassphraseEncrypted string                `yaml:"snmp_priv_passphrase_encrypted,omitempty"`
	// ShortCommunity marks a community under wire.MinSNMPCommunityLength the
	// operator stored deliberately (add-credential --allow-short-community).
	// Not a secret, like Unbound, and part of this family, so it goes when
	// the community does. Lookup refuses a short community without it.
	ShortCommunity bool `yaml:"short_community,omitempty"`
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

	// The identifiers are plaintext in the file; every other field is
	// decrypted, each failure naming its field, never its value.
	cred := Credential{
		Username:         entry.Username,
		SNMPUser:         entry.SNMPUser,
		SNMPAuthProtocol: entry.SNMPAuthProtocol,
		SNMPPrivProtocol: entry.SNMPPrivProtocol,
	}
	for _, f := range []struct {
		what, encrypted string
		into            func([]byte)
	}{
		{"password", entry.PasswordEncrypted, func(b []byte) { cred.Password = string(b) }},
		{"private key", entry.PrivateKeyEncrypted, func(b []byte) { cred.PrivateKeyPEM = b }},
		{"passphrase", entry.PassphraseEncrypted, func(b []byte) { cred.Passphrase = string(b) }},
		{"certificate", entry.CertificateEncrypted, func(b []byte) { cred.CertificatePEM = b }},
		{"bundle", entry.PFXEncrypted, func(b []byte) { cred.PFXBase64 = string(b) }},
		{"token", entry.TokenEncrypted, func(b []byte) { cred.Token = string(b) }},
		{"enable secret", entry.EnablePasswordEncrypted, func(b []byte) { cred.EnablePassword = string(b) }},
		{"SNMP community", entry.SNMPCommunityEncrypted, func(b []byte) { cred.SNMPCommunity = string(b) }},
		{"SNMP authentication passphrase", entry.SNMPAuthPassphraseEncrypted, func(b []byte) { cred.SNMPAuthPassphrase = string(b) }},
		{"SNMP privacy passphrase", entry.SNMPPrivPassphraseEncrypted, func(b []byte) { cred.SNMPPrivPassphrase = string(b) }},
	} {
		if f.encrypted == "" {
			continue
		}
		plaintext, err := s.decryptField(f.encrypted)
		if err != nil {
			return Credential{}, fmt.Errorf("failed to decrypt %s for device %s: %w", f.what, deviceName, err)
		}
		f.into(plaintext)
	}

	// What was read is held to the rules it was saved under, so a plaintext
	// protocol name edited in the file, a short community stored without its
	// mark, or a key a save before this check put beside a certificate never
	// reaches a run.
	if err := checkStorable(cred, LoginFamily|SNMPFamily, entry.ShortCommunity); err != nil {
		return Credential{}, fmt.Errorf("the credential stored for device %s cannot be used: %w (replace it with pleiades add-credential %s)", deviceName, err, deviceName)
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
//
// A directory in the path that is a regular file is such a failure. POSIX
// reports it as ENOTDIR, but Windows reports the path as not found, so the
// parent is looked at before a missing file is believed, and both say the
// same thing.
func readCredentialsDocument(path string) (credentialsDocument, error) {
	// Read only when it is its owner's alone, like the master key.
	data, err := crypto.ReadPrivateFile(path)
	if errors.Is(err, crypto.ErrLooseSecretFile) {
		return credentialsDocument{}, err
	}
	if err != nil {
		if info, statErr := os.Stat(filepath.Dir(path)); statErr == nil && !info.IsDir() {
			return credentialsDocument{}, fmt.Errorf("failed to read credential file %s: %s is not a directory", path, filepath.Dir(path))
		}
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
