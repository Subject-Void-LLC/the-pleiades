// Container registry accounts in the project's credential file, beside the
// devices' and in a namespace of their own.
package credential

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// RegistryStore resolves the account stored for one container registry.
// The Crawl tier's run hands it to every method through
// sdk.RegistryCredentials, keyed by the host the method's own image names,
// so a stored password reaches only its own registry.
type RegistryStore interface {
	// LookupRegistry returns the account stored for host, and false when
	// there is none.
	LookupRegistry(ctx context.Context, host string) (username, password string, ok bool, err error)
}

// SaveRegistryCredential stores an account for the registry at host,
// replacing any earlier one, encrypted under the project's master key like
// every device credential.
func SaveRegistryCredential(dir string, key []byte, host, username, password string) error {
	host = wire.RegistryHost(host)
	if host == "" {
		return fmt.Errorf("a registry credential needs the registry's host")
	}
	svc, err := crypto.NewAESService(key)
	if err != nil {
		return fmt.Errorf("failed to initialize credential encryption: %w", err)
	}
	return updateCredentialsDocument(dir, func(doc *credentialsDocument) error {
		if doc.Registries == nil {
			doc.Registries = make(map[string]credentialEntry)
		}
		entry, err := buildCredentialEntry(svc, Credential{Username: username, Password: password})
		if err != nil {
			return fmt.Errorf("failed to encrypt the credential for registry %s: %w", host, err)
		}
		doc.Registries[host] = entry
		return nil
	})
}

// LookupRegistry implements RegistryStore.
func (s *fileStore) LookupRegistry(_ context.Context, host string) (string, string, bool, error) {
	doc, err := readCredentialsDocument(s.path)
	if err != nil {
		return "", "", false, err
	}
	entry, ok := doc.Registries[wire.RegistryHost(host)]
	if !ok {
		return "", "", false, nil
	}
	password := ""
	if entry.PasswordEncrypted != "" {
		plain, err := s.decryptField(entry.PasswordEncrypted)
		if err != nil {
			return "", "", false, fmt.Errorf("failed to decrypt the password for registry %s: %w", host, err)
		}
		password = string(plain)
	}
	return entry.Username, password, true, nil
}

// LookupRegistry implements RegistryStore, resolving the master key on
// first use as Lookup does.
func (l *lazyFileStore) LookupRegistry(ctx context.Context, host string) (string, string, bool, error) {
	if err := l.open(); err != nil {
		return "", "", false, err
	}
	rs, ok := l.store.(RegistryStore)
	if !ok {
		return "", "", false, nil
	}
	return rs.LookupRegistry(ctx, host)
}
