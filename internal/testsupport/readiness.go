// Readiness for the containers this repository starts through a
// testcontainers module rather than through StartNATS, with the startup
// bound every other container here already uses.
//
// # The defect this exists for
//
// testcontainers' WithWaitStrategy and WithAdditionalWaitStrategy both
// wrap their strategies in wait.ForAll(...).WithDeadline(60 seconds), and
// the postgres module's BasicWaitStrategies is built on the second. So
// every postgres and toxiproxy container in this repository waited for
// readiness under a sixty second deadline nobody here chose, while every
// NATS container waited under ContainerStartupTimeout, two minutes.
//
// Under `make test-race` that difference is the whole story. The readiness
// loop polls the Docker daemon for the container's state, and with
// twenty seven container packages running the daemon answers slowly;
// internal/backup's chaos gate failed with "get state ... context
// deadline exceeded" after 561 polls and exactly sixty seconds, and passed
// alone in four. tests/e2e's chaos harness had the identical defect, which
// flaky-packages.json recorded as "harness-only and fixable".
//
// # Why these REPLACE the strategy rather than wrap it
//
// Wrapping is the obvious fix and it does not work. A ForAll carrying a
// deadline runs its children under context.WithTimeout of that deadline,
// so an outer two minute ForAll around the module's inner sixty second one
// still gives up at sixty. The only way to change the deadline is to set
// the strategy again with it, which is what these do.
//
// That means they restate each module's own readiness condition, and that
// is the cost worth naming: if an upstream module changes what "ready"
// means, these will not follow on their own. Each one says which upstream
// definition it mirrors, at which version, so the next dependency bump has
// something to check against.
//
// # Every step carries the bound too, not only the group
//
// The group's deadline is not the whole of it. Each strategy inside a
// ForAll also applies its OWN startup timeout, sixty seconds when none is
// set (wait.defaultStartupTimeout), as a context.WithTimeout of its own
// beneath the group's; and a ForAll's WithStartupTimeoutDefault only sets a
// context around the step, which the step then narrows back to sixty. So
// the first version of these helpers, which set the group's deadline and
// nothing else, still gave up at sixty seconds on every step, and
// internal/backup kept failing exactly as before (FAILURE_PATTERNS 350).
// The only setting a step obeys is its own WithStartupTimeout, so every
// step here names ContainerStartupTimeout, and
// TestReadinessCarriesTheAgreedBound reads each one back.
package testsupport

import (
	"net/http"

	"github.com/testcontainers/testcontainers-go"
	tctoxiproxy "github.com/testcontainers/testcontainers-go/modules/toxiproxy"
	"github.com/testcontainers/testcontainers-go/wait"
)

// PostgresReady waits for a postgres container to accept connections,
// under ContainerStartupTimeout.
//
// It REPLACES testpg.BasicWaitStrategies(), and the two must never be
// passed together: BasicWaitStrategies appends under its own hardcoded
// sixty second deadline, so passing it after this re-wraps the strategy
// and reinstates the defect. TestNoContainerWaitsUnderTheLibraryDeadline
// fails if any test file still calls it.
//
// Mirrors testcontainers-go/modules/postgres@v0.43.0's
// BasicWaitStrategies: the readiness log line twice, because postgres
// restarts itself once after initialising, and then the published port,
// because Docker Desktop and Rancher Desktop start a separate proxy for it
// that can lag the server.
func PostgresReady() testcontainers.CustomizeRequestOption {
	return testcontainers.WithWaitStrategyAndDeadline(ContainerStartupTimeout,
		wait.ForLog("database system is ready to accept connections").WithOccurrence(2).
			WithStartupTimeout(ContainerStartupTimeout),
		wait.ForListeningPort("5432/tcp").WithStartupTimeout(ContainerStartupTimeout),
	)
}

// ToxiproxyReady waits for a toxiproxy container's control API, under
// ContainerStartupTimeout.
//
// Passed as an ordinary option to tctoxiproxy.Run, which applies the
// caller's options after its own (append(moduleOpts, opts...)), so this
// runs last and its deadline is the one that holds.
//
// Mirrors testcontainers-go/modules/toxiproxy@v0.43.0's own strategy: an
// HTTP 200 from /version on the control port.
func ToxiproxyReady() testcontainers.CustomizeRequestOption {
	return testcontainers.WithWaitStrategyAndDeadline(ContainerStartupTimeout,
		wait.ForHTTP("/version").WithPort(tctoxiproxy.ControlPort).
			WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }).
			WithStartupTimeout(ContainerStartupTimeout),
	)
}

// LocalStackReady waits for a LocalStack container's health endpoint,
// under ContainerStartupTimeout.
//
// The localstack module sets its step's own timeout to 120 seconds but
// installs it through WithWaitStrategy, whose group deadline is sixty, so
// the group gave up first and the step's two minutes never applied.
// Passed as an ordinary option to localstack.Run, which applies the
// caller's options after its own, so this one replaces the module's.
//
// Mirrors testcontainers-go/modules/localstack@v0.43.0's own strategy: an
// HTTP 200 from /_localstack/health on port 4566.
func LocalStackReady() testcontainers.CustomizeRequestOption {
	return testcontainers.WithWaitStrategyAndDeadline(ContainerStartupTimeout,
		wait.ForHTTP("/_localstack/health").WithPort("4566/tcp").
			WithStartupTimeout(ContainerStartupTimeout),
	)
}
