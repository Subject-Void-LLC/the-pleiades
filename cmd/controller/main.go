// Command controller is the Control Plane composition root PLAN.md
// Section 16 names: the API Gateway, backed by the embedded state store
// and the real NATS-backed event.Bus, dispatching Runbooks that
// cmd/runner's Agents pick up.
//
// This is Section 25's own named deadline: "Composition root (cmd/controller,
// cmd/runner) | Every driver construction in the system | Build by Phase 2 |
// Every other primitive is advisory until something assembles them." Every
// port this phase's checklist touches (event.Bus, and through it
// api.Dispatcher and api.LogStreamer) gets its first real, running,
// non-test caller here.
//
// Deliberately out of scope: a Postgres-backed store. PLAN.md Section 16
// names PostgreSQL as the v1 default, but no ent Postgres migration path
// exists anywhere in this repository yet (internal/ent/embedded.go only
// has a SQLite embedded-migration path), and adding one is not a Phase 2
// checklist item; this composition root uses the same embedded SQLite
// path cmd/pleiades (the Walk-tier CLI) already does.
//
// This is also Phase 4's own composition root: exactly one running
// controller replica must hold the "pleiades-scheduler-leader" lease at
// a time (PATTERNS.md's "Leader Election (The Scheduler Pattern)"
// entry), via a lock.NewNatsLockManager constructed here and an
// internal/election.LeaderElector run in the background alongside the
// HTTP server. This phase wires the election *primitive* only: nothing
// in this binary yet gates real work behind elector.IsLeader() (Phase 23,
// The RRULE Scheduler, is what will), so lock.NewNatsLockManager's own
// per-device counterpart (engine.Executor's runtime locking) still has no
// caller here either, unchanged from before this phase -- that remains
// cmd/runner's concern.
//
// **Correction, a later session's fan-out reaper fix: a second,
// independent LeaderElector (reaperElector, fanOutReaperLeaseKey) now
// does gate real work.** dispatch.Reaper.Run is
// the first real consumer of election.LeaderElector.IsLeader anywhere in
// this repository, ahead of Phase 23. It exists because
// dispatch.JobStore.BeginFanOut's own staleAfter reclaim was unreachable
// in production: see dispatch.DefaultFanOutLeaseTTL's own doc comment for
// why a redelivered job.requested alone never triggers it.
//
// This is also Phase 5's own composition root: internal/crypto's DEK/KEK
// envelope encryption (PLAN.md Section 17) is registered on the Device
// entity here, its first real, running, non-test caller anywhere in this
// repository -- previously it was wired only inside its own package test.
// MASTER_ENCRYPTION_KEY is required and read directly, deliberately not
// through internal/crypto.ResolveKey's file-fallback tiers: those are
// right for cmd/pleiades (a single local operator owns the whole
// machine), but wrong for a network-facing server, where a
// redeployed/fresh-filesystem container silently generating and saving a
// new key next to the database would permanently orphan every row
// already encrypted under the old one. Fail closed at startup instead,
// the same shape this file already uses for JWT_SECRET.
//
// This is also Phase 14's own composition root, "The Dispatcher"
// (.SPECIFICATION/IMPLEMENTATION.md; PLAN.md Section 28.4). api.Dispatcher
// no longer streams a target group and publishes one event per device
// inline inside an HTTP request; it now only resolves the requested
// runbook, persists a Job, and publishes a single job.requested event,
// answering 202 Accepted immediately. Two new dependencies exist here to
// support that shift, both wired the same fail-closed-at-startup way
// every other dependency in this file already is: a runbook.Source
// (runbook.NewDirSource, rooted at RUNBOOK_DIR, defaulting to
// inventory.DefaultRunbookDir), which resolves a runbook id to its
// compiled capability requirements from real YAML files on disk rather
// than the "no runbook storage at all" gap that predated this phase; and
// a dispatch.JobStore (dispatch.NewEntJobStore, over the same already-open
// Device client every other repository in this process shares), which
// persists the Job and JobTask rows both api.Dispatcher (write-only, at
// launch) and the new api.JobHandler (read-only, at GET /jobs/{id}) share.
// A dispatch.Worker is subscribed to topology.JobRequestedSubject()
// alongside them: it is the durable consumer that actually performs the
// per-device fan-out a Job's launch only records the intent for, running
// entirely off the HTTP request path so a 10,000-device dispatch no
// longer holds a request open for as long as the slowest publish and no
// longer loses its progress if the process dies mid-fan-out.
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
	"github.com/Subject-Void-LLC/the-pleiades/internal/announce"
	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/resolve"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	credfile "github.com/Subject-Void-LLC/the-pleiades/internal/credtype/lookup/file"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/election"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	// The built-in launch kinds. A blank import because their init()
	// functions are the only thing that populates internal/launch's
	// registry, and a template is validated against its kind's descriptor at the write, so a Controller that did not import this would refuse every template as an unknown kind
	// (FAILURE_PATTERNS.md #52).
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"

	"github.com/Subject-Void-LLC/the-pleiades/internal/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/telemetry"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
	uiweb "github.com/Subject-Void-LLC/the-pleiades/internal/ui/web"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
)

// serviceName identifies this process in every span it emits.
const serviceName = "pleiades-controller"

// serviceVersion is the build identifier the UI shows in its sidebar. It
// is a constant rather than a linker flag for now: Phase 20 owns release
// packaging and is where a real version stamp belongs.
const serviceVersion = "v0.1.0-alpha"

// schedulerLeaseKey is the well-known key every controller replica
// contends for to become the one holder of the scheduler lease. It lives
// here, at the one real call site that cares about it, rather than
// inside internal/election itself: election.LeaderElector takes a key
// from its caller precisely so no key is hardcoded into the reusable
// primitive (PLAN.md Section 25's "Leader elector" Build-Once Contract).
const schedulerLeaseKey = "pleiades-scheduler-leader"

// fanOutReaperLeaseKey is the well-known key every controller replica
// contends for to become the one holder of the fan-out reaper lease, kept
// distinct from schedulerLeaseKey above: internal/election.LeaderElector's
// own doc comment requires two different keys for two logically distinct
// elections, and there is no reason a replica's reaper leadership should
// be coupled to its (still-unclaimed, Phase 23) scheduler leadership.
const fanOutReaperLeaseKey = "pleiades-fanout-reaper-leader"

