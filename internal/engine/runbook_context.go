package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// runbookContext is the Crawl-tier sdk.RunbookContext handed to a Collection
// method: it collects whatever the method emits so the executor can report
// it, and resolves secrets for the device the method is acting on.
//
// It is in-process by design. pkg/sdk's contract is shaped for an
// out-of-process method eventually talking over IPC, but at Crawl tier the
// engine links the method directly, so "send this fact back to the engine"
// is a map write. Nothing about the method's own code changes if that
// carrier is replaced later.
type runbookContext struct {
	// mu guards facts. A method is free to emit from more than one
	// goroutine, and nothing in the SDK contract says it may not.
	mu    sync.Mutex
	facts map[string]interface{}

	// secrets is what InjectSecrets returns. It is populated per device by
	// the composition root; an empty map means this run resolved none,
	// which is different from failing to look them up.
	secrets map[string]string

	// pool, when set, lends this method's SSH connections from the run's
	// pool (sdk.ConnectionPooler). The Collection executor sets it only
	// for a device whose connections persist; see WithConnectionPool.
	pool *remoteexec.Pool
}

// compile-time proof this satisfies both the SDK contract and the engine's
// own fact-collection interface.
var (
	_ sdk.RunbookContext   = (*runbookContext)(nil)
	_ FactCollector        = (*runbookContext)(nil)
	_ sdk.ConnectionPooler = (*runbookContext)(nil)
)

// NewRunbookContext builds a context carrying secrets for one device.
func NewRunbookContext(secrets map[string]string) sdk.RunbookContext {
	copied := make(map[string]string, len(secrets))
	for k, v := range secrets {
		copied[k] = v
	}
	return &runbookContext{facts: map[string]interface{}{}, secrets: copied}
}

// InjectSecrets returns the secrets available to this method.
func (c *runbookContext) InjectSecrets() map[string]string {
	snapshot := make(map[string]string, len(c.secrets))
	for k, v := range c.secrets {
		snapshot[k] = v
	}
	return snapshot
}

// ConnectionPool returns the pool this method's connections are lent
// from, or nil when its device's connections do not persist.
func (c *runbookContext) ConnectionPool() *remoteexec.Pool {
	return c.pool
}

// SetStat records a statistic. It shares storage with EmitFact rather than
// keeping a second map, because both end up in the same ActionResult.Stats
// and a split would only force the executor to merge them back.
func (c *runbookContext) SetStat(key string, value interface{}) error {
	return c.record(key, value)
}

// EmitFact records a fact discovered about the device.
func (c *runbookContext) EmitFact(key string, value interface{}) error {
	return c.record(key, value)
}

// record stores one emitted value, rejecting an empty key. An empty key
// would produce a stat nothing can reference in a later when_cel, which is
// a silent no-op rather than an error the author can see.
func (c *runbookContext) record(key string, value interface{}) error {
	if key == "" {
		return fmt.Errorf("runbook context: refusing to record a value under an empty key")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.facts[key] = value
	return nil
}

// Facts returns a snapshot of everything recorded.
func (c *runbookContext) Facts() map[string]interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()

	snapshot := make(map[string]interface{}, len(c.facts))
	for k, v := range c.facts {
		snapshot[k] = v
	}
	return snapshot
}

// RunbookContextFunc builds the sdk.RunbookContext one Collection method
// invocation is handed, for the one device it is acting on.
//
// It takes a context and returns an error because resolving a device's
// secrets is real work that can fail: the Crawl tier reads and decrypts a
// credential file here. Before this signature existed the composition
// root had nowhere to report that, so it passed every method an empty
// secret set and no method needing a credential could run through the
// CLI at all.
type RunbookContextFunc func(ctx context.Context, device inventory.InventoryItem) (sdk.RunbookContext, error)

// NewDeviceRunbookContext is the RunbookContextFunc for a caller that
// resolves no secrets at all: every method it builds a context for sees
// an empty InjectSecrets.
//
// It is correct for a composition root with no credential store, and for
// tests. A real Crawl-tier run wants NewCredentialRunbookContext instead;
// this one is not the sensible default it was once used as.
func NewDeviceRunbookContext(_ context.Context, _ inventory.InventoryItem) (sdk.RunbookContext, error) {
	return NewRunbookContext(nil), nil
}

// NewCredentialRunbookContext returns a RunbookContextFunc that resolves
// each device's stored credential and hands it to the Collection method
// as flattened secrets.
//
// This is what makes a credential-needing Collection method work at the
// Crawl tier. The Walk tier does the same thing by a different route:
// the Controller resolves the credential at dispatch time and attaches
// it to the payload, and the per-task subprocess builds its context from
// that. Both ends read the map by the same wire.Secret* keys.
//
// A device with no stored credential is NOT an error. It produces an
// empty secret set, exactly as an empty payload does on the other tier,
// and only a method that actually needs a secret fails, at the point it
// needs one, with a message about authentication rather than about a
// file. Any other lookup failure (an unreadable store, a wrong master
// key, a corrupt entry) is reported, because that is a broken
// installation rather than an absent credential and the two must not
// look alike.
func NewCredentialRunbookContext(store credential.Store) RunbookContextFunc {
	return func(ctx context.Context, device inventory.InventoryItem) (sdk.RunbookContext, error) {
		if store == nil || device == nil {
			return NewRunbookContext(nil), nil
		}

		cred, err := store.Lookup(ctx, device.Name())
		switch {
		case errors.Is(err, credential.ErrNotFound):
			return NewRunbookContext(nil), nil
		case err != nil:
			return nil, fmt.Errorf("resolve credential for device %q: %w", device.Name(), err)
		}

		return NewRunbookContext(credential.Flatten(cred)), nil
	}
}
