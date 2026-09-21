package credential

import "github.com/Subject-Void-LLC/the-pleiades/pkg/wire"

// The secret keys Flatten produces, aliased from pkg/wire rather than
// restated.
//
// A Collection method reads these back out of
// sdk.RunbookContext.InjectSecrets() by the same keys, and it cannot
// import this package to get them: a Collection may import only pkg/
// (internal/catalog's own established convention, so a third-party
// Collection built against pkg/collection can satisfy the same
// constraint). pkg/wire is the one place both sides can reach, which is
// why the definitions live there and these are aliases.
//
// They were literals here until Phase 78d, matching a second copy in
// internal/credtype and a third in pkg/wire, with only an external test
// holding two of the three together. Aliasing removes the drift instead
// of testing for it. Nothing about the wire format changed: every value
// below is the string it always was.
const (
	SecretUsername       = wire.SecretUsername
	SecretPassword       = wire.SecretPassword
	SecretPrivateKeyPEM  = wire.SecretPrivateKeyPEM
	SecretPassphrase     = wire.SecretPassphrase
	SecretCertificatePEM = wire.SecretCertificatePEM
	SecretPFXBase64      = wire.SecretPFXBase64
)

// Unflatten is Flatten's inverse: it rebuilds a Credential from the
// map[string]string shape sdk.RunbookContext.InjectSecrets() and
// wire.DispatchPayload.Secrets carry, for a caller that needs a real
// Credential value rather than the flattened map (NewStaticStore, below).
// A missing key produces that field's Go zero value, the same as a
// Credential that never had it set.
func Unflatten(secrets map[string]string) Credential {
	return Credential{
		Username:       secrets[SecretUsername],
		Password:       secrets[SecretPassword],
		PrivateKeyPEM:  bytesOrNil(secrets[SecretPrivateKeyPEM]),
		Passphrase:     secrets[SecretPassphrase],
		CertificatePEM: bytesOrNil(secrets[SecretCertificatePEM]),
		PFXBase64:      secrets[SecretPFXBase64],
	}
}

// bytesOrNil converts a string to []byte, but returns nil rather than an
// empty slice for an empty string.
//
// []byte("") is a non-nil slice of length zero, which is indistinguishable
// from nil to every consumer in this codebase (all of them ask len(...) !=
// 0) but NOT to reflect.DeepEqual. Without this, Unflatten(Flatten(c))
// returns a value that is not DeepEqual to c whenever c has no key, so the
// two functions are not the inverses this file says they are, and every
// test comparing them has to write []byte{} in its expectation to work
// around it. Fixing it here is cheaper than that workaround appearing once
// per byte-valued field, and it makes the round-trip claim literally true.
func bytesOrNil(s string) []byte {
	if s == "" {
		return nil
	}
	return []byte(s)
}

// Flatten converts a Credential into the map[string]string shape
// sdk.RunbookContext.InjectSecrets() returns and wire.DispatchPayload.
// Secrets/wire.ChildRequest.Secrets carry across the wire and the Section
// 17.5 subprocess boundary. A field that is empty (Username is never
// empty in practice, but PrivateKeyPEM/Passphrase legitimately are for a
// password-only credential) is simply absent from the result rather than
// present with an empty string, so a Collection method's own `secret, ok
// := rc.InjectSecrets()[credential.SecretPrivateKeyPEM]` reads exactly as
// "this device has no key" instead of "this device has a key that happens
// to be empty."
func Flatten(cred Credential) map[string]string {
	secrets := make(map[string]string, 6)
	if cred.Username != "" {
		secrets[SecretUsername] = cred.Username
	}
	if cred.Password != "" {
		secrets[SecretPassword] = cred.Password
	}
	if len(cred.PrivateKeyPEM) != 0 {
		secrets[SecretPrivateKeyPEM] = string(cred.PrivateKeyPEM)
	}
	if cred.Passphrase != "" {
		secrets[SecretPassphrase] = cred.Passphrase
	}
	if len(cred.CertificatePEM) != 0 {
		secrets[SecretCertificatePEM] = string(cred.CertificatePEM)
	}
	if cred.PFXBase64 != "" {
		secrets[SecretPFXBase64] = cred.PFXBase64
	}
	return secrets
}