// actorFromRequest is the activity stream's ActorSource: who is making the
// change carried by this context.
//
// It reads the authenticated identity the auth middleware placed there,
// which is the same value for a Bearer token and for a browser session,
// because internal/api owns the one context key both credential kinds write
// through. That is what makes one decorator cover both write surfaces.
//
// An empty return is not a default. It means no identity reached the store,
// and access.NewAuditedStore refuses the write rather than recording it
// against nobody: see access.ErrUnattributed for why an audit trail with
// anonymous rows is worse than one with gaps.
func actorFromRequest(ctx context.Context) string {
	if identity, ok := api.IdentityFromContext(ctx); ok {
		return identity.Subject
	}
	return ""
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// fatal logs a startup failure at error level and exits non-zero.
//
// It replaces log.Fatalf, which this file used to call. That stopped being
// correct once main installed a JSON slog handler as the default: Go's
// standard log package routes through slog.Default at *info* level, so
// every startup failure in this binary was being emitted as an INFO line
// and no alerting rule keyed on level would ever have fired for one.
//
// It does not fix the separate, pre-existing residual risk described
// below: exiting here still skips deferred cleanup.
func fatal(msg string, err error) {
	slog.Error(msg, slog.String("error", err.Error()))
	os.Exit(1)
}

// decodeEnvelopeKey base64-decodes the value of envVar and requires it to
// decode to exactly 32 bytes, matching crypto.NewAESService's own AES-256
// requirement. Unlike internal/crypto.ResolveKey, there is no file
// fallback here: see this file's own doc comment for why a server
// composition root must fail closed on a missing key rather than
// silently generate and persist one.
func decodeEnvelopeKey(envVar string) ([]byte, error) {
	raw := os.Getenv(envVar)
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("%s must be base64-encoded and decode to exactly 32 bytes", envVar)
	}
	return key, nil
}

// loadEnvelopeService builds the DEK/KEK envelope encryption service
// (internal/crypto.EnvelopeService) from environment configuration.
// MASTER_ENCRYPTION_KEY is required. MASTER_ENCRYPTION_KEY_VERSION is
// optional (defaults to "v1", PLAN.md Section 17.2's own example tag).
// MASTER_ENCRYPTION_KEY_PREVIOUS and MASTER_ENCRYPTION_KEY_PREVIOUS_VERSION
// are optional but must be set together: during a key rotation window an
// operator moves the old current key and its version tag into these two,
// so Decrypt can still open rows written under it while every new write
// uses the new current key (PLAN.md Section 17.2's "loads a secondary
// key, decrypts... with the old, re-encrypts... with the new").
func loadEnvelopeService() (*crypto.EnvelopeService, error) {
	if os.Getenv("MASTER_ENCRYPTION_KEY") == "" {
		return nil, fmt.Errorf("MASTER_ENCRYPTION_KEY is required")
	}
	currentKey, err := decodeEnvelopeKey("MASTER_ENCRYPTION_KEY")
	if err != nil {
		return nil, err
	}
	currentVersion := getenv("MASTER_ENCRYPTION_KEY_VERSION", "v1")

	havePreviousKey := os.Getenv("MASTER_ENCRYPTION_KEY_PREVIOUS") != ""
	havePreviousVersion := os.Getenv("MASTER_ENCRYPTION_KEY_PREVIOUS_VERSION") != ""
	if havePreviousKey != havePreviousVersion {
		return nil, fmt.Errorf("MASTER_ENCRYPTION_KEY_PREVIOUS and MASTER_ENCRYPTION_KEY_PREVIOUS_VERSION must be set together, or not at all")
	}
	if !havePreviousKey {
		return crypto.NewEnvelopeService(currentKey, currentVersion, nil, "")
	}

	previousKey, err := decodeEnvelopeKey("MASTER_ENCRYPTION_KEY_PREVIOUS")
	if err != nil {
		return nil, err
	}
	previousVersion := os.Getenv("MASTER_ENCRYPTION_KEY_PREVIOUS_VERSION")
	return crypto.NewEnvelopeService(currentKey, currentVersion, previousKey, previousVersion)
}

// loadKeyProvider builds the auth.KeyProvider this process verifies
// tokens against. JWKS_URL, when set, selects the real Federated Identity
// path (auth.NewJWKSKeyProvider); otherwise JWT_SECRET backs a static,
// development-only provider (auth.NewStaticKeyProvider), which fails
// closed on a short or empty secret rather than a stale open condition
// letting a forgeable key start serving traffic.
//
// PLAN.md Section 32.1 says a shared symmetric secret "must be rejected at
// startup in any other mode" than local development. No deploy-mode
// config surface (dev vs. production) exists anywhere in this codebase
// yet, so that stronger enforcement is not built here: an operator can
// choose JWKS today by setting JWKS_URL, but nothing yet forces the
// choice. Stated plainly as a deferred gap, not silently skipped.
// resolveDatabaseDSN decides which database this process talks to.
//
// DB_DSN is the real knob and names any backend internal/ent.OpenDatabase
// supports, which is what lets a deployment choose PostgreSQL without
// this binary knowing anything about dialects. DB_PATH is kept as the
// SQLite shorthand it always was, so an existing single-file deployment,
// both cmd/controller release gates, and the getting-started guide all
// keep working unchanged.
//
// Setting both is a startup error rather than a silent precedence rule.
// An operator who sets both has two different intentions written down
// and deserves to be told, rather than to discover months later which one
// this code happened to prefer.
func resolveDatabaseDSN() (string, error) {
	dsn := os.Getenv("DB_DSN")
	path := os.Getenv("DB_PATH")

	switch {
	case dsn != "" && path != "":
		return "", fmt.Errorf("DB_DSN and DB_PATH are both set; DB_DSN names any supported backend and DB_PATH is the SQLite shorthand, so set exactly one")
	case dsn != "":
		return dsn, nil
	case path != "":
		return "sqlite://" + path, nil
	default:
		// The historical default: a SQLite file beside the binary.
		return "sqlite://controller.db", nil
	}
}

func loadKeyProvider() (auth.KeyProvider, error) {
	if jwksURL := os.Getenv("JWKS_URL"); jwksURL != "" {
		return auth.NewJWKSKeyProvider(jwksURL)
	}
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required when JWKS_URL is not set")
	}
	return auth.NewStaticKeyProvider([]byte(jwtSecret))
}

