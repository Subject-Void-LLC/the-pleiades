# Failure Patterns

A catalogue of bugs found while working this codebase. Append an entry (Symptom, Root cause, Fix,
Lesson) before debugging anything new, per `.AGENTS/AGENTS.md`.

## 1. Capability name collision between two packages

**Symptom:** `pkg/capability` and `internal/inventory` each defined a `CiscoIOSCapable` interface with a
different method set (`RunCommand`/`BackupConfig` versus `IOSVersion`/`SupportsNETCONF`), and neither was
implemented by any concrete type.

**Root cause:** the capability vocabulary was declared independently in two packages instead of owned by
one. Nothing forced them to agree.

**Fix:** consolidated into `pkg/capability` as the single blessed vocabulary, with a registry (`Register`,
`Lookup`, `Implements`) that panics on a duplicate name and binds each name to a real structural check
(`Descriptor.Assert`).

**Lesson:** a capability or interface vocabulary belongs in exactly one package. A second package defining
its own version of the same name is the collision namespacing exists to prevent, not a variation.

## 2. Unchecked property map assertion panicking the dispatcher

**Symptom:** `internal/api/dispatcher.go` panicked on `device.Properties()["ip"].(string)` for any device
lacking an `ip` property.

**Root cause:** `Properties()` returned a raw `map[string]interface{}`, so every caller had to perform its
own unchecked type assertion at the point of use.

**Fix:** `pkg/inventory.Properties` is a narrow typed accessor (`String`, `Int`, `Bool`) that returns
`(value, ok)` instead of panicking.

**Lesson:** any property bag that crosses a dispatch boundary needs a checked accessor defined once, not
scattered unchecked assertions at every call site.

## 3. go vet copylocks warning from embedding a mutex by value

**Symptom:** `go vet` flagged "literal copies lock value from base" when constructing
`CiscoRouter{baseDevice: base}`.

**Root cause:** `baseDevice` holds a `sync.RWMutex` and was embedded by value; the constructor's return
value got copied into the outer struct literal.

**Fix:** `newBaseDevice` returns `*baseDevice`; `CiscoRouter`/`LinuxServer` embed the pointer, not the
value.

**Lesson:** any type holding a mutex or other non-copyable synchronization primitive must be constructed
once and embedded by pointer everywhere. Check `go vet` after any refactor that changes how a struct
holding one gets constructed, not just after the type is first defined.

## 4. stdlib `flag` rejects the exact CLI usage the spec documents

**Symptom:** `pleiades add-host webserver1 --type linux_server` (flags after the positional name) failed
with a usage error, even though `PLAN.md` Section 7's own scaffolding example
(`pleiades add-host webserver1 --type linux`, named `auto-roboto` at the time this bug was found; the
product was renamed later the same session) uses exactly this ordering.

**Root cause:** `flag.FlagSet.Parse` stops parsing flags at the first non-flag token; it does not support
flags interspersed after a positional argument the way most users expect.

**Fix:** `cmd/pleiades/addhost.go`'s `splitPositional` extracts the one positional argument wherever it
appears in `args` before handing the remainder to `flag.Parse`.

**Lesson:** never assume stdlib `flag` matches conventional CLI argument ordering. Manually run the exact
invocation the spec documents before trusting that flag parsing works the way it looks like it should.

## 5. A validation test that could never actually fail

**Symptom:** while drafting the W3 capability-validation test, targeting an `ssh_exec` action at a
`cisco_router` to prove rejection did nothing: both built-in device types (`CiscoRouter`, `LinuxServer`)
declare `SSHTransportCapable`, so there is no built-in device type that fails an SSH-capability check.

**Root cause:** assumed device-type diversity without checking which capabilities the two concrete types
actually declare.

**Fix:** added a second Walk-tier action, `ios_backup` -> `CiscoIOSCapable`, which only `CiscoRouter`
declares, and manually ran the failing case through the built binary before writing the automated test.

**Lesson:** manually exercise a new validation or CLI path with a real failing case before writing the
test that is supposed to prove it fails. A test that always passes because its fixture cannot trigger the
failure path is worse than no test (RULE 0).

## 6. Pre-existing data race in the log-streaming test package

**Symptom:** `go test -race ./internal/api/...` fails in `TestStreamLogs_ReleaseGate`
(`internal/api/logs.go` / `logs_test.go`), unrelated to any change made this session.

**Root cause:** not investigated. `logs.go`/`logs_test.go` are untracked, in-flight Phase 14 work this
session did not touch (see `HANDOFF_DOCUMENT.md`).

**Lesson:** recorded here so whoever picks up Phase 14's log streaming work knows the race exists, rather
than discovering it fresh. Also a reminder that `-race` failures in a package fail the whole package's
race run, so an unrelated pre-existing race can mask or gets conflated with new code under test unless you
isolate with `-run`.

