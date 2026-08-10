package credential

// The secret keys Flatten produces. A Collection method reads these back
// out of sdk.RunbookContext.InjectSecrets() by these exact string
// literals, not by importing this package: a Collection method may import
// only pkg/ (internal/catalog's own established convention, so a
// third-party Collection built against pkg/collection can satisfy the same
// constraint), so this package cannot hand it a shared constant. The
// literal strings are the contract instead, the same way
// internal/catalog/net/catalyst/client.go's own secretUsername/
// secretPassword constants already are for that namespace's narrower
// two-key case.
const (
	SecretUsername      = "username"
	SecretPassword      = "password"
	SecretPrivateKeyPEM = "private_key_pem"
	SecretPassphrase    = "passphrase"
)

// Unflatten is Flatten's inverse: it rebuilds a Credential from the
// map[string]string shape sdk.RunbookContext.InjectSecrets() and
// wire.DispatchPayload.Secrets carry, for a caller that needs a real
// Credential value rather than the flattened map (NewStaticStore, below).
// A missing key produces that field's Go zero value, the same as a
// Credential that never had it set.
func Unflatten(secrets map[string]string) Credential {
	return Credential{
		Username:      secrets[SecretUsername],
		Password:      secrets[SecretPassword],
		PrivateKeyPEM: []byte(secrets[SecretPrivateKeyPEM]),
		Passphrase:    secrets[SecretPassphrase],
	}
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
	secrets := make(map[string]string, 4)
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
	return secrets
}
