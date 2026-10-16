// Package external: the RunbookContext a child process hands its method.
package external

import (
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// RunbookContext is the sdk.RunbookContext a child process hands to the
// Collection method it runs, whether that child is the Runner re-executing
// itself or an external Collection built outside this repository.
//
// Secrets are held as mutable []byte internally, never string (which Go
// cannot zero once created: every string value is an immutable,
// potentially-shared, garbage-collector-managed byte sequence), so Zero
// can overwrite them the instant the invocation returns. This is a
// best-effort measure against the storage this type itself controls: it
// cannot reach a copy the method's own code retained past its call (Go's
// own string immutability means InjectSecrets's own map[string]string
// return value is already a copy the caller could keep indefinitely). A
// stronger guarantee would need a breaking sdk.RunbookContext change (a
// Secret type wrapping []byte with an explicit Zero method).
//
// SetStat and EmitFact both write into the same facts map, mirroring
// internal/engine/runbook_context.go's own established convention for the
// identical reason: nothing downstream distinguishes a "stat" from a
// "fact" today.
type RunbookContext struct {
	mu      sync.Mutex
	secrets map[string][]byte
	facts   map[string]interface{}
	// pool, when set, lends the method's SSH connections (see
	// InvokeRequestWithPool). It holds no secret: a pooled connection
	// keeps only a keyed digest of the credential it logged in with.
	pool *remoteexec.Pool
	// registry is the dispatch's container registry credential, given to a
	// method only for its own host (RegistryCredential). The Runner sets it
	// for its own built-in methods; an external program never receives one.
	registry *wire.RegistryCredential
	// databases are the dispatch's named database connections, given to a
	// method only by name (DatabaseConnection). Like registry, the Runner
	// sets them for its own built-in methods only.
	databases []wire.DatabaseConnection
}

var (
	_ sdk.RunbookContext      = (*RunbookContext)(nil)
	_ sdk.ConnectionPooler    = (*RunbookContext)(nil)
	_ sdk.RegistryCredentials = (*RunbookContext)(nil)
	_ sdk.DatabaseConnections = (*RunbookContext)(nil)
)

// ConnectionPool implements sdk.ConnectionPooler: the pool this call's
// connections are lent from, or nil when every Connect logs in afresh.
func (c *RunbookContext) ConnectionPool() *remoteexec.Pool {
	return c.pool
}

// NewRunbookContext builds a RunbookContext over secrets, which it copies
// into its own mutable storage rather than referencing the caller's map,
// so Zero never risks mutating memory the caller still owns.
func NewRunbookContext(secrets map[string]string) *RunbookContext {
	stored := make(map[string][]byte, len(secrets))
	for k, v := range secrets {
		stored[k] = []byte(v)
	}
	return &RunbookContext{secrets: stored, facts: map[string]interface{}{}}
}

// InjectSecrets implements sdk.RunbookContext.
func (c *RunbookContext) InjectSecrets() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]string, len(c.secrets))
	for k, v := range c.secrets {
		out[k] = string(v)
	}
	return out
}

// SetStat implements sdk.RunbookContext.
func (c *RunbookContext) SetStat(key string, value interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.facts[key] = value
	return nil
}

// EmitFact implements sdk.RunbookContext.
func (c *RunbookContext) EmitFact(key string, value interface{}) error {
	return c.SetStat(key, value)
}

// Facts returns a snapshot copy of every SetStat and EmitFact call this
// invocation made. It satisfies internal/engine's FactCollector, which is
// how a method run in-process through this context reports its output.
func (c *RunbookContext) Facts() map[string]interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]interface{}, len(c.facts))
	for k, v := range c.facts {
		out[k] = v
	}
	return out
}

// Zero overwrites every stored secret's backing bytes with 0 and drops
// this context's own reference to them. Call it via defer immediately
// after the method returns, on every path (success or failure).
func (c *RunbookContext) Zero() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.secrets {
		for i := range v {
			v[i] = 0
		}
		delete(c.secrets, k)
	}
	c.databases = nil
}

// DatabaseConnection implements sdk.DatabaseConnections. The Runner masks
// every bound connection's password out of whatever its child sends back,
// a one-shot child's and a session child's alike, so the child registers
// none.
func (c *RunbookContext) DatabaseConnection(name string) (wire.DatabaseConnection, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	conn, ok := wire.FindDatabaseConnection(c.databases, name)
	return conn, ok, nil
}

// RegistryCredential implements sdk.RegistryCredentials.
func (c *RunbookContext) RegistryCredential(host string) (string, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.registry.For(host)
}