**Update (Phase 2):** investigated and closed. This session rewired `StreamLogs` for an unrelated reason
(per-request consumer scoping, entry #31) and, while fixing a second, separately-discovered bug (entry
#32, the response committing to 200 before `Consume` could still fail), introduced and then closed a real
data race between the handler and its own delivery callback both writing to `w` unsynchronized (entry
#33). `go test -race ./internal/api/...` and the full repository's `go test ./... -race` are both clean as
of this session; whether #33's race is the same one this entry originally found, or this entry's original
race was already resolved by an intervening rewrite and #33 was a fresh one this session's own reordering
introduced, was not distinguishable in hindsight -- the file has been rewritten multiple times since this
entry was first recorded. Recorded as closed on the strength of the current, real, clean `-race` run, not
on tracing the exact lineage of the original report.

## 7. A parallel terminology rename missed a same-meaning string outside the renamed files

**Symptom:** after a parallel agent renamed "playbook" to "runbook" across `internal/api`, `internal/runner`,
`internal/adapters/native`, and `cmd/pleiades`, an independent audit found `internal/auth/evaluator.go`'s
doc comment and `internal/auth/jwt_test.go`'s scope-string test still said `"playbook:execute"`, while
`internal/api/dispatcher.go` (which the rename agent did touch) now checked `"runbook:execute"`.

**Root cause:** the rename was scoped by an explicit file list built from a targeted grep of the packages
believed to reference the term. `internal/auth` was not on that list because it does not import or
reference `DispatchPayload`; it only contains an illustrative comment and a test using the same string
convention independently, so a file-scoped grep for the type/field names being renamed did not surface it.

**Fix:** grepped the whole repository for the bare string "playbook" (not just the identifiers being
renamed) after the scoped rename landed, and fixed the two remaining occurrences by hand.

**Lesson:** after any terminology rename, do a final unscoped, whole-repo grep for the literal word being
renamed, not just the specific identifiers or files a targeted search turned up. A rename scoped to "files
that reference type X" misses files that independently reused the same vocabulary in a comment, a log
string, or a test fixture without any structural coupling to X.

## 8. Parallel agents' own build/test runs can observe a mid-flight, inconsistent repo state

**Symptom:** the workflow agent implementing the `type` kvp reported that a repo-wide `go build ./...` it
ran mid-task failed with `undefined: Record` / `undefined: capability.Name`, but correctly diagnosed this as
unrelated (file mtimes showed a concurrent agent mid-rewrite in `internal/inventory`) and did not attempt to
"fix" it.

**Root cause:** four parallel agents were writing to the same working directory at once (deliberately, since
their file sets were disjoint by design). A repo-wide build run by any one of them mid-flight can transiently
observe another agent's half-written state.

**Lesson:** when running several agents in parallel against a shared working tree, scope each agent's own
verification commands (`go build`, `go test`) to the specific packages it owns, not the whole repository,
and explicitly tell it that a repo-wide check is expected to be unreliable until all parallel work lands.
Reserve the repo-wide verification pass for after the barrier, run once, by whichever process (or agent)
is responsible for integration.

## 9. Ansible-shape sniff over-fired on a bare, non-Ansible top-level list

**Symptom:** an adversarial review found that `Builder.BuildFromYAML`'s pre-parse sniff labeled `[]`
(empty list) and `- foo\n- bar\n` (a list of scalars) as "shaped like an Ansible playbook," which is a
false, overclaiming diagnosis: neither has anything resembling a play.

**Root cause:** the sniff only checked "is the top-level YAML value a list," not whether the list actually
contained anything play-shaped (a map).

**Fix:** `internal/engine/yaml.go` now only returns the specific Ansible-playbook error when at least one
list element is itself a map; a bare list that isn't play-shaped gets a generic "top-level YAML list, not a
native Pleiades runbook" error instead. Regression test:
`TestBuildFromYAML_NonAnsibleListNotOverclaimed`.

**Lesson:** a format-detection heuristic should assert only what it actually checked. "Top-level list" and
"shaped like an Ansible playbook" are not the same claim; conflating them produces a confidently wrong error
message, which is worse for a user than a generic one.

## 10. Unknown runbook keys are silently dropped, which would defeat any migration guarantee

**Symptom:** a runbook containing `become: true`, `loop:`, `tags:`, or `notify:` at task level builds and
validates successfully, exactly as if those keys had never been written. No error, no warning, no report.
Found while specifying The Forge of Hephaestus (`docs/hephaestus.md`), not by a failing test.

**Root cause:** neither decode path restricts unknown fields. `internal/engine/yaml.go` calls
`yaml.Unmarshal` and `internal/engine/dag.go` calls `json.Unmarshal`, both permissive by default, and
`grep -rn "KnownFields\|DisallowUnknownFields"` over the repository returns nothing. `Task` has a fixed
set of keys with no catch-all, so anything outside that set is discarded during decode. The existing
Ansible-shape sniff catches a whole playbook file, but catches nothing inside a single already-converted
play.

**Fix:** not yet applied. Recorded as a required item in `IMPLEMENTATION.md` Phase 35, which must land it
before the playbook translator ships.

**Lesson:** a converter's "nothing is dropped silently" guarantee is only as strong as the parser that
reads its output. A translator can faithfully report that it could not convert `loop:` and still lose the
data, if the format it writes into discards unrecognized keys one layer downstream. When promising
lossless conversion, verify the guarantee end to end, through the consumer, not just at the boundary where
the promise is made.

## 11. A non-string `target` silently disables two validation rules

**Symptom:** a task written as `params: {target: [web1, web2]}` passes validation with zero findings, and
neither the capability rule nor the blast-radius rule examines it at all. The runbook looks clean
precisely because the rules gave up on it.

**Root cause:** both `internal/validate/capability_rule.go` and `internal/validate/blast_radius.go` read
the target with an unchecked type assertion, `task.Params["target"].(string)`, which yields `""` when the
value is not a string. Both then treat the empty result as "nothing to check" and `continue`, so a
malformed target is indistinguishable from an absent one.

**Fix:** not yet applied. `Params` is `map[string]interface{}`, the only untyped structure in the schema,
which is why this class of failure is possible at all.

**Lesson:** a failed type assertion that falls through to "skip this item" converts a malformed input into
a silent pass. In a validator this is the worst possible direction to fail, because the tool's entire
purpose is to report problems, and here the malformed case produces more confidence than a well-formed
one. Distinguish absent from unparseable, and report the second.

## 12. An instruction that cannot be followed will be violated

**Symptom:** the agent rules require AST-aware navigation ("LSP over Grep") for reasoning about Go code.
Every such claim this session was actually derived from `grep`, including several written into permanent
documents as verified findings.

**Root cause:** `gopls` was not installed in the environment, so the rule was literally unfollowable as
written. The rule also named no concrete command, so there was nothing to run even if a tool had been
present. An instruction with no executable form degrades to a preference, and a preference under time
pressure degrades to whatever is at hand.

**Fix:** installed `gopls`, then rewrote the `IDE & LSP Tooling` section of `.AGENTS/AGENTS.md` to carry
the install command, a question-to-command table, the rule that interface method references must be
queried at the interface declaration rather than the implementation, and a mandatory control step. All
grep-derived claims from this session were re-verified with `gopls references`; they happened to be
correct, which was luck rather than method.

**Lesson:** when a rule is violated, check whether the environment makes it possible to obey before
treating it as a discipline problem. A rule that names a tool must also name how to get the tool and
what to type. Fix the environment and the instruction in the same change, or the violation recurs.

## 13. An empty query result is indistinguishable from a misaimed query

**Symptom:** `gopls references` on the `AddInfo`, `History`, and `Version` methods of `record.Base`
returned zero references, including from tests known to call them. Read naively, that says the methods
are entirely dead.

**Root cause:** the callers reach those methods through the `inventory.InventoryItem` interface, and a
reference query anchored on the concrete implementation does not resolve calls made through an
interface. Querying the method position in the interface declaration instead returned the real callers.
A separate instance of the same class: an earlier control query returned "no identifier found" purely
because the line and column pointed at a comment rather than a symbol.

**Fix:** the `IDE & LSP Tooling` section now requires a control before any "zero references" claim:
confirm with `gopls definition` that the position names the intended symbol, and confirm with a nearby
symbol that must have callers that the query resolves references at all.

**Lesson:** absence of evidence and evidence of absence produce byte-identical output. Any tool that can
return nothing needs a positive control proving it would have returned something. This is the same
discipline as the census rule that "not observed" must never be reported as "not affected"; it is the
same failure at a different layer.

## 14. `internal/runner`'s test package did not compile, and the one visible symptom was not the only bug

**Symptom:** `go vet ./...` failed with `internal/runner/agent_test.go:107:6: MockAdapter redeclared in
this block`. This was known and tracked as pre-existing, out-of-scope Phase 15 work across every prior
session and across all four Phase W4 sessions that touched other packages in parallel.

**Root cause:** three separate, independent breakages had accumulated in the same test package, only the
first of which the vet error actually named. (1) `agent_test.go` and `agent_bench_test.go` each declared
an identical `MockAdapter` type, the literal redeclaration vet reported. (2) Both of those declarations
were themselves broken even in isolation: the package is `runner_test` (external test package), but each
`Execute` method signature referenced the bare identifier `DispatchPayload` instead of the required
`runner.DispatchPayload`, so neither would have compiled alone once the duplicate was resolved.
(3) `runner.NewAgent` had already been widened, in this same uncommitted working tree, from
`NewAgent(consumer, logger)` to `NewAgent(consumer, adapter ExecutionAdapter, logger)`, and all three
call sites in the package (`agent_test.go`, `agent_bench_test.go`, `agent_fuzz_test.go`) still called the
old two-argument form. `go build ./...` cannot see any of this, since it does not build test files; only
`go vet ./...` (which type-checks tests) surfaces it, and it stopped at the first error rather than
reporting all three at once, so fixing the reported symptom just exposed the next one, twice.

**Fix:** applied, not deferred. `ExecutionAdapter` already existed in `agent.go` with the exact shape a
mock adapter needs, so the fix was mechanical rather than a Phase 15 design decision: kept one
`MockAdapter` (in `agent_test.go`), qualified its signature as `runner.DispatchPayload`, deleted the
duplicate from `agent_bench_test.go`, and updated all three `NewAgent` call sites to pass `&MockAdapter{}`
as the second argument. `go vet ./...` and `go test ./internal/runner/... -race -cover` both pass now
(83.8% coverage). Building the real production `ExecutionAdapter` (a working transport/execution path)
remains genuinely Phase 15 scope; only the test-only stub and its call sites were touched.

**Lesson:** a compiler error names one bug, not all of them. When a long-deferred "pre-existing,
out-of-scope" failure is finally worth a look, check whether fixing the named symptom reveals another
error before deciding whether "defer it" is still the right call versus "just fix it": here, the total
fix was three mechanical call sites and one qualifier, cheaper than the workaround (a build-tag exclusion)
would have been, and it removed real technical debt instead of hiding it behind a tag. Per
`LESSONS_LEARNED.md` entry 19, this is the same class of failure as a widened interface breaking a mock
`go build` cannot see: the fix here is the concrete instance, applied three call sites deep.

## 15. `lock.Manager.Acquire`'s `ttl` parameter is honored by one implementation and silently ignored by
the other

**Symptom:** nothing observable yet, since only one implementation of `lock.Manager` existed until this
session. The gap became visible only once a second, independent implementation had to satisfy the same
interface.

**Root cause:** `natsLockManager` (`internal/lock/nats.go`) was built against one fixed 24-hour
bucket-wide TTL set once at bucket creation, described in its own comment as an "absolute maximum
failsafe." It never reads the `ttl` argument `Acquire` receives per call. Nothing about the `Manager`
interface's doc comment (`internal/lock/manager.go`) states whether `ttl` is a hard per-call contract or
a best-effort hint, so this was not a violation of anything written down, just an ambiguity no one had
reason to notice with a single implementation.

**Fix:** not applied to `natsLockManager`, deliberately. `inProcessManager` (new this session,
`internal/lock/inprocess.go`) honors `ttl` per call exactly, via a token-guarded expiry deadline that
makes an abandoned lock reclaimable once its own requested duration elapses. This makes it a strictly
more complete implementation of the interface's documented contract than `natsLockManager`, not a
divergent one. The shared conformance suite both adapters run
(`internal/lock/conformance_test.go`) deliberately excludes ttl-timing assertions, so it proves real
substitutable behavior without papering over this one known asymmetry.

**Lesson:** a port with only one implementation can hide contract ambiguity indefinitely; a second,
independent implementation is what actually forces an interface's doc comment to become precise. This is
a real, open question for whoever next touches `internal/lock`: tighten `Manager.Acquire`'s doc comment to
state whether `ttl` is a hard contract, and if so, decide whether `natsLockManager` should be changed to
honor it, rather than leaving two adapters silently disagreeing about what their shared interface promises.

## 16. `yaml.Marshal` panics, rather than returns an error, on a value it cannot encode

**Symptom:** none observed in production; found and fixed during Phase W4 test-writing for the new
file-backed `inventory.Repository`, before it could reach a real caller.

**Root cause:** `inventory.InventoryItem.AddInfo` accepts any `PropertyValue` (a type alias for `any`,
`pkg/inventory/item.go`), so a caller can record a property or a revision value of any Go type, including
one `go.yaml.in/yaml/v3` cannot encode at all (a channel, a func). Its `Marshal` wraps most encoding
failures in a returned error already, but for a handful of reflect kinds it has no case for, the failure
escapes as an internal panic instead. `fileRepository.Save` writes exactly these caller-supplied values to
disk, so an adversarial or merely mistaken property value could have crashed the whole process on a write
that should have just returned an error.

**Fix:** applied. `marshalSidecar` (`internal/inventory/file_repository_state.go`) and
`encodeHostsSafely` (`internal/inventory/file_repository_save.go`) both wrap their `yaml.Marshal`/
`EncodeHosts` call in a `defer`/`recover` that converts any panic into a normal wrapped error. Covered by
`TestFileRepository_Save_UnmarshalableRevisionValue_Errors`
(`internal/inventory/file_repository_errors_test.go`).

**Lesson:** a value that is `any`-typed because it is genuinely caller-controlled (a property bag, in this
case) makes every downstream encoder a boundary that must be checked for panic-on-bad-input, not just
error-on-bad-input, before it is trusted with data this codebase does not control the shape of. A library
returning `(data, error)` from most paths does not guarantee it never panics on the paths it did not
anticipate; recover at the seam where untyped, external data first reaches that library, not after.

## 17. `adapters/native.Adapter` publishes to a NATS subject its own stream is not configured to capture

**Symptom:** none observed yet; found while designing Phase W5's `engine.Executor`, which needed to
decide what subject convention to publish its own lifecycle events under.

**Root cause:** `event.NewNatsBus` (`internal/event/nats.go`) creates its one JetStream stream with
`Subjects: []string{"pleiades.events.>"}`. `adapters/native.Adapter.streamLog`
(`internal/adapters/native/adapter.go`) publishes to `fmt.Sprintf("jobs.logs.%s", payload.JobID)`, which
does not match that prefix at all, and it calls `jetstream.JetStream.PublishMsg` directly rather than
going through the `event.Bus.Publish` port. A JetStream publish to a subject no stream is configured to
capture is not silently equivalent to a durable publish; depending on broker configuration it can fail
outright or simply never persist. `streamLog` discards the publish error unconditionally ("We ignore
errors in logging to keep execution moving"), so a real failure here would never surface anywhere.

**Fix:** not applied. This is Phase 14's own file, out of Phase W5's scope; recorded here so whoever next
touches the native Adapter or the Dispatcher/Agent job-log path fixes the subject (and stops swallowing
`PublishMsg`'s error) rather than rediscovering the mismatch from a silently missing log stream in
production. `engine.Executor`'s own event publishing (`executor.go`) deliberately publishes under
`"pleiades.events.workflow.<dag-id>.node.<node-id>"`, inside the stream's actual configured prefix, and
goes through the real `event.Bus.Publish` port rather than a raw JetStream client, so it does not repeat
this mismatch.

**Lesson:** a subject string handed to a JetStream client is not self-validating: publishing to a subject
that does not match any stream's configuration is a silent no-op from the publisher's point of view unless
the caller checks the returned error, which is exactly the error this codebase's own convention
(`event.Bus.Publish`) already centralizes. Bypassing the port to call the broker client directly, even for
something that reads like "just logging," reintroduces the exact class of bug the port exists to prevent.

**Update (Phase 2):** closed for real, not just reconfirmed. `internal/adapters/native/adapter.go`'s
`streamLog` and `internal/ansible/receptor.go`'s `publish` both now go through `event.Bus.Publish` (a
`bus event.Bus` field replaces the raw `jetstream.JetStream` each used to hold) to
`topology.LogSubject(jobID)` (`"pleiades.jobs.logs.<id>"`), which is covered by the single stream
`internal/topology` now owns (`StreamSubjectRoot = "pleiades.>"`) -- the exact mismatch this entry
originally found is structurally impossible now, not just fixed at one call site. The swallowed-error half
is also closed: `streamLog`'s publish failure is logged via `slog.Error` rather than discarded (Execute's
own caller still does not see it, deliberately -- log streaming is an observability side effect of an
execution that already happened, not a precondition for it -- but the failure is no longer silent).
Verified by `TestNativeAdapter_Execute_ToleratesPublishFailure` and the real end-to-end run described in
Phase 2's own Release Gate.

## 18. An unvalidated runbook `id:` can widen or misroute a NATS subject

**Symptom:** none observed in production; found during Phase 39 (Schema & Injection Hardening)'s audit of
`event.Bus.Publish`/`Subscribe` call sites for subject/topic injection.

**Root cause:** `WorkflowDef.ID` (`internal/engine/dag.go`) decodes straight off a runbook's `id:` field
with no charset check, and `Executor.publish` (`executor.go`) embeds it directly into a NATS subject:
`fmt.Sprintf("pleiades.events.workflow.%s.node.%s", dag.ID, nodeID)`. NATS treats `.` as the subject-token
delimiter and `*`/trailing `>` as wildcards. Proven empirically with a throwaway test against the real
in-process `Bus`: a runbook with `id: billing.exfil` publishes under
`pleiades.events.workflow.billing.exfil.node.tasks[0]`, which a subscriber scoped to
`pleiades.events.workflow.billing.>` (the natural way to watch one workflow's own events, given this exact
topic shape) also matches, letting one workflow's events masquerade as a subtree of an unrelated
workflow's namespace. No scoped subscriber exists in production code yet, so this was latent, not
exploited, at the time it was found.

**Fix:** applied. `buildFromDef` (`dag.go`) now rejects any `id` that is not empty and not made up solely
of letters, digits, hyphens, and underscores, via a new `validRunbookID` regexp, checked once at the
single domain-level compilation path every surface format (JSON, YAML) shares, so no future caller of
`dag.ID` can reintroduce the same gap by skipping a check at the point of use. Covered by
`TestDAGBuilder_RejectsUnsafeRunbookID` and `TestDAGBuilder_AllowsSafeRunbookID` (`dag_test.go`).

**Lesson:** any user-authored string that later gets embedded into a structured addressing scheme (a NATS
subject, a SQL identifier, a file path) needs its charset constrained at the point it is first parsed, not
assumed safe because "it's just an identifier a human types." The identifier's own author has no reason to
know it will later become part of a wildcard-capable routing key, so the constraint has to be enforced by
the code that introduces that later use, applied as early as possible.

## 19. A single `when_cel` string can hang a task's execution for minutes with no special privilege

**Symptom:** none observed in production; found during Phase 39 (Schema & Injection Hardening)'s CEL
expression safety audit.

**Root cause:** `engine.NewCELEvaluator` (`cel.go`) builds its `cel.Program` with no cost limit, and
`Executor.runNode` (`executor.go`) calls `Program.Eval` with no timeout of its own. cel-go bounds an
expression's parsed *size* (100,000 code points, its own default) but not its evaluation *cost*: a nested
comprehension well within that size limit,
`"[0..3200].all(x, [0..3200].all(y, x+y>=0))"`-shaped, measured multiple real seconds of CPU time on a
single `Eval` call in local testing, and scales quadratically with list length, so a runbook author
(accidentally, or adversarially) can turn one ordinary-looking `when_cel` condition into an effectively
unbounded hang, entirely within cel-go's own advertised limits.

**Fix:** applied. `Compile` (`cel.go`) now creates every `cel.Program` with `cel.CostLimit(defaultCELCostLimit)`
(100,000, chosen empirically: it rejects the pathological expression above in well under a second while
costing any realistic condition, a handful of comparisons or a linear scan over a few hundred elements,
nothing). Covered by `TestCELEngine_RejectsExpensiveComprehension` (`cel_test.go`), which fails if the cost
limit is ever removed or raised past the point of usefulness.

**Lesson:** a library's own advertised safety limit (here, cel-go's expression-size cap) can leave an
entirely separate axis, evaluation cost, completely unbounded; "this parser has a size limit" and "this
parser has a cost limit" are different claims, and only reading the library's own option list (not its
marketing description) reveals which one is actually missing. Verify empirically, the way this entry's fix
was chosen: measure the actual pathological case's real wall-clock cost before picking a limit, rather than
guessing a round number.

