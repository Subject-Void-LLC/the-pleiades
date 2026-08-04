# Lessons Learned

Architectural rules derived from hard experience. Append a numbered entry focusing on the rule, not the
story, per `.AGENTS/AGENTS.md`.

1. **A corrected scaffold does not mean corrected code.** `CODE_SCAFFOLD.md` had already been updated to
   the full `InventoryItem` contract by the time this session started, but the live `.go` files still
   implemented the old, smaller interface it warns against. Always check the actual source files, not just
   whether the spec documents agree with each other.

2. **A capability or interface vocabulary lives in exactly one package.** Two packages each declaring their
   own version of the same capability name is the exact collision namespacing exists to prevent, and it
   fails silently: both compile, neither is caught until an audit or a test tries to use the name and gets
   surprised by which definition won.

3. **Storage adapters own their own row-to-domain conversion; the factory never imports the storage
   package.** `internal/inventory/ent_repository.go` converts `*ent.Device` into a storage-agnostic
   `Record`; `ItemFactory.Build` only ever sees `Record`. This is what makes "pluggable state store" a
   property of the code instead of a name in a doc comment: a YAML-backed repository produces the same
   `Record` shape with zero knowledge that ent exists.

4. **Embed synchronization primitives by pointer, always.** A struct holding a `sync.Mutex` or
   `sync.RWMutex` must be constructed once and referenced by pointer from every embedder. `go vet`'s
   copylocks check catches a value-embedded violation, but only if you run vet after the change that
   introduced the copy, not just after the type was first defined.

5. **A Release Gate that says "drives a real execution" cannot honestly close before the executor and
   transport it depends on exist**, no matter how real the surrounding plumbing (parsing, validation, DAG
   construction) is. Checking the box because everything up to the actual verb works is exactly the
   false-green checkbox the roadmap's Gate 2 exists to catch.

6. **A narrow, single-purpose utility can be built ahead of the phase that "owns" the larger feature it
   feeds**, as long as it is scoped to exactly that utility. `engine.TopologicalOrder` (a Kahn's-algorithm
   ordering function) was built during Part 0 Phase W1/W2 so `pleiades run` could print a real plan before
   Phase W5's DAG executor exists; it does not evaluate CEL conditions or execute anything, which is the
   line that keeps it a reusable primitive for W5 rather than a shadow implementation of it.

7. **`gofmt -w` across an entire repository is unconditionally safe.** It is formatting only, carries zero
   behavioral risk, and `AGENTS.md` declares it mandatory with no exceptions. There is never a reason to
   scope a gofmt pass down to only the files a given session happened to touch.

8. **Test the exact invocation the spec documents, not the invocation that happens to work with a
   library's default behavior.** Go's stdlib `flag` package does not support flags after a positional
   argument; the project's own scaffolding example puts the positional argument first. A CLI's automated
   tests are only meaningful if they run the same argument order a real user, following the docs, would
   actually type.

9. **When a proposed terminology or field-naming decision touches a spec document, read the spec's
   existing usage before inventing a new convention.** `PLAN.md` Section 23 already specified a `type:`
   discriminator field for distinguishing native and Ansible artifacts in a shared repository (originally
   `type: auto-roboto` versus `type: ansible`, later reconciled to `type: native` versus `type: ansible`
   once the product itself was renamed, a value chosen specifically to be product-name-agnostic so it
   would not need to change again). The right move was to reuse that field name for the Walk-tier runbook
   format's own type marker, not invent a competing `schema:` key. Two different field names solving the
   identical problem is exactly the kind of drift a single spec document exists to prevent.

13. **A value tied to a product name will need to change again the next time the product is renamed; a
    value that names what the thing IS rather than what it is CALLED survives that rename for free.**
    `type: native` (chosen over `type: auto-roboto` or `type: pleiades`) is the concrete example: when
    "Auto-Roboto" was retired in favor of "The Pleiades" later in the same session, every prose reference
    and illustrative binary name needed updating, but the runbook `type` field needed no change at all.

10. **A rename is only as good as the unscoped grep that follows it.** Scoping a terminology rename to
    "files that import or reference the type/field being renamed" is the right way to execute the rename,
    but it will miss files that reused the same word independently, with no structural coupling (a
    comment, a test's example string, a log field name chosen by convention rather than by importing a
    shared constant). Always follow a scoped rename with one final, unscoped, whole-repository grep for the
    literal word, and treat every hit as something to individually justify or fix, not skip because it
    "wasn't in the file list."

11. **Four agents can safely write to the same working directory in parallel only if their file sets are
    genuinely disjoint by design, and each agent's own verification should be scoped to the files it owns,
    not the whole repository.** A repo-wide build run by one agent mid-flight can transiently observe
    another's half-written state; that is expected and not a bug, but it means the authoritative
    build/test/vet pass has to happen once, after all parallel work lands, run by whoever is doing
    integration.

12. **When Go's own visibility rules (`internal/`) block a promise the spec makes (third parties can add
    new concrete types without touching core), a package-layout refactor cannot satisfy that promise by
    itself.** Splitting device types into per-vendor packages under `internal/inventory/devices/` improves
    modularity, but genuine out-of-module extensibility requires the factory and its DTOs to live under
    `pkg/`, which is a separate, larger decision. Do not let a structural refactor quietly stand in for an
    architectural promise it does not actually deliver; say so explicitly instead.