// loadRateLimiter builds the ingress token bucket from environment
// configuration. RATE_LIMIT_RPS is the sustained per-caller rate and
// RATE_LIMIT_BURST the back-to-back allowance; both have defaults, and
// setting RATE_LIMIT_RPS to 0 turns throttling off entirely.
//
// The limiter is on by default rather than opt-in. PATTERNS.md's Rate
// Limiter entry names the realistic threat as "a misconfigured CI pipeline
// retrying a failed playbooks.dispatch call in a tight loop," and a
// protection nobody remembers to enable does not defend against a mistake
// nobody meant to make. The defaults are generous enough that no
// legitimate interactive or scripted caller reaches them.
func loadRateLimiter() (*api.RateLimiter, error) {
	rps, err := strconv.ParseFloat(getenv("RATE_LIMIT_RPS", "50"), 64)
	if err != nil || rps < 0 {
		return nil, fmt.Errorf("RATE_LIMIT_RPS must be a non-negative number")
	}
	if rps == 0 {
		return nil, nil
	}
	burst, err := strconv.Atoi(getenv("RATE_LIMIT_BURST", "100"))
	if err != nil || burst < 1 {
		return nil, fmt.Errorf("RATE_LIMIT_BURST must be a positive integer")
	}
	return api.NewRateLimiter(api.RateLimiterConfig{
		RequestsPerSecond: rps,
		Burst:             burst,
	}), nil
}

// readinessChecks are the dependencies /readyz reports on, the two this
// process cannot serve a single API request without.
//
// The NATS check uses the connection this binary already holds for the
// log streamer rather than reaching inside event.Bus for its own. That is
// a real check, not a proxy: it is a live connection to the same broker,
// so a broker outage or a network partition flips it exactly when it
// flips for the Bus. Reaching for the Bus's own connection would mean
// widening the event.Bus port with a health method that six test doubles
// would then have to implement, to learn the same fact.
//
// The database check issues a real query rather than pinging the pool,
// because SQLite's pool will happily hand out a handle to a file that has
// been deleted or corrupted underneath it.
func readinessChecks(nc *nats.Conn, client *ent.Client) []api.ReadinessCheck {
	return []api.ReadinessCheck{
		{
			Name: "nats",
			Probe: func(ctx context.Context) error {
				if !nc.IsConnected() {
					return fmt.Errorf("nats connection is %s", nc.Status())
				}
				return nil
			},
		},
		{
			Name: "database",
			Probe: func(ctx context.Context) error {
				if _, err := client.Device.Query().Limit(1).IDs(ctx); err != nil {
					return fmt.Errorf("state store query failed: %w", err)
				}
				return nil
			},
		},
	}
}

