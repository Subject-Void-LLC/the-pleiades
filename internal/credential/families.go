// Package credential: the two families a stored credential holds, the login
// and SNMP, each saved and replaced whole without touching the other, and the
// checks a credential is held to before it is stored.
//
// Decided 2026-11-15: saving SNMP replaces every SNMP field together, so a
// device switched from a v3 user to a community keeps no stale v3
// passphrase, and keeps the login; saving a login replaces the login family
// and keeps the SNMP fields. The family a save leaves alone is copied as the
// file holds it, ciphertext and all, and never decrypted.
package credential

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// SaveOptions are which families SaveFileStoreWith replaces and how it marks
// the entry it writes.
type SaveOptions struct {
	// Unbound marks the entry as stored for a name no inventory device had
	// when it was stored. It is about the name, so every save sets it.
	Unbound bool
	// Families are the families the save replaces; zero means each family
	// the credential sets a field of (familiesOf). A family named here that
	// the credential leaves empty is cleared, which is how a device's SNMP
	// fields are removed while its login stays.
	Families Families
	// AllowShortCommunity is the operator's acceptance of an SNMP community
	// shorter than wire.MinSNMPCommunityLength (add-credential
	// --allow-short-community). The entry is then marked, every run that
	// uses it warns that it is masked by shape only, and without it such a
	// community is refused.
	AllowShortCommunity bool
}

// SaveFileStoreWith is SaveFileStore replacing the families opts names and
// marking the entry as opts says. A family the save does not replace is
// copied as the file holds it, ciphertext and all, so it is never decrypted
// and needs no key to survive. The unbound mark is set from opts on every
// save, so saving again for a device the inventory now has clears an old
// one.
func SaveFileStoreWith(dir string, key []byte, deviceName string, cred Credential, opts SaveOptions) error {
	families := opts.Families
	if families == 0 {
		families = familiesOf(cred)
	}
	if err := checkStorable(cred, families, opts.AllowShortCommunity); err != nil {
		return fmt.Errorf("refusing the credential for device %s: %w", deviceName, err)
	}
	svc, err := crypto.NewAESService(key)
	if err != nil {
		return fmt.Errorf("failed to initialize credential encryption: %w", err)
	}
	return updateCredentialsDocument(dir, func(doc *credentialsDocument) error {
		if doc.Devices == nil {
			doc.Devices = make(map[string]credentialEntry)
		}
		// What is stored now, read for its families only: the zero entry
		// when the device has none, so a family it never had stays empty.
		entry := doc.Devices[deviceName]
		if families&LoginFamily != 0 {
			login, err := buildLoginEntry(svc, cred)
			if err != nil {
				return fmt.Errorf("failed to encrypt credential for device %s: %w", deviceName, err)
			}
			entry.loginEntry = login
		}
		if families&SNMPFamily != 0 {
			snmp, err := buildSNMPEntry(svc, cred, opts.AllowShortCommunity)
			if err != nil {
				return fmt.Errorf("failed to encrypt credential for device %s: %w", deviceName, err)
			}
			entry.snmpEntry = snmp
		}
		entry.Unbound = opts.Unbound
		doc.Devices[deviceName] = entry
		return nil
	})
}

// Families is the set of families a save replaces.
type Families uint8

// The two families. LoginFamily is how a device is logged into: Username,
// Password, PrivateKeyPEM, Passphrase, CertificatePEM, PFXBase64, Token and
// EnablePassword. SNMPFamily is the six SNMP fields.
const (
	LoginFamily Families = 1 << iota
	SNMPFamily
)

// String names the families, for a message.
func (f Families) String() string {
	switch f {
	case LoginFamily:
		return "login"
	case SNMPFamily:
		return "SNMP"
	case LoginFamily | SNMPFamily:
		return "login and SNMP"
	}
	return "no family"
}

// SNMP is c's SNMP family, as the wire carries it.
func (c Credential) SNMP() wire.SNMPCredential {
	return wire.SNMPCredential{
		Community:      c.SNMPCommunity,
		User:           c.SNMPUser,
		AuthProtocol:   c.SNMPAuthProtocol,
		AuthPassphrase: c.SNMPAuthPassphrase,
		PrivProtocol:   c.SNMPPrivProtocol,
		PrivPassphrase: c.SNMPPrivPassphrase,
	}
}

// WithSNMP is c with its SNMP family replaced by s, and its login as it was.
func (c Credential) WithSNMP(s wire.SNMPCredential) Credential {
	c.SNMPCommunity, c.SNMPUser = s.Community, s.User
	c.SNMPAuthProtocol, c.SNMPAuthPassphrase = s.AuthProtocol, s.AuthPassphrase
	c.SNMPPrivProtocol, c.SNMPPrivPassphrase = s.PrivProtocol, s.PrivPassphrase
	return c
}

// HasLogin reports whether c sets any field of the login family: whether a
// save replacing the login would discard something.
func (c Credential) HasLogin() bool {
	return c.Username != "" || c.Password != "" || len(c.PrivateKeyPEM) != 0 || c.Passphrase != "" ||
		len(c.CertificatePEM) != 0 || c.PFXBase64 != "" || c.Token != "" || c.EnablePassword != ""
}

// familiesOf is the families c sets a field of, which is what a save that
// names none replaces. A credential setting nothing is a login, which is
// every save there was before the SNMP family existed.
func familiesOf(c Credential) Families {
	var f Families
	if c.HasLogin() {
		f |= LoginFamily
	}
	if !c.SNMP().Empty() {
		f |= SNMPFamily
	}
	if f == 0 {
		f = LoginFamily
	}
	return f
}

// checkStorable refuses a credential no save may write: a field of a family
// the save does not replace (it would be dropped without a word), an SNMP
// family wire.SNMPCredential.Check refuses, and a certificate holding a
// block that is not a certificate. allowShortCommunity is the operator's
// acceptance of a short community.
func checkStorable(c Credential, families Families, allowShortCommunity bool) error {
	if families&LoginFamily == 0 && c.HasLogin() {
		return fmt.Errorf("it carries login fields, and this save replaces only the %s fields", families)
	}
	if families&SNMPFamily == 0 && !c.SNMP().Empty() {
		return fmt.Errorf("it carries SNMP fields, and this save replaces only the %s fields", families)
	}
	if families&SNMPFamily == 0 && allowShortCommunity {
		return fmt.Errorf("a short community is allowed, and this save replaces only the %s fields", families)
	}
	if families&SNMPFamily != 0 {
		if err := c.SNMP().Check(allowShortCommunity); err != nil {
			return err
		}
	}
	return checkCertificate(c.CertificatePEM)
}

// checkCertificate refuses a certificate holding a PEM block that is not one,
// naming the field a private key belongs in.
func checkCertificate(certificate []byte) error {
	if problem := wire.ClientCertificateProblem(certificate); problem != "" {
		return fmt.Errorf("the certificate %s: a private key goes in the private key field (PrivateKeyPEM), never beside the certificate", problem)
	}
	return nil
}
