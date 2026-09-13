package topology

import "github.com/nats-io/nats.go"

// ConnectStateForTest exposes connectState to this package's external
// test package.
//
// The name follows internal/archtest's TestEveryTestSeamIsNamedForOne
// convention, and TestNoProductionCodeCallsATestSeam is what keeps it out
// of shipping code. It exists in export_test.go, which the go tool
// compiles only into the test binary, so it cannot reach a built binary
// at all.
func ConnectStateForTest(nc *nats.Conn) (bool, error) { return connectState(nc) }

// CredentialOptionForTest exposes credentialOption to this package's
// external test package.
//
// It exists because Phase 101b added the credential dial path and tested
// it only from internal/meshid's container gate, which proves the path
// works end to end but leaves this package's own error branches
// unexercised, and coverage is per package. The parsing failures matter on
// their own: a malformed .creds body has to produce a clean Go error at
// dial time rather than a connection that fails later against a broker.
func CredentialOptionForTest(creds []byte) (nats.Option, error) { return credentialOption(creds) }

// SettingsForTest applies opts and reports what they produced, so a
// ConnectOption can be asserted without dialling anything.
func SettingsForTest(opts ...ConnectOption) (creds []byte) {
	var s connectSettings
	for _, o := range opts {
		o(&s)
	}
	return s.creds
}