13. **Two features that both sound like "migration" will be conflated until a document states the boundary
    in a table.** This project carried three: running an unconverted playbook as Ansible in a container
    (Section 23), importing an AWX server's control-plane objects (a former Phase 29), and converting
    Ansible source files into native runbooks (The Forge). Each had a different input and a different
    output, but prose descriptions of them read almost identically, and review time was repeatedly spent
    re-deriving which was which. Prose is where these blur. A table with an explicit Input column and
    Output column is what makes the difference legible, and it belongs in the doc a reader reaches first.

14. **Build the generator before the content it generates, and let the content be the generator's test.**
    A commitment to cover thirty six Ansible modules reads like a mandate to hand-write thirty six
    packages. Inverting it, so the scaffolding tool ships first and then generates the catalog, costs
    nothing in sequence and buys a real release gate: if generating our own catalog with our own scaffold
    is painful, the scaffold is wrong, and we learn that on our own code before an outside author does.
    A hand-written entry that the scaffold should have produced hides exactly the defect worth finding.

15. **A declared-but-unimplemented capability must fail loudly, and the linter must know about it.**
    Shipping a registry of names whose implementations do not exist yet is legitimate: it gives the
    linter and the IDE a complete surface to work against long before every implementation lands. It is
    only safe under three conditions, and all three are structural, not conventions. A stub returns an
    explicit error and never success, because a module that silently does nothing makes a failed run look
    like a successful one. The manifest carries an implementation status as data, not as tribal knowledge.
    A validation rule turns a call to an unimplemented name into a write-time error. Get those three and
    an incomplete catalog is safe, because the tooling tells the truth about what is real.

16. **A store with more than one writer cannot be an ownership ledger, and that decides whether a diff
    engine is possible at all.** Terraform can derive an action from a difference because its state is
    intent-shaped: it records what Terraform itself wrote, so exactly one thing can explain a mismatch.
    An inventory that is observation-shaped, and specifically an observation of a third party that other
    actors also write to, has no such property. "This differs from what I last wrote" stops meaning
    anything the moment more than one actor writes. Before proposing any converge, reconcile, or diff
    feature, count the writers of the record being diffed. More than one means the answer is a report,
    never an action.

17. **Unknown values are containable; unknown actions are contagious.** Terraform's plans work because
    an unresolved value can sit inside a known operation. An ordered task list with `register` and
    `when` produces something different in kind: a task that may not execute at all, which makes every
    later task's precondition unknown too. Any plan or preview feature over control flow, rather than
    data flow, has to answer this before it is designed, not after. A related consequence: a preview
    that mixes predictable and unpredictable steps has only two renderings for the unpredictable ones,
    "no change" (which makes a run that will definitely change something look inert) or "always
    changes" (which is unreviewable), and neither is acceptable.

18. **Verify a type is called before describing what it does.** This project's state model, per-field
    revisions with versions, lifecycle states, and source authority, is correct Go with zero production
    callers, no database columns to persist into, and a read-only repository interface with no write
    path at all. Reading the type definitions and describing the behavior they imply produced a
    confident architectural assessment that was wrong by roughly a factor of five. The check is cheap:
    grep for callers outside tests, and look for the column or the write method. Doing so turns "we
    already have a better state store than Terraform" into "we have a better state store *design*, and
    it has never been written to disk," which leads to an entirely different next step.

19. **`go build ./...` does not build tests, so it cannot tell you a port change broke an
    implementation.** Widening the `Repository` interface with two methods left the build entirely
    green while a mock in a different package no longer satisfied the interface. `go vet ./...`
    type-checks test files and caught it immediately. After any change to an exported interface, vet is
    the check that matters; build will happily tell you everything is fine.

20. **A test that has never been shown to fail has not been shown to test anything.** Both central
    assertions of the new write path were mutation tested before being trusted: reverting the version
    restore in `NewBase` must fail the round-trip test, and deleting the compare-and-swap predicate
    must fail the concurrency test. Both did. This costs about two minutes and is the only thing
    separating a real regression test from `FAILURE_PATTERNS.md` entry 5, a validation test that could
    never actually fail. Do it for any test guarding a subtle invariant, and note in the test comment
    which mutation it catches.

21. **A port with zero production callers can be proven substitutable by a test suite, but not proven
    "selected by wiring in the composition root" until something in the real call graph actually makes
    that selection.** Phase W4 built local adapters behind three ports. `inventory.Repository` already
    had a real caller (`cmd/pleiades/load.go`, which was calling `StaticYAMLPlugin` directly instead of
    going through the port at all), so rewiring that one call site to construct `NewFileRepository`
    closed its Release Gate for real, verified against the built binary's own end-to-end tests.
    `lock.Manager` and `event.Bus` have no production caller anywhere in this codebase yet, by design:
    nothing executes a runbook yet (that is Phase W5/W6), so nothing needs to lock a device or publish an
    event. A shared conformance test suite run against both the local adapter and a real, containerized
    NATS broker (`internal/lock/conformance_test.go`, `internal/event/conformance_test.go`) proves those
    two adapters behave identically to each other, which is genuine, real evidence, but it is evidence of
    substitutability, not evidence of composition-root selection, and the two claims are not the same
    thing. State which one a Release Gate actually has before marking it closed; a port with no caller
    yet cannot honestly claim the second kind of proof regardless of how good its test suite is.