## 20. JWT validation has no issuer/audience pinning, deferred pending a real token-issuing path

**Symptom:** none observed; found during Phase 39 (Schema & Injection Hardening)'s JWT auth audit. A
token signed with the correct HMAC secret, but carrying an unrelated `iss`/`aud` and any `role`/`scopes`
the caller chooses, is accepted by `jwtEvaluator.ValidateToken` (`internal/auth/jwt.go`) and passes
`CheckAccess` for any scope it claims.

**Root cause:** `ValidateToken`'s `jwt.ParseWithClaims` call pins the signing algorithm (HMAC only,
verified safe against `alg: none` and RS256/HS256 confusion by a real forged-token test) but passes no
`jwt.WithIssuer`/`jwt.WithAudience` parser option, so nothing ties an accepted token to a specific issuer
or audience beyond "signed with this one shared secret."

**Fix:** not applied, deliberately. There is currently no token-issuing code anywhere in this repository
(grepped for `jwt.NewWithClaims`/`RegisteredClaims{` outside test files: zero production hits), so there is
no established issuer/audience convention yet to validate against; PLAN.md Section 17 (Secrets Management)
and Section 32 (Identity Federation) are the phases that will define one. Adding a hardcoded issuer/audience
check now, with nothing real on the other side of it, would be guessed validation, not a real fix. This is
a real gap only in a scenario this codebase does not yet have (the same HMAC secret shared across more
than one token-issuing service or environment), and `internal/auth`'s own `Evaluator` is not wired into
`internal/api`'s router yet either (`NewRouter` registers only `/healthz` and `/metrics`), so there is no
live caller to protect today.

**Lesson:** algorithm pinning and issuer/audience pinning are different claims a JWT validator can make;
this codebase's fuzz and unit tests already prove the first (`jwt_fuzz_test.go`, `jwt_test.go`) but neither
asserts the second, because the second has no real value to assert against until a real issuer exists.
Revisit this the moment Section 17 or 32 defines what should populate `iss`/`aud` on a real issued token,
and add `jwt.WithIssuer`/`jwt.WithAudience` at that point, not before.

## 21. `add-host --set port=<n>` silently produced an unusable port, defaulting `SSHPort()` to 22

