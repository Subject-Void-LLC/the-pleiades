// Package credential: credentials stored for a name the inventory does not
// have, marked unbound, listed, and announced the first time a run uses one.
//
// Decided 2026-11-11: add-credential for a device the inventory does not
// have is refused in 0.4.0 unless --no-device is given. A 0.3.x patch only
// adds the flag and a warning, so no shipped script breaks, and marks the
// entry unbound, because a typo otherwise leaves a secret under the wrong
// name and a device added later under that name is sent a credential nobody
// chose for it. The mark is data beside the entry, never a secret, so
// listing and clearing it need no master key.
package credential

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// SaveOptions are how SaveFileStoreWith marks the entry it writes.
type SaveOptions struct {
	// Unbound marks the entry as stored for a name no inventory device had
	// when it was stored.
	Unbound bool
}

// SaveFileStoreWith is SaveFileStore with the entry marked as opts says.
// The entry is replaced whole, mark included, so saving again for a device
// the inventory now has clears an old mark.
func SaveFileStoreWith(dir string, key []byte, deviceName string, cred Credential, opts SaveOptions) error {
	svc, err := crypto.NewAESService(key)
	if err != nil {
		return fmt.Errorf("failed to initialize credential encryption: %w", err)
	}
	return updateCredentialsDocument(dir, func(doc *credentialsDocument) error {
		if doc.Devices == nil {
			doc.Devices = make(map[string]credentialEntry)
		}
		entry, err := buildCredentialEntry(svc, cred)
		if err != nil {
			return fmt.Errorf("failed to encrypt credential for device %s: %w", deviceName, err)
		}
		entry.Unbound = opts.Unbound
		doc.Devices[deviceName] = entry
		return nil
	})
}

// CredentialInfo is one stored device credential as a listing shows it:
// its name, its account, which kinds of secret it holds, and whether it is
// unbound. Never a value.
type CredentialInfo struct {
	// Name is the device name the credential is stored under.
	Name string `json:"name"`
	// Username is the account it logs in as, empty for a certificate or a
	// token.
	Username string `json:"username,omitempty"`
	// Kinds names each secret it holds: password, private_key, passphrase,
	// certificate, pfx, token, enable_password.
	Kinds []string `json:"kinds"`
	// Unbound is set when it was stored for a name no inventory device had.
	Unbound bool `json:"unbound,omitempty"`
}

// ListCredentials returns every device credential in dir's credential
// file, sorted by name. Listing needs no master key and decrypts nothing.
func ListCredentials(dir string) ([]CredentialInfo, error) {
	doc, err := readCredentialsDocument(credentialsPath(dir))
	if err != nil {
		return nil, err
	}
	out := make([]CredentialInfo, 0, len(doc.Devices))
	for name, e := range doc.Devices {
		info := CredentialInfo{Name: name, Username: e.Username, Unbound: e.Unbound, Kinds: []string{}}
		for _, kind := range []struct {
			name string
			set  bool
		}{
			{"password", e.PasswordEncrypted != ""},
			{"private_key", e.PrivateKeyEncrypted != ""},
			{"passphrase", e.PassphraseEncrypted != ""},
			{"certificate", e.CertificateEncrypted != ""},
			{"pfx", e.PFXEncrypted != ""},
			{"token", e.TokenEncrypted != ""},
			{"enable_password", e.EnablePasswordEncrypted != ""},
		} {
			if kind.set {
				info.Kinds = append(info.Kinds, kind.name)
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// BindFileStore clears the unbound mark of deviceName's credential,
// reporting whether it had one. Nothing else in the file changes, and
// nothing is decrypted.
func BindFileStore(dir, deviceName string) (bool, error) {
	cleared := false
	err := updateCredentialsDocument(dir, func(doc *credentialsDocument) error {
		if e, ok := doc.Devices[deviceName]; ok && e.Unbound {
			e.Unbound = false
			doc.Devices[deviceName] = e
			cleared = true
		}
		return nil
	})
	return cleared, err
}

// noticeUnbound is a Store that announces the first lookup of each unbound
// entry and then clears its mark.
type noticeUnbound struct {
	inner  Store
	dir    string
	notice func(name string)

	load    sync.Once
	mu      sync.Mutex
	unbound map[string]bool
}

// NoticeUnbound wraps inner, the store a run reads dir's credentials
// through, so the first successful lookup of an unbound entry calls notice
// with its name and clears the mark: the run that first uses such a
// credential says so, and later runs do not. A file that cannot be read for
// the marks leaves every lookup to inner alone.
//
// The store a run reads through also answers named secrets, registry
// accounts and database connections (SecretStore, RegistryStore,
// DatabaseStore), and callers find those by asserting on the store they were
// handed. So when inner answers all three, the wrapper does too, passing each
// straight to inner: a wrapper that hid them made every secret reference in a
// run fail as unresolvable.
func NoticeUnbound(inner Store, dir string, notice func(name string)) Store {
	n := &noticeUnbound{inner: inner, dir: dir, notice: notice}
	if full, ok := inner.(interface {
		SecretStore
		RegistryStore
		DatabaseStore
	}); ok {
		return struct {
			*noticeUnbound
			SecretStore
			RegistryStore
			DatabaseStore
		}{n, full, full, full}
	}
	return n
}

// Lookup implements Store.
func (n *noticeUnbound) Lookup(ctx context.Context, deviceName string) (Credential, error) {
	cred, err := n.inner.Lookup(ctx, deviceName)
	if err != nil {
		return cred, err
	}
	n.load.Do(func() {
		n.unbound = map[string]bool{}
		listed, listErr := ListCredentials(n.dir)
		if listErr != nil {
			return
		}
		for _, c := range listed {
			if c.Unbound {
				n.unbound[c.Name] = true
			}
		}
	})
	n.mu.Lock()
	first := n.unbound[deviceName]
	delete(n.unbound, deviceName)
	n.mu.Unlock()
	if first {
		n.notice(deviceName)
		// The mark is cleared once the run has said so. A failure to write
		// it leaves the mark, so a later run says so again, which is the
		// harmless direction.
		_, _ = BindFileStore(n.dir, deviceName)
	}
	return cred, nil
}
