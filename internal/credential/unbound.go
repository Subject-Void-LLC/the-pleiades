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
	"sort"
	"sync"
	"time"
)

// CredentialInfo is one stored device credential as a listing shows it:
// its name, its account, which kinds of secret it holds, whether it is
// unbound, and its private key's type and fingerprint. Never a secret's
// value.
type CredentialInfo struct {
	// Name is the device name the credential is stored under.
	Name string `json:"name"`
	// Username is the account it logs in as, empty for a certificate or a
	// token.
	Username string `json:"username,omitempty"`
	// SNMPUser is its SNMPv3 USM user, when it has one.
	SNMPUser string `json:"snmp_user,omitempty"`
	// Kinds names each secret it holds: password, private_key, passphrase,
	// certificate, pfx, token, enable_password, snmp_community, snmp_v3_auth
	// and snmp_v3_priv.
	Kinds []string `json:"kinds"`
	// Unbound is set when it was stored for a name no inventory device had.
	Unbound bool `json:"unbound,omitempty"`
	// DeviceExists is set, on an unbound entry, when a device of its name is
	// in the inventory now (added other than by add-host, which binds it at
	// once): its first run binds it and says so. Only a caller that read the
	// inventory sets it; ListCredentials never does.
	DeviceExists bool `json:"device_exists,omitempty"`
	// ShortCommunity is set when its SNMP community is shorter than
	// wire.MinSNMPCommunityLength and was stored deliberately, so every run
	// masks it by shape only.
	ShortCommunity bool `json:"short_community,omitempty"`
	// KeyType names its private key's algorithm and size (ed25519,
	// rsa-3072, ecdsa-p256), as recorded when the key was stored (key_info.go).
	KeyType KeyType `json:"key_type,omitempty"`
	// KeyFingerprint is that key's SHA256 fingerprint, as ssh-keygen -l
	// prints it, recorded beside it. It is not a secret. list-credentials
	// shows it, and add-credential --generate prints the one it made; it is
	// never put in a fact, a stat or a journal.
	KeyFingerprint string `json:"key_fingerprint,omitempty"`
	// KeyNotRecorded is set when it holds a private key whose type and
	// fingerprint were never recorded: one stored before they were, or one
	// that could not be read or named when it was stored (a DSA key, a
	// sealed key stored without its passphrase, an encrypted PKCS#8 key),
	// which storing again does not change. Nothing is guessed.
	KeyNotRecorded bool `json:"key_not_recorded,omitempty"`
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
		info := CredentialInfo{Name: name, Username: e.Username, SNMPUser: e.SNMPUser, Unbound: e.Unbound, ShortCommunity: e.ShortCommunity, Kinds: []string{}}
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
			// The SNMP kinds name what a run would hold, by version and level:
			// a v1/v2c community, a v3 user's authentication, its privacy.
			{"snmp_community", e.SNMPCommunityEncrypted != ""},
			{"snmp_v3_auth", e.SNMPAuthPassphraseEncrypted != ""},
			{"snmp_v3_priv", e.SNMPPrivPassphraseEncrypted != ""},
		} {
			if kind.set {
				info.Kinds = append(info.Kinds, kind.name)
			}
		}
		// The key's record, read as stored: only an entry holding a key has
		// one, and Lookup, not a listing, is what holds it to the key.
		if e.PrivateKeyEncrypted != "" {
			info.KeyType, info.KeyFingerprint = e.KeyType, e.KeyFingerprint
			info.KeyNotRecorded = e.KeyType == "" && e.KeyFingerprint == ""
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
	return BindFileStoreContext(context.Background(), dir, deviceName)
}

// BindFileStoreContext is BindFileStore whose wait for the credential file
// lock ends when ctx does, for a caller inside a run, which must not sit
// behind another command's save.
func BindFileStoreContext(ctx context.Context, dir, deviceName string) (bool, error) {
	cleared := false
	err := updateCredentialsDocument(ctx, dir, func(doc *credentialsDocument) error {
		if e, ok := doc.Devices[deviceName]; ok && e.Unbound {
			e.Unbound = false
			doc.Devices[deviceName] = e
			cleared = true
		}
		return nil
	})
	return cleared, err
}

// unboundBindWait is how long a lookup waits for the credential file lock to
// clear an unbound mark before it leaves the mark for a later run. A variable
// so a test can shorten it.
var unboundBindWait = 2 * time.Second

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
	return keepStores(inner, &noticeUnbound{inner: inner, dir: dir, notice: notice})
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
		// harmless direction; that is why the error is dropped. The write
		// waits for the credential file lock, so it is bounded: the lookup's
		// own ctx ends it, and so does unboundBindWait, after which the
		// credential is returned with the mark still set. A run never waits
		// the full save bound behind another command for a mark.
		bindCtx, cancel := context.WithTimeout(ctx, unboundBindWait)
		_, _ = BindFileStoreContext(bindCtx, n.dir, deviceName)
		cancel()
	}
	return cred, nil
}