**Symptom:** found while building Phase W6's own Release Gate, which needed to point a scaffolded
inventory at a real container's random mapped SSH port. `pleiades add-host container1 --type linux_server
--set host=127.0.0.1 --set port=32853` succeeded and printed no warning, but the written `inventory.yaml`
held `port: "32853"`, a quoted YAML string, not the integer `SSHPort()` (`internal/inventory/devices/{cisco,linux}/*.go`)
needs. `capability.SSHTransportCapable.SSHPort()` reads it via `Properties().Int("port")`, which only
recognizes a Go `int` or `float64` (`pkg/inventory/properties.go`'s own doc comment: "JSON and YAML both
decode bare numbers as float64"); a `string` value returns `(0, false)`, so `SSHPort()` silently fell back
to its own hardcoded default of 22, never erroring. The identical gap applied to every other non-string
property in the vocabulary, `netconf_enabled` (`Properties.Bool`) included, confirmed by inspection of the
same code path; nothing had ever exercised `--set` for a non-string property through the real CLI before,
since every existing test (`internal/inventory/factory_test.go`, `factory_bench_test.go`) constructs a
`record.Record` fixture directly with native Go types, bypassing `add-host`'s own flag parsing entirely.

**Root cause:** `keyValueList.Set` (`cmd/pleiades/addhost.go`) stored every `--set key=value` flag's value
as a raw Go `string`, unconditionally. A value hand-authored directly in `inventory.yaml` decodes through
YAML's own type inference (a bare `22` becomes an `int`/`float64`, `true` becomes a `bool`), but a value
that arrived through `--set` never went through that inference at all, so it silently diverged in Go type
from a value meaning the exact same thing written by hand, and `Properties`'s typed accessors treat that
type difference as "not set," not as "set to the wrong thing."

**Fix:** applied. `keyValueList.Set` now runs every value through a new `parsePropertyValue` (`addhost.go`):
exact `"true"`/`"false"` become `bool`, anything that parses as a base-10 integer via `strconv.Atoi` becomes
`int`, everything else (including a dotted version string like `"15.2"` or `"6.6.87"`, which would silently
corrupt into a `float64`/truncate if a float case were added) stays a `string`. Deliberately no float case:
this property vocabulary has no float-typed property today, and adding one would break exactly the
version-string properties (`ios_version`, `kernel_version`) that must stay strings. Covered by
`TestParsePropertyValue` (`cmd/pleiades/addhost_test.go`) and proven end to end by Phase W6's own Release
Gate (`TestCLI_RunExecutesSSHTransport`, `cmd/pleiades/ssh_release_gate_test.go`), which genuinely
SSH-connects to a real container on its real non-default mapped port set via `--set port=<n>` through the
real binary.

**Lesson:** a CLI flag and a config file that both populate the same typed field must decode a scalar
value into the same Go type, or a typed accessor built to read either source silently reads only one of
them. "The flag accepted the value and wrote *a* file" is not evidence the value is usable; the only real
evidence is a typed accessor actually reading it back correctly, which is exactly what no test exercised
before this phase needed a non-default port for real.

## 22. `credential.Credential` leaked every secret through `encoding/json` and `log/slog`'s JSON handler

**Symptom:** none observed in production; found by Phase W6's own Schema/Injection Hardening audit, run
as a background workflow of independent Go agents against the real `internal/credential` package, then
adversarially re-verified by a second, independent pass before being trusted. `json.Marshal(cred)` emitted
`{"Password":"the real password","PrivateKeyPEM":"base64 of the real key bytes","Passphrase":"the real
passphrase",...}` in full, and `slog.New(slog.NewJSONHandler(...)).Info("msg", "credential", cred)`
reproduced the identical leak through structured logging.

**Root cause:** `Credential.String`/`GoString` (`internal/credential/credential.go`) redact correctly for
every `fmt` verb (`%v`, `%s`, `%q`, `%#v`, including nested inside a larger struct formatted with `%+v`),
because `fmt` consults `fmt.Stringer`/`fmt.GoStringer` before falling back to reflection. Neither
`encoding/json.Marshal` nor `log/slog`'s handlers consult those interfaces at all: `json.Marshal` reflects
directly over a struct's exported fields, and slog's JSON handler does the same unless a value implements
`slog.LogValuer`, which `Credential` did not. The type's own doc comment claimed leaking a secret through
it was "structurally impossible," and that claim was true for exactly one family of Go's serialization
mechanisms, not all of them.

**Fix:** applied. `Credential` now implements `MarshalJSON() ([]byte, error)` (returning the same
redacted shape `String` does, so `json.Marshal` never sees the real fields) and `LogValue() slog.Value`
(returning `slog.StringValue(c.String())`, which `log/slog` resolves automatically before handing a value
to any handler, JSON or text). Covered by `TestCredential_MarshalJSONRedactsSecrets` and
`TestCredential_LogValueRedactsSecrets` (`internal/credential/credential_test.go`), both proven through
the real `encoding/json`/`log/slog` APIs, not by calling the new methods directly.

**Lesson:** a type's "this can never leak a secret" claim is scoped to whichever serialization mechanisms
were actually tested against, never to serialization in general. `fmt.Stringer` and `json.Marshaler` (and
`slog.LogValuer`) are three independent interfaces Go's standard library consults in three independent
code paths; redacting one says nothing about the other two. Any secret-carrying type needs a redaction
test written against every serialization mechanism a caller could plausibly reach it through, not just the
one the type's author happened to reach for first.

## 23. A masked command's `Stdout`/`Stderr` did not cover the transport-error path, only the success path

**Symptom:** none observed with real `golang.org/x/crypto/ssh` errors today; found by the same Phase W6
audit as entry 22. A `transport.Transport.Exec` failure (dial rejected, auth rejected, connection lost)
whose returned error happened to embed credential material would have reached
`cmd/pleiades/run.go`'s printed stdout and `event.Bus`'s published payload completely unmasked, because
`internal/engine/action_ssh.go`'s masking was applied only to `transport.Result.Stdout`/`Stderr` on the
success path, after `binding.Transport.Exec` had already returned `nil` for its error.

**Root cause:** the masking step and the transport-error-handling step were written as if they were
unrelated concerns (one about command output, one about connection failures), when both actually read
from the exact same credential and both can reach the exact same unmasked-output surfaces downstream. The
transport-error branch's `fmt.Errorf(..., %w, err)` had no reason to be safe, because nothing had reason
to think it carried a secret, until the audit asked "does anything guarantee that."

**Fix:** applied. The masking secret list (`cred.Password`, `cred.PrivateKeyPEM`, `cred.Passphrase`) is
now computed once, before `Transport.Exec` is called, and both failure shapes are masked with it: a
transport-level error is wrapped with `%s` over `credential.Mask(secrets, err.Error())` (deliberately not
`%w`, so a masked error can never be unwrapped back to its original unmasked `Error()` text by a caller
further up the stack), and the existing non-zero-exit-code path is unchanged. Covered by
`TestTransportActionExecutor_MasksSecretsInTransportError` (`internal/engine/action_ssh_test.go`), using a
fake `transport.Transport` whose error deliberately embeds the real password, the same shape the audit
used to prove the gap.

**Lesson:** when a function masks a secret out of one return path, audit every OTHER path the same
function can return through for the same credential, not just the path the masking requirement was
originally written against. "This function returns an error or a result" is exactly two paths; a masking
guarantee that only covers one of them is not a masking guarantee, it is a masking guarantee for the
common case.

## 24. `MkdirAll`'s mode argument only applies to a directory it creates, not one that already exists

**Symptom:** none observed as a real exposure (the secret files themselves are always written `0o600`
regardless); found by the same Phase W6 audit as entries 22-23, rated low severity. Pre-creating
`.pleiades` with `0o777` before calling `ResolveMasterKey` or `SaveFileStore` (`internal/credential`) left
the directory at `0o777` afterward, even though both functions call `os.MkdirAll(dir, 0o700)`.

**Root cause:** `os.MkdirAll`'s mode argument is only used for path components it actually creates; on an
already-existing directory it is pure no-op, matching POSIX `mkdir`'s own semantics. Neither call site
checked or corrected the permissions of a directory that turned out to already exist.

**Fix:** applied. Both `ResolveMasterKey`'s `generateAndSaveMasterKey` (`master_key.go`) and
`SaveFileStore` (`file_store_save.go`) now follow their `MkdirAll` call with an explicit
`os.Chmod(dir, 0o700)`, so the directory ends up at the intended permission whether it was just created or
already existed. Covered by `TestResolveMasterKey_TightensPreexistingDirPermissions` and
`TestSaveFileStore_TightensPreexistingDirPermissions`, both pre-creating the directory at `0o777` before
asserting the post-call mode.

**Lesson:** `os.MkdirAll(path, mode)` is not "ensure this directory exists with this mode"; it is "ensure
this directory exists, applying this mode only if I am the one creating it." Any code relying on a
directory's permissions for a security property (not just convenience) needs a separate, explicit
`os.Chmod` after `MkdirAll`, since the directory may predate the call for reasons entirely outside that
code's control (a stale run, a permissive umask, a different process).

## 25. A bulk-insert batch size tuned against one schema width silently stopped being safe after the schema grew

**Symptom:** `internal/inventory/iterator_test.go`'s `TestIteratorMemoryFlatline` (5,000-row `CreateBulk`
batches) and `iterator_fuzz_test.go`'s `FuzzIteratorPagination` (a 5,000-row single `CreateBulk` call)
started failing with `insert nodes to table "devices": too many SQL variables` the moment Phase 1's new
`Device` columns (`device_id`, `source`, `source_synced_at`, `tags`, plus the `TimestampMixin`'s
`created_at`/`updated_at`) landed, with no change to either test's own logic. `iterator_bench_test.go`'s
`BenchmarkIterator` (a single 10,000-row `CreateBulk` call) had the identical latent exposure, just not
yet observed because benchmarks are not part of a normal `go test` run.

**Root cause:** a single `CreateBulk` call binds one SQL placeholder per column per row in one statement,
and SQLite's compiled-in `SQLITE_MAX_VARIABLE_NUMBER` (32,766 in the `mattn/go-sqlite3` build this repo
uses) bounds that total, not the row count alone. A batch size chosen when `Device` had roughly four
bound columns (`name`, `properties`, `version`, `state`) silently stopped being safe once the row grew to
roughly ten columns, because `rows * columns` crossed the fixed variable ceiling with no compiler warning
and no error until the exact statement ran.

**Fix:** applied. All three call sites now route through one shared helper,
`bulkCreateDevices` (`internal/inventory/ent_bulk_testutil_test.go`), which chunks any slice of
`*ent.DeviceCreate` builders into `sqliteBulkInsertBatch`-sized (1,000-row) `CreateBulk` calls, a size
chosen with deliberate headroom rather than computed exactly against today's column count, so the next
column added to `Device` does not reintroduce this failure.

**Lesson:** a `CreateBulk` (or any single-statement multi-row insert) batch size is not a constant that
can be chosen once and forgotten; it is a function of `rows * columns_per_row` against the driver's fixed
placeholder ceiling, and every new column silently lowers the safe row count for every existing caller
that has not been re-measured. Any hardcoded bulk-insert batch size should either carry deliberate
headroom (as the fix above does) or be computed from the schema at hand, not tuned once against whatever
the row shape happened to be on the day the test was written.

## 26. Stray git worktrees under `.claude/` were not gitignored and polluted whole-tree tooling run from repo root

**Symptom:** the Phase 0 CI harness session's first `gofmt -l .` run reported dozens of violations, and
`gosec ./...` errored while importing directories, none of it in this module's own source. Every offending
path was under `.claude/worktrees/agent-*`.

**Root cause:** two full git worktrees from unrelated prior agent sessions (`Agent`/`Workflow` tool runs
with `isolation: "worktree"`) were left under `.claude/worktrees/`, each a complete checkout of this repo
on its own branch. `.gitignore`'s `.[A-Z]*` pattern only matches a dot-directory whose next character is
uppercase, so `.claude` (lowercase `c`) is untracked but not ignored, and both `gofmt -l <path>` (a plain
filesystem walk) and `gosec`'s own directory walker descend into it same as any other subdirectory. `go
build ./...`/`go vet ./...` were unaffected only because each worktree carries its own `go.mod`, which Go's
tooling treats as a module boundary and does not cross uninvited.

**Fix:** applied, scoped to CI's own commands, not the worktrees themselves (deleting another session's
git worktree is a destructive action outside this session's authority). The Phase 0 Makefile's `fmt`
target excludes `.claude/` when building the file list `gofmt -l` scans; `gosec` (via
`tools/gosec-check`) and `TestOnlyDesignatedAdaptersImportConcreteDrivers`'s underlying `go list` calls are
scoped to `./...`/the module path rather than a bare filesystem walk, and gosec is additionally invoked
with `-exclude-dir=.claude`.

**Lesson:** a tool that walks the filesystem (`gofmt -l .`, most linters' default mode) sees anything
`.gitignore` does not hide, including another tool's own scratch state (worktrees, generated caches) that
was never meant to be source. Before wiring any whole-tree command into CI, check `git status --porcelain`
for untracked-but-not-ignored directories at the repo root first; do not assume "not tracked" means "not
scanned."

## 27. An architecture test written to check import layering also caught a real, dormant production bug

**Symptom:** while writing `internal/archtest`'s adapter-allowlist check (verifying every package that
imports a concrete driver is on a written allowlist), `internal/ent` failed
`TestAdapterAllowlistHasNoStaleEntries`: it was on the allowlist (it opens a SQLite connection via
`dialect.SQLite`/`stdsql.Open` in `embedded.go`) but `go list`'s own `.Imports` for that package did not
actually include `github.com/mattn/go-sqlite3`.

**Root cause:** `embedded.go` never imported the driver package itself; every existing caller of
`OpenEmbedded` is a `_test.go` file, and every one of those imports `mattn/go-sqlite3` (directly or via
`internal/ent/enttest`) to register the driver for its own use, which incidentally made the driver
available process-wide for every test binary. `OpenEmbedded` has no production caller anywhere in this
module yet (Phase W4's own text already says so), so the real `pleiades` binary, which links none of those
test files, would have hit `sql: unknown driver "sqlite3"` the first time something actually called it.

**Fix:** applied. Added `_ "github.com/mattn/go-sqlite3"` to `embedded.go`'s own imports, the file that
actually opens the connection, rather than continuing to rely on whichever caller happens to import it.

**Lesson:** a `database/sql` driver registration is a process-global side effect of an `init()` function
triggered by any import anywhere in the binary, so a package that calls `sql.Open("driver-name", ...)`
can compile and every one of its own tests can pass while the driver is registered only because a test
file imported it, not the production code path. The package that opens the connection should import its
own driver, even if nothing calls it yet: an architecture test built for an unrelated reason (import
layering) is exactly the kind of check that surfaces this, since it asks "does this package really import
what it needs" rather than "do the tests pass."

## 28. A second Rule added to an already-documented O(n) call site silently paid that cost twice

**Symptom:** the same session that added `LifecycleRule` beside `CapabilityRule`
(`internal/validate`) doubled `BenchmarkValidateFullInventory` from ~239ms to ~480ms for a 10,000-host,
10,000-node `WorldView`, with no change to the benchmark itself and no failing test: `go test ./...` and
`-race` both stayed green throughout, because nothing asserts a specific latency.

**Root cause:** `WorldView.Resolve(target)` was already documented, in Phase W3's own Pattern Entry Gate,
as an O(n) linear scan, "O(n^2) at that scale" because it runs once per task. That accounting assumed one
rule doing the resolving. `LifecycleRule` was written to the same, already-established convention every
other rule in the package follows (`for id, task := range world.DAG.Nodes { ...; devices := world.Resolve(target) }`),
which is correct in isolation and wrong in aggregate: `Validate` runs every registered rule against the
same `WorldView`, so a second rule resolving the same target for the same task pays the identical O(n)
scan a second time, silently doubling the constant factor the moment it was registered.

**Fix:** applied. `WorldView` gained an unexported `resolveCache map[string][]inventory.InventoryItem`;
`Resolve` checks and populates it when non-nil, and is a no-op fallback (compute directly) when nil, so no
existing caller building a `WorldView` as a bare struct literal is affected. `Validate` initializes the
cache once, on its own local copy, before dispatching to any rule; because a map field's underlying data
is shared across Go's by-value struct copies, every rule ends up sharing that one cache for the duration
of that `Validate` call. Benchmark back to ~239-244ms with two rules now calling `Resolve`, not one.

**Lesson:** a scalability note that says "O(n) per call site" is a statement about the number of call
sites, and that number is not fixed by the pattern that made it easy to add another rule (the whole point
of a Rule Registry). Before adding a second consumer of an already-flagged linear-scan primitive, re-run
its own benchmark, not just the correctness tests; a correctness suite has no opinion on whether a change
made something twice as slow, only a benchmark does, and this repository's own Checkbox Discipline rule 2
already says a Fuzz/Stress claim requires a benchmark that actually ran, not one read from an earlier
session's number.

## 29. An idempotency key derived from message content, not message identity, silently vanished real events

**Symptom:** a real NATS container test, written during Phase 2's own development to sanity-check
`natsBus.Publish`/`Subscribe` with two back-to-back messages, delivered only the first. `stream.Info`
showed the stream's own message count had only incremented by one for two publishes.

**Root cause:** `DefaultIdempotencyKeyDerivation`'s first draft hashed `topic + evt.Data` (the domain
payload), deliberately excluding `Event.ID` on the reasoning that `WrapPayload` mints a fresh ID on every
call and hashing it would defeat determinism. Both test messages happened to publish `nil` payloads to the
same topic, so both derived the identical key; `natsBus.Publish` passes that key as
`jetstream.WithMsgID(...)`, and JetStream's producer-side dedup window silently treated the second publish
as a duplicate of the first and never stored it. The reasoning was backwards: `Event.ID` is minted once,
by `WrapPayload`, and stays fixed on that value for the rest of its life, including across a caller-side
retry that resends the same already-built `Event` -- the actual shape "a retry of the same logical
operation" takes in this codebase, since nothing re-calls `WrapPayload` mid-retry. Keying on content
instead means two entirely distinct events that happen to share a topic and payload (two identical
health-check pings a minute apart, for example) collide on the same key and the second is silently
dropped, never delivered, with no error anywhere.

**Fix:** applied. `DefaultIdempotencyKeyDerivation` (`internal/event/dedup.go`) returns `evt.ID` directly.
Pinned by `TestDefaultIdempotencyKeyDerivation_DistinctEventsWithIdenticalContentDoNotCollide` (two
distinct IDs, identical `Data`, must derive different keys) and
`TestDefaultIdempotencyKeyDerivation_RetryOfSameEventIsStable` (the same `Event` value must derive the
same key across repeated calls), plus a real-broker proof,
`TestNatsBusPublish_ProducerSideDedupSuppressesRetryOfSameEvent` /
`_DistinctEventsBothStored`, asserting the actual stored message count on a real stream rather than
trusting the derivation function's own unit tests alone.

**Lesson:** see `LESSONS_LEARNED.md`'s new entry on deriving idempotency keys from operation identity, not
operation content.

## 30. A consumer that bypasses the event envelope silently zeroed every field after an unrelated interface change

**Symptom:** `tests/e2e`'s `TestGrandIntegration` started failing after Phase 2 rewired `api.Dispatcher` to
publish through `event.Bus.Publish` instead of a raw `jetstream.JetStream.PublishMsg` call: the runner
picked up every dispatched job, but logged `runbook="" device=""`, and the downstream native adapter's own
log-subject construction (`topology.LogSubject(payload.JobID)`) collapsed to a bare, ID-less subject.

**Root cause:** every real publish in the codebase now wraps its domain payload in the `Event` envelope
(`internal/event/payload.go`) before it ever reaches the wire, so the raw bytes JetStream actually delivers
are a marshaled `Event`, not a bare `DispatchPayload`. `natsBus.Subscribe` callers get this unwrapped for
them automatically (decode `Event`, hand the caller `Event`, and the caller reads `evt.Data` itself for
whatever's next). `runner.Agent.handleMessage` (`internal/runner/agent.go`) does not go through
`Bus.Subscribe` -- it stays on its own pull-based `jetstream.Consumer.FetchNoWait` loop by design
(PATTERNS.md's own "Push vs Pull Execution Model" entry) -- so it never got that automatic unwrap, and its
own `json.Unmarshal(msg.Data(), &payload)` was decoding the *outer envelope's* raw bytes straight into
`DispatchPayload`. `encoding/json` does not error on unknown or missing fields by default, so this
succeeded silently: `DispatchPayload`'s fields (`runbook_id`, `device_name`, ...) simply don't exist at the
envelope's top level (they live one level down, inside its `Data` field), so every field decoded to its
zero value instead of failing loudly.

**Fix:** applied. `handleMessage` now decodes in the same two steps `natsBus.Subscribe` performs
internally: raw bytes into `event.Event` first, then `evt.Data` into `DispatchPayload`. Caught and proven
by the real `tests/e2e.TestGrandIntegration` run (real Postgres, real NATS, the actual composition-root
wiring), not a narrower unit test with a hand-built payload that happened to already be in the new,
correct wire shape.

**Lesson:** changing what a shared serialization boundary wraps its payload in is a breaking change for
*every* consumer of the wire format, not just the ones that go through the one port that got updated to
match. A consumer that deliberately bypasses a port (for a real, named architectural reason, like `Agent`'s
pull model) still shares that port's wire contract and must be updated in lockstep with it; grepping for
"every caller of `Bus.Subscribe`" would have missed this one entirely, since `Agent` is not a caller of
`Bus.Subscribe` at all. The real, load-bearing proof here was a genuine cross-process integration test with
real infrastructure, not a mock that would have needed the exact same (wrong) assumption baked into it to
even compile.

## 31. A single shared consumer meant every SSE log viewer saw whatever job was requested first, not the job it asked for

**Symptom:** none observed in production (no real composition root existed to expose this until Phase 2
built one); found while wiring `cmd/controller`'s real HTTP surface and asking what `LogStreamer` would
actually do with two concurrent requests for two different job IDs.

**Root cause:** `api.NewLogStreamer` (`internal/api/logs.go`) took one already-constructed
`jetstream.Consumer` at construction time, whose `FilterSubject` was fixed to whatever job ID the caller
happened to build it with (`cmd/demo/main.go` hardcoded it to `"123"`). `StreamLogs` reads the requested
job ID from the URL (`chi.URLParam(r, "id")`) but never used it for anything -- the shared `Consumer`
object was reused verbatim for every request, regardless of which job the URL actually named. Every
concurrent viewer, whatever job ID they requested, would have received whichever job's logs the one shared
consumer was constructed against.

**Fix:** applied. `NewLogStreamer` now takes a `jetstream.JetStream` handle instead of a pre-built
`Consumer`; `StreamLogs` builds a fresh, `topology.LogViewerConsumerConfig(jobID)`-scoped ephemeral
consumer per request, so the URL parameter actually determines what each viewer sees.
`TestStreamLogs_ReleaseGate` and the new `TestStreamLogs_ReturnsErrorWhenConsumerCreationFails` exercise
this directly.

**Lesson:** a constructor parameter that looks like dependency injection (`NewX(alreadyBuiltThing)`) can
quietly bake a per-request value into a shared, long-lived object if the "already built thing" itself
carries per-request configuration (here, a consumer's `FilterSubject`). The fix is to inject the *factory*
(`jetstream.JetStream`, capable of building a correctly-scoped consumer per call) rather than one instance
of the thing the factory would have produced.

## 32. An SSE handler committed its response to 200 before it could still fail

**Symptom:** a new test (`TestStreamLogs_ReturnsErrorWhenConsumeFails`, written while closing a coverage
gap, not while investigating a report) expected a 500 when `consumer.Consume` fails, and got 200 instead.

**Root cause:** `StreamLogs` (`internal/api/logs.go`) wrote the initial `"event: init"` SSE line and
flushed it *before* calling `consumer.Consume`. `http.ResponseWriter`'s first `Write` implicitly calls
`WriteHeader(200)` if nothing set a status explicitly, so by the time `Consume` could still fail, the
response had already committed to 200; the subsequent `http.Error(w, ..., 500)` call could only append body
text to an already-successful response, never actually change the reported status.

**Fix:** applied. `consumer.Consume` (and the `http.Flusher` type-assertion check before it) now run
*before* anything is written to `w`; only once streaming can actually start does the handler set headers
and write the init line. This means every real failure path in the handler -- missing job ID, consumer
creation failure, unsupported streaming, `Consume` failure -- can still report its own honest HTTP status.

**Lesson:** in a streaming handler, the first byte written to `http.ResponseWriter` is the point of no
return for the status code, not the point `WriteHeader` is called explicitly. Every fallible step that
could still need to report an error status must run before that first write, not be sequenced by how
naturally the code reads top to bottom.

## 33. Fixing #32 introduced a genuine data race between the handler and its own delivery callback

**Symptom:** `go test -race` failed on `TestStreamLogs_ToleratesAckFailure` (a test that happened to
exercise concurrent delivery, not a test written to find this) immediately after the fix for #32 landed:
`WARNING: DATA RACE` on two goroutines both calling `httptest.ResponseRecorder.Write`.

**Root cause:** the fix for #32 moved `consumer.Consume`'s call earlier, before the handler's own
`"event: init"` write. `Consume` starts delivering messages asynchronously in its own goroutine as soon as
it returns successfully; the handler's own goroutine then goes on to write the init line to the same
`http.ResponseWriter` right after. `http.ResponseWriter` is not safe for concurrent use (the net/http
Handler contract requires a handler to serialize its own writes), so these two goroutines writing to `w`
with no synchronization between them is a genuine race, not just a test artifact: even the real,
network-latency-bound JetStream adapter has no structural guarantee that a message can't be delivered in
the gap between `Consume` returning and the init line being written.

**Fix:** applied. A `sync.Mutex` local to each `StreamLogs` call now guards every write to `w`: both the
per-message handler callback and the init-ping write take it before writing and release it after. Proven
by `go test -race` passing clean on the full `internal/api` suite afterward.

**Lesson:** reordering code to fix a correctness bug (#32) can introduce a concurrency bug that pure
sequential reasoning about "what happens in what order" will not catch, because the whole point of the
reorder was to let a background goroutine's work (message delivery) start running before the main
goroutine's own subsequent code executes. Any reordering that moves a call starting background work
earlier relative to the caller's own remaining writes to a shared, not-concurrency-safe resource needs a
`-race` run specifically checking that reordering, not just a green test suite: the existing tests all
passed under `go test` without `-race`, plain `go test` does not detect races reliably, only `-race`
(or `-race`-covered CI) does.

## 34. A shared lock's own join lost most of a concurrent burst under the default contention policy

**Symptom:** `TestNatsManagerConformance/SharedHoldersConcurrent` (a new test written for Phase 3's own
shared-lock-mode work, not a report) launched 10 goroutines under `ModeShared`/`PolicyReject` against the
same fresh `itemID` and expected all 10 to succeed. Only 2 did; the other 8 returned `ErrLockHeld`.

**Root cause:** `natsLockManager.tryAcquireOnce`'s shared-join path was a single, non-retrying
read-`Get`-modify-CAS-`publishWithTTL` cycle: the first goroutine's `kv.Create` won, every other goroutine
lost that race and fell into the join branch, read the same revision, and raced each other's CAS `Update`.
Exactly one join could win per revision; every other one's CAS failed with `jetstream.ErrKeyExists`, which
the code surfaced straight through as `ErrLockHeld`, the same as *real* contention (an incompatible mode).
`PolicyReject`'s single-attempt contract then correctly, but wrongly in this case, failed on the first
loss instead of trying again -- the request was for something `ModeShared` fully permits, not something in
conflict with the current holder.

**Fix:** applied. `tryAcquireOnce` now distinguishes the two cases explicitly: a mode mismatch (exclusive
vs. anything, or shared vs. a live exclusive holder) is real contention and returns `ErrLockHeld`
immediately, honoring the caller's `ContentionPolicy` as before; losing a shared join's own CAS race
against a *different, concurrent, equally compatible* join or release is retried internally, in a small
bounded backoff loop, regardless of `ContentionPolicy` -- since nothing here is exclusive and no live
holder is being asked to yield anything, it is optimistic-concurrency noise, not contention in the
`AcquireOptions.Policy` sense. `KeepAlive` and `Release`'s own shared-mode read-modify-CAS-write cycles got
the identical retry loop for the same reason. Proven by the same test now passing, plus a dedicated
regression covering 10 simultaneous holders.

**Lesson:** when two independent CAS-based operations are individually correct but not mutually exclusive
by business meaning (two shared joins should both eventually succeed, not race each other to fail), losing
the CAS is not evidence of real contention and must not be surfaced through the same error the genuinely
conflicting case uses. Conflate them and a `ContentionPolicy` that reasonably fails fast on real contention
(`PolicyReject`) will incorrectly fail fast on this too, silently losing most of a legitimate concurrent
burst; the fix belongs at the layer that can tell the two apart (inside the CAS retry itself), not by
asking every caller to choose a waiting policy just to get correct behavior for compatible operations.

## 35. A fuzz test that never called the function under test

**Symptom:** none reported; found by direct code reading while auditing this phase's own existing
`FuzzLockAcquisition` (`internal/lock/nats_fuzz_test.go`) before extending it, not by a failure.

**Root cause:** the entire fuzz body was `_ = itemID`, with a comment explaining that spinning up a real
NATS container per fuzz iteration would be too slow, and no fallback ever built. The seed corpus (garbage,
overly long, and SQL-injection-shaped strings) suggested this fuzzed something real; it fuzzed nothing.

**Fix:** applied. `FuzzLockAcquisition` now starts one real NATS container shared across every iteration
(not one per iteration, the actual performance concern the original comment named) and genuinely calls
`Acquire`/`Release` against it every time, with each iteration's `itemID` namespaced by a monotonic counter
so iterations can never collide with each other's leftover state while the fuzzed content still reaches
real subject construction. This immediately found two real defects, #36 and #37 below, that the theater
version could never have found no matter how long it ran.

**Lesson:** "too slow to fuzz the real thing" is a real constraint, but the answer is amortizing the
expensive setup across the whole fuzz run (one container, matching the same pattern this package's own
`TestNatsManagerConformance` already uses for its own container-per-suite-run), not silently fuzzing
nothing while keeping the shape of a fuzz test. A fuzz target's own seed corpus is a promise about what it
exercises; a reviewer (human or agent) trusting that promise without reading the body would never catch
this, which is exactly why it survived until this phase's own audit.

## 36. A sub-second positive lock TTL reached the NATS server as an opaque API error instead of a clear one

**Symptom:** `FuzzLockAcquisition`, once fixed to be real (#35), failed within seconds on a fuzzed positive
`ttl` of a few nanoseconds: `nats: nats: API error: code=400 err_code=10165 description=invalid per-message
TTL`, surfaced to the caller as a generic "failed to acquire lock" wrap of that raw server error.

**Root cause:** JetStream KV's per-key TTL (`jetstream.KeyTTL`, this phase's own new mechanism for
honoring the `ttl` argument at all, LESSONS_LEARNED.md) requires whole-second granularity server-side; nothing in this
package validated that before handing an arbitrary caller-supplied `ttl` to the server, so the only
feedback for a too-small-but-positive value was a raw, code-and-number API error several layers removed
from anything a caller could act on without reading NATS's own error code table.

**Fix:** applied. A new `minPositiveTTL = time.Second` check in `natsLockManager.tryAcquireOnce` rejects a
positive `ttl` below this with a clear, immediate domain error, before any network call. Confirmed
empirically, not assumed: a real `nats:2.11` container accepts `ttl == 1*time.Second` and rejects every
tested value below it (500ms, 100ms, 1µs, 1ns) with the exact same server error this fix now preempts.
`inProcessManager` deliberately does *not* gain the same floor (`validateTTL`, shared by both adapters,
only rejects negative `ttl`): a pure in-memory map has no external granularity constraint to honor, and
imposing a NATS-specific quirk on it would cost real test speed for no genuine correctness benefit. This is
a real, stated adapter asymmetry, not an oversight (`conformance_test.go`'s own doc comment names it).

**Lesson:** a distributed store's own operational limits (granularity, minimum/maximum values, format
constraints) are real API surface even when nothing in a client library's type system enforces them; fuzz
inputs land on these edges far faster than hand-written test cases do, and the fix belongs at the boundary
that knows the constraint (here, the adapter that actually talks to that specific store), not pushed onto
every caller to discover by reading a raw server error.

## 37. A key containing consecutive dots passed this package's own key validation but silently hung the server

**Symptom:** `FuzzLockAcquisition`, once fixed to be real (#35) and past #36, minimized a failing input to
the 3-character `itemID` `"..0"`: `Acquire` hung for the full 10-second test context timeout, then returned
`nats: no response from stream` -- reproduced deterministically in isolation, not a flake.

**Root cause:** this package builds the raw NATS subject `$KV.<bucket>.<itemID>` for its own TTL-refresh
publish path (`kvSubject`, needed because `jetstream.KeyValue.Update` cannot set TTL at all -- see
LESSONS_LEARNED.md). `nats.go` v1.52.0's own `keyValid` (`validKeyRe = ^[-/_=\.a-zA-Z0-9]+$`) permits any run
of dots, including two in a row, and only separately checks that the key does not start or end with one;
it never checks that a dot pair does not produce an *empty* subject token. `"..0"` passes that check
cleanly, so `kv.Create` submits a publish to a subject NATS's own token-based subject matching cannot
route into the bucket's stream at all -- not rejected, just never acknowledged, so the client eventually
times out rather than getting a clean error.

**Fix:** applied. `itemIDValid` (`nats.go`) rejects any `itemID` containing `".."` before it ever reaches
`kv.Create` or the raw-publish bypass, with a clear, immediate error. The exact failing input is pinned as
a permanent fuzz corpus regression entry.

**Lesson:** a wrapper library's own input validation is a promise about *its* interface, not a guarantee
that every value it accepts is safe for a lower-level mechanism built on top of that same input (here, a
hand-built subject string) -- `keyValid` was never wrong about what `Create` itself needs, it simply never
had to account for a caller who also constructs subjects by hand for a capability (TTL renewal) the
wrapped method does not expose. Any place this codebase drops below a library's own abstraction for a real,
justified reason inherits that library's *unstated* assumptions along with its stated ones, and those are
exactly the ones fuzzing catches and code review does not.

## 39. A canceled context raced a ticker-driven renewal call from two different directions at once

**Symptom:** `TestLeaderElection_ThreeReplicas_OnlyOneLeaderAndGracefulHandover`'s graceful-shutdown step
(canceling the leader's own `ctx`, simulating SIGTERM) logged `WARN leader election: lease renewal
failed, releasing and stepping down ... error="failed to keep alive lease ...: context canceled"` on
real runs, even though shutdown was intentional and the test's own correctness assertions (exactly one
new leader after handover) still passed every time.

**Root cause:** two distinct, independent races in `LeaderElector.Run`'s `select` loop. First: Go's
`select` does not prioritize among simultaneously ready cases, so when `ctx` is canceled at nearly the
same instant `ticker.C` fires, the `ticker.C` case can still be chosen over the `ctx.Done()` case,
dispatching a `KeepAlive`/`Acquire` call against an already-dead `ctx`. Second, independent of the
first: even when a tick legitimately fires before cancellation, the resulting network call (a NATS
publish-and-await-ack round trip) can still be in flight when `cancel()` lands concurrently, so the call
observes `context.Canceled` and returns an error indistinguishable, at the point `Run` inspects it, from
a genuine store-side renewal failure.

**Fix:** applied. The `ticker.C` case now checks `ctx.Err() != nil` first and defers to the next loop
iteration if so (closing the first race: `ticker.C` cannot be ready again for a full `electionInterval`,
so the next `select` only has `ctx.Done()` to choose from). The `KeepAlive` failure branch separately
checks `ctx.Err() != nil` after the call itself fails and, if so, takes the same graceful
release-and-return path the `ctx.Done()` case takes, without that path's own deliberate absence of a
`slog.Warn` (closing the second race). The equivalent check (`case ctx.Err() != nil:`) was added to the
`Acquire` failure switch for the same reason.

**Lesson:** LESSONS_LEARNED.md #40.

## 40. A benchmark's own container teardown was measured as part of the call it was timing

**Symptom:** `BenchmarkLeaderElectorKeepAlive`'s first real run against a real `nats:2.11` container
reported ~100ms-2s per op (`b.N` between 1 and 100 across several `-benchtime` values) for a call
independently confirmed, by wrapping each individual `KeepAlive` call in its own `time.Now()`/
`time.Since`, to take a consistent ~0.7-0.9ms.

**Root cause:** `go test -bench` measures elapsed wall time from `b.ResetTimer()` to the benchmark
function's own return. The function used `defer natsContainer.Terminate(ctx)` and `defer mgr.Close()`
for its own container teardown (the same shape `engine.Scheduler`'s own, now-deleted,
`BenchmarkSchedulerKeepAlive` used). Go's `defer` statements execute during the function's return
sequence, before the function is considered to have finished returning to its caller, so several
seconds of real container-stop/terminate work fell inside the exact window `go test -bench` was
measuring, and got divided by `b.N` and added to every reported op.

**Fix:** applied. Both calls now register via `b.Cleanup` instead of `defer`; `b.Cleanup`-registered
functions are documented to run strictly after the benchmark's own measurement completes, unlike a
naked `defer`. `internal/lock/nats_bench_test.go`'s own `startBenchNats` helper already used this
correct pattern; this benchmark was rewritten to match it, and now reports ~685-703µs/op, consistent
with the independent per-call control measurement and with Phase 3's own previously recorded
`BenchmarkLockKeepAlive` figure (~0.76ms) for the identical underlying call.

**Lesson:** LESSONS_LEARNED.md #41.

## 41. A new enum field had a Go-to-string direction but no string-to-Go direction, so a runbook could not actually author it

**Symptom:** manually running the real `pleiades` binary end to end against this phase's own new
`lock_acquisition` field (AGENTS.md's Rule 0: the manual check is evidence, not the automated suite alone)
-- writing `lock_acquisition: all_at_plan_time` in a real `.yaml` runbook and running `pleiades validate`
against it would have failed to decode (caught before it shipped, by testing the actual authoring path
before considering the checklist item done, not by a written test that happened to already assume the
right shape).

**Root cause:** `engine.AcquisitionStrategy` (`lock_acquisition.go`) was given a `String()` method (Go
value to human-readable text, used in error messages and logs) but no `UnmarshalYAML`/`UnmarshalJSON`
(human-readable text back to a Go value), the direction a runbook author's own YAML file actually needs.
Every unit and integration test added earlier in this phase constructed the DAG from a JSON payload with
the raw `int` value already correct, matching the executor's own read path, so nothing exercised what a
human would actually type until the real CLI was driven with a hand-written runbook file.

**Fix:** applied. `ParseAcquisitionStrategy` (String's inverse) plus `UnmarshalYAML`/`UnmarshalJSON`/
`MarshalYAML`/`MarshalJSON` on `AcquisitionStrategy`, mirroring `pkg/inventory.LifecycleState`'s own
`String`/`ParseLifecycleState` pair. Every test that had encoded the field as a raw JSON int was updated to
the string form a real runbook uses, and a new round-trip test covers both YAML and JSON in both
directions. Proven against the real binary afterward: `pleiades validate` and `pleiades run` both accept
`lock_acquisition: all_at_plan_time` in a real runbook file today.

**Lesson:** a `String()` method proves a type can explain itself in output; it says nothing about whether
it can be authored in input, and a test suite built entirely from already-correctly-shaped payloads (JSON
built by the test author, not decoded from a file a human would actually write) can stay green while the
literal, spec-named YAML key name for the field is unusable. For any field meant to be hand-authored in a
runbook, the manual end-to-end check must include writing the field the way a human actually would, not
just confirming the executor behaves correctly once a correctly-shaped value already exists in memory.

## 42. A composition root's new fail-closed startup requirement broke an existing test that spawns the real binary as a subprocess

**Symptom:** `TestControllerLeaderElection_ReleaseGate` (`cmd/controller`, Phase 4's own Release Gate,
spawning three real `controller` OS subprocesses) started failing after Phase 5's envelope encryption
wiring landed: "no process printed the acquired-lease message within 15s." `go test ./... -race
-count=1` run repo-wide caught this; it was not visible from `internal/crypto`'s own package tests,
which never touch `cmd/controller` at all.

**Root cause:** `cmd/controller/main.go` now calls `log.Fatal` at startup if `MASTER_ENCRYPTION_KEY` is
unset or malformed (a deliberate fail-closed choice, see this phase's own plan: a server composition
root must not silently generate-and-persist a local key the way `internal/credential`'s Walk-tier
fallback does). `startController` (the test's own subprocess launcher) set `NATS_URL`, `DB_PATH`,
`LISTEN_ADDR`, and `JWT_SECRET` in each spawned process's environment, but had no reason to know about
an env var that did not exist when it was written (Phase 4). Every one of the three subprocesses hit the
new `log.Fatal` before `main` ever reached the leader-election code, so none of them ever printed the
message this test scrapes for.

**Fix:** applied. `startController` gained a `masterEncryptionKey` parameter, set as
`MASTER_ENCRYPTION_KEY` in the subprocess environment; the real test constructs it as a base64-encoded
32-byte value (not a real credential, matching the existing `jwtSecret` constant's own "well-formed but
not real" convention), the same shape an operator's real env var would be.

**Lesson:** LESSONS_LEARNED.md #43.

## 43. A property named the same as the encryption marker key silently bypassed encryption of the entire map

**Symptom:** none observed in production; found by a dedicated multi-lens adversarial security review of
Phase 5's new envelope encryption code (a Workflow-orchestrated review, not a human bug report), before
any code shipped with the defect.

**Root cause:** `internal/crypto/device_hook.go`'s `encryptPropertiesMap` treated the mere *presence* of
`EncryptedKeyMarker` ("_encrypted") as a key in a `Device.properties` map as proof the map was already
encrypted, and returned it unchanged (its documented "double-encryption failsafe"). A caller-supplied
property literally named `_encrypted`, sitting alongside real secrets in the same map (for example
`{"_encrypted": "x", "password": "hunter2"}`), tripped this failsafe and caused the ENTIRE map, including
the real secret, to be persisted completely in the clear. No error was returned; the write silently
succeeded.

**Fix:** applied. `isAlreadyEncryptedShape` now requires the exact shape `encryptPropertiesMap` itself
produces: the marker must be the map's only key, and its value must be a string. A map containing more
than the marker alone is always treated as ordinary plaintext and encrypted as a whole, even if one of
its keys happens to share the marker's name. The read side (`decryptPropertiesMap`) deliberately keeps
the original, more lenient presence-only check: there, treating any marker-bearing map as "try to
decrypt this" is the correct fail-closed behavior, so a corrupted or wrongly-shaped stored value still
fails loudly with a clear error instead of being silently passed through as plaintext.
`TestDeviceEnvelopeProperties_ColludingPropertyKeyDoesNotBypassEncryption` is the regression test.

**Lesson:** LESSONS_LEARNED.md #44.

## 44. A batch query's decrypt-error handling aborted the entire result set on the first bad row

**Symptom:** none observed in production; found by the same adversarial review as #43, by tracing a
realistic failure scenario (one corrupted or unrecognized-key-version row) against the actual code, not
by a failing test.

**Root cause:** `internal/crypto/device_hook.go`'s `DeviceEnvelopePropertiesInterceptor` returned a
decrypt error directly from inside its `[]*ent.Device` loop, discarding the entire query result the
moment any single row failed to decrypt. This broke every other, perfectly healthy device in the same
query, including `RotateDeviceProperties`'s own opening listing query (`rotate.go`), which could then
never again make progress on any row once a single row became unreadable -- the application's own read
path could no longer even reach the bad row to fix it. Tracing `ent`'s generated code
(`internal/ent/device_query.go`, `internal/ent/client.go`) further showed `Get`/`Only` are themselves
implemented as `Limit(2).All(ctx)`, so this interceptor never sees a bare `*ent.Device` through any real
caller; a single-result caller was exposed to the identical risk, not a separate one.

**Fix:** applied. The interceptor now logs (`slog.Warn`) and continues past a row's decrypt failure
instead of returning the error, leaving that row's `Properties` in its raw, still-encrypted shape.
`isAlreadyEncryptedShape` (introduced for #43) lets downstream code, notably `RotateDeviceProperties`,
recognize that shape and skip it rather than attempt a pointless re-write.
`TestDeviceEnvelopeProperties_DecryptFailureIsIsolatedNotFatal` is the regression test.

**Lesson:** LESSONS_LEARNED.md #45.

## 45. A key-rotation write had no compare-and-swap, silently losing a concurrent legitimate write

**Symptom:** none observed in production; found by the same adversarial review, by tracing
`RotateDeviceProperties`'s write against a concurrent domain write to the same row.

**Root cause:** `internal/crypto/rotate.go`'s `RotateDeviceProperties` wrote each row's re-encrypted
properties via `client.Device.UpdateOneID(row.ID).SetProperties(...).Save(ctx)`, unconditionally, with no
check that the row's stored `version` column still matched what was read moments earlier -- unlike
`internal/inventory/ent_save.go`'s own `entRepository.Save`, which conditions its update on
`device_id`+`version` for exactly this reason. A legitimate domain write (an `AddInfo`+`Save` call)
landing between `RotateDeviceProperties`'s read and write was silently overwritten and lost, while the
stored `version` column was left describing the concurrent writer's content rather than the properties
this function had just persisted -- desynchronizing the optimistic-concurrency counter from the row's
actual content.

**Fix:** applied. The write is now `client.Device.Update().Where(device.IDEQ(row.ID),
device.VersionEQ(row.Version)).SetProperties(row.Properties).Save(ctx)`, the same compare-and-swap shape
`entRepository.Save` uses, deliberately without also setting `Version`: re-wrapping a DEK under a new KEK
is not a domain content change, so the counter must stay exactly what it was. A row that loses the CAS
(`affected == 0`) is skipped and logged, not overwritten and not treated as an error -- it stays on its
current key version until a later rotation pass picks it up, safe as long as that version's key remains
configured.

**Lesson:** LESSONS_LEARNED.md #46.

## 46. An exclusive file create still left a window where a concurrent reader could observe an empty file

**Symptom:** `TestResolveKey_ConcurrentGenerationConverges` (a new test written specifically to probe
this) failed for real on its first run against an intermediate fix: `key from
.../resolve.key must decode to exactly 32 bytes, got 0`.

**Root cause:** the first fix attempt for a real TOCTOU race in `internal/crypto/key_resolve.go`'s
`generateAndSaveKey` (concurrent callers each generating a different key when no key file exists yet, only
the last writer's surviving on disk) used `os.OpenFile(keyPath, O_WRONLY|O_CREATE|O_EXCL, 0o600)`,
believing exclusivity alone was sufficient. It is not: `O_CREATE|O_EXCL` makes the file appear
(empty) atomically, but the subsequent `Write` is a separate step, leaving a real window where a
concurrent reader (another caller that lost the `O_EXCL` race and fell back to reading the file) can
observe the file already existing but not yet containing its key.

**Fix:** applied. Content is now written to a temp file in the same directory first (`os.CreateTemp`),
fully written and closed, and only then committed to the final path via `os.Link` (not `os.Rename`,
which would silently replace an existing destination instead of failing so a loser can detect it and
defer to the winner). No reader can observe the destination path in a partially-written state: it is
either absent or fully written, never in between. Verified with `-race -count=20` against
`TestResolveKey_ConcurrentGenerationConverges` (16 concurrent goroutines each run) after the fix, clean.

**Lesson:** LESSONS_LEARNED.md #47.

## 47. A key-rotation pass ran synchronously before the HTTP server started listening

**Symptom:** none observed in production; found by the same adversarial review, citing the real Helm
chart's own liveness/readiness probe configuration (`helm/the-pleiades/values.yaml`) against the actual
startup ordering in `cmd/controller/main.go`.

**Root cause:** `ROTATE_ENCRYPTION_KEYS=true`'s original wiring called `crypto.RotateDeviceProperties`
synchronously, strictly before `repo`/`bus`/`router`/`srv` were constructed and before
`srv.ListenAndServe()` ever ran, with no timeout on the `ctx` it used. On a fleet large enough (or under
ordinary DB latency) that the full, sequential, un-batched rotation loop exceeded a default Kubernetes
liveness probe's failure threshold, nothing was yet bound to `listenAddr` -- including the liveness probe
itself -- so the kubelet would kill the pod before rotation, or the server, ever completed, and the new
pod would restart into the same blocking rotation again: a crash-loop that never converges.

**Fix:** applied. The rotation call now runs in its own goroutine, started immediately after (never
before) the `go func() { srv.ListenAndServe() }()` call, so the HTTP server -- and its liveness/readiness
probe -- is already serving while rotation proceeds concurrently in the background. A rotation failure is
now logged (`slog.Error`), not `log.Fatal`: a partially rotated fleet is a degraded-but-fully-functional
state (every not-yet-rotated row still decrypts correctly through the previous-key slot), not a reason to
tear down an already-serving process.

**Lesson:** LESSONS_LEARNED.md #48.

## 48. A classification path resolver rejoined its whole prefix on every level, making it quadratic in path length with no bound on that length

**Symptom:** none observed in production; found by an independent adversarial review during Phase 6
("Inventory Factory & Hydration"), which benchmarked the real function directly rather than reasoning
about it in the abstract.

**Root cause:** `internal/classification/rule.go`'s `RuleSet.Classify` walked every prefix of its input
`path` (root to leaf) looking for a matching rule at each level, and rebuilt the lookup key for each
prefix from scratch via `strings.Join(path[:i], ".")` inside the loop. Total cost was O(n^2) in
`len(path)`, with `validateSegments` checking only that `path` was non-empty and that each segment
matched a character-shape regex, never an upper bound on segment count. The adversarial review measured
this directly against the real function: ~4.7ms at 1,000 segments, ~1.58s at 20,000, ~40s at 100,000.
Both of this phase's own new input boundaries feed `path` straight into this loop with nothing to stop a
caller from supplying an arbitrarily long one: a hand-edited `inventory.yaml`'s `HostSpec.Classify` list,
and `add-host --classify`'s comma-split CLI flag value. A few hundred KB of YAML or a single CLI argument
(both far under any pre-existing size limit anywhere in this codebase) was enough to hang `pleiades
validate`/`run`/`add-host` for tens of seconds to well over an hour, depending on segment count.

**Fix:** applied, two independent changes rather than one. `Classify` now builds the key incrementally
with a `strings.Builder` (one segment appended per iteration; `Builder.String()` returns the accumulated
bytes without copying, and Go strings are immutable, so each intermediate prefix string captured into a
`Layer.Name` stays valid even as later iterations keep appending to the same builder), making the loop
O(n) total instead of O(n^2). Independently, `validateSegments` now also rejects a path longer than a
new `maxPathSegments` (64, generous headroom over PLAN.md Section 6d's own 3-4 level worked example), so
a future change to `Classify`'s algorithm that reintroduced worse-than-linear scaling could not reproduce
the same unbounded blowup on its own. `TestClassify_PathTooLongErrors` and `BenchmarkClassify_Depth`
(confirming roughly linear, not quadratic, scaling up to the new maximum) are the regression evidence;
neither the existing benchmark (a fixed 3-segment path) nor the existing fuzz corpus (topping out at 200
segments) had exposed this before the adversarial review measured it directly.

**Lesson:** LESSONS_LEARNED.md #49.

## 50. A shared CLI positional-argument parser silently swallowed a real flag as a bare boolean flag's value

**Symptom:** `runForgeNewCollection([]string{"pkg.apt.install", "--capabilities", "AptCapable",
"--transports", "ssh", "--requires-elevation", "--engine-version", ">=1.0.0"})` failed with `unexpected
extra argument: ">=1.0.0"`, even though every flag was spelled correctly and `--engine-version` was
given a value.

**Root cause:** `cmd/pleiades/addhost.go`'s `splitPositional` (shared by `add-host`, `add-credential`,
and, as of Phase 33, `forge new-device`/`forge new-collection`) assumed every `"-"`-prefixed token
without an inline `=value` consumes the next token as its value, a premise true of every flag `add-host`
itself defines (all string-valued) but false for a bare boolean flag in its short form (`--flag`, no
following token, matching the stdlib `flag` package's own bool-flag convention). `forge new-collection`
introduced the first caller with a boolean flag (`--requires-elevation`) reachable through this shared
function: `splitPositional` treated `--engine-version` (the token immediately after
`--requires-elevation`) as that flag's "value," which pushed `>=1.0.0` into the position `splitPositional`
reads as a second positional argument. The same defect was already live, unnoticed, for
`add-credential --passphrase` (`cmd/pleiades/addcredential.go`), a pre-existing boolean flag that just
happened never to be exercised with another flag following it in any existing test.

**Fix:** applied. `splitPositional` gained a `boolFlags map[string]bool` parameter naming every flag (by
name, without its leading dashes) that takes no following value in its bare form; only a flag in that set
is exempted from the "next token is this flag's value" rule, and inline `--flag=value` continues to work
unconditionally either way. Every call site was updated: `add-host` and `forge new-device` (which define
no boolean flags) pass `nil`; `add-credential` now passes `map[string]bool{"passphrase": true}`, closing
the pre-existing latent bug; `forge new-collection` passes `map[string]bool{"requires-elevation": true}`.
`cmd/pleiades/addhost_test.go`'s `TestSplitPositional_BoolFlagTakesNoFollowingValue` is the regression
test, including a case proving the bug reproduces when `boolFlags` is omitted for a command that needs it.

**Lesson:** LESSONS_LEARNED.md #54.