22. **Build a graph-walking primitive against the general shape the type declares, not the special-case
    shape its one current producer happens to emit.** `synthesizeChain` only ever compiles a linked list
    (every node in-degree 1, out-degree 1), and `PATTERNS.md`'s own Composite entry already commits to
    the DAG type supporting real multiple-parent nodes later ("nodes can have multiple parents... the
    structure is explicitly checked for cycles rather than assumed acyclic-by-construction"). Phase W5's
    `LevelIterator` (`internal/engine/level_iterator.go`) and `Executor.Run` (`internal/engine/executor.go`)
    are written against `DAG.Adjacency` generically, grouping by in-degree rather than assuming one node
    per step, so a diamond (two parents converging on one child) already schedules correctly today even
    though nothing in this codebase can author one yet. The alternative, a walker hard-coded to "the next
    single node in the chain," would have worked identically today and silently produced the wrong
    schedule the day parallel task groups or `Register`-inferred edges (this file's own entry 17-adjacent
    territory, `HANDOFF_DOCUMENT.md`'s "highest-value change available" note) landed. Proving this for
    real, not just arguing it, cost one fuzz target (`FuzzLevelIterator`) that builds synthetic diamond
    and chain graphs directly rather than through the tree-walk builder that cannot produce them.

23. **A port's own doc comment can describe a payload shape ("a marshaled JSON string of the Event
    struct") that is easy to violate by accident once a second caller writes to it.** Writing
    `engine.Executor`'s first draft of event publishing, `bus.Publish` was called with a raw, un-enveloped
    JSON payload; every test still passed except the one asserting on a subscriber's decoded field, which
    silently received a zero-valued struct instead of an error, because `json.Unmarshal` happily decodes a
    payload missing an expected field into that field's zero value rather than failing. The fix was
    routing every publish through `event.WrapPayload` like the port's own doc comment already specifies.
    The lesson generalizes past this one call site: a decode-from-JSON boundary that tolerates missing
    fields will not fail loudly on a malformed envelope, so the only reliable proof a caller honors a
    port's payload contract is a test that decodes what a *real subscriber* would decode and asserts on a
    field's actual value, not merely that `Publish` returned `nil`.

24. **Retry belongs strictly before the point of no return, never after it.** `internal/transport/ssh`'s
    `dialWithRetry` retries only the dial phase (TCP connect plus SSH handshake); once a `Session.Run` has
    actually sent a command to the remote side, Phase W6 never retries it, because a network failure
    *during* that call leaves the command's real-world outcome unknown, and retrying an unknown-outcome
    side effect (delete a file twice, restart a service twice) is exactly what this codebase's Convergence
    principle forbids. The general rule: before writing a retry loop around any operation, name the exact
    line past which the operation has externally observable side effects, and never let the loop's retry
    boundary cross it. A retry loop that "wraps the whole function" is only safe if the whole function is
    provably idempotent; a raw remote command never is.

25. **A security-critical default (host key verification, credential exposure) must fail closed on every
    branch, not just the branch you tested.** `internal/transport/ssh/known_hosts.go`'s `hostKeyCallback`
    treats a missing `known_hosts` file, an unset `$HOME`, and a host absent from an existing file all as
    hard errors, and the ONLY bypass is one explicitly named, loudly documented field
    (`InsecureSkipHostKeyVerify`) that defaults false. The lesson is the enumeration discipline itself:
    listing every way the function could be reached (file missing, file present but empty, file present
    but unreadable, `$HOME` unset, explicit opt-out) and deciding each one deliberately, rather than
    handling the one path a happy-path test exercises and letting every other path fall through to
    whatever `golang.org/x/crypto/ssh` does by default (which, for a nil `HostKeyCallback`, is to reject
    the dial entirely, but relying on that as an accidental safety net rather than an explicit decision is
    exactly the kind of gap Phase 39's own audits were built to catch). Apply this same enumeration
    discipline to any new function whose failure mode is "silently less secure" rather than "loudly
    broken."

26. **When an entity needs a stable, opaque public identity but already has a heavily-relied-on internal
    primary key, add a second column rather than changing the primary key's type.** Phase 1's `DeviceID`
    could have been built by switching `Device`'s ent primary key itself from an auto-increment integer to
    a UUID string, which every ORM tutorial shows as "the" way to get an opaque ID. That path would have
    touched every generated edge/FK reference and every existing internal call site that reads `dev.ID` as
    an integer, for a blast radius the actual requirement (an opaque value distinct from the mutable
    `name`, safe to expose on the wire) never asked for. Adding `device_id` as an ordinary indexed, unique,
    immutable column instead, and leaving the integer primary key exactly alone as a pure storage-layer
    detail used only for edges, delivered the same external guarantee for a fraction of the diff and zero
    risk to code nobody was asked to touch. Before widening an identity's *type*, check whether the actual
    requirement can be met by widening its *columns* instead; the more radical change is not automatically
    the more correct one.

27. **When a framework's most-documented path for a feature assumes infrastructure your deployment target
    does not have, look for the simpler primitive the framework already generates before reaching for a
    heavier dependency.** ent's own versioned-migrations documentation leads with the `sql/
    versioned-migration` feature flag plus an Atlas "dev database" (a live, disposable MySQL/Postgres,
    typically Docker), which is a poor fit for a dependency-free embedded-SQLite CLI. The actually-needed
    primitive, diffing an empty database against the desired schema to capture the initial DDL, turned out
    to already exist in the *currently generated* code with no feature flag at all
    (`(*migrate.Schema).WriteTo`), because ent's "automatic migration" and "versioned migration" flows
    share more machinery than the tutorial's framing suggests. The lesson generalizes: when a library's
    headline workflow for a feature assumes infrastructure you do not want to add, read what the library
    already generates for you before adding a new dependency or a heavier workflow to work around the
    assumption; the piece you need may already be sitting in code you have not looked at yet.

28. **RULE 0 ("run the same config the platform runs") applies to the one test whose own stated purpose is
    proving a mechanism, not to every test that happens to touch that mechanism's byproduct.** This phase
    replaced ent's auto-migration with a hand-written versioned-migration runner, which raised the
    question of whether every `enttest.Open`-based test in the repository needed repointing to the new
    path. It does not: the overwhelming majority of those tests exist to prove repository or business
    logic (`Save` round-trips a property, a conformance suite behaves identically across adapters), and an
    auto-migrated schema that is structurally identical to the versioned-migrated one serves that claim
    exactly as well. Only `internal/ent/client_test.go`'s `TestGraphTraversal` needed repointing, because
    it is this phase's own literal Release Gate text ("spin up in-memory SQLite... successfully traverse
    the graph") and its job is specifically to prove the new mechanism produces a working schema. Repointing
    every test "to be safe" would have been a worse outcome than the risk it guards against: it would bury
    the one test whose real job is proving the mechanism among dozens of tests that do not need to care how
    the schema got there, making the actual Release Gate harder to find, not easier to trust.

29. **When a value's secrecy is only known at runtime, the safety check for whether it is even safe to
    substring-mask has to live at the exact call site that adds it to the mask set, not deferred to print
    time.** `internal/credential.Mask` has no minimum-length guard by design: its callers only ever pass
    known SSH credential fields, which are never accidentally short or common. The new secret_fields/
    secret_mask mechanism (`internal/engine/executor_secrets.go`) accepts an arbitrary runtime value,
    where nothing stops it from being a stringified bool or a one-digit exit code; blindly adding that to
    the mask set would scrub that substring out of every later message and printed line for the rest of
    the run, corrupting unrelated output, which is worse than not masking at all. By the time anything
    prints, there is no way to tell a genuine short secret from an unrelated short string that happens to
    match, so the type/length check (`secretMaskValue`: must be a string, must be at least
    `minMaskableSecretLength` bytes) has to run once, at the moment a value is proposed for the mask set,
    and reject there, loudly, without leaking the value itself into the rejection. The general rule: a
    masking mechanism whose input is discovered at runtime, not pre-registered, needs its own safety gate
    at the point of discovery; it cannot inherit the guarantees of a mechanism whose input was always
    known in advance.

30. **A new cross-cutting validation rule's blast radius is every existing test fixture with a permissive
    zero value, not just the tests written for the rule itself.** Adding `LifecycleRule` (plan-time) and
    the executor's matching runtime guard both meant every `inventorytest.Stub` in the whole repository
    suddenly mattered for its `StubState` field, not just the tests that already cared about lifecycle.
    `Stub`'s zero value is `StateDiscovered` (`LifecycleState`'s own `iota` order, not `StateActive`), a
    reasonable default for a stub whose test never mentions lifecycle at all, but it meant a dozen existing
    tests across `internal/validate` and `internal/engine`, none about lifecycle, started failing the
    moment either rule went live, because their devices were now, by the new rule's own correct logic,
    not eligible for real work. None of those tests were wrong; the new rule was newly, correctly, looking
    at a field they had never needed to set. The general rule: before landing a validation or guard that
    inspects a field on a shared test double, grep every construction of that double for whether the field
    is set explicitly, not just whether the double's own package still compiles; a shared test double's
    zero value is a promise to every existing caller, and a new rule that starts reading a previously-
    ignored field breaks that promise for all of them at once, not just the caller who added the field.

31. **A default idempotency/dedup key must be derived from operation identity, not operation content.**
    `internal/event`'s `DefaultIdempotencyKeyDerivation` first hashed a publish's topic plus its domain
    payload, on the reasoning that the envelope's own `ID` field is "freshly minted every call" and
    therefore unsafe to key determinism on. That reasoning conflated two different things: an `ID` is
    minted once, at the moment the caller builds the value (`WrapPayload`), and stays fixed on it for the
    rest of its life, including across a caller-side retry that resends the same already-built value --
    the actual, concrete shape "a retry of the same logical operation" takes in this codebase. Content is
    the wrong axis entirely: two genuinely different operations (two independent health-check pings a
    minute apart) can easily share identical content, and keying on content makes the second one look like
    a duplicate of the first and vanish. The general rule: when a value needs a default identity for
    dedup/idempotency purposes and the caller hasn't supplied one explicitly, key on something that is
    fixed once per logical operation and would differ between any two operations regardless of their
    content (a minted ID, a sequence number), never on the operation's own payload bytes, however tempting
    "deterministic" sounds as a reason to hash content instead. A real NATS container test caught this
    directly (`FAILURE_PATTERNS.md` #29), not a mock -- a hand-rolled fake dedup store would have needed
    the exact same wrong assumption baked into it to even notice.

32. **An ephemeral JetStream pull consumer with no pull request against it is cleaned up by the server
    after `InactiveThreshold` (default 5s), and a test that creates one long before its first `Fetch`
    call will silently read from a consumer that no longer exists.** Several of this session's own
    container tests for `HandleDeliveryFailure`'s dead-letter path created a diagnostic consumer on the
    dead-letter subject at test setup, then spent 10+ seconds waiting through a real redelivery-and-backoff
    cycle before ever calling `Fetch` on it -- long enough for the server to have already garbage-collected
    the idle consumer. The resulting `Fetch` call did not error (a Go client issuing a pull request against
    a consumer the server no longer recognizes does not necessarily surface as a hard error); it just
    returned an empty result, which looked identical to "the dead letter genuinely never arrived" until a
    minimal, tighter repro (create the consumer and `Fetch` back to back, no gap) proved the underlying
    mechanism was correct all along. The general rule: an ephemeral pull consumer used for test assertions
    must be created immediately before the `Fetch` that reads it, not at test setup, whenever anything
    between setup and the read could plausibly exceed a few seconds of real wall-clock time; a durable
    consumer does not have this problem, but reaching for one just to sidestep this would be the wrong fix
    for a diagnostic, one-shot read.

33. **Reusing a fixed message ID across repeated `Publish` calls in a loop (a benchmark, a retry helper)
    silently engages producer-side dedup, and for anything that also waits on a resulting delivery, this
    doesn't just understate a number -- it hangs outright.** `BenchmarkNatsBusPublishSubscribeRoundTrip`'s
    first draft built one `Event` outside its `for i := 0; i < b.N; i++` loop and published the identical
    value every iteration; since the dedup key is the event's own `ID` (see #31), every publish after the
    first was treated as a duplicate of the first and never actually delivered, so the round trip's own
    `<-done` receive blocked forever on the second iteration. `BenchmarkNatsBusPublish` (no delivery to wait
    on) had the identical bug but surfaced only as an artificially fast, non-representative number, not a
    hang -- worth noticing specifically because the *lack* of an obvious failure is what would have let it
    ship unnoticed. The general rule: any loop that calls `Publish` (or an equivalent produce-once
    semantics call) repeatedly for benchmarking or testing purposes needs a fresh identity per iteration,
    the same way a real caller's own distinct operations would each have one; reusing one value across
    iterations is convenient exactly because it's wrong -- it looks like "the same kind of thing happening
    many times" when the dedup layer is specifically designed to collapse "the same thing happening more
    than once" into one.

34. **Reordering a handler's writes to fix a correctness bug can turn a background goroutine's existing,
    previously-benign concurrency into an active race, and only `-race` on the specific reordered path
    will catch it.** `internal/api/logs.go`'s `StreamLogs` originally wrote its own "connected" line before
    starting the JetStream `Consume` call that hands message delivery off to a background goroutine; moving
    `Consume` earlier (to let its own errors still report as an HTTP status, `FAILURE_PATTERNS.md` #32) put
    that background goroutine's first possible write *before* the handler's own subsequent write to the
    same `http.ResponseWriter`, which is not safe for concurrent use. The existing test suite, run without
    `-race`, stayed green throughout both the bug and the fix; only a `-race` run on the file the reorder
    touched surfaced the problem, and by construction a race like this cannot be caught by asserting on
    output content, since Go's race detector flags the unsynchronized access itself, not any particular
    corrupted result it might occasionally produce. The general rule: any change that moves a call which
    hands off work to a new goroutine earlier relative to a caller's own remaining code needs its own
    `-race` run specifically exercising that reordered path before being trusted, not just the pre-existing
    test suite passing under plain `go test`.

35. **Wrapping every publish's payload in a shared envelope is a wire-format change binding on every
    consumer of the raw bytes, including ones that deliberately bypass the port whose own signature
    changed.** Once `internal/api/dispatcher.go` started publishing through `event.Bus.Publish` (which
    wraps a domain payload in the `Event` envelope before it reaches the wire) instead of a raw
    `jetstream.JetStream.PublishMsg` call, `runner.Agent.handleMessage` -- which by deliberate design stays
    on its own pull-based `jetstream.Consumer` rather than going through `Bus.Subscribe`
    (`PATTERNS.md`'s own "Push vs Pull Execution Model" entry) -- silently started decoding the wrong
    layer, since it still expected the old, un-enveloped bytes (`FAILURE_PATTERNS.md` #30). `Agent` is not
    a caller of `Bus.Subscribe` at all, so "grep every caller of the port that changed" would not have
    found it; it shares the wire format the port produces without sharing the port's own interface. The
    general rule: before changing what a shared serialization boundary wraps a payload in, enumerate every
    consumer of the *wire format* (including deliberate, by-design bypasses of whatever port normally
    produces it), not just every caller of the specific function whose signature changed -- and prefer a
    real, cross-process integration test over a narrower unit test for catching this class of bug, since a
    unit test with a hand-built payload will happily encode the same wrong assumption the production bug

36. **A wrapper library's public method signature is a promise about what it exposes, not about what the
    underlying protocol actually supports -- read the library's own source for the lower-level primitive
    before assuming the wrapper cannot do what you need.** Designing Phase 3's real per-key lock TTL, the
    first read of `nats.go` v1.52.0's public `jetstream.KeyValue` interface suggested `Update` could not
    refresh a key's TTL at all (its signature takes no options and its implementation always publishes with
    `ttl=0`), which looked like a hard blocker for a `KeepAlive`-driven Heartbeat renewal. Reading the
    package's own unexported `updateRevision` (what `Create` and `Update` both call internally) showed the
    real primitive was reachable anyway: the exported `jetstream.JetStream.PublishMsg`, plus the exported
    `jetstream.WithExpectLastSequencePerSubject` and `jetstream.WithMsgTTL` options, against the KV bucket's
    own documented subject convention (`$KV.<bucket>.<key>`). Confirmed empirically, not by reading source
    alone: a real `nats:2.11` container proved this genuinely slides the TTL forward and that a stale-CAS
    renewal fails with an error `errors.Is`-matching the same `jetstream.ErrKeyExists` `Create` already
    handles, so the existing contention translation reused cleanly. The general rule: when a wrapper's own
    public surface seems to foreclose something the underlying protocol should support, check whether the
    wrapper's *other* exported functions can reach the same primitive by a different path before concluding
    the capability does not exist -- and prove the workaround actually works against the real system, not
    just that it compiles.

37. **A third-party server's version is real, load-bearing API surface for a feature the client library
    merely wraps -- verify the minimum version empirically against a real instance before designing around
    a capability, rather than assuming whatever version existing tests happen to pin already supports it.**
    Every container test in this repository pinned `nats:2.10` before this phase; Phase 3's real per-key TTL
    design (`jetstream.KeyValueConfig.LimitMarkerTTL`, required for `jetstream.KeyTTL` to have any effect at
    all) genuinely requires `nats-server` 2.11 or newer, confirmed by running both versions against the same
    bucket-creation call in a disposable scratch harness before writing any package code: 2.10 rejected it
    outright (`"limit marker TTLs not supported by server"`); 2.11 accepted it and the TTL itself behaved
    exactly as documented. The version bump was scoped to only the files that actually construct a
    `natsLockManager` against a real container (`internal/lock`'s own two, plus `internal/engine`'s
    scheduler tests, found by grepping for the real constructor call, not by guessing which files might be
    affected) rather than bumped repository-wide, since no other package's tests exercise this specific
    capability. The general rule: a version pin sitting unremarked in existing test files is not evidence
    that version is sufficient for a new capability being designed on top of the same dependency; check it
    against a real instance of the actual version in use, and scope any required bump to the files whose own
    tests actually need the new capability, found by tracing real call sites rather than assumed.

38. **In a CAS-based system, losing a compare-and-swap race is not by itself evidence of real contention --
    only the caller can know whether the operation it lost against was compatible or conflicting, and
    conflating the two silently breaks whichever operation is more common under load.** `natsLockManager`'s
    shared-lock join path lost most of a burst of simultaneous, mutually-compatible `ModeShared` joins to
    exactly this conflation (`FAILURE_PATTERNS.md` #34): every CAS loss, whether against a genuinely
    conflicting exclusive holder or against another equally-valid shared joiner, surfaced as the identical
    `ErrLockHeld`, and `PolicyReject`'s single-attempt contract then correctly honored a signal that was
    wrong for the shared case. The fix moved the distinction to the one place that can actually make it:
    inside the CAS retry itself, which already knows whether the just-read state was mode-compatible before
    the CAS was even attempted. The general rule: a CAS failure means "the state changed since I read it,"
    nothing more; whether that change makes the operation impossible or merely means "read again and retry"
    is a property of what the two racing operations *mean*, not of the CAS mechanism, and that judgment
    belongs at the layer with enough context to make it, not pushed onto every caller as generic contention.

39. **When a spec's contention policy asks for revoking a live, in-use resource and the underlying mutual-
    exclusion guarantee has no way to detect the holder is even still doing something with it, narrow the
    feature to only ever act on what is provably abandoned, and say so explicitly, rather than build the
    literal ask and create a new correctness hazard.** `PLAN.md` Section 13's "priority" contention policy
    describes a higher-priority caller preempting a lower-priority one outright. `engine.Executor.runOne`
    acquires a device lock for the duration of one task's action but never calls `KeepAlive` during it (ttl
    alone bounds the hold); an independent design review (a Plan-agent asked to pressure-test the initial
    "eagerly steal on higher priority" draft, not a test) found that revoking such a lock mid-action would
    let two callers physically execute against the same device concurrently -- worse than the pre-existing,
    already-accepted, already-named risk of an action simply outliving its own ttl. The design was narrowed
    before any code was written: priority is only ever allowed to reclaim an entry whose own declared
    deadline has *already* elapsed (an eager alternative to waiting for the store's own lazy sweep, never a
    revocation of a live hold), and the mechanism ultimately shipped is narrower still -- a client-side,
    priority-weighted retry cadence among waiting contenders, since real per-key TTL made the original
    "eager steal of an expired entry" idea degenerate into something every policy already gets for free once
    the entry is provably gone. The general rule: when a feature's literal spec wording implies revoking
    something whose true liveness this system cannot observe, build the version that only acts on what is
    provably safe, and record the narrower, real contract as a deliberate scope decision -- not a silent
    downgrade -- so a later phase adding real cancellation of in-flight work has a named, honest seam to
    widen instead of a comment that quietly claims more than the code does.
    depends on.

40. **A `select` between a cancellation case and a periodic-work case must treat "canceled the instant a
    tick also fires" and "canceled while the tick's own dispatched call was already in flight" as the
    same expected outcome, not two different code paths that happen to converge by luck.** Go's `select`
    does not prioritize among simultaneously ready cases, so a `ctx.Done()` case and a `ticker.C` case
    becoming ready together can still resolve to the `ticker.C` branch; separately, a call already
    dispatched from an earlier, legitimate tick can still be in flight when cancellation lands
    concurrently, returning `context.Canceled` indistinguishable, at the call site, from a genuine
    failure. `election.LeaderElector.Run` hit both during this phase's own real-container test
    (`FAILURE_PATTERNS.md` #39), logging a `renewal failed` warning on every ordinary graceful shutdown.
    The general rule: any loop selecting between a context's own done-ness and periodic work must check
    `ctx.Err()` at both entry points a race can occur -- before dispatching the next call, and again after
    an error comes back -- and route both to the exact same "this is shutdown, not a failure" handling,
    rather than trusting `select`'s case order or a single check at the top of the loop to cover it.

41. **A benchmark that provisions its own external test infrastructure (a container, a subprocess) inside
    the benchmarked function must register its teardown via `b.Cleanup`, never a naked `defer`.**
    `go test -bench` measures elapsed wall time from `b.ResetTimer()` to the benchmark function's own
    return; a `defer`red call runs during that same return, before the function is considered finished,
    so its cost is silently divided by `b.N` and added to every reported op. `BenchmarkLeaderElectorKeepAlive`
    hit this for real (`FAILURE_PATTERNS.md` #40): several seconds of real container teardown inflated a
    genuine ~0.7ms call to a reported 100ms-2s. `b.Cleanup`-registered functions run strictly after the
    benchmark's own measurement completes, closing this for good rather than requiring every benchmark
    author to remember an explicit `b.StopTimer()` before every `return` path. `internal/lock/nats_bench_test.go`'s
    own `startBenchNats` helper already used the correct pattern; any new benchmark that starts its own
    container or process should copy that helper's shape, not the shape of an ordinary test's setup.

42. **`go test` caches a PASS result per package, build flags, and test binary hash, regardless of whether
    the test itself has real external side effects (starting a container, killing a real process), and
    silently prints `(cached)` instead of re-executing on a later, identical invocation.** Re-running the
    same `go test` command several times in a row to gather multiple independent real measurements (this
    phase's own Release Gate needed several real `SIGKILL`-to-failover timings, per RULE 0) returns the
    exact same cached result every time once the first run has passed and nothing in the package changed,
    which looks identical to genuine repeated runs unless the `(cached)` marker in `go test`'s own summary
    line is specifically checked for. Caught only because five apparently-independent measurements came
    back bit-for-bit identical to the microsecond, an implausible outcome for anything involving real
    process scheduling and container networking. `-count=1` (or any other flag/input change) forces a
    fresh execution every time; any workflow that needs N independent real samples from the same test must
    pass it explicitly, not just re-invoke the same command N times.

43. **Adding a new required env var to a composition root's fail-closed startup check is a breaking change
    to every existing test that spawns that binary as a real OS subprocess, not just to hand-run
    invocations.** `cmd/controller/main.go`'s new `MASTER_ENCRYPTION_KEY` requirement (Phase 5) silently
    broke Phase 4's own `TestControllerLeaderElection_ReleaseGate`, which builds and runs the real
    `controller` binary in three subprocesses: none of them could start at all once the new check landed,
    so the test failed with a symptom (no leader ever elected) that looked unrelated to its actual cause. A
    package's own test suite (`internal/crypto`'s, in this case) cannot catch this: the break is entirely
    in a different package's subprocess-spawning test, only visible to a full `go test ./...` run. Before
    adding a new required startup env var to any binary this repository already spawns as a subprocess in a
    test (`cmd/controller`, `cmd/pleiades`'s SSH release gate), grep that binary's own test files for
    `exec.Command`/`cmd.Env` and update every spawn site in the same change, then confirm with a full
    repo-wide test run, not just the package the env var was added for.

44. **A "did we already process this" failsafe keyed on a single marker field's presence is bypassable by
    anything that controls that field's name.** `encryptPropertiesMap`'s original "already encrypted"
    check asked only "does this map contain a key named `_encrypted`", not "does this map have exactly the
    shape I myself produce." Any caller-controlled data sharing a schema with the marker's own namespace
    (a generic `map[string]interface{}` property bag, here) can collide with a marker name chosen for
    convenience rather than collision-resistance, and turn a safety check into a bypass. When a failsafe
    exists to distinguish "already in the terminal state I produce" from "ordinary input," check the full
    shape that state actually has (field count, value type), not just whether one expected field is
    present -- and do this asymmetrically if the read and write sides have different failure-mode
    requirements: strict on write, where being lenient creates a security hole; lenient on read, where
    being strict trades a loud, correct error for a silent, wrong pass-through.

45. **An interceptor sitting in front of a generated ORM's batch query method must not fail the whole batch
    for one bad row, and single-result methods built on top of a batch query inherit that same exposure.**
    `ent`'s generated `Only`/`Get` are themselves `Limit(2).All(ctx)` plus a length check performed AFTER
    the query (and any interceptor) runs; there is no ent-level way to give a "single result expected"
    caller different treatment from a genuine batch caller, because the interceptor never sees that
    distinction. Before assuming a generated query builder's `Get`/`Only`/`First` gives an interceptor a
    different result shape than `All` does, read the actual generated code (`*_query.go`'s builder methods,
    not just the client-facing docs) rather than assuming from the method names alone.

46. **A maintenance/rotation write that bypasses a resource's normal optimistic-concurrency write path must
    still honor that same compare-and-swap, or it becomes an unconditional last-writer-wins that silently
    discards concurrent legitimate changes.** `RotateDeviceProperties`'s original `UpdateOneID(...).
    Save(ctx)` had no conditional on the row's `version` column, unlike the domain's own
    `entRepository.Save`; a legitimate concurrent write landing between its read and write was silently
    overwritten, and the stored version counter was left describing content that was no longer what got
    persisted. A background/maintenance operation touching the same storage a domain write path also
    touches needs the identical CAS discipline that domain path already earned through its own tests, even
    when the maintenance operation's own semantic content (re-wrapping ciphertext) makes it tempting to
    treat as "not really" a concurrent-write hazard.

47. **`O_CREATE|O_EXCL` alone does not make a file's *content* atomic, only its *existence*.** A file
    becoming visible to a concurrent reader and a file being fully written are two different moments; an
    exclusive create closes the "who gets to be the writer" race but leaves the file observably empty (or
    partially written) between the create and the write completing. The correct pattern for content that
    must never be observed partially written is: write the full content to a temp file in the same
    directory, close it, then commit it into its final name atomically (`os.Link`, which fails on an
    existing destination so a loser can detect and defer, not `os.Rename`, which silently replaces one).
    This was caught by a test written specifically to probe the race
    (`TestResolveKey_ConcurrentGenerationConverges`, 16 concurrent goroutines under `-race`), not by
    inspection: the bug was invisible reading the O_EXCL version's code in isolation, since exclusivity
    genuinely does prevent two callers from both "winning" the create -- it just doesn't prevent one from
    reading the other's in-progress state before the write finishes.

48. **A composition root gaining new one-time startup work (a migration, a rotation pass, a cache warm) must
    run it concurrently with, never strictly before, whatever makes the process's health probes pass.** A
    synchronous call added directly in `main()` before `ListenAndServe` seems like the obviously-correct,
    simple place to put "do this once at startup" work, but it silently couples that work's completion time
    to the process's liveness, which a container orchestrator is independently watching on its own clock. A
    Kubernetes liveness probe does not know or care that a controller is "doing important setup work"; it
    only knows nothing has answered its `httpGet` yet, and will kill and restart the container into the
    exact same blocking work, indefinitely. One-time startup work with an unbounded or fleet-scaled runtime
    belongs in its own goroutine started once the server is already listening, not inline before it.

49. **A function walking every prefix of a caller-controlled sequence must build its accumulated key
    incrementally, never rejoin the whole prefix from scratch on each step, once that sequence can be
    externally supplied.** `internal/classification.Classify`'s first draft rebuilt its dotted lookup key
    via `strings.Join(path[:i], ".")` inside a loop over every prefix of `path`, an obviously-correct,
    simple way to write the loop that is O(n^2) in `len(path)`. That cost was invisible against the
    hand-written 3-4 level paths every existing test and the default rule set actually used, and it stayed
    invisible in review until an adversarial pass benchmarked the real function directly against a long
    input rather than reasoning about the loop shape in the abstract. Once a sequence's length is
    attacker- or user-controlled (a YAML list a person can hand-edit, a CLI flag split on commas), an
    O(n^2) prefix-rebuild is a real, cheap resource-exhaustion vector, not a theoretical one: a few hundred
    KB of input reproduced a multi-second-to-minutes hang. The fix is mechanical once spotted (a
    `strings.Builder` appended to once per step, since its `String()` method returns accumulated bytes
    without copying) but the lesson is in when to reach for it: any loop that reconstructs a growing prefix
    of caller-supplied data on every iteration is a candidate, and a bound on the caller-supplied length
    (this package's new `maxPathSegments`) is a cheap, independent second line of defense that survives
    even if a future change to the algorithm reintroduces worse-than-linear cost.

50. **An adversarial review's suggested code fix must itself be checked against the invariant it's meant to
    protect before applying it, especially when the review is really pointing at a doc/code mismatch rather
    than a functional bug.** The same review that found Lesson 49 also flagged `pkg/policy.IntersectSlices`'s
    own doc comment ("a nil or empty acc means no constraint yet") as inconsistent with the code, which only
    special-cased `nil`. The tempting fix (special-case `len(acc) == 0` instead of `acc == nil`) would have
    been wrong: `IntersectSlices` is called repeatedly inside a fold, once per layer, with each call's
    result becoming the next call's `acc`, so a genuinely-narrowed-to-empty result midway through the fold
    must stay sticky, or a later, disjoint layer can revive a value an earlier layer already excluded,
    which is exactly the non-monotonic-widening bug this function exists to prevent. Tracing the fold
    sequence by hand (and then writing
    `TestIntersectSlices_EmptyResultStaysStickyAcrossFold` to pin it) showed the code's `nil`-only check
    was the correct behavior and the doc comment's broader "nil or empty" claim was the actual defect.
    Treat "the code and its own doc comment disagree" as two candidate fixes, not one: fix whichever side
    is actually wrong, verified against the real invariant, not whichever side is easier to edit.
