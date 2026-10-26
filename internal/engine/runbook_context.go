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
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
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

	// registries, when set, answers a method's request for the account of
	// one container registry (sdk.RegistryCredentials), from the project's
	// credential store, for the host the method names and no other.
	registries credential.RegistryStore
	// databases, when set, answers a method's request for a named database
	// connection (sdk.DatabaseConnections), from the project's credential
	// store, for the name the method asks for and no other.
	databases credential.DatabaseStore
	// namedSecrets, when set, answers a method's request for one field of a
	// named secret (sdk.NamedSecrets), from the project's credential store,
	// for the name and field a task's reference gives and no other.
	namedSecrets credential.SecretStore
	// route dials through the device's hops, nil when it has none
	// (sdk.Router); the Collection executor sets it per task.
	route sdk.RouteDial
	// console dials the device's console line through its console server,
	// nil when it has none (sdk.Consoler); the Collection executor sets it
	// per task.
	console sdk.ConsoleDial
	// projectDir is the project directory a method may read files from
	// (sdk.ProjectFiles), "" where the run has none on this machine; see
	// WithProjectFiles.
	projectDir string
	// projectGuards are the files and directories, besides .pleiades, that
	// a write into the project may not replace or write into; see
	// WithProjectFiles.
	projectGuards []string
	// ai, when set, is the run's project for the ai methods
	// (sdk.AIProject); nil answers sdk.ErrNoAIProject.
	ai *aiProject
	// reviews asks the person at the terminal about a held result; nil
	// keeps it pending (sdk.Reviewer).
	reviews *reviewDesk

	// registered is every secret the method obtained while it ran
	// (sdk.SecretRegistrar): a registry password this context answered with,
	// and any value the method registered itself. The Collection executor
	// adds them to the run's masking set once the method returns. Guarded by
	// mu.
	registered []string
}

// compile-time proof this satisfies both the SDK contract and the engine's
// own fact-collection interface.
var (
	_ sdk.RunbookContext      = (*runbookContext)(nil)
	_ FactCollector           = (*runbookContext)(nil)
	_ sdk.ConnectionPooler    = (*runbookContext)(nil)
	_ sdk.RegistryCredentials = (*runbookContext)(nil)
	_ sdk.DatabaseConnections = (*runbookContext)(nil)
	_ sdk.NamedSecrets        = (*runbookContext)(nil)
	_ sdk.AIProject           = (*runbookContext)(nil)
	_ sdk.Reviewer            = (*runbookContext)(nil)
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

// addSecrets adds extra to what InjectSecrets returns. The seeded login
// of a method that creates a machine arrives this way (LoginSeeder),
// beside its target's own credential; seed keys never collide with a
// credential's.
func (c *runbookContext) addSecrets(extra map[string]string) {
	for k, v := range extra {
		c.secrets[k] = v
	}
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
		if store == nil {
			return NewRunbookContext(nil), nil
		}
		if device == nil {
			return withRegistries(NewRunbookContext(nil), store), nil
		}

		cred, err := store.Lookup(ctx, device.Name())
		switch {
		case errors.Is(err, credential.ErrNotFound):
			return withRegistries(NewRunbookContext(nil), store), nil
		case err != nil:
			return nil, fmt.Errorf("resolve credential for device %q: %w", device.Name(), err)
		}

		return withRegistries(NewRunbookContext(credential.Flatten(cred)), store), nil
	}
}

// withRegistries lets rc answer for container registries, named database
// connections and named secrets from store, when the store keeps them.
func withRegistries(rc sdk.RunbookContext, store credential.Store) sdk.RunbookContext {
	c, ok := rc.(*runbookContext)
	if !ok {
		return rc
	}
	if rs, ok := store.(credential.RegistryStore); ok {
		c.registries = rs
	}
	if ds, ok := store.(credential.DatabaseStore); ok {
		c.databases = ds
	}
	if ss, ok := store.(credential.SecretStore); ok {
		c.namedSecrets = ss
	}
	return c
}

// NamedSecret implements sdk.NamedSecrets. A reference a task wrote is
// required, so a store that cannot be read is an error, and a run whose store
// keeps no named secrets answers that there is none.
func (c *runbookContext) NamedSecret(name, field string) (string, bool, error) {
	if c.namedSecrets == nil {
		// A context built with no credential store (a Runner's) cannot say
		// whether the project has the secret, so it says why rather than
		// "no such secret".
		return "", false, sdk.ErrNoNamedSecrets
	}
	value, ok, err := c.namedSecrets.LookupSecret(context.Background(), name, field)
	if err != nil || !ok {
		return "", false, err
	}
	// The value joins the run's masking set the moment it is handed out, as a
	// registry password does.
	c.RegisterSecret(value)
	return value, true, nil
}

// DatabaseConnection implements sdk.DatabaseConnections. Unlike a
// registry, a connection a task names is required, so a store that cannot
// be read is an error rather than an absent connection.
func (c *runbookContext) DatabaseConnection(name string) (wire.DatabaseConnection, bool, error) {
	if c.databases == nil {
		return wire.DatabaseConnection{}, false, nil
	}
	conn, ok, err := c.databases.LookupDatabase(context.Background(), name)
	if err != nil || !ok {
		return wire.DatabaseConnection{}, false, err
	}
	// The password joins the run's masking set the moment it is handed out,
	// as a registry password does.
	c.RegisterSecret(conn.Password)
	return conn, true, nil
}

// RegistryCredential implements sdk.RegistryCredentials. A store that
// cannot be read answers false, and the registry's own refusal of an
// anonymous pull then says what was missing.
func (c *runbookContext) RegistryCredential(host string) (string, string, bool) {
	if c.registries == nil {
		return "", "", false
	}
	user, password, ok, err := c.registries.LookupRegistry(context.Background(), host)
	if err != nil || !ok {
		return "", "", false
	}
	// The password joins the run's masking set the moment it is handed out,
	// as a device credential does when the run starts.
	c.RegisterSecret(password)
	return user, password, true
}

// RegisterSecret implements sdk.SecretRegistrar.
func (c *runbookContext) RegisterSecret(values ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, v := range values {
		if v != "" {
			c.registered = append(c.registered, v)
		}
	}
}

// RegisteredSecrets returns every secret registered while the method ran,
// for the Collection executor to mask (secretRegistry).
func (c *runbookContext) RegisteredSecrets() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.registered...)
}

// DeviceRoute implements sdk.Router.
func (rc *runbookContext) DeviceRoute() sdk.RouteDial { return rc.route }

// DeviceConsole implements sdk.Consoler: the dialer to the device's console
// line, or nil when the device has no console server.
func (rc *runbookContext) DeviceConsole() sdk.ConsoleDial { return rc.console }
