// Which values of a flattened credential every output masks by value: the
// one rule, kept beside the keys it names so a key added here is decided
// here.
package wire

// MaskedValues returns the values of a flattened credential (the map a
// DispatchPayload, a ChildRequest and sdk.RunbookContext.InjectSecrets carry)
// that output must never show, for a caller to mask by value. It is the one
// rule every path holds a credential to: a Collection method's task and the
// transport actions (internal/engine), a native Runner's child, an Ansible
// job's output (internal/adapters/legacy), an external Collection's program
// (internal/loader) and onboarding's answers (internal/inventory/onboard).
// It lives here, in pkg/, because internal/loader may not import
// internal/engine and internal/redact imports only the standard library.
//
// Every value is masked except the ones a credential names as identifiers:
// its username, a certificate's public half, a seeded login's username,
// public key and host, an SNMPv3 USM user and the two USM protocol names.
// Masking is a substring scrub, so masking a username such as root would
// scrub every path under /root from a run's report, and masking
// "aes256-reeder" or a USM user named "snmp" would scrub those words from
// every output. A key this rule has never heard of is masked, so a secret
// field added to a credential later is masked by default.
//
// A community shorter than MinSNMPCommunityLength (SNMPCredential.
// ShortCommunity, stored only when the operator allowed it) is left out too:
// masking "public" by value would turn "republic" into "re********". It is
// masked by the shape of the lines that hold it alone, and every run that
// carries one says so.
func MaskedValues(secrets map[string]string) []string {
	out := make([]string, 0, len(secrets))
	short := SNMPOf(secrets).ShortCommunity()
	for key, value := range secrets {
		switch key {
		case SecretUsername, SecretCertificatePEM, SecretSeedUsername, SecretSeedAuthorizedKey, SecretSeedHost,
			SecretSNMPUser, SecretSNMPAuthProtocol, SecretSNMPPrivProtocol:
			continue
		case SecretSNMPCommunity:
			if short {
				continue
			}
		}
		out = append(out, value)
	}
	return out
}