// Known, deliberate residual risk (confirmed by an adversarial review of
// Phase 5, not fixed here): every fatal call below reaches os.Exit
// directly, which skips Go's deferred-function cleanup. A startup
// failure after client, bus, or lockMgr are constructed (several of
// which this phase's own envelope-service/rotation wiring added) exits
// without running the same graceful client.Close()/bus.Close()/
// lockMgr.Close() the SIGINT/SIGTERM path below performs explicitly.
// Impact is bounded, not a persistent cross-restart leak: nothing has
// flowed through bus/lockMgr yet at any of these points, and the OS
// reclaims open sockets and SQLite file locks immediately on process
// exit. This is a pre-existing pattern in this file predating this
// phase (only two of its call sites are new here), not something Phase
// 5's own scope owns; a real fix means reworking every startup error
// path in this file to return through one shared cleanup sequence
// instead of exiting immediately, out of scope for "Envelope
// Encryption."
func main() {
	natsURL := getenv("NATS_URL", nats.DefaultURL)
	dbDSN, err := resolveDatabaseDSN()
	if err != nil {
		fatal("failed to resolve database configuration", err)
	}
	listenAddr := getenv("LISTEN_ADDR", ":8080")
	jwtIssuer := getenv("JWT_ISSUER", "pleiades-controller")
	jwtAudience := getenv("JWT_AUDIENCE", "pleiades-api")
	keyProvider, err := loadKeyProvider()
	if err != nil {
		fatal("failed to init auth key provider", err)
	}

	// One logger, built here and injected into everything that logs,
	// rather than each package building its own from a package-level var.
	// slog.SetDefault means the packages this phase did not touch still
	// emit the same JSON to the same place instead of plain text.
	//
	// The handler options carry the shared secret-masking ruleset. They are
	// built by redact rather than written out here, so a handler cannot be
	// constructed that holds the ruleset and forgot to install it, and
	// internal/archtest fails the build if any binary passes nil here
	// instead. The ruleset has to reach the logger through ReplaceAttr
	// rather than through a wrapping slog.Handler: a wrapper cannot see
	// attributes added with Logger.With, because those are pre-formatted
	// into a byte buffer at WithAttrs time, and it sees the message only as
	// an opaque string. internal/redact's wrapper_control_test.go
	// demonstrates both failures against a real wrapping handler.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, redact.Shared().HandlerOptions(slog.LevelInfo)))
	slog.SetDefault(logger)

	// The corollary, and the reason it is two lines instead of one: a
	// log.Fatalf or a log.Printf from any dependency bypasses slog
	// entirely and still reaches an operator's terminal. A masking control
	// that covers the structured path and not the failure path emits
	// unmasked exactly when things are going wrong.
	log.SetOutput(redact.Shared().Writer(os.Stderr))

	// One private Prometheus registry, not the process-global default:
	// two routers in one process (or one process that later grows a
	// second listener) must not fight over one registry, and a test that
	// asserts on metrics must be able to read an isolated set.
	metricsRegistry := prometheus.NewRegistry()

	rateLimiter, err := loadRateLimiter()
	if err != nil {
		fatal("failed to init rate limiter", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Tracing is built before any port so the spans describing startup
	// itself are recorded. PLAN.md Section 19 requires OTEL in the
	// foundation of the Controller API, and this is where the distributed
	// trace that reaches cmd/runner begins.
	telemetryCfg, err := telemetry.ConfigFromEnv(serviceName, "")
	if err != nil {
		fatal("failed to read telemetry configuration", err)
	}
	tracerProvider, err := telemetry.Setup(ctx, telemetryCfg)
	if err != nil {
		fatal("failed to init telemetry", err)
	}

	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: dbDSN})
	if err != nil {
		fatal("failed to open the controller database", err)
	}
	defer client.Close()

	// Envelope encryption is installed before the client is used for
	// anything else, so no Device write or read anywhere in this process
	// can bypass it. See this file's own doc comment for why
	// MASTER_ENCRYPTION_KEY has no file fallback here.
	envelopeSvc, err := loadEnvelopeService()
	if err != nil {
		fatal("failed to init envelope encryption", err)
	}
	client.Device.Use(crypto.DeviceEnvelopePropertiesHook(envelopeSvc))
	client.Device.Intercept(crypto.DeviceEnvelopePropertiesInterceptor(envelopeSvc))

	// Survey answers. This pair was written, tested, and then registered
	// nowhere: until Phase 22 it was referenced only from its own package
	// test, which meant SavedLaunchConfig.answers was stored in plaintext
	// while internal/apispec's own saved-configuration schema told API
	// callers it was encrypted at rest. A shipped false claim about a
	// security control is worse than a missing control, because it stops
	// anybody looking. TestEveryCryptoHookIsComposed now fails the build if
	// a hook this package exports is left unregistered again.
	client.SavedLaunchConfig.Use(crypto.SavedLaunchConfigAnswersHook(envelopeSvc))
	client.SavedLaunchConfig.Intercept(crypto.SavedLaunchConfigAnswersInterceptor(envelopeSvc))

	// Credential inputs, using the BOUND envelope: the ciphertext is
	// cryptographically tied to its own row, so a database writer cannot
	// relocate one organization's secrets onto another organization's
	// credential and have the platform inject them. Device and
	// SavedLaunchConfig remain on the unbound form; internal/crypto/
	// envelope_bound.go records why that is acceptable there and not here.
	client.Credential.Use(crypto.CredentialInputsHook(envelopeSvc))
	client.Credential.Intercept(crypto.CredentialInputsInterceptor(envelopeSvc))

	rotateKeys := getenv("ROTATE_ENCRYPTION_KEYS", "") == "true"

	repo := inventory.NewEntRepository(client, inventory.NewItemFactory())
	evaluator, err := auth.NewJWTEvaluator(keyProvider, jwtIssuer, jwtAudience)
	if err != nil {
		fatal("failed to init auth evaluator", err)
	}

	bus, err := event.NewNatsBus(ctx, natsURL)
	if err != nil {
		fatal("failed to connect event bus", err)
	}

	// lockMgr backs the scheduler leader election below. Like the
	// LogStreamer connection just below, this is a distinct NATS
	// connection from event.NewNatsBus's own internal one: lock.Manager
	// and event.Bus are separate ports with no shared adapter today.
	lockMgr, err := lock.NewNatsLockManager(ctx, natsURL)
	if err != nil {
		fatal("failed to init lock manager", err)
	}

	elector := election.NewLeaderElector(lockMgr, schedulerLeaseKey,
		election.WithOnAcquired(func() {
			slog.Info("Acquired Scheduler Lease", slog.String("key", schedulerLeaseKey))
		}),
	)
	electorDone := make(chan struct{})
	go func() {
		defer close(electorDone)
		elector.Run(ctx)
	}()

	// LogStreamer needs a raw jetstream.JetStream handle for its own
	// per-request ephemeral consumers (PLAN.md Section 26.4's deliberate
	// exception to going through Bus.Subscribe: every log viewer must see
	// every line, which a shared durable consumer group cannot give). A
	// second, independent NATS connection backs it -- the same documented
	// tradeoff cmd/demo/main.go already accepts, not an oversight.
	nc, err := nats.Connect(natsURL)
	if err != nil {
		fatal("failed to connect to nats", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		fatal("failed to get jetstream", err)
	}

	// runbookDir defaults to inventory.DefaultRunbookDir ("runbooks"), the
	// same scaffolding convention cmd/pleiades's own scaffolded projects
	// already use, though runbook.NewDirSource accepts any directory an
	// operator points RUNBOOK_DIR at. Constructed and fatal()'d exactly
	// like every other startup dependency in this file: a misconfigured
	// or unreadable runbook directory is a startup-time failure the
	// operator must fix, never a condition discovered lazily on a
	// caller's first dispatch request.
	runbookDir := getenv("RUNBOOK_DIR", inventory.DefaultRunbookDir)
	runbooks, err := runbook.NewDirSource(runbookDir)
	if err != nil {
		fatal("failed to init runbook source", err)
	}

	// playbooks is the unconverted-Ansible half of the launch catalog,
	// present only when the operator points PLAYBOOK_DIR somewhere. No
	// default directory, unlike RUNBOOK_DIR, because a deployment that has
	// never run Ansible has no playbooks and should not be scanning a
	// conventionally-named directory for some; absent means the playbook
	// kind lists nothing, creating a playbook template is refused with a
	// reason, and a playbook job is failed by the fan-out rather than
	// guessed at. When the variable IS set, a bad path is fatal exactly
	// like RUNBOOK_DIR's: an explicitly configured directory that cannot
	// be read is an operator error to surface at startup.
	//
	// The Runner reads the same variable for the execution side
	// (cmd/runner/main.go), the identical two-binary convention
	// RUNBOOK_DIR already follows.
	var playbooks *playbook.DirSource
	if playbookDir := getenv("PLAYBOOK_DIR", ""); playbookDir != "" {
		playbooks, err = playbook.NewDirSource(playbookDir)
		if err != nil {
			fatal("failed to init playbook source", err)
		}
	}

	// credentials resolves a device's stored SSH credential at dispatch
	// time, so worker below can attach it directly to
	// wire.DispatchPayload.Secrets (Phase 16, Native Go Execution
	// Adapter: the Runner never holds its own copy of the decryption
	// key). This is deliberately the same Walk-tier file-backed adapter
	// cmd/pleiades already trusts (credential.NewLazyFileStore), not
	// PLAN.md Section 17's full Postgres/Vault-backed CredentialStore,
	// which remains unbuilt future work; see this phase's own plan for
	// why that full store is out of scope here. Lazy construction means a
	// controller with no credentials configured yet still starts cleanly
	// and only touches disk the first time a dispatch actually needs one.
	credentialsDir := getenv("CONTROLLER_CREDENTIALS_DIR", ".")
	deviceCredentials := credential.NewLazyFileStore(credentialsDir)

	// The Crawl-tier credential surface, which is a different axis from
	// the per-device file store above rather than a replacement for it.
	// That store answers "what does this DEVICE authenticate with"; this
	// one answers "what does this TEMPLATE run as", which is AWX's own
	// model and the one an imported job template needs. Both survive: the
	// fan-out consults a template's machine credential first and falls
	// back to the file store, so every dispatch that worked before this
	// phase still works unchanged.
	//
	// The store gets a render engine because it validates a credential
	// type's injector templates at the write, so an author learns about a
	// template naming an input the type does not declare when they save
	// it rather than when an operator launches a job.
	credentialStore := credstore.NewEntStore(client, render.New())

	// The credential types this build ships, installed on every startup
	// rather than by a migration, because a migration cannot be re-run when
	// a later release adds a type or corrects one. See
	// credstore.ReconcileManaged for why a failure here warns rather than
	// stopping the controller.
	if err := credstore.ReconcileManaged(ctx, credentialStore, managed.Types(), logger); err != nil {
		logger.WarnContext(ctx, "some managed credential types are not installed", "error", err)
	}

	// credentialResolver is the OTHER credential port, and the split
	// between the two is a security boundary rather than a naming choice:
	// credentialStore above cannot return a plaintext secret because its
	// projection has no field for one, and this one can. It is handed to
	// the fan-out worker and to nothing else, and internal/archtest fails
	// the build if internal/api ever imports the package it comes from.
	//
	// The file lookup is the one real external secret source this platform
	// implements. A deployment that has not set its directory gets a
	// resolver that works normally and fails only the specific credential
	// that names an external reference, which is why this is wired
	// unconditionally rather than behind a configuration check.
	externalLookups, err := credtype.NewLookups(credfile.FromEnvironment())
	if err != nil {
		fatal("failed to build the external secret source table", err)
	}
	credentialResolver := resolve.NewEntResolver(client, resolve.WithLookups(externalLookups))

	// injector renders a resolved credential into what a run executes with.
	// It takes the same render engine the store validates writes with, so a
	// credential type that saved successfully cannot fail to render for a
	// reason the author was never shown.
	injector, err := credtype.NewInjector(render.New())
	if err != nil {
		fatal("failed to build the credential injector", err)
	}

	// jobStore persists Job and JobTask rows over the same already-open
	// Device client every other repository in this process shares. It
	// backs both api.Dispatcher (which only ever creates a Job and reads
	// nothing back) and api.JobHandler (which only ever reads), and it is
	// what worker below claims fan-out ownership through.
	jobStore := dispatch.NewEntJobStore(client)

	// sets is the Inventory store: the named, shareable device sets a
	// template targets. Constructed here rather than beside the UI's own
	// handlers because the fan-out worker below needs it to resolve what a
	// job actually dispatches against.
	sets := inventory.NewEntSetStore(client)

	// worker is the durable job.requested consumer (internal/dispatch's
	// own doc comment: PLAN.md Section 28.4's "a durable worker performs
	// the actual per-device fan-out later, off the HTTP request path
	// entirely"). It depends on the same repo and bus every other piece
	// of this composition root already holds, plus runbooks and jobStore
	// just constructed above, so a job.requested event handed to
	// HandleJobRequested has everything it needs to resolve the job,
	// stream the target group, admit or skip each device, and publish a
	// wire.DispatchPayload per admitted device.
	//
	// WithSetStore is what lets it resolve the Inventory a job names into
	// the devices it holds. Without it the Worker cannot target a job
	// launched from a template at all, and refuses it rather than falling
	// through to an unrestricted selector: a job that could not be targeted
	// must dispatch to nothing, never to every device the platform manages.
	// The playbook kind's fan-out preparation, wired in the same breath as
	// the source that enables it. FAILURE_PATTERNS.md #110's lesson: when a
	// phase adds an option a feature requires, the composition root is part
	// of that feature, not a follow-up; a Worker missing this would fail
	// every playbook job with a correct refusal and nothing failing at
	// build time.
	//
	// WithCredentials is the Phase 22 half: the resolver and the injector
	// travel together, so a Worker cannot hold one without the other. A
	// deployment that has created no credential type is unaffected, since
	// a job binding nothing never enters that path.
	workerOpts := []dispatch.WorkerOption{
		dispatch.WithSetStore(sets),
		dispatch.WithCredentials(credentialResolver, injector),
	}
	if playbooks != nil {
		workerOpts = append(workerOpts,
			dispatch.WithDefinitionSource("playbook", dispatch.NewPlaybookDefinitionSource(playbooks)))
	}
	worker := dispatch.NewWorker(jobStore, repo, runbooks, bus, deviceCredentials, workerOpts...)
	// Subscribe launches its own goroutine and returns quickly
	// (internal/event/consumer.go), so this call does not block startup;
	// a failure here is handled the same fatal() way every other startup
	// error in this file is, since a controller that could not subscribe
	// its own fan-out worker would accept dispatch launches it can never
	// actually carry out.
	if err := bus.Subscribe(ctx, topology.JobRequestedSubject(), worker.HandleJobRequested); err != nil {
		fatal("failed to subscribe job fan-out worker", err)
	}

	// reaperElector is a second, independent LeaderElector (a distinct key
	// from elector/schedulerLeaseKey below, sharing the same lockMgr:
	// internal/election's own doc comment guarantees two LeaderElectors
	// with different keys never contend with each other even against one
	// Manager). Exactly one replica's Reaper.Run ever sees isLeader true at
	// a time, bounding JobStore.ListStaleFanOuts's own query load to once
	// per interval across the whole deployment regardless of replica
	// count.
	reaperElector := election.NewLeaderElector(lockMgr, fanOutReaperLeaseKey,
		election.WithOnAcquired(func() {
			slog.Info("Acquired Fan-Out Reaper Lease", slog.String("key", fanOutReaperLeaseKey))
		}),
	)
	reaperElectorDone := make(chan struct{})
	go func() {
		defer close(reaperElectorDone)
		reaperElector.Run(ctx)
	}()

	// reaper is what actually triggers dispatch.JobStore.BeginFanOut's own
	// staleAfter reclaim: worker.go's own doc comment on
	// dispatch.DefaultFanOutLeaseTTL explains why a redelivered
	// job.requested alone never reaches it (JetStream's redelivery budget
	// dead-letters the message long before DefaultFanOutLeaseTTL elapses).
	// dispatch.DefaultFanOutLeaseTTL is passed explicitly here, the exact
	// same value worker above uses implicitly (it was built with no
	// WithFanOutLeaseTTL override): the two must agree for a job either one
	// considers stale to actually be the same job, so if worker above is
	// ever given an explicit override, this call must change to match.
	reaper := dispatch.NewReaper(jobStore, bus, dispatch.DefaultFanOutLeaseTTL)
	reaperDone := make(chan struct{})
	go func() {
		defer close(reaperDone)
		reaper.Run(ctx, reaperElector.IsLeader)
	}()

	// The template store, and the two narrow ports the Dispatcher takes off
	// it. One concrete value, two interfaces: launching reads a template and
	// records what a launch was configured with, and cannot administer one.
	// api.NewTemplateHandler below takes the store itself, because
	// administering templates is precisely what it is for.
	// launchCatalog is every definition this deployment can launch: the
	// runbook catalog always, the playbook catalog when PLAYBOOK_DIR is
	// set. It feeds two consumers that must agree: the template store
	// verifies a created template's definition resolves (through the same
	// compile path a dispatch will use, so a runbook that exists but does
	// not compile is refused at authoring time), and the Templates view's
	// RUNS picker offers exactly this set, so what can be chosen and what
	// can be saved are one list.
	kindCatalogs := map[string]launch.KindCatalog{
		launch.DefaultKind: launch.KindCatalogFuncs{
			ListFunc: runbooks.List,
			VerifyFunc: func(ctx context.Context, definition string) error {
				if _, err := runbooks.Get(ctx, definition); err != nil {
					if errors.Is(err, runbook.ErrNotFound) {
						return fmt.Errorf("%w: no runbook %q", launch.ErrDefinitionNotFound, definition)
					}
					return fmt.Errorf("resolving runbook %q: %w", definition, err)
				}
				return nil
			},
		},
	}
	if playbooks != nil {
		kindCatalogs["playbook"] = launch.KindCatalogFuncs{
			ListFunc: playbooks.List,
			VerifyFunc: func(ctx context.Context, definition string) error {
				if _, err := playbooks.Get(ctx, definition); err != nil {
					if errors.Is(err, playbook.ErrNotFound) {
						return fmt.Errorf("%w: no playbook %q", launch.ErrDefinitionNotFound, definition)
					}
					return fmt.Errorf("resolving playbook %q: %w", definition, err)
				}
				return nil
			},
		}
	}
	launchCatalog := launch.NewSourceCatalog(kindCatalogs)

	templateStore := launch.NewEntStore(client, launchCatalog)
	dispatcher := api.NewDispatcher(runbooks, jobStore, bus,
		api.WithTemplates(templateStore),
		api.WithLaunchConfigs(templateStore),
		// The REDACTING store, deliberately, not the resolver: a relaunch
		// needs to know whether a bound credential's type prompts at
		// launch, which is a question about the type rather than about any
		// value.
		api.WithCredentialReader(credentialStore))
	templates := api.NewTemplateHandler(templateStore, logger)
	// WithBindingTemplates is what lets a binding be refused at the moment
	// an operator makes it, rather than at the first launch afterwards,
	// when the template's execution path cannot honour the credential
	// type's injectors. The run-time backstop in internal/adapters/native
	// is what guarantees the rule regardless; this is what makes the
	// refusal actionable.
	credentials := api.NewCredentialHandler(credentialStore, render.New(),
		api.WithBindingTemplates(templateStore))
	streamer := api.NewLogStreamer(js)
	// The factory is the same one the repository hydrates stored rows
	// with, so a device created over the API is built by exactly the code
	// path that rebuilds one read back out of storage. A second factory
	// here would be a second answer to "which Go type is a linux_server".
	devices := api.NewDeviceHandler(repo, inventory.NewItemFactory(), logger)
	jobs := api.NewJobHandler(jobStore)
	catalog := api.NewRunbookHandler(runbooks, logger)

	// The two resources the web UI's navigation is built around: the
	// shareable device sets a runbook is dispatched against, and the
	// operator broadcast the dashboard renders.
	inventories := api.NewInventoryHandler(sets, logger)
	announcements := api.NewAnnouncementHandler(announce.NewEntStore(client), logger)

	// The access administration surface. Without it none of the four
	// entities the RBAC resolver reads could be created at all, which is
	// what left the tenancy axis unusable and the Inventories create form
	// unsubmittable (FAILURE_PATTERNS.md #101).
	// The activity stream, and the decorator that writes it.
	//
	// Wrapped here, once, rather than recorded from inside the handlers.
	// One value feeds both write surfaces -- the JSON API's handlers below
	// and the web UI's own writers, which reach the store directly and pass
	// through no handler at all -- so wrapping it at the single point they
	// share is what makes the audit trail cover both. Recording from
	// handlers would have covered one and left the other silent.
	activityStream := activity.NewEntStore(client)
	accessStore := access.NewAuditedStore(access.NewEntStore(client), activityStream, actorFromRequest, logger)
	accounts := api.NewAccessHandler(accessStore, logger)
	activityLog := api.NewActivityHandler(activityStream, logger)

	// chain is Phase 8's own Chain of Responsibility. It carries one rule
	// today, NewTokenScopeRule, the token-scope axis. The Team/RoleBinding
	// axis (NewScopeRule/auth.ScopeResolver) is deliberately not appended
	// here: it needs a ScopeTarget (which Group/Device/Organization the
	// request is against), and an HTTP route has none to give it until a
	// handler resolves one, which is Phase 14's own concern once it holds
	// a device.
	//
	// It is built once, as its own value, because two things consume it
	// and they must be the same evaluation. That is the single most
	// important line in this wiring, so it is stated plainly: admission
	// enforces, hateoas advertises, and because both run this identical
	// rule list, a link this API offers and a 403 it returns cannot
	// disagree. Building two chains here would reintroduce exactly the
	// drift a server-side permission table always develops.
	chain := auth.AdmissionChain{auth.NewTokenScopeRule(evaluator)}

	// admission wraps the chain with the Audit Trail entry PATTERNS.md
	// names: every enforcement decision, allow or deny, is recorded.
	admission := auth.Admission{
		Chain:    chain,
		Recorder: auth.NewSlogRecorder(logger),
	}

	// hateoas takes the bare chain, deliberately unrecorded. A link
	// computation probes every affordance a resource declares, so routing
	// it through admission would emit one audit line per candidate per
	// request and log every affordance a caller merely lacks as a denial
	// at Warn. That buries the real denials, the ones where somebody
	// actually attempted something, under speculative ones nobody
	// attempted. Nobody asked to delete a device by loading a page.
	hateoas, err := auth.NewAdmissionHATEOASGenerator(chain)
	if err != nil {
		fatal("failed to build HATEOAS generator", err)
	}

	// Routes are registered as a declarative table rather than a callback
	// that groups onto the returned mux. NewRouter mounts every entry
	// under api.APIVersionPrefix with auth, the rate limiter, and that
	// route's own RequireScope(admission, Scope) already applied, and
	// refuses to build at all if any entry omits its Scope or if Admission
	// is nil, so "every application route is versioned, authenticated,
	// throttled, and authorized" holds structurally: there is no way to
	// register a route here that skips any of the four.
	// Each Route pairs apispec's documented Method/Pattern/Scope/Rel with
	// this process's own real handler method value: the same data
	// tools/gendocs reads to emit the OpenAPI document and the generated
	// API reference page, so neither can ever describe a route this
	// server does not actually serve, or vice versa.
	//
	// apispec.Routes pairs the two sets by name and refuses to return a
	// route table if either side has an entry the other lacks, so this
	// process cannot start while advertising a route it does not mount.
	// The previous hand-written literal could not make that promise: a
	// new Endpoint that nobody added here 404'd silently.
	routes, err := apispec.Routes(map[string]http.HandlerFunc{
		apispec.ListJobs.Name:      jobs.List,
		apispec.GetJob.Name:        jobs.Get,
		apispec.StreamJobLogs.Name: streamer.StreamLogs,
		apispec.RelaunchJob.Name:   dispatcher.RelaunchJob,

		// Templates split across two handlers on purpose, along the same
		// line the scopes split on: administering one is the TemplateHandler
		// under template:read/template:write, running one is the Dispatcher
		// under runbook:execute.
		apispec.ListTemplates.Name:        templates.List,
		apispec.GetTemplate.Name:          templates.Get,
		apispec.CreateTemplate.Name:       templates.Create,
		apispec.UpdateTemplate.Name:       templates.Update,
		apispec.DeleteTemplate.Name:       templates.Delete,
		apispec.CopyTemplate.Name:         templates.Copy,
		apispec.LaunchTemplate.Name:       dispatcher.LaunchFromTemplate,
		apispec.ListTemplateConfigs.Name:  templates.ListConfigs,
		apispec.CreateTemplateConfig.Name: templates.CreateConfig,

		// The credential surface. Every one of these handlers holds
		// credentials (the credstore.Store projection) and never the
		// resolver, so none of them can return a plaintext secret; that is
		// enforced by internal/archtest rather than by this comment.
		apispec.ListCredentialTypes.Name:      credentials.ListCredentialTypes,
		apispec.GetCredentialType.Name:        credentials.GetCredentialType,
		apispec.CreateCredentialType.Name:     credentials.CreateCredentialType,
		apispec.UpdateCredentialType.Name:     credentials.UpdateCredentialType,
		apispec.DeleteCredentialType.Name:     credentials.DeleteCredentialType,
		apispec.TestCredentialType.Name:       credentials.TestCredentialType,
		apispec.ListCredentials.Name:          credentials.ListCredentials,
		apispec.GetCredential.Name:            credentials.GetCredential,
		apispec.CreateCredential.Name:         credentials.CreateCredential,
		apispec.UpdateCredential.Name:         credentials.UpdateCredential,
		apispec.DeleteCredentialEndpoint.Name: credentials.DeleteCredential,
		apispec.ListTemplateCredentials.Name:  credentials.ListTemplateCredentials,
		apispec.SetTemplateCredentials.Name:   credentials.SetTemplateCredentials,
		apispec.ListDevices.Name:              devices.List,
		apispec.CreateDevice.Name:             devices.Create,
		apispec.GetDevice.Name:                devices.Get,
		apispec.UpdateDevice.Name:             devices.Update,
		apispec.DeleteDevice.Name:             devices.Delete,
		apispec.ListRunbooks.Name:             catalog.List,
		apispec.GetRunbook.Name:               catalog.Get,

		apispec.ListInventories.Name: inventories.List,
		apispec.GetInventory.Name:    inventories.Get,
		apispec.CreateInventory.Name: inventories.Create,
		apispec.UpdateInventory.Name: inventories.Update,
		apispec.DeleteInventory.Name: inventories.Delete,

		apispec.ListAnnouncements.Name:  announcements.List,
		apispec.CreateAnnouncement.Name: announcements.Create,
		apispec.UpdateAnnouncement.Name: announcements.Update,
		apispec.DeleteAnnouncement.Name: announcements.Delete,

		apispec.ListOrganizations.Name:  accounts.ListOrganizations,
		apispec.GetOrganization.Name:    accounts.GetOrganization,
		apispec.CreateOrganization.Name: accounts.CreateOrganization,
		apispec.UpdateOrganization.Name: accounts.UpdateOrganization,
		apispec.DeleteOrganization.Name: accounts.DeleteOrganization,
		apispec.AttestOrganization.Name: accounts.AttestOrganization,

		apispec.ListTeams.Name:  accounts.ListTeams,
		apispec.GetTeam.Name:    accounts.GetTeam,
		apispec.CreateTeam.Name: accounts.CreateTeam,
		apispec.UpdateTeam.Name: accounts.UpdateTeam,
		apispec.DeleteTeam.Name: accounts.DeleteTeam,
		apispec.AttestTeam.Name: accounts.AttestTeam,

		apispec.ListUsers.Name:  accounts.ListUsers,
		apispec.GetUser.Name:    accounts.GetUser,
		apispec.CreateUser.Name: accounts.CreateUser,
		apispec.UpdateUser.Name: accounts.UpdateUser,
		apispec.DeleteUser.Name: accounts.DeleteUser,

		apispec.ListBindings.Name:  accounts.ListBindings,
		apispec.GetBinding.Name:    accounts.GetBinding,
		apispec.CreateBinding.Name: accounts.CreateBinding,
		apispec.UpdateBinding.Name: accounts.UpdateBinding,
		apispec.DeleteBinding.Name: accounts.DeleteBinding,

		apispec.ListActivity.Name:     activityLog.ListActivity,
		apispec.GetActivityEntry.Name: activityLog.GetActivityEntry,

		apispec.ListContacts.Name:  accounts.ListContacts,
		apispec.GetContact.Name:    accounts.GetContact,
		apispec.CreateContact.Name: accounts.CreateContact,
		apispec.UpdateContact.Name: accounts.UpdateContact,
		apispec.DeleteContact.Name: accounts.DeleteContact,
	})
	if err != nil {
		fatal("api route table does not match the declared endpoints", err)
	}

	// The web UI. Registering the view resources fails closed: a
	// controller that cannot build its own UI must not start and then
	// serve broken pages.
	if err := resources.RegisterAll(resources.Deps{
		Access:     accessStore,
		Activity:   activityStream,
		Inventory:  repo,
		Sets:       sets,
		Announce:   announce.NewEntStore(client),
		Factory:    inventory.NewItemFactory(),
		Jobs:       jobStore,
		Runbooks:   runbooks,
		Templates:  templateStore,
		Catalog:    launchCatalog,
		Dispatcher: dispatcher,
		// The redacted credential store, never the resolver: the UI's
		// credential views hold a projection with no field a plaintext
		// value could occupy, and internal/archtest fails the build if
		// this side of the system ever imports the package that can
		// decrypt one.
		Credentials: credentialStore,
		// The same render engine the store validates injector templates
		// with, so the Credential Types view's Test action and a real
		// dispatch cannot disagree about what a document produces.
		Render: render.New(),
	}); err != nil {
		fatal("failed to register UI views", err)
	}

	// __Host- cookies require Secure, Secure requires HTTPS, and a
	// developer on http://localhost has neither. The opt-out is named,
	// and it announces itself at startup rather than being discovered in
	// a header dump later.
	insecureCookies := os.Getenv("PLEIADES_UI_INSECURE_COOKIES") == "1"
	if insecureCookies {
		logger.Warn("PLEIADES_UI_INSECURE_COOKIES is set: the session cookie drops the __Host- prefix and the Secure attribute; never set this in a deployment anyone else can reach")
	}

	// The environment / classification banner. It is deployment
	// configuration and fails closed: an unknown level is a startup error
	// rather than a silently omitted marking, because an operator who
	// configured a classification banner and got none would believe a
	// marking was displayed when it was not.
	banner, err := view.ParseBanner(os.Getenv("PLEIADES_BANNER_LEVEL"), os.Getenv("PLEIADES_BANNER_TEXT"))
	if err != nil {
		fatal("invalid banner configuration", err)
	}

	sessions := session.NewEntStore(client)
	// One codec, shared by the UI subtree and the JSON API's cookie
	// credential source. Two codecs could disagree about the cookie's name,
	// and a session written under one name and read under another fails as
	// "not signed in" rather than as a configuration error.
	cookieCodec := session.CookieCodec{Insecure: insecureCookies}

	ui := uiweb.New(uiweb.Config{
		Prefix:    "/ui",
		Version:   serviceVersion,
		Banner:    banner,
		Sessions:  sessions,
		Cookie:    cookieCodec,
		Tokens:    evaluator,
		HATEOAS:   hateoas,
		Admission: admission,
		Logger:    logger,
	})

	// One replica sweeps expired sessions, behind the same election every
	// other cluster singleton here runs behind.
	sweepDone := make(chan struct{})
	go func() {
		defer close(sweepDone)
		session.SweepExpired(ctx, sessions, 10*time.Minute, reaperElector.IsLeader, func(n int) {
			logger.Info("swept expired sessions", slog.Int("count", n))
		})
	}()

	r, err := api.NewRouter(api.RouterConfig{
		Logger:      logger,
		Tracer:      tracerProvider.Tracer("github.com/Subject-Void-LLC/the-pleiades/internal/api"),
		Propagator:  tracerProvider.Propagator(),
		Registry:    metricsRegistry,
		Readiness:   readinessChecks(nc, client),
		RateLimiter: rateLimiter,
		// Two credential kinds, in this order. An Authorization header is
		// an unambiguous statement of intent; a cookie is ambient, so
		// cookie-first would let a stale session silently override a token
		// a caller deliberately supplied.
		//
		// The cookie source on the JSON API is the entire fix for the SSE
		// log stream. An EventSource cannot set headers at all, so with
		// Bearer as the only accepted credential that endpoint was
		// unreachable from any browser regardless of what the UI looked
		// like -- and this line, not the UI, is what closes it.
		Auth: api.IdentityMiddleware(nil,
			api.BearerSource{Validator: evaluator},
			session.CookieSource{Store: sessions, Cookie: cookieCodec},
		),
		Admission: admission,
		HATEOAS:   hateoas,
		Routes:    routes,
		UI:        ui.Routes(),
		UIPrefix:  "/ui",
	})
	if err != nil {
		fatal("failed to build router", err)
	}

	srv := &http.Server{
		Addr:    listenAddr,
		Handler: r,
		// ReadHeaderTimeout bounds how long a client can trickle in
		// request headers before the server gives up, which is what
		// prevents a Slowloris-style attack from exhausting the
		// connection pool with connections that never finish sending
		// their headers.
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("controller listening", slog.String("addr", listenAddr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fatal("server failed", err)
		}
	}()

	// Runs concurrently with, never before, the HTTP server above: an
	// adversarial review of this phase found that running this
	// synchronously before ListenAndServe (its first shape) left nothing
	// bound to listenAddr, including the liveness/readiness probe this
	// binary's own Helm chart configures, until every device finished
	// rotating. On a large fleet or under ordinary DB latency that
	// window can exceed a default Kubernetes liveness probe's failure
	// threshold, killing the pod before rotation -- or the server --
	// ever completes, and restarting into the same blocking rotation
	// again. A failure here is logged, not fatal: a partially rotated
	// fleet is a degraded-but-fully-functional state (every row not yet
	// rotated still decrypts correctly through envelopeSvc's previous-key
	// slot), not a reason to tear down an already-serving process.
	if rotateKeys {
		go func() {
			rotated, err := crypto.RotateDeviceProperties(ctx, client, envelopeSvc)
			if err != nil {
				slog.Error("key rotation failed", slog.String("error", err.Error()))
				return
			}
			slog.Info("rotated device properties encryption", slog.Int("rotated", rotated))
		}()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("shutting down")

	// Cancel immediately (not only via the deferred cancel() above, which
	// only fires when main returns) so elector.Run begins its own
	// graceful lease release concurrently with the HTTP drain below,
	// instead of waiting for the drain to finish first.
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", slog.String("error", err.Error()))
	}

	// Wait for both electors' own bounded release (internal/election's own
	// releaseTimeout) to finish before closing lockMgr: closing the
	// underlying NATS connection while either release call is still in
	// flight would make it fail. reaperDone carries no lockMgr dependency
	// of its own (Reaper.Run's only cleanup is its ticker, stopped via a
	// deferred call), but is waited on here too so no goroutine this
	// composition root started is still running, and possibly still
	// logging, after main returns.
	<-electorDone
	<-reaperElectorDone
	<-reaperDone
	if err := lockMgr.Close(); err != nil {
		slog.Error("lock manager close failed", slog.String("error", err.Error()))
	}

	if err := bus.Close(); err != nil {
		slog.Error("event bus drain failed", slog.String("error", err.Error()))
	}

	// Flushed last, on its own timeout: the spans describing this whole
	// shutdown sequence are only exported if the provider outlives
	// everything that emits them.
	telemetryCtx, telemetryCancel := context.WithTimeout(context.Background(), telemetry.ShutdownTimeout)
	defer telemetryCancel()
	if err := tracerProvider.Shutdown(telemetryCtx); err != nil {
		slog.Error("telemetry shutdown failed", slog.String("error", err.Error()))
	}
}
