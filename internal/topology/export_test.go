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
