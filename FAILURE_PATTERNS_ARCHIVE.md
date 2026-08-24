# Failure Patterns Archive

Full Symptom / Root cause / Fix / Lesson detail for every entry indexed in `FAILURE_PATTERNS.md`, same numbering. See `.AGENTS/AGENTS.md`'s Mandatory Documentation Rules for how to append here.

---

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

**Fix:** added a second Crawl-tier action, `ios_backup` -> `CiscoIOSCapable`, which only `CiscoRouter`
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

**Resolved (2026-08-06, Phase 8: RBAC & Identity Validation).** `NewJWTEvaluator` now takes an explicit
`issuer, audience string` pair and pins both via `jwt.WithIssuer`/`jwt.WithAudience`, plus
`jwt.WithExpirationRequired()` (a token with no `exp` claim was also accepted forever until this phase)
and `jwt.WithValidMethods(provider.Algorithms())` (replacing "any HMAC family" with the exact algorithm
set a given `KeyProvider`'s keys are valid for). `cmd/controller/main.go` sources both from
`JWT_ISSUER`/`JWT_AUDIENCE` env vars, non-empty defaulted so an empty issuer/audience can never
accidentally become the value being matched against. This closes the gap this entry named as its own
condition for revisiting: a real caller now exists (`cmd/controller`'s composition root), so the
previously-guessed validation is now a real one.

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
root must not silently generate-and-persist a local key the way `internal/credential`'s Crawl-tier
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

## 51. `pkg/capability`'s own Windows capability file never compiled on any non-Windows platform

**Symptom:** while wiring Phase 34's generated `windows.Server` device type, `internal/forge/catalogdata`
failed to build with `undefined: capability.NameWindows` / `NameWinRM` / `NameWindowsFeature`, even though
`pkg/capability/capabilities_windows.go` plainly declares all three, has no `//go:build` line, and
`go build ./pkg/capability/...` on its own succeeded with zero errors.

**Root cause:** the filename itself, not its content, was the problem. Go treats a source file whose name
ends in `_GOOS.go` (or `_GOOS_GOARCH.go`) as implicitly constrained to that platform, with no explicit
build tag required; `capabilities_windows.go`'s trailing `_windows` exactly matches the reserved GOOS value
`windows`, so the Go toolchain silently excluded the entire file from every build and test run on this
project's actual platform (`linux/amd64`). `go build ./pkg/capability/...` succeeded because the package
still compiled correctly *without* the file; `go list -f '{{.IgnoredGoFiles}}' ./pkg/capability` was what
finally surfaced it (`go doc`/`go list -f '{{.GoFiles}}'` both silently omitted the file's symbols too).
The three capabilities it declares (`WindowsCapable`, `WinRMCapable`, `WindowsFeatureCapable`) had existed
since Phase 32 but had never been compiled, registered via their own `init()`, or covered by any test on
any developer or CI machine building for Linux or macOS -- Phase 32's own "26 capabilities, 100% coverage"
claims for `pkg/capability` were measured against a package that silently only had 23 of them. This is the
only file anywhere in the repository whose name collides with a real `GOOS`/`GOARCH` suffix (confirmed by
searching for every `*_windows.go`/`*_linux.go`/`*_darwin.go`/etc. filename in the tree).

**Fix:** applied. Renamed the file to `pkg/capability/capabilities_win.go` (`git mv`, preserving history);
`"win"` matches no `GOOS` or `GOARCH` value, so nothing about the implicit-constraint mechanism can trigger
on it again. No code inside the file changed. `go list -f '{{.GoFiles}}' ./pkg/capability` now includes it,
and `go test ./pkg/capability/...` exercises it for the first time.

**Lesson:** LESSONS_LEARNED.md #55.

## 52. A generated Collection catalog compiled and unit-tested cleanly while being completely unreachable from the real binary

**Symptom:** `internal/validate`'s own stress test (`TestCollectionRule_StressAllCatalogNames`, built to
prove the new collection-declared-but-unimplemented validation rule against every real catalog entry)
failed every single case with "not a registered collection name" instead of "declared but not yet
implemented" -- even though all 71 generated `internal/catalog/...` packages built, and each one's own
`_test.go` (asserting `collection.Lookup` succeeds) passed in isolation.

**Root cause:** a generated Collection package's `init()` (the only thing that calls
`collection.MustRegister`) only runs once something imports that package. Running `go test
./internal/catalog/...` triggers each package's own `init()` inside its own, isolated test binary, so its
own test always finds itself registered -- proving nothing about any other binary. Nothing in this
codebase, including the real `pleiades` binary, ever imported any `internal/catalog/...` package for any
other reason (confirmed by a repo-wide grep before this fix), so `pkg/collection`'s registry was, in
every real context except a catalog package's own test, completely empty. This is structurally identical
to entry #27 above (a `database/sql` driver whose only real registration happened to be a test import):
per-package tests all green, real binary silently broken, with no failing test anywhere to say so, because
nothing had reason to import the aggregate outside a test.

**Fix:** applied, before anything shipped, not after: added `internal/catalog/builtins.go` (regenerated by
`tools/gencatalog` on every run, from `internal/forge/catalogdata`'s own entries, deduplicated and sorted
-- never hand-maintained, so it can never silently drift the way a hand-written 27-line import list
could), mirroring `internal/inventory/builtins.go`'s established pattern for device types. Added
`cmd/pleiades/catalog_builtins.go`, the one blank import that makes the real binary actually pull in that
aggregator. Added `internal/archtest/catalog_test.go`
(`TestCatalogCollections_AllRegistered`/`TestCatalogDevices_AllRegistered`) so a future catalog entry added
to `catalogdata` without regenerating `builtins.go` fails a fast, targeted test instead of silently
reproducing this exact gap.

**Lesson:** LESSONS_LEARNED.md #56.

## 53. A real end-to-end test writing into the live module tree can race an architecture test's `go list` scan of the same tree

**Symptom:** `go test ./...` (no `-race`, ordinary full-suite run) intermittently failed
`internal/archtest`'s `TestAdapterAllowlistHasNoStaleEntries` on one run and
`TestRegistryConsumerAllowlistHasNoStaleEntries` on a different run, both with `go list -json
github.com/SubjectVoidLLC/the-pleiades/internal/...: exit status 1 / cannot find package`. Both tests
passed cleanly every time when run in isolation.

**Root cause:** `go test ./...` runs different packages' test binaries concurrently. Three separate
packages' end-to-end tests (`cmd/pleiades/e2e_test.go`'s `TestCLI_ForgeNewDevice_EndToEnd`/
`TestCLI_ForgeNewCollection_EndToEnd`, and, as of this phase, `tools/gencatalog`'s
`TestGencatalog_DogfoodsRealCLI_EndToEnd`) each drive the real built `pleiades` binary to write a real,
temporary package directory under the real `internal/catalog/` or `internal/inventory/devices/` tree, then
remove it via `t.Cleanup`, per RULE 0's requirement to prove the actual CLI surface against the actual
module rather than a stand-in. `internal/archtest`'s own tests shell out to `go list -json
.../internal/...`, a live filesystem-plus-module-graph scan of that same tree. When one of the three
mutating tests' temporary directory is created or removed at the exact moment `go list` is mid-walk, `go
list` can observe a half-consistent state and fail outright rather than simply omitting the transient
package. This risk already existed with only `cmd/pleiades`'s own two e2e tests; adding this phase's third
instance of the identical pattern made it measurably more likely to actually manifest in a given run.

**Fix:** not applied. This is a real, narrow, and low-frequency race between test-suite-only I/O, not a
production code path, and every real-tree-mutating e2e test it involves is already the established,
intentional pattern for proving a CLI surface against the real module (Phase 33's own precedent, extended
here rather than invented). Building cross-process test synchronization (a shared file lock between three
otherwise-unrelated test packages) to close a rare, test-only race was judged disproportionate to this
phase's scope. `make ci`/`go test -race ./...` run cleanly on a normal single pass; a repeat run is the
existing, already-documented remedy for this class of transient failure (see this project's own prior
"run `make ci` twice to rule out a transient failure" precedent).

**Lesson:** LESSONS_LEARNED.md #57.

## 54. A real end-to-end test's fixture used a device property key the code under test never read, so a passing test proved nothing about the feature it appeared to exercise

**Symptom:** `tests/e2e/integration_test.go`'s `TestGrandIntegration` failed with "expected 2 dispatched,
got 0" the moment Phase 7 made `entRepository.GetGroup` actually filter by its `Selector`'s `GroupName`,
even though this test predates Phase 7 and had passed on every prior run, including full `-race` CI runs.

**Root cause:** the test seeded two devices with `properties["group"] = "edge"` and dispatched with
`?group=edge`, but `entRepository.GetGroup` discarded its group argument entirely before this phase
(exactly the bug Phase 7's own checklist named: "the current implementation discards its group argument
and streams the entire device table") and streamed every device regardless of what was asked for. The
`"group"` property was pure decoration: nothing in the code path this test exercised ever read it. The
test looked like it verified group-scoped dispatch and actually only verified "dispatch works when every
device happens to match, because filtering is not wired up at all."

**Fix:** applied. The test now attaches both devices to a real ent `Group` named `"edge"` via
`client.Group.Create().SetName("edge").AddDevices(rtr1, rtr2)`, the mechanism
`Selector{GroupName: "edge"}` actually filters against, matching
`internal/inventory/ent_repository_selector_test.go`'s own real-`Group` fixture pattern.

**Lesson:** LESSONS_LEARNED.md #58.

## 55. Two end-to-end tests in different packages each removed the *shared parent* directory their per-process fixtures lived under, so a parallel run deleted one test's package while it was still building

**Symptom:** `cmd/pleiades`'s `TestCLI_ForgeNewCollection_EndToEnd` failed under `go test ./...` with
`no required module provides package .../internal/catalog/test/gencatalogtest<pid>`, while passing every
time it was run alone or as its own package. The failing test name moved between runs: sometimes
`cmd/pleiades`, sometimes `tools/gencatalog`.

**Root cause:** both `cmd/pleiades/e2e_test.go` and `tools/gencatalog/main_test.go` generate a synthetic
Collection into the real module tree under `internal/catalog/test/<something><pid>`, correctly namespacing
their own subdirectory by process id, and then both registered a `t.Cleanup` that removed
`internal/catalog/test` -- the shared parent, not their own subdirectory. `go test ./...` runs separate
packages concurrently as separate processes, so whichever test finished first deleted the other's
package mid-build. The pid namespacing made the *creation* safe and did nothing for the *deletion*,
which is why the bug survived: every author correctly reasoned about collisions on the way in.

This is the same family as #53 (a real end-to-end test writing into the live module tree racing another
package's scan of it), reached from the cleanup side rather than the write side.

**Fix:** applied. Both cleanups now remove only their own pid-namespaced subdirectory
(`internal/catalog/test/<suffix>`), never the shared parent. Two consecutive full `go test ./...` runs
pass where the failure previously reproduced roughly one run in three.

**Lesson:** LESSONS_LEARNED.md #59.

## 56. A repository port had no create operation at all, so the first sync plugin had nothing to onboard a device with

**Symptom:** implementing `syncplugin.Reconcile` against `inventory.Repository` had no way to persist a
newly discovered device. `Save` exists, but it is a conditional update guarded by a version token and
short-circuits to a no-op when `current == baseVersion`, which is exactly the state a brand new item is
in, so calling it on a new device silently wrote nothing and returned nil.

**Root cause:** nothing had ever needed to create a device through the port. `pleiades add-host` writes
`hosts.yaml` directly with `ReadHosts`/`WriteHosts`, bypassing `Repository` entirely, and the ent adapter's
`Save` is an `Update().Where(...)` with no insert path. The port therefore looked complete (get, list,
save) while being write-only for devices that already existed. Two smaller gaps fell out of the same
hole: `GetByName` returned a bare formatted error for not-found, so a caller could not distinguish "this
device is new" from "the backend failed", and no accessor exposed the registry key an item was hydrated
through, so even a working `Create` could not have known what to store in the immutable `type` column.

**Fix:** applied. `Repository.Create` added and implemented on both adapters, guarded by a new
`ErrItemExists`; `ErrItemNotFound` added and wrapped by both adapters' `GetByName`; `record.Base.DeviceType`
added and read through an unexported `typed` interface, mirroring how `versioned` already keeps
`BaseVersion` out of the public `InventoryItem` contract. All four are covered by new conformance tests
running against both backends.

**Lesson:** LESSONS_LEARNED.md #60.

## 57. A file-backed repository named its own storage backend as the authoritative sync plugin, so the first real sync plugin reported every host as a conflict

**Symptom:** the static YAML sync plugin's first conformance run reported `conflict` for every host, with
the reason `device is owned by sync plugin "file"`, against a project whose inventory no plugin had ever
touched.

**Root cause:** `fileRepository.buildRecord` defaulted `Source` to `SourceAuthority{Plugin: "file"}` for any
host whose sidecar recorded no provenance. That conflated two different questions: where a device's data
is stored, and which sync plugin authoritatively owns it. Section 11's One Authority Per Item is about the
second. Naming the storage backend as the owner meant every hand-written `hosts.yaml` entry looked like it
was already claimed, and reconciliation correctly refused to adopt any of them. The default was harmless
for as long as nothing compared against it, which is why it survived until the first plugin did.

**Fix:** applied. `buildRecord` now leaves `Source` zero when the sidecar recorded none, which is the honest
answer for "provenance was never recorded" and the value reconciliation already treats as adoptable. The
conformance test that pinned the old behavior was strengthened rather than deleted: both backends now
round-trip a real, caller-supplied plugin name, which is a stronger claim than the asymmetric one it
replaced.

**Lesson:** LESSONS_LEARNED.md #61.

## 58. A read-only guard that failed loudly turned a dry run into an abort on the first device

**Symptom:** `pleiades inventory sync --plugin catalyst_center --read-only` against the real DevNet sandbox
exited with `refusing to create sandboxdnac.cisco.com: inventory is open read-only` after processing one
device, having reported nothing about the other four.

**Root cause:** `NewReadOnlyRepository` was built to fail loudly, which is right: a component that must not
write should be told so rather than silently ignored. But `--read-only` on a sync is a *dry run*, and a user
asking what would change gets no answer from an abort. Both behaviors are legitimate; the mistake was
assuming one mechanism could serve both without the caller in between deciding which it wanted.

**Fix:** applied. The guard still refuses every write. `syncplugin.Reconcile` now catches
`ErrInventoryReadOnly` specifically and records `would add` / `would update` rather than failing the sync,
so the same guard produces the simulate-first proof with no second code path that could drift: every
decision above the write is identical, and only the final write is intercepted.

**Lesson:** LESSONS_LEARNED.md #62.

## 59. A migration-generation tool's zero-option schema diff silently never emits `DROP COLUMN`

**Symptom:** Phase 8 removed `User.role` from `internal/ent/schema/user.go` (the orphaned-permission
anti-pattern PLAN.md Section 18.2 forbids, replaced by Team-bound `RoleBinding`s), then ran
`internal/ent/migrate/gen/main.go` to generate the incremental migration. The generated
`0003_add_rbac_teams.sql` would have added the new `teams`/`role_bindings`/`team_users` tables but left
the `users.role` column behind, silently: no error, no warning, a schema that visibly still diverged from
`internal/ent/schema/user.go` after the "correct" tool ran.

**Root cause:** `gen/main.go`'s `client.Schema.WriteTo(ctx, &buf)` call passed zero `MigrateOption`s. ent's
own default for `WithDropColumn` is `false` (`entgo.io/ent/dialect/sql/schema`), a deliberate safety choice
in ent itself so an accidental field removal cannot accidentally drop a column carrying real data. That
default is correct for ent in general and wrong for silence: nothing in `gen/main.go`'s own output said a
column was being deliberately skipped, so the omission looked identical to "the tool correctly detected
no change needed here."

**Fix:** applied. `gen/main.go` now calls `client.Schema.WriteTo(ctx, &buf, schema.WithDropColumn(true))`,
with an inline comment explaining why. Verified by inspecting the regenerated `0003_add_rbac_teams.sql`
directly: it now includes the SQLite rename-table-recreate-copy sequence that drops `role` from `users`
(ent's own diff engine chose that approach over a native `ALTER TABLE ... DROP COLUMN`, even though the
bundled SQLite 3.53.4 supports the native form).

**Lesson:** a "regenerate the migration and trust the tool" workflow is only as trustworthy as the tool's
own default options. A schema diff tool that defaults to *not* emitting a destructive statement is right
to default that way, but the caller must still verify a schema *removal* actually produced the DDL it
implies, not just that the tool exited zero - the same "correct code, empty result, still wrong" trap
`.AGENTS/AGENTS.md`'s "Always run a control first" guidance names for `gopls` queries, here for a codegen
tool instead.

## 60. A `coverage-floor.json` regression check flagged four packages this phase never touched

**Symptom:** `go run ./tools/coverage-check`, run as part of Phase 8's own Release Gate verification,
reported `internal/forge/genutil` (75.0% vs. a 96.0% floor), `internal/inventory/record` (29.5% vs. 30.0%),
`pkg/collection` (91.7% vs. 100.0%), and `tools/gencatalog` (70.3% vs. 70.8%) all below their recorded
floors - none of which Phase 8's diff touches at all (`git status` confirms zero changes to any of the
four).

**Root cause:** not investigated past confirming it predates this phase, which is the actual point of this
entry. A `git worktree add --detach` checkout of this branch's base commit (before any Phase 8 change)
measured the identical four percentages for the identical four packages, proving the drift already existed
and this phase did not introduce, worsen, or trigger it.

**Fix:** not applied, deliberately, and not this phase's to apply. Lowering these floors to match reality
would silently hide a real regression from whatever change actually caused it (`coverage-floor.json`'s own
header: "floors only rises over time... never silently down"); fixing the underlying test coverage in four
unrelated packages is scope creep this phase's own RBAC/Identity Validation mandate does not cover. Recorded
here, plainly, so the next session that runs `make ci` and sees it fail does not spend time re-deriving
that Phase 8 is not the cause.

**Lesson:** before treating any red `make ci`/`coverage-check` result as "this session's problem to fix,"
check whether it predates the session's own diff - a real, cheap way to do that (not a memory or a guess)
is a disposable `git worktree add --detach <base-commit>` and re-running the identical check there. The
same class of finding this project already accepts for flaky container tests (`internal/lock`,
`internal/transport/ssh`, and, this session, `internal/event` and `tests/e2e` under heavy concurrent Docker
load) generalizes to coverage drift: a pre-existing gap discovered during verification is worth recording
honestly, not silently absorbed into "this session's own regressions" or silently ignored.

## 61. A full concurrent `go test ./...` run reliably flakes on containerized-dependency packages in this sandboxed environment, and reducing package-level parallelism reliably fixes it

**Symptom:** Phase 9's own verification (`internal/engine`-only diff, zero changes to any of the packages
below) ran `go test ./... -race -count=1` and separately `go test ./... -cover -count=1` (the exact
command `tools/coverage-check` shells out to). Each full run failed a different, non-overlapping subset
of: `internal/event` (`TestNatsBus_SurvivesConnectionSeverance`, `TestNatsBusPublish_DistinctEventsBothStored`),
`internal/lock` (`TestNewNatsLockManagerRejectsOldServer`), and `tests/e2e` (`TestGrandIntegration`) - all
real-container (testcontainers-go: NATS, toxiproxy, Postgres, ryuk) tests, none touched by this phase's
diff (`git status` confirms). Every one of the four passed cleanly, every time, run individually in
isolation.

**Root cause:** `go test ./...`'s default package-level concurrency (one test binary per package, run in
parallel up to `GOMAXPROCS`) means several packages spin up their own testcontainers-managed Docker
containers at the same moment. In this sandboxed environment that contention is enough to blow past
container-ready wait timeouts and connection deadlines on an unpredictable subset each run - the specific
package that loses the race changes between runs, which is the signature of resource contention, not a
code defect. This is the same category `HANDOFF_DOCUMENT.md`'s own prior sessions already named narratively
(`internal/lock`/`internal/transport/ssh` "under parallel container load"), here isolated with a concrete,
actionable mitigation for the first time.

**Fix:** not a code fix (there is no code defect to fix). `go test ./... -p 4` (capping package-level
parallelism, distinct from `-race`'s own goroutine concurrency within a package) ran the identical full
suite clean, twice, with no failures, by reducing how many containerized-dependency packages start their
own Docker containers at the same moment. Use `-p 4` (or lower) for a full-suite run in this environment
when verifying a change that does not touch `internal/event`/`internal/lock`/`tests/e2e`/`internal/transport/ssh`
themselves, rather than repeatedly retrying the default-parallelism command and hoping for a clean window.

**Lesson:** in a resource-constrained sandbox, "flaky under `go test ./...`, passes in isolation" is not
automatically a dead end once a package is confirmed innocent (`git status` on the diff) - `-p 4` is a
cheap, real, repeatable way to get a genuine full-suite signal instead of settling for "probably fine,
retried once." Reach for it before spending further time re-deriving that a failure is the known container
category.

## 62. Two independent unbounded-recursion sites in the DAG builder scaled stack usage directly with externally-supplied input size

**Symptom:** Phase 10 (Workflow DAG Builder)'s own checklist named one specific instance ("bound the
cycle-detection recursion") but research found the same defect class in three call sites, not one:
`dag.go`'s `hasCycle` (a recursive DFS whose depth is driven by `Adjacency` path length - a large *flat*
`tasks:` list, no nesting needed at all, since `synthesizeChain` always chains a flat list into one long
line), and `tasktree.go`'s `synthesizeChain`/`collectSubtree` plus `import_tasks.go`'s
`resolveImportTasksInList` (whose depth is driven by `block`/`rescue`/`always`/`parallel`/`import_tasks`
*nesting* depth instead - a structurally different risk from the first, since a flat list has nesting
depth 1 no matter how many tasks it holds). Neither had a depth bound. `dag_fuzz_test.go`'s existing seed
corpus (a 3-level nested `block` seed) came nowhere near adversarial depth and did not catch this.

**Root cause:** every one of these three functions is a plain recursive walk with no depth accounting,
over a graph/tree shape an external caller fully controls via a runbook JSON or YAML payload. Go's
goroutine stacks grow dynamically (unlike a fixed-size C stack), so this is not a "one tiny payload
instantly crashes the process" bug the way it would be in a language with fixed-size stacks - but the
growth ceiling is still finite (1GB by default, `runtime/debug.SetMaxStack`), and exceeding it is a fatal,
unrecoverable process crash, not a catchable `panic`/`recover()`. A single request large enough to reach
that ceiling is a real but non-trivial payload (tens of MB); more realistically, several concurrent
requests each moderately deep can exhaust process memory well before any single one reaches the per-
goroutine ceiling alone. Either way this is a real resource-exhaustion vector on an externally-supplied
boundary (Phase 39's Schema/Injection Hardening categories), not merely a theoretical concern.

**Fix:** `hasCycle` converted to an iterative DFS over an explicit stack (`dfsFrame`/`dfsColor`,
`internal/engine/dag.go`), removing its recursion limit entirely rather than picking an arbitrary cap on
legitimate large runbooks - the right fix for a risk driven by input *size*, not depth.
`resolveImportTasksInList` (`internal/engine/import_tasks.go`) gained a real depth check at the top of the
function, reusing its own pre-existing `maxImportDepth` constant (renamed `maxTaskNestingDepth`, value
unchanged at 32) for both its original purpose (import-hop-chain length) and plain nesting depth. Because
`resolveImportTasksInList` runs first and unconditionally on the complete tree (`resolveImportTasks` is
`buildFromDef`'s very first step), bounding it there transitively bounds `tasktree.go`'s own recursion too
- one check, not two, for the one risk. `TestHasCycle_NoStackOverflowOnLongChain` (500,000-node chain,
`dag_internal_test.go`), `TestDAGBuilder_LargeFlatTaskListDoesNotCrash` (20,000 flat tasks through the
real `Build()` path), and `TestDAGBuilder_ExcessiveNestingRejected` (a 200-level nested payload, rejected
with a clear error; a 10-level one still builds) are the regression tests; `FuzzDAGBuilder`'s seed corpus
gained a 500-level-deep adversarial seed.

**Lesson:** when a checklist item names one instance of a recursion-depth risk, grep the same package for
the identical shape (a function recursing over the same externally-supplied tree/graph with no depth
accounting) before considering the item closed - `import_tasks.go`'s `resolveImportTasksInList` was found
this way, not named in the original item, and would have been an identical, un-fixed gap sitting right
next to the one just closed. Separately: a recursion-depth risk in Go is real even though Go's growable
stacks make it a higher bar to hit than in a fixed-stack language - state the actual mechanics (a finite
but large ceiling, a fatal, unrecoverable crash once reached, concurrent load lowering the practical
bar) rather than either dismissing the risk as impossible or overstating it as "one tiny payload always
crashes the process," a description that is accurate for other languages but not for Go's default stack
behavior.

## 63. A caller-supplied job ID was concatenated straight into a NATS subject, so `>` streamed every job's logs

**Symptom:** `GET /api/v1/jobs/{id}/logs` returned the log stream of exactly the job named by `{id}`, in
every ordinary test and every manual check, because every job ID any part of this platform mints is a
UUID and a UUID has no special meaning to NATS. Nothing looked wrong.

**Root cause:** `internal/api/logs.go`'s `StreamLogs` read `{id}` with `chi.URLParam` and checked only
that it was non-empty, then handed it to `topology.LogViewerConsumerConfig`, whose `FilterSubject` is
`topology.LogSubject(jobID)`, which is the bare concatenation `logSubjectPrefix + jobID`. NATS subject
wildcards are ordinary characters in an ordinary string: `>` matches every remaining token and `*`
matches one. A request for `/api/v1/jobs/%3E/logs` therefore built the filter subject
`pleiades.jobs.logs.>` and subscribed the caller to the live execution logs of *every job in the
system*, and `.` re-tokenized the subject so a partial ID could be widened the same way. Any
authenticated caller, holding any scope, could read every other tenant's job output. The boundary was
never validated because the only producer of job IDs is trusted (`api.Dispatcher`'s own `uuid.New()`),
which is exactly the reasoning that makes an input look safe while an attacker supplies it directly.

Two things hid it. First, `gosec` did flag the adjacent line (`G705`, the same `jobID` reaching an SSE
write) and that finding had been individually waived across three phases as a low-severity XSS question,
which framed the whole variable as a cosmetic escaping concern rather than an authorization one; nobody
followed the same variable one line up into the subject builder. Second, the only route in this
repository that reads a URL parameter at all is this one, so there was no second instance to compare
against and notice the pattern.

**Fix:** `StreamLogs` now rejects any `{id}` that does not parse as a UUID (`uuid.Parse`), with `400`,
before the value reaches `topology` or the bus. That is the boundary check, placed at the boundary: every
job ID this platform issues is a UUID, so the restriction costs nothing, and it closes the subject
injection and the waived `G705` SSE-write question in the same stroke. The 500 error path stopped echoing
the caller-supplied ID back into the response body as well; the real error goes to the structured log,
where it is just as useful and not attacker-readable.
`TestStreamLogs_RejectsSubjectInjectingJobIDs` is the regression test: a table of `>`, `*`, a wildcard
suffix, an embedded subject separator, an embedded newline, empty, and a plain non-UUID string, each
asserted to be refused *before* consumer creation (the fake JetStream it runs against answers `500` if
reached, so a `400` proves the bus was never touched) and asserted not to echo the input back.

**Lesson:** a string that is safe because of who *usually* produces it is not validated, it is lucky. The
question to ask at any boundary is not "what does our code put here" but "what happens if the caller puts
anything here," and the answer has to be traced through every consumer of the value, not just the one the
static analyzer happened to point at. Concretely: message-broker subject construction belongs on the same
mental list as SQL and shell construction, because subject wildcards are an access-control mechanism, so
injecting one is privilege escalation rather than a formatting bug. A waived static-analysis finding is
also a map of tainted data, not just a ticket to close: this hole was one line away from a finding that
had been read, understood, and dismissed three times.

## 64. A live SSE handler mutated its response header map while its own consumer goroutine was already writing the body

**Symptom:** `go test ./internal/api/ -race` failed intermittently, roughly one run in five, with a
`WARNING: DATA RACE` between `net/textproto.MIMEHeader.Set` in `StreamLogs` and `fmt.Fprintf` inside the
`Consume` callback. Runs in isolation always passed. It had been passing in CI by luck.

**Root cause:** `internal/api/logs.go`'s `StreamLogs` started the JetStream `Consume` callback *before*
setting its own SSE response headers, deliberately: a `Write` implicitly commits a 200 status, so
starting `Consume` first is what lets a consumer-creation failure still report an honest error code. The
file already knew two goroutines write to `w` and guarded that with a `writeMu`. But the header
assignments after `Consume` are not writes to `w` in the sense the mutex covers: they mutate the header
map, while the callback goroutine's first body write *reads* that same map to build the response. One
side of the race never takes the mutex because it never looked like a write at all.

The existing comment even anticipated the shape ("Consume above may already be delivering messages
concurrently by this point... nothing structurally guarantees" a gap) and drew the wrong boundary from
it, protecting the body writes and leaving the header map exposed.

**Fix:** the four `w.Header().Set` calls moved above the `Consume` call. Setting a header is not a write
and commits nothing, so the "no write before `Consume` is checked" property that motivated the original
ordering is fully preserved, while the header map is now final before any goroutine that could read it
exists. Confirmed by four consecutive clean `-race` runs of the package.

**Lesson:** "which goroutines write to this variable" is the wrong question for a `http.ResponseWriter`;
the right one is "which goroutines touch this object's state, including through methods that do not look
like writes." A mutex named for one operation invites exactly this: `writeMu` reads as covering
everything dangerous, and it covered one of the two dangerous things. When a handler hands its
`ResponseWriter` to another goroutine, everything the handler still intends to do to that writer,
headers included, must happen before the handoff, not after.

## 65. `GET /api/v1/jobs/{id}/logs` authenticated every caller and authorized none of them

**Symptom:** none, to any test written before this phase. Every existing test for this route asserted a
401 for a missing or invalid Bearer token and stopped there, which is exactly the shape of test that
cannot see this bug: it never presented a *valid* token belonging to an identity with the wrong scope,
because nothing in the route ever checked a scope at all.

**Root cause:** Phase 11's own composition root (`cmd/controller/main.go`) mounted `streamer.StreamLogs`
behind `api.AuthMiddleware(evaluator)` and stopped. `AuthMiddleware` answers exactly one question, "is
this a valid token," and places the resulting `*auth.Identity` in context; it was never wired to ask "is
this identity allowed to do this." Phase 8 had already built the real answer,
`auth.AdmissionChain`/`auth.Admission`, months earlier, and `PATTERNS.md`'s own Chain of Responsibility
entry already claimed, in writing, that "every API request is stripped, token-validated, and checked
against scope before it ever touches application logic." Grepping for a production caller of
`auth.Admission.Evaluate` outside of `internal/auth`'s own tests returned nothing: the sentence in
`PATTERNS.md` described a mechanism that existed and a wiring that did not.

The practical effect: any caller holding any validly signed token, regardless of what scopes it carried
(including an empty `Scopes` slice), could stream the live execution log of any job in the system by UUID.

**Fix:** `api.RequireScope` (`internal/api/authz.go`), mounted per route via the new declarative
`RouterConfig.Routes []Route` table, each entry naming the `auth.Scope` it requires. `NewRouter` now
refuses to build at all if a `Route` omits its `Scope` or if `Routes` is non-empty with a nil
`RouterConfig.Admission` (see item 66's own construction-time companion). `cmd/controller` wires
`auth.Admission{Chain: auth.AdmissionChain{auth.NewTokenScopeRule(evaluator)}, Recorder:
auth.NewSlogRecorder(logger)}` and declares `/jobs/{id}/logs` as requiring `auth.ScopeJobRead`.

**Lesson:** a Pattern Entry Gate that names a mechanism is not evidence the mechanism runs. `PATTERNS.md`
described `auth.Admission.Evaluate` in the present tense for months while its production call-site count
was zero; the fix here is the same one Phase 11 already drew for its own "Telemetry" phase that had no
telemetry (`HANDOFF_DOCUMENT.md`'s own "the name was aspirational" line): the Release Gate must assert the
thing the phase title claims, not a weaker thing a partial implementation already satisfies. See also
`LESSONS_LEARNED.md` #76.

## 66. An unauthorized `runbook:execute` dispatch returned HTTP 200 with a per-device failure tally

**Symptom:** none observed in production; found by the same chain audit that found item 65, while reading
every caller of `auth.Evaluator.CheckAccess`. `internal/api/dispatcher.go`'s `DispatchRunbook` called
`d.auth.CheckAccess(ctx, id, "runbook:execute")` *inside* its per-device loop and, on failure,
incremented `errCount` and `continue`d to the next device rather than returning an error response.

**Root cause:** the scope check was written as if a partial batch failure (one device down, one API call
erroring) were the case being handled, but the condition it actually checked, "does this identity hold
`runbook:execute`," is an invariant: identical for every device on every iteration of one request. A
caller with no scopes therefore received `200 {"status":"dispatched","dispatched":0,"failed":N}`, a
successful-looking response describing a request that should have been rejected outright, and the
in-loop check re-evaluated the same true-or-false fact against a `MockAuthEvaluator` up to ten thousand
times per test run (`TestDispatcher_ReleaseGate`) without ever being able to fail differently the second
time than the first.

**Fix:** removed outright rather than moved. `api.RequireScope` (item 65) now enforces
`auth.ScopeRunbookExecute` once, at the router boundary, before `DispatchRunbook` is ever invoked; an
unauthorized caller now gets `403` and reaches no device at all. The in-loop check's own test,
`TestDispatcher_UnauthorizedDeviceCountsAsFailed`, is deleted with it: the behavior it asserted (silent
per-device failure for a caller-wide condition) is exactly what item 65's fix eliminates.
`api.NewDispatcher` no longer takes an `auth.Evaluator` at all, since nothing inside it ever needed one
once the redundant check was gone.

**Lesson:** a scope check that is identical for every iteration of a loop is not defense in depth, it is
one accurate check and N-1 expensive no-ops wearing the same clothes. The tell was in the test already:
a mock that returns the same `Allow bool` on every call, asserted against thousands of iterations, is
proving a boundary condition holds at the boundary, not inside a loop that crosses it once per call and
never again. When a per-device check and a per-request check would always agree, keep the one that runs
once, at the door, per Phase 14's own now-updated checklist item (`IMPLEMENTATION.md`).

## 67. `cmd/demo` mounted a production SSE handler on a router with no auth, no tracing, and no rate limiting, and its own advertised URL had 400'd for a full phase without anyone noticing

**Symptom:** none reported; found while auditing every place `api.NewRouter` is *not* used, the same
"second unguarded entry point" question this phase's own Adversarial Pattern Justification has to answer.
`cmd/demo/main.go` built `chi.NewRouter()` directly and mounted `streamer.StreamLogs` on it with zero
middleware, rather than going through the Front Controller (`api.NewRouter`) every other binary in this
repository uses.

**Root cause:** `cmd/demo` predates `api.NewRouter` growing route registration (Phase 11) and was never
migrated. Two independent problems followed from that one omission. First, the security one: its one
route was reachable by anyone who could reach the port, with no Bearer token, no scope check, no trace,
and no metric, exactly the shape items 65 and 66 above closed everywhere else. Second, a functional one
that had been silently broken since Phase 11 landed: the demo's job ID was the literal string `"123"`,
and `LogStreamer.StreamLogs` has required a UUID-shaped `{id}` since `FAILURE_PATTERNS.md` #63, so the
binary's own printed instruction (`/api/v1/jobs/123/logs`) had returned `400` for an entire phase and
nobody had run the demo to notice.

**Fix:** `cmd/demo` now builds its router through `api.NewRouter`, wires a real, freshly generated HMAC
`auth.Evaluator` and `auth.Admission` chain identical in shape to `cmd/controller`'s, mints one real
signed admin token at startup, and prints it alongside a working `curl` command. The job ID is now a real
`uuid.New()`.

**Lesson:** a demo binary is a production binary that nobody threatens to page anyone over, which is a
reason to keep watching it, not a reason to stop. "Nothing hand-rolls a router outside `cmd/controller`"
is a claim that has to be checked by grepping for every `chi.NewRouter()` call in the repository, not by
reasoning about which binaries matter; the check found exactly one, and it was wrong in two unrelated
ways at once. Related to item 65's own root cause: a security control that lives in one composition root
is not a platform guarantee until every composition root is confirmed to use it.

## 68. Phase 39's "five real forged tokens... correctly rejected by the real `ValidateToken`" audit left no test file behind

**Symptom:** none, until asked directly whether this codebase tests the classic `alg: none` JWT forgery.
Grepping for `SigningMethodNone`, `alg.*none`, `Tamper`, `Stripped`, or `RS256` anywhere under
`internal/auth` before this entry returned nothing but production code and JWKS-rotation tests. Both
`IMPLEMENTATION.md` Phase 39 and Phase 12 state, in the past tense, that `alg: none`, an algorithm-
confusion token, an expired token, a not-yet-valid token, a tampered signature, and a stripped signature
were all run against the real `ValidateToken` and all correctly rejected. That was apparently true when
written, and nothing in this repository would have caught it becoming false the next time `jwt.
ParseWithClaims`'s option list changed.

**Root cause:** the audit was real but transient: run by hand (or by an agent, once, in a session that
did not persist it) rather than committed as a `_test.go` file. A checklist line that says a security
property was verified is not itself evidence the property still holds; without a runnable artifact, "was
tested" and "was asserted in prose" are indistinguishable to the next reader, and to CI.

**Fix:** `internal/auth/jwt_forgery_test.go` (six cases: `alg: none`, RS256-against-HMAC confusion,
expired, not-yet-valid, tampered signature, stripped signature, all run against the real
`auth.jwtEvaluator.ValidateToken`) and `internal/api/middleware_forgery_test.go` (the `alg: none` and
algorithm-confusion cases re-run through the real `api.AuthMiddleware`, closing Phase 12's own
"re-audit the full request path, not just the evaluator, once one is [mounted]" note - Phase 12 is the
first phase for which a secured endpoint actually exists to re-audit against).

**Lesson:** "audited, see Phase N" is a pointer, not a proof, unless Phase N's own diff contains a test
a future `go test ./...` will actually run. A security claim that cannot regress-test itself has already
regressed once, silently, by the time anyone thinks to ask.

## 69. `go test ./...`'s default parallelism raced a real scaffolded package's temp directory against `go list`'s own tree walk

**Symptom:** a plain `go test ./... -count=1` (the exact command `tools/coverage-check` shells out to)
failed `internal/archtest`'s `TestKnownRegistryConsumersImportPkgRegistry` with `go list -json .../...:
exit status 1: cannot find package "." in: .../internal/catalog/test/relgate<pid>`. Neither
`internal/archtest` nor `internal/catalog` is touched by this phase's diff (`git status` confirms). A
second, immediate re-run of the identical command passed clean, which is the signature of a race, not a
code defect (the same signature `FAILURE_PATTERNS.md` #61 already names for a different pair of
packages).

**Root cause:** `internal/forge/collectionscaffold`'s own release-gate test
(`release_gate_test.go`) writes a real, temporary Go package to
`internal/catalog/test/relgate<pid>/` to prove the collection scaffold's generated code actually builds,
then removes it. `internal/archtest`'s `TestKnownRegistryConsumersImportPkgRegistry` calls `go list -json
.../...`, which walks the *entire* module tree including that directory. `go test ./...`'s default
package-level concurrency runs both packages' test binaries at once with no ordering guarantee between
them, so `go list` occasionally observes the directory mid-write or mid-delete (present but not yet a
valid package, or already gone) and fails the whole walk. Confirmed unrelated to this phase by the
immediate clean re-run; not investigated further than that, per `FAILURE_PATTERNS.md` #60's own
precedent for a finding whose only relevant fact is "predates this diff."

**Fix:** not a code fix, and not this phase's to make one (the same posture #60 and #61 already took for
their own unrelated pre-existing gaps). `GOFLAGS="-p=4" go test ./...` (or `make coverage`/`make ci` run
under the same env var) caps package-level parallelism without editing `tools/coverage-check`'s own
hardcoded command, and ran clean. This is the identical mitigation #61 already established for a
different race (containerized-dependency contention); the concrete finding here is that the same
"`go test ./...` default parallelism races something," "reduce `-p`" shape now covers a filesystem race
between two ordinary, non-containerized packages too, not only container-contention.

**Lesson:** any test that walks the whole module tree (`go list ./...`, `go build ./...`, or anything
shelling out to either) is implicitly coupled to every other package that ever writes a file under that
tree during its own test, even one it shares no import with. `-p 4` (or lower) is the standing, cheap
answer for a full-suite run in this environment whenever a failure's signature is "fails sometimes, passes
in isolation, passes on immediate retry, touches a package the diff never touched" - reach for it before
re-deriving the diagnosis from scratch a third time.

## 70. Wrapping `http.ResponseWriter` silently dropped `http.Flusher`, so mounting the HATEOAS middleware would have killed the SSE log stream; the only reason it never did is that nothing ever mounted it

**Symptom:** none observed in production, and that is the finding. `internal/api/hateoas.go`'s
`HATEOASMiddleware` had passing unit tests, a benchmark, and a fuzz target, and `PATTERNS.md`'s HATEOAS
entry described it in the present tense. A probe run against the real middleware
(`TestProbe_RecorderDropsFlusher`, deleted with the code it probed) reports `Flusher=false` for a handler
running behind it, against a control of `Flusher=true` for the identical assertion with the middleware
removed. `internal/api/logs.go`'s `StreamLogs` does `flusher, ok := w.(http.Flusher)` and answers
`500 "Streaming unsupported"` when that assertion fails, so the first `GET /api/v1/jobs/{id}/logs`
served through this middleware would have returned 500 instead of a stream.

**Root cause:** `hateoasRecorder` embeds the `http.ResponseWriter` *interface*, not the concrete value
behind it. Go promotes exactly the embedded interface's own method set: `Header`, `Write`, `WriteHeader`.
Every optional interface the real writer also satisfies (`http.Flusher`, `http.Hijacker`, `io.ReaderFrom`,
`http.Pusher`) is invisible to a type assertion on the wrapper. This is not a bug in the wrapper's logic,
it is a property of wrapping: a decorator cannot forward an interface it does not know to declare, and
`net/http` deliberately made those interfaces optional and open-ended.

**Fix:** stop wrapping. Link generation moved into an encoder seam (`api.Respond`) the handler calls
directly, so a streaming handler holds the real `http.ResponseWriter` and never meets a wrapper at all.
The middleware, its recorder, and its three test files were deleted rather than repaired: forwarding
`Flusher` would have fixed one assertion and left `Hijacker` and `ReaderFrom` still broken, and a
buffering middleware is structurally incompatible with a streaming handler on the same router regardless
of how many interfaces it forwards.

**Lesson:** a middleware that wraps `http.ResponseWriter` is lossy by construction, and the loss is
invisible to every test that does not exercise the specific optional interface it dropped. Before adding
one, ask what else on the same router asserts something about its writer. When the answer is "a streaming
handler," the right shape is a seam the handler calls, not a decorator that intercepts it. A passing test
suite is not evidence here, because the interface loss is only observable from a handler that asks.

## 71. A response-rewriting middleware round-tripped every body through `map[string]interface{}`, silently corrupting integers, dropping links from collections, and clobbering handler-set keys

**Symptom:** four distinct wrong answers from one probe table (`TestProbe_NonObjectAndCollision`), all
against the real middleware:

- `{"count":9007199254740993}` was served to the client as `{"count":9007199254740992}`. The value
  changed in transit.
- A top-level JSON array, `[{"id":"a"},{"id":"b"}]`, came back byte-identical with **no `_links` at
  all**. Every collection response was silently unlinked.
- A handler that set its own `"_links":"handler-owned"` had it replaced outright, with no error and no
  log.
- A handler declaring `Content-Type: application/json; charset=utf-8`, which is a legal spelling of the
  same media type, got **no links**.

**Root cause:** the middleware buffered the handler's entire body, then `json.Unmarshal`ed it into a
`map[string]interface{}`, mutated that map, and re-marshaled. Each symptom falls out of that one
decision. `encoding/json` decodes every JSON number into `float64` when the target is `interface{}`, and
`float64` has 53 bits of mantissa, so any integer past 2^53 is rounded on the way in and the rounded
value is what gets re-marshaled. A JSON array does not unmarshal into a map, so the error branch fell
through to raw passthrough, which looks identical to success. `data["_links"] = links` is a map
assignment, which overwrites rather than conflicts. And the media-type guard was
`Header().Get("Content-Type") != "application/json"`, an exact string compare against a header that
carries parameters.

**Fix:** the encoder seam marshals the handler's own typed struct exactly once and never decodes it.
`_links` is a typed field on the response DTO (`Links []Link` with a `json:"_links,omitempty"` tag), not
a map entry, so a collision is not something to detect, it is something the type system makes
unrepresentable. Collections carry their links on the enclosing object rather than needing a top-level
array. No media-type sniffing is involved, because the seam knows it is writing JSON.

**Lesson:** decoding a body you are about to re-encode is not a transformation, it is a lossy round trip,
and everything it loses (numeric precision, key order, type fidelity) is lost silently and far from the
code that caused it. If a layer needs to add a field to a response, give it the typed value before
encoding rather than the bytes afterward. "Parse, modify, re-serialize" on data you already had in typed
form is a smell, not a technique.

## 72. A caller-controlled request path was reflected straight back into the response body as a hypermedia `href`

**Symptom:** `TestProbe_HrefReflection` issued `GET /api/v1/devices/%22evil%22` and the middleware
answered with `_links` entries whose `href` was `/api/v1/devices/"evil"`, the caller's own decoded input.
Every link the middleware emitted, including `self`, used `r.URL.Path` verbatim.

**Root cause:** `links := []Link{{Rel: "self", Href: r.URL.Path, ...}}`, and the action links repeated
the same value. `r.URL.Path` is whatever the client sent, decoded, with no relationship to any route the
server actually serves. `encoding/json` escapes `<`, `>`, `&`, and quotes on the way out, so this is not
a script-injection into the body; the defect is that the *value* of a hypermedia affordance, the thing a
client is invited to follow, was chosen by the caller rather than by the server.

**Fix:** every href is now built from the server's own matched chi route pattern with each URL parameter
re-escaped through `url.PathEscape`. The URL's structure is always the server's; only the parameter
values come from the request, and they are escaped as values. A path that matched no route produces no
links rather than a link to itself.

**Lesson:** the same rule #63 drew for NATS subjects applies to any string the server hands back for a
client to act on: a value is not safe because of who usually produces it. `r.URL.Path` is caller input
that happens to look like server output, which is the most confusing possible shape for a tainted value,
and the matched route pattern is the untainted thing that was available the whole time.

## 73. The authorization generator's error was discarded, so a policy-backend outage would have been indistinguishable from a caller who is legitimately allowed to do nothing

**Symptom:** not reachable today, because the only implementation of the port was a test mock that
cannot fail. It becomes reachable the moment any real implementation exists, which is what this phase
builds.

**Root cause:** `allowedMethods, _ := generator.GetAllowedMethods(r.Context(), r.URL.Path)`. On error,
`allowedMethods` is nil, the loop that appends action links runs zero times, and the response is a
well-formed `200` carrying only a `self` link. A client, or a UI deciding which buttons to render, sees
exactly what it would see for a correctly-evaluated caller with no permissions. The two facts are
different and the wire could not tell them apart.

**Fix:** the error is handled. On a generator failure the response omits the `_links` key entirely and
logs at `Error`. An absent `_links` means "could not be computed"; an empty `_links` array means
"computed, and you may do nothing." Those are now distinct on the wire, and a client that hides every
action on an absent array is choosing to fail closed rather than being told a falsehood.

**Lesson:** `x, _ :=` on an authorization decision converts an outage into a silent denial, which is the
one failure mode that looks exactly like correct operation. Any port whose answer drives what a caller
is shown must distinguish "no" from "could not determine," and the wire format has to carry that
distinction or the caller cannot act on it.

## 74. A roadmap checklist item asserted a status-code bug the code did not have, and the phase that inherited it nearly fixed a symptom that does not reproduce

**Symptom:** `.SPECIFICATION/IMPLEMENTATION.md`'s Phase 13 checklist read: "Fix the recorder, which
captures the status code but never forwards it, and never triggers an implicit write. Any handler
returning a non-200 status is currently reported as 200." A probe table run against the real middleware
(`TestProbe_RecorderStatusForwarding`) reports the opposite in every case: a handler writing `404` is
observed by the client as `404`, `500` as `500`, `201` as `201`, and a handler that only writes a body as
`200`. The stated symptom does not reproduce under any input tried.

**Root cause:** `hateoasRecorder.WriteHeader` does record-without-forwarding, which is what the item's
author presumably read. But all four of the middleware's exit branches then call
`w.WriteHeader(rec.statusCode)` on the real writer before writing the body, so the status is forwarded
after the fact on every path. Reading the recorder's method in isolation gives the item's conclusion;
reading the middleware that owns it does not. The item was written from the former.

**Fix:** the item is rewritten to state that its premise was wrong, rather than checked off as though a
bug were fixed. The real defects at that boundary are #70 through #73, none of which the item named.
The code is deleted regardless, for #70's reasons, so nothing was "fixed" here and claiming otherwise
would put a false entry in the roadmap's own history.

**Lesson:** the same shape as `LESSONS_LEARNED.md` #72 ("an Expected pattern list is a draft prediction
to verify, not a mandate to satisfy"), now demonstrated for a defect claim rather than a pattern claim. A
roadmap item that describes a bug is a hypothesis with a plausible-sounding mechanism attached, and the
cost of believing it is not just wasted work: it is a phase writeup asserting it fixed something, which
is a false record that survives long after the code does. Reproduce the stated symptom first. If it does
not reproduce, that is the finding.

## 75. A JWT signature-forgery test tampered with base64 padding bits instead of the signature, so one run in sixteen "caught" a forgery that had never been forged

**Symptom:** `TestValidateToken_RejectsTamperedSignature` (`internal/auth/jwt_forgery_test.go`, added
by Phase 12) failed during Phase 13's full-suite run with "expected a tampered signature to be
rejected, got a validated identity". Nothing in Phase 13's diff touches `internal/auth/jwt.go`, the
evaluator, or that test. Re-running the test 400 times in one process passed every time, which looked
like a fluke and was not: `generateTestToken` stamps `exp` at second granularity, so all 400 iterations
inside one `go test` invocation sign the *same* claims and produce the *same* signature. The test only
varies across runs that land in different wall-clock seconds, which is why it reads as rare and random.

**Root cause:** the `flipLastChar` helper swapped the final character of the base64url signature segment
for the first *textually* different character in the alphabet, and its own doc comment claimed this
"guaranteed to decode to different bytes." It is not. An HMAC-SHA256 signature is 32 bytes, which
`base64.RawURLEncoding` encodes as 43 characters. 43 characters carry 258 bits; the signature carries
256. The final character's low 2 bits are therefore padding the decoder discards. Characters whose
values differ only in those 2 bits decode to identical bytes: 'A' (0) and 'B' (1) are one such pair, and
'A' is exactly what the helper's alphabet scan returns for any segment not already ending in 'A'.
Enumerated exhaustively over all 256 possible final signature bytes, **16 of them (6.2%) produce a
"tampered" token that decodes to the byte-identical original signature.** In those cases the token was
never altered, `ValidateToken` accepted it entirely correctly, and the test reported that as a forgery
slipping past the validator.

**Fix:** `tamperSignature` decodes the segment, flips one bit of the signature bytes themselves,
asserts the bytes actually changed, and re-encodes. Tampering with the decoded value rather than its
encoding removes the entire class of question. `TestTamperSignature_AlwaysChangesTheDecodedBytes`
enumerates all 256 final bytes and is the persisted regression test, so the property is now asserted
deterministically rather than sampled once per run.

**Lesson:** a security test that fails intermittently is not flaky infrastructure to be retried, it is a
test whose own setup is wrong, and the direction of the failure says which way. This one failed
"closed" (reporting a forgery that did not exist), which is loud. The same defect failing the other way,
a test that silently stops forging anything and passes, would have reported a validator was rejecting
attacks it had never been shown. When a helper's doc comment states a guarantee ("guaranteed to decode
to different bytes"), that guarantee is an assertion the helper should make at runtime, not a claim in
prose: prose cannot fail a build. And any test that manipulates an encoded representation to change the
value underneath it needs to verify the *decoded* value changed, because encodings with padding,
canonicalization, or case-insensitivity all admit edits that change the text and nothing else.

## 76. `DispatchRunbook`'s per-device loop read the management address from a property key ("ip") no device type in the codebase ever populates, so every real dispatch silently skipped every device while the endpoint still answered 200

**Symptom:** before this phase's rewrite, `POST /api/v1/dispatch?group=...&runbook=...`
(`internal/api/dispatcher.go`'s old, pre-Phase-14 `DispatchRunbook`) returned `200
{"status":"dispatched","dispatched":0,"failed":N}` for any non-empty group, regardless of how many real
devices it held. Nothing in the response, the logs, or the NATS traffic told a caller this apart from a
group whose runbook genuinely required no action against any device in it; the endpoint looked healthy
on every single call it ever served.

**Root cause:** the loop read `deviceIP, ok := device.Properties().String("ip")` and, on a miss,
ran `errCount++; continue` rather than dispatching, moving on to the next device without recording
which key it had gone looking for. Every concrete device type this codebase ships (`internal/inventory/devices/cisco.Router`/`Switch`,
`internal/inventory/devices/linux.Server`) stores its management address under the property key
`"host"`, read back through each type's own `SSHHost()` method; none of them has ever populated `"ip"`.
The lookup therefore missed on every device, on every call, by construction, not by misconfiguration.
Because a missing property and a genuine publish failure both incremented the same `errCount`, the
response's `failed` field could not tell a caller which one had actually happened either.

**Fix:** `internal/dispatch.Worker.HandleJobRequested`, the fan-out this phase moves entirely off the
HTTP request, reads `device.Properties().String("host")`, the key every real device type actually
populates, and `pkg/wire.DispatchPayload` renames the field itself from `DeviceIP` to `DeviceHost` so the
wire type can no longer be filled from the wrong key by construction. A device with no `"host"` property
is now recorded as its own explicit `OutcomeSkipped` task, with a reason naming the missing property,
distinguishable in the job's task list from a genuine publish failure (`OutcomeFailed`) rather than
folded into one undifferentiated counter.

**Lesson:** a property-key lookup that always misses reads, from outside the function, identically to a
lookup that legitimately found nothing to do; `errCount++; continue` on a missing property and
`errCount++; continue` on a real publish failure produce the same response shape, which is exactly how
this shipped past whatever review it got. When a wire field is filled from
`device.Properties().String(key)`, the test proving it has to plant that key under its real name on a
real concrete device type (RULE 0), not merely assert that some property produces some payload; asserting
against a fixture that hand-sets whatever key the code under test happens to read would have passed on
both the buggy and the fixed version.

## 77. The dispatch payload's `DeviceName` field was populated from `device.ID()`, not `device.Name()`, so a dispatched device's own display name never actually reached the wire

**Symptom:** nothing failed a build or a type check: `device.ID()` and `device.Name()` both return
`string`-shaped values, so a field named `DeviceName` silently accepted whichever one a call site handed
it. The defect was only visible by comparing what `DeviceName` actually held against
`pkg/inventory.InventoryItem.Name()`'s own documented meaning, not by reading the field's type or its
JSON tag.

**Root cause:** the old handler (`internal/api/dispatcher.go`, pre-Phase-14) wrote `deviceName :=
string(device.ID())` and used that one local both as the payload's `DeviceName` field and as half of the
event's idempotency key (`jobID+":"+deviceName`). `pkg/inventory.InventoryItem` declares `ID()` and
`Name()` as two distinct methods with two distinct meanings; nothing in the type system enforced that a
field named `DeviceName` actually came from `Name()`, so the plausible-looking `device.ID()` call
compiled cleanly, matched no test's assertion, and shipped.

**Fix:** `pkg/wire.DispatchPayload` carries `DeviceID` and `DeviceName` as two separate fields, and
`internal/dispatch.Worker` populates each from its own matching accessor (`string(device.ID())` and
`device.Name()`, respectively), so a publisher has a distinct place to put each value rather than one
field two different call sites could each be tempted to fill from whichever accessor happened to
compile. `TestWorker_HandleJobRequested_DispatchesHealthyDevice` (`internal/dispatch/worker_test.go`),
whose own doc comment names it "the direct regression test for the original ID-into-Name bug", builds a
fixture device whose id (`"dev-id-123"`) and display name (`"router-display-name"`) deliberately differ
and asserts the published payload's `DeviceID` and `DeviceName` are both correct and distinct from each
other: a fixture where the two values happen to coincide would pass under both the old, wrong code and
the fix, and would prove nothing.

**Lesson:** two accessors with related but different meanings (`ID()` vs `Name()`) landing in one call
site's local variable (`deviceName := device.ID()`) is a self-inflicted trap: the variable's own name
asserts a fact its initializer contradicts, and every later read of that variable inherits the false
assertion silently, with nothing left in the code to notice. Give the two values their own fields and
their own accessors from the start, and write the regression test against a fixture where the two values
genuinely differ; a fixture where they coincide cannot distinguish a fix from the bug it was meant to
catch.

## 78. A runbook id read straight from an HTTP query parameter had no allow-list before it reached `filepath.Join`, so `internal/runbook`'s new filesystem-backed `Source` needed its own injection boundary built from nothing

**Symptom:** before `internal/runbook` existed, `internal/api/dispatcher.go`'s old `DispatchRunbook`
accepted a `runbook` query parameter and used it only to label an outgoing NATS payload; there was no
runbook storage behind it at all, and therefore no path-construction boundary of any kind for that string
to reach. The moment this phase gives a runbook id a real filesystem-backed lookup
(`internal/runbook.DirSource`), the same caller-controlled string that used to be inert became an input
to `filepath.Join`, with nothing yet validating it before that call.

**Root cause:** an id such as `"../../etc/passwd"`, or one starting with `/`, has no structural reason to
be rejected by `filepath.Join(dir, id+".yaml")` on its own; `filepath.Join` cleans the resulting path, but
cleaning a traversal does not stop it from resolving outside `dir`, it only normalizes the escape into a
shorter, still-escaping form.

**Fix:** `dirSource.Get` (`internal/runbook/dir_source.go`) validates `id` against `validRunbookID`, an
allow-list regex (`^[A-Za-z0-9_-]{1,64}$`) applied before any path is constructed at all, so no
caller-controlled byte ever reaches `filepath.Join`. A second, belt-and-suspenders check re-verifies that
the resolved absolute path still has `dir`'s own absolute path as a prefix; given the regex above this
branch should be structurally unreachable, and it is kept anyway as a total check on this package's one
real injection boundary, so it does not depend on `validRunbookID` never changing in some way that
reopens the gap later. `TestDirSource_Get_RejectsHostileIDs` (`dir_source_test.go`) exercises
`"../../etc/passwd"`, an absolute path, and other hostile ids directly against the real `Get`
implementation, not a mocked path-builder.

**Lesson:** a query parameter that is inert today (used only to label a message, never to touch a
filesystem) can become a real injection boundary the moment a later phase gives it somewhere to read
from, and the validation has to be built at that moment, not assumed to already exist because the string
"looked" constrained by convention. Reject before the first `filepath.Join`, not after inspecting the
joined result, and keep a second, cheap, total check downstream of it anyway: a check that depends on an
allow-list never changing is one future refactor away from silently reopening the exact gap it closed.

## 79. `dispatch.JobStore.BeginFanOut`'s idempotency guard had no way back, so a Controller crash between claiming a job and completing it stranded that job in `"fanning_out"` forever

**Symptom:** none observable in the first-shipped version's own test suite, all of which exercised a
single, uninterrupted `Worker.HandleJobRequested` call to completion. Only an adversarial review asking
"what happens if the process dies mid-fan-out" surfaced it: `internal/dispatch/job.go`'s `JobStore`
originally offered `BeginFanOut(ctx, jobID) (began bool, err error)`, a one-shot conditional update
(`state = "pending" -> "fanning_out"`) with no corresponding path back to `"pending"` or any other
revisitable state.

**Root cause:** NATS JetStream's at-least-once delivery is exactly what should let a fresh `Worker`
instance pick up and finish a job whose original handler crashed before acking. But `BeginFanOut`'s own
guard could not distinguish "a redelivery of a job someone is still actively, correctly working" from "a
redelivery of a job whose original worker died", because both cases look identical from the database's
point of view: `state = "fanning_out"`. Every redelivery, crash-caused or not, saw `began = false` and
returned immediately, doing nothing. No reaper, lease timeout, or periodic sweep existed anywhere in the
repository to revisit a `"fanning_out"` job. `GET /api/v1/jobs/{id}` on such a job never reports
`"completed"` or `"failed"`; it reports `"fanning_out"` forever.

**Fix:** `BeginFanOut` gained a `staleAfter time.Duration` parameter (`internal/dispatch/ent_store.go`)
and now reclaims a job whose `state` is `"fanning_out"` **and** whose `updated_at` heartbeat has not
advanced in at least `staleAfter`, via one WHERE-guarded conditional update
(`job.Or(job.StateEQ(job.StatePending), job.And(job.StateEQ(job.StateFanningOut),
job.UpdatedAtLTE(cutoff)))`), never a read-then-write. `RecordTask` refreshes that heartbeat (throttled
to once per minute, so an actively-progressing fan-out is never mistaken for an abandoned one).
`Worker.fanOutLeaseTTL` is the one value both the reclaim's `staleAfter` and, per #80 below, the
handler's own context deadline are derived from, so "how long before a stalled job becomes reclaimable"
and "how long before this handler gives up on its own" cannot drift apart into two different numbers
nobody reconciled. `TestWorker_HandleJobRequested_StaleFanOutIsReclaimed` reproduces the crash scenario
directly: `BeginFanOut` claims a job, the "crash" is simulated by never calling `Complete`, and a second,
independent `Worker` instance is proven to reclaim and finish it.

**Lesson:** an idempotency guard that only ever answers "has this already started" is half of a
crash-recovery story; the other half, "how do I know the thing that started it is actually dead, not just
still working", needs its own explicit answer (a heartbeat, a lease, a TTL) or the guard itself becomes
the single point of failure it was built to protect against. See #80: this fix, on its own, opened a
second, narrower gap of the identical shape.

## 80. `BeginFanOut`'s `staleAfter` reclaim (#79's own fix) was a lease with no fencing token, so a `Worker` that was merely slow, not dead, could keep writing after being reclaimed and silently corrupt the job's terminal record

**Symptom:** found by the same adversarial review pass that reviewed #79's own fix, before it ever
shipped to production. `dispatch.JobStore.Complete` and `Fail` (`internal/dispatch/ent_store.go`, now
`ent_store_terminal.go`) performed an unconditional `Job.Update().Where(job.JobIDEQ(jobID))` with no
state predicate at all, unlike `BeginFanOut`'s own guarded update right next to them.

**Root cause:** #79's `staleAfter` reclaim answers "is the original claimant probably dead" with a
timestamp alone, which cannot actually prove it: a `Worker` blocked past `staleAfter` on one slow
downstream call (a stalled `iter.Next`, a degraded NATS publish) is still alive and still running, and
nothing stops it from eventually finishing its own, now-stale view of the fan-out and calling
`Complete`/`Fail` exactly as a legitimate sole owner would. Because those two methods carried no guard of
their own, whichever of the two workers (the original, now-superseded one, or the one that reclaimed)
called `Complete`/`Fail` *last* silently won, overwriting the other's tallies and state with no error and
no record that a collision ever happened. `TestEntJobStore_Complete`/`TestEntJobStore_Fail` each called
their method exactly once, so this was invisible to the existing suite.

**Fix:** a `fence int64` column on `Job` (`internal/ent/schema/job.go`), atomically incremented
(`AddFence(1)`, inside the same conditional write, never a separate read-modify-write) every time
`BeginFanOut` claims or reclaims ownership. `RecordTask`, `Complete`, and `Fail` all now take the fence
value the caller believes it holds and condition their own write on `job.FenceEQ(fence)` (`Complete`/
`Fail` additionally re-check `job.StateEQ(job.StateFanningOut)` as a second, independent guard against
the exact double-terminal-write #79's own gap allowed). A stale fence returns `ErrFenced`, a
`errors.Is`-checkable sentinel distinct from `ErrJobNotFound`; `Worker.HandleJobRequested` stops cleanly
(logs, acks, returns `nil`) the instant it sees `ErrFenced`, rather than treating it as a failure worth
retrying, since retrying changes nothing once superseded.
`TestEntJobStore_StaleReclaimFencesOutOriginalCaller` proves both halves directly: the original caller's
post-reclaim `RecordTask`/`Complete`/`Fail` calls all get `ErrFenced`, and the reclaiming caller's own
calls, presenting the fresh fence, succeed normally.

**Lesson:** a lease (a timestamp plus a timeout) tells a second party when it is *entitled* to take over;
it does not, by itself, stop the first party from continuing to act as if it still owns what it lost. Any
lease-based reclaim needs a fencing token, a value bumped on every claim that every subsequent write must
present, or a party that is slow rather than dead can keep mutating shared state after another party has
already, correctly, taken over. The two halves of a redelivery story, "who may start" (#79) and "who may
still write" (#80), are two different guarantees and were fixed as two different, sequential findings for
exactly that reason: closing the first does not imply the second is closed too.

## 81. `wire.DispatchPayload.JobID` reached two NATS subjects by bare string concatenation on the Runner side, with no format validation, the identical shape #63 had already fixed once at the HTTP-facing boundary

**Symptom:** found while building Phase 15 (Runner Agent Scaffold)'s write-ahead-log result reporting,
before it shipped: `native.Adapter.streamLog` (pre-existing) builds `topology.LogSubject(payload.JobID)`,
and the new WAL flush path (`internal/runner/agent_wal.go`) was about to build
`topology.ResultSubject(entry.JobID)` from the identical field, both by bare `prefix + jobID`
concatenation. NATS subject wildcards (`>`, `*`) and the `.` token separator are ordinary characters in an
ordinary Go string; a `JobID` of `">"` would build the filter subject `pleiades.jobs.logs.>` and stream
every job's logs, the exact vulnerability shape `FAILURE_PATTERNS.md` #63 already found and fixed once,
at `internal/api/logs.go`'s `StreamLogs` handler.

**Root cause:** #63's own fix validated the HTTP-facing entry point (`StreamLogs`'s own `{id}` URL
parameter) but not every other place the identical value later flows to. `wire.DispatchPayload.JobID` is
a second, independent entry point for the same underlying value: `internal/dispatch.JobStore.Create`'s
own contract explicitly allows a caller-supplied `JobID` to override the generated default (documented in
`ent_store.go`), so nothing in the type system or the store's own contract actually guarantees every
`JobID` a Runner ever decodes off the wire is a server-generated UUID, even though every real producer
today happens to be one. A value being safe *in practice*, because of who happens to produce it today, is
not the same as the boundary itself being validated; #63's own Lesson names this trap directly, and this
is a second entry point falling into the identical trap the first fix did not, and could not, reach.

**Fix:** `internal/runner/agent.go`'s `handleMessage` validates `payload.JobID` with `uuid.Parse`
immediately after decoding `wire.DispatchPayload`, before the value is used anywhere else in the
function, including the call into `executeWithLease`/`adapter.Execute` (which reaches `LogSubject`
indirectly) and the WAL append/flush path (which reaches the new `ResultSubject`). A failing `JobID` is
`Term()`'d, matching the two malformed-payload branches immediately above it in the same function: a
hostile or malformed `JobID` can never become valid no matter how many times the message is redelivered.
`TestAgent_HandleMessage_RejectsSubjectInjectingJobID` (`internal/runner/agent_security_test.go`) mirrors
`TestStreamLogs_RejectsSubjectInjectingJobIDs`'s own table exactly (`>`, `*`, a wildcard suffix, an
embedded `.`, empty, a space, an embedded newline, a non-UUID string), asserting `Term`, never `Ack`,
against the real `Agent.Run`/`handleMessage` path.

**Lesson:** see `LESSONS_LEARNED.md` #84.

## 82. `executeWithLease` had no panic recovery, so a panicking `ExecutionAdapter.Execute` crashed the whole Runner process and could race its own deferred lease release against a still-running heartbeat goroutine

**Symptom:** found by an adversarial multi-agent review of Phase 15 (Runner Agent Scaffold) before it
shipped, and empirically reproduced: a temporary `ExecutionAdapter` whose `Execute` panics, run through a
real `Agent.Run`, crashed the test process outright with the panic propagating straight through
`executeWithLease` -> `handleMessage` -> `worker`, no `recover()` anywhere in the chain.

**Root cause:** `executeWithLease`'s normal return path explicitly ran `cancelExec(); <-done; return err`
as ordinary statements, not deferred code, after `a.adapter.Execute(...)` returned. A panic inside
`Execute` skips every ordinary statement that would have followed it in the same function, including that
explicit `<-done` wait, and only the two already-registered deferred calls (`cancelExec()`, and
`lease.Release(context.Background())`) still ran, in LIFO order, during the panic's own unwind. Nothing
recovered the panic itself, so it propagated past `executeWithLease` and crashed the process, taking every
other concurrently in-flight pool worker down with it before any of them could run their own deferred
`lease.Release` -- each of their devices would then stay locked in the backing `lock.Manager` (a NATS
JetStream KV store in production, which outlives this one process) for up to `execLeaseTTL`, even after a
supervisor restarted the Runner. Separately, because the panic path skipped the `<-done` synchronization
the normal path relies on to guarantee the heartbeat goroutine has stopped calling `lease.KeepAlive`
before `lease.Release` touches the same lease value, a heartbeat tick in flight at the exact moment of a
panic could race the deferred `Release` against `lock.NewNatsLockManager`'s own unsynchronized
`natsLease.revision`/`deadline` fields -- a genuine, `-race`-detectable data race reachable specifically
through this one panic path, that the already-tested normal return path does not have.
`internal/event/consumer.go`'s `handleDelivery` already wraps its own handler call in an identical
`recover()` for the identical reason (a handler this codebase does not control must not crash the process
that invokes it); `internal/runner`'s new pull-based `Agent` path never reused or mirrored that
protection.

**Fix:** `executeWithLease` (`internal/runner/agent_exec.go`) now has a single deferred cleanup function
that runs `cancelExec(); <-done` unconditionally -- on both a normal return and a panic unwind, since a
registered `defer` always runs, unlike ordinary statements after the call that panicked -- and then calls
`recover()`, converting a caught panic into an ordinary `error` return (`execErr`, a named return value)
exactly like any other adapter execution failure, so the message is still routed through the normal
Nak/DLQ path rather than the process crashing. `TestAgent_ExecuteWithLease_RecoversAdapterPanic`
(`internal/runner/agent_exec_test.go`) proves the test process survives at all (the historical bug would
have crashed it), the panicking message is Nak'd like any other execution failure, and the device lease
is genuinely released (a fresh `Acquire` for the same device succeeds immediately afterward).

**Lesson:** see `LESSONS_LEARNED.md` #85.

## 83. `executeWithLease`'s per-execution context was an unconditional child of `Agent.Run`'s own shutdown-cancelable context, so `interruptible: false` only survived a lease-heartbeat failure, never a graceful Runner shutdown -- the far more routine trigger the PLAN.md exception actually exists for

**Symptom:** found by the same adversarial review pass, before it shipped, and confirmed against the real
call chain and an already-shipped test. `cmd/runner/main.go` cancels `Agent.Run`'s own `ctx` on
SIGINT/SIGTERM (an ordinary restart or redeploy, not a network partition). `executeWithLease` built
`execCtx` as `context.WithCancel(ctx)`, a direct child of that same context, so canceling `ctx` canceled
`execCtx` too, regardless of `payload.Interruptible`. `TestAgent_Run_GracefulShutdownDrainsInFlightWork`
(already shipped, proving a different, correct property) happened to use a fixture whose JSON omitted the
`interruptible` key entirely, decoding `wire.DispatchPayload.Interruptible` to Go's own `bool` zero value,
`false` -- and that test's own passing assertion, that the in-flight execution *is* aborted on outer `ctx`
cancellation, was silently encoding the bug as intended behavior.

**Root cause:** `payload.Interruptible` was only ever consulted inside `heartbeat`'s own
`KeepAlive`-failure branch. Go's `context.WithCancel(parent)` unconditionally propagates the parent's own
cancellation to the child with no way to gate that propagation on an application-level flag, and nothing
in `executeWithLease` introduced a seam to do so. PLAN.md Section 16's own named exception,
"Un-abortable tasks (`interruptible: false`) finish execution," and `executeWithLease`'s own doc comment
promising the identical behavior, were therefore honored against exactly one of the two triggers Section
16 actually describes (a lost lease heartbeat), never against the other, more routine one (the Runner
process itself shutting down).

**Fix:** `execCtx` (`internal/runner/agent_exec.go`) is now built over `detachedValueContext{parent: ctx}`,
a small wrapper that still delegates `Value()` lookups to `ctx` (so the active OpenTelemetry span
`handleMessage` already started is still carried through) but reports itself as never canceled and having
no deadline, breaking the automatic cancellation-propagation chain `context.WithCancel(ctx)` alone cannot
break. A watcher goroutine then explicitly propagates `ctx`'s own cancellation into `execCtx` only when
`payload.Interruptible` is true, mirroring `heartbeat`'s own identical gate on the same flag, so both real
triggers (a lease-heartbeat failure and an outer Runner shutdown) now honor `interruptible: false`
consistently. `TestAgent_SelfAbort_NonInterruptibleSurvivesOuterShutdown`
(`internal/runner/agent_exec_test.go`) cancels the outer `Agent.Run` context directly, not a heartbeat, and
proves a non-interruptible execution is unaffected by it -- the scenario the existing
`TestAgent_Run_GracefulShutdownDrainsInFlightWork` could not have caught, since it exercises the opposite
(interruptible) case.

**Lesson:** see `LESSONS_LEARNED.md` #86.

## 84. `reportResult` appended to the Runner's own write-ahead log using the same context `Agent.Run`'s shutdown cancels, so a job whose execution was still in flight at shutdown had its outcome silently and permanently dropped instead of durably recorded

**Symptom:** found by the same adversarial review pass, before it shipped, and confirmed by tracing the
real call chain end to end. `cmd/runner/main.go`'s SIGINT/SIGTERM handler cancels the same `ctx` passed to
`Agent.Run`; that `ctx` reaches `handleMessage` unchanged, and `native.Adapter.Execute`'s own
cancellation-aware `sleepOrDone` (added this same phase specifically so cancellation reaches it promptly)
returns `context.Canceled` promptly instead of finishing. `handleMessage` classifies that as a genuine
execution failure and calls `reportResult(ctx, payload, execErr)` with the identical, already-canceled
`ctx`. `fileWAL.Append`'s own `if err := ctx.Err(); err != nil { return ... }` guard (a deliberate,
correct check against a genuinely stale caller context in the ordinary case) then rejected the write
outright, at exactly the moment the WAL exists to catch: the process going away with a real, already-
determined outcome that had not yet been durably recorded.

**Root cause:** `reportResult` used `ctx`, the same context object whose cancellation can be *caused by*
the very outcome it is trying to record (a self-abort, or a graceful shutdown mid-execution). Durability
work that must survive the cancellation that triggered it cannot itself be a child of that cancellation --
`executeWithLease`'s own `lease.Release(context.Background())` had already established this exact pattern
for the identical reason, one file away, but `reportResult` did not follow it.

**Fix:** `reportResult` (`internal/runner/agent_wal.go`) now performs both the `wal.Append` call and its
own eager `flushOne` attempt against a fresh `context.WithTimeout(context.Background(), walDurabilityTimeout)`
(10 seconds), never `ctx`, mirroring `executeWithLease`'s own precedent exactly.
`TestAgent_ReportResult_SurvivesShutdownDuringExecution` (`internal/runner/agent_wal_test.go`) reproduces
the exact scenario (cancel the outer `Agent.Run` context while a `WithResultWAL`-configured execution is
mid-flight) and proves the resulting `"failed: context canceled"` outcome is now durably recorded and
delivered, where it was silently lost before.

**Lesson:** see `LESSONS_LEARNED.md` #86.

## 85. The Runner's write-ahead log minted a fresh random idempotency key on every `Append` call instead of a key stable across a JetStream redelivery of the identical job, so a crash-then-redeliver-then-reexecute sequence could publish the same logical outcome twice with no dedup catching it

**Symptom:** found by the same adversarial review pass, before it shipped, and empirically reproduced with
a temporary test feeding the identical dispatch through two separate `Agent` instances sharing one WAL
directory (modeling redelivery of an already-locally-acknowledged job): two distinct `job.result` events
were received for the one `JobID`, with two different, unrelated IDs, proving no dedup collapsed them.

**Root cause:** `reportResult` (`internal/runner/agent_wal.go`) always built a bare `ResultEntry{...}`
with `ID` left at its zero value, and `fileWAL.Append`'s own `if entry.ID == "" { entry.ID =
uuid.New().String() }` fallback then minted a fresh random UUID on every single call. That ID doubles as
the outgoing publish's own idempotency key (`event.WithIdempotencyKey`), so a redelivery of the identical
dispatch -- re-entering `handleMessage` from scratch after a crash between this Runner's own local
`wal.Acknowledge` succeeding and the broker-side `msg.Ack()` landing -- called `reportResult` again with a
second, entirely unrelated random ID. `event.DefaultIdempotencyKeyDerivation`'s own doc comment
(`internal/event/dedup.go`) already names exactly this class of mistake: dedup only ever recognizes a key
it has seen before, so two different random IDs describing the logically same outcome can never collapse
into one.

**Fix:** `reportResult` now sets `ID: payload.JobID + ":" + payload.DeviceID` explicitly, a key that stays
identical across any number of redeliveries of the same dispatch, mirroring
`internal/dispatch/worker_devices.go`'s own identical `JobID+":"+DeviceID` key for the identical reason.
`fileWAL.Append` needed no change: it already honored a caller-supplied, non-empty `ID` as-is.
`TestAgent_ReportResult_UsesStableIdempotencyKeyAcrossRedelivery`
(`internal/runner/agent_wal_test.go`) runs the identical wire payload through two separate `Agent`
instances (standing in for two delivery attempts of the same message) and proves both resulting entries
share the one stable key.

**Lesson:** see `LESSONS_LEARNED.md` #87.

## 86. The DAG executor treated a task naming no target as controller-side and returned before ever consulting the resolver, so a mesh-dispatched runbook's already-chosen device never reached the Collection method

**Symptom:** found by Phase 16's own SSH mesh Release Gate
(`cmd/runner/ssh_mesh_release_gate_test.go`), running against real NATS and real sshd containers, and
by nothing else: a runbook dispatched through the full Runner mesh failed with `collection method
"net.ssh.ping": ipc collection executor requires a *wireDevice, got <nil>`. The device the dispatch
payload named never arrived at the Collection method at all. The `<nil>` in that message is `%T`
printing a nil interface value, not a `*wireDevice` of the wrong concrete type.

**Root cause:** `run.resolveDevices` (`internal/engine/executor.go`) read a task's effective target via
`TaskTarget` (the task's own `params.target`, falling back to `dag.Hosts`) and returned `(nil, nil)`
immediately when that was empty, classifying "this task names no target" as "this is a controller-side
task with no device," without ever calling `Executor.resolver`. That is correct at Crawl tier, where a
runbook's own `hosts:` key is the only way a device is ever chosen. It is wrong one tier up: in the
Runner mesh the Controller selects devices from the dispatch request's own group
(`internal/dispatch/worker_devices.go`) and fans out one `wire.DispatchPayload` per device, so the
dispatched runbook legitimately carries no `hosts:` key at all, because that choice was already made
upstream. Phase 16's `singleDeviceResolver` (`internal/adapters/native/resolver.go`) exists precisely to
supply that already-chosen device for any target string, and the early return meant it was never asked.
Every unit test in `internal/adapters/native` passed throughout, because each drives the adapter with a
fixture runbook that does carry `hosts:`; only a test exercising the real, mesh-shaped path (no
`hosts:`, device chosen Controller-side) could surface it.

**Fix:** `resolveDevices` now calls `r.x.resolver.Resolve("")` for an empty target rather than returning
early, and treats an empty result as the same controller-side task it always did, deliberately not as
the error the non-empty branch raises: "this task names no target" and "this task names a target that
matches nothing" are different conditions, and only the second is a mistake. Crawl-tier behavior is
unchanged and provably so, since `validate.WorldView.Resolve("")` matches no device Name and no Tag and
`internal/engine`'s own test `mapResolver` returns `m[""]`, so both answer empty exactly as before. The
alternative fix, having the Runner set `dag.Hosts` to the dispatched device's name, was rejected:
`internal/runbook.DirSource` serves a Flyweight-cached, shared `*engine.DAG` pointer, so mutating it per
job would be a genuine data race across concurrent jobs on one Runner.

**Lesson:** see `LESSONS_LEARNED.md` #89.

## 87. `internal/election`'s coverage was nondeterministic across identical runs, swinging from 85.0% to 100% against a fixed 90.0% floor, so `make ci` failed at the coverage ratchet on runs where no code had changed

**Symptom:** `make ci` failed at the `coverage` target on a Phase 16 branch, reporting
`internal/election: 87.5% dropped below its floor of 90.0%`, for a package Phase 16 never touched.
Re-running the identical commit produced 85.0%, 92.5%, 95.0%, 97.5% and 100% on different runs. The
failure looked like it belonged to whatever branch happened to be open at the time, which is what made
it read as "we keep pushing code that fails CI" rather than as one standing defect.

**Root cause:** five branches of `LeaderElector.Run` had no test that drove them on purpose. They were
reached only *incidentally*, as a side effect of which way the real NATS JetStream store happened to
lose a timing race inside `TestLeaderElection_ThreeReplicas_OnlyOneLeaderAndGracefulHandover`: the
`ctx.Err()` arms of both the renewal and the acquire paths, the acquire switch's `default` (store
unreachable) arm, the `slog.Warn` inside `releaseBestEffort`'s own failed-release branch, and the
`continue` guard for a tick racing cancellation. Every one of those is by construction a race arm, so
which of them executed varied with machine load. Coverage measured the outcome of a dice roll. A
loaded CI runner rolls differently from an idle laptop, which is why the failure was far more frequent
in CI than locally and why it never reproduced on demand.

**Fix:** `mockLease` gained an `onKeepAliveFail` hook and `mockManager` gained `acquireErr` and an
`onAcquire` hook, so each arm is now driven deliberately by a scripted test double instead of being
waited for. Canceling the elector's own context from *inside* the mock's `KeepAlive`/`Acquire` call is
what makes the two shutdown-race arms deterministic: `Run` re-checks `ctx.Err()` the instant the call
returns, so the cancellation is guaranteed already visible, with no sleep and no dependence on
scheduling. Five tests cover the failed-release, store-unreachable, contended, renewal-during-shutdown
and acquire-during-shutdown arms. Measured coverage went from a 85.0-100% spread to a hard 97.5%
minimum (100% when the one remaining race arm, the tick/cancel `continue`, happens to land, which is
now pure upside). The same 97.5% holds under `-short`, which skips the container test entirely, so
coverage no longer depends on Docker or on NATS timing at all. The floor was then ratcheted 90.0 ->
95.0, leaving one statement of headroom below the deterministic minimum rather than sitting on it.

The tick/cancel `continue` guard was deliberately left uncovered rather than chased: reaching it
requires `select` to choose `ticker.C` while `ctx.Done()` is also ready, which Go decides at random by
design, and the only way to force it would be injecting a clock into production code purely to satisfy
a coverage number.

**Lesson:** see `LESSONS_LEARNED.md` #90.

## 88. `internal/archtest` shelled out to a whole-module `go list` while sibling tests were creating and deleting scaffolded packages inside that same module tree, so an architecture test failed intermittently for a reason unrelated to architecture

**Symptom:** `make ci` failed at the `coverage` target, whose own `go test ./... -cover -count=1` reported:

```
--- FAIL: TestAuthtestNeverImportedByProductionCode (0.50s)
    testonly_test.go:28: go list -json -deps github.com/Subject-Void-LLC/the-pleiades/...: exit status 1
        cannot find package "." in:
        	/home/noot/auto-roboto/internal/catalog/test/relgate62784
```

Roughly one run in three, on identical code. `go test ./internal/archtest/...` on its own never
failed, and the immediately preceding `go test -race ./...` in the same `make ci` had passed. The
named directory does not exist in the repository: `relgate62784` is `relgate` plus the test process's
own PID.

**Root cause:** two tests in different packages, run concurrently by `go test ./...`, over one shared
mutable resource: the module tree itself. `internal/forge/collectionscaffold`'s `TestGenerate_ReleaseGate`
generates a scaffolded Collection into a real directory under `internal/catalog/test/relgate<PID>`,
builds and tests it, then deletes it. It writes into the live tree rather than a throwaway module
because the scaffold's *generated test* imports the package by its own `internal/...` path, which Go's
internal-package rule makes unimportable from any module other than this one. Three other tests do the
same thing for the same reason (`internal/inventory/devicescaffold`, `tools/gencatalog`,
`cmd/pleiades`). Meanwhile `internal/archtest`'s `goList` runs `go list -json -deps <module>/...`, and
`go list` matches packages in two phases: it walks and matches directories, then loads them. A
directory that held `.go` files at match time and none at load time -- precisely the window
`os.RemoveAll` opens as it unlinks a package's files -- produces `cannot find package "."`, and
without `-e` one such directory aborts the entire listing. Six of `archtest`'s own listings are
exposed, since `<module>/...`, `<module>/internal/...` and the catalog prefix all glob the scratch
paths.

A second, independent defect sat in the same place: `collectionscaffold`'s cleanup removed
`internal/catalog/test`, the *shared parent*, not its own `relgate<PID>` directory. `tools/gencatalog`
and `cmd/pleiades` write sibling packages under that same parent and run concurrently with it, so that
cleanup could delete a sibling's generated package out from under the `go build` compiling it.
`tools/gencatalog`'s own cleanup already carried a comment warning about exactly this hazard; the
sibling call site had never been brought in line.

**Fix:** `goList` now passes `-e`, so `go list` reports a per-package load failure in that package's
own `Error` field and exits 0 instead of aborting, and drops any package carrying one. This costs no
coverage: `make ci` runs `build` and `vet` over the whole module before any test, both of which fail
loudly on a package that genuinely does not load, so a committed package cannot reach `archtest`
broken; a package that fails to load only here is by construction one of the transient scaffold
directories. `collectionscaffold`'s cleanup was scoped to its own `pkgDir`. Verified by reproducing
the exact condition (a zero-byte `.go` file under `internal/catalog/test/`, which makes the bare
`go list` exit 1) and confirming `archtest` passes with it present.

**Lesson:** see `LESSONS_LEARNED.md` #91.

## 89. `internal/api`'s dispatcher Release Gate sized its wall-clock budget against a plain `go test` run while `make ci` judges the `-race` build, leaving 20% headroom on an idle machine and none under load

**Symptom:** `make ci` failed in `test-race`:

```
--- FAIL: TestDispatcher_ReleaseGate (61.20s)
    dispatcher_release_test.go:217: job ... did not reach a terminal state within 1m0s (last observed state "fanning_out")
```

followed by a cascade of `sql: database is closed` errors from the in-process event handler. Those
errors are a red herring worth naming, because they invite a hunt for a database lifecycle bug that
does not exist: `t.Fatalf` had already run the test's cleanup, closing the store while the dispatcher's
own goroutines were still fanning out. The database closing is the *consequence* of the timeout, not
its cause.

**Root cause:** the budget was a flat `60*time.Second` for a job fanned out to 10,000 devices with each
outcome recorded individually. Measured on an otherwise idle machine, that package runs in about 5
seconds without the race detector and about 50 with it -- roughly a tenfold slowdown, leaving about 20%
headroom in the best case the race build ever sees. `make ci` runs `go test -race ./...`, which is also
running a dozen other packages concurrently, several of them starting Docker containers, so the real
CI margin was negative. The number was correct for the configuration a developer runs by reflex (`go
test ./internal/api/`) and wrong for the only configuration that gates a merge.

**Fix:** `pollJobUntilTerminal` now multiplies its caller's timeout by `raceTimeScale`, a constant
defined twice under `//go:build race` / `//go:build !race` (10 and 1). Scaling inside the helper rather
than at each call site keeps a call site expressing the thing a reader can reason about -- how long
this work should take on a normal machine -- and means a newly added caller cannot forget the detector
exists; the sibling call in `dispatcher_selector_test.go`, at a much tighter 5 seconds, was covered by
the same change without being touched. The factor is the measured ratio, not a guess. The budget is a
ceiling rather than a duration, so the happy path costs nothing: the poll returns as soon as the job is
terminal.

**Lesson:** see `LESSONS_LEARNED.md` #90, of which this is a second instance in a different shape: the
first was a branch covered only when a race was won, this one a deadline sized under a configuration
CI never runs.

## 90. A legacy Ansible adapter designed against PLAN.md's own prose, without running a real ansible-playbook first, would have targeted a callback plugin that does not exist and an inventory format that fails to parse

**Symptom:** none observed in shipped code, caught before implementation began. Two design assumptions
read naturally off PLAN.md's Legacy Ansible Interoperability section and Phase 17's own Release Gate
wording ("a structured JSON event payload") both turned out to be false the moment they were tested
against a real `ansible-core 2.19.11`:

```
$ ANSIBLE_STDOUT_CALLBACK=json ansible-playbook -i "hostA,hostB," probe.yml
[ERROR]: Could not load 'json' callback plugin.
$ ansible-doc -t callback -l | grep -i json
community.general.syslog_json   Sends JSON events to syslog
```

and, separately, a plain JSON file written in the dynamic-inventory-script shape
(`{"<group>": {"hosts": [...]}, "_meta": {"hostvars": {...}}}`) and passed via `-i inventory.json`:

```
[WARNING]: Failed to parse inventory with 'yaml' plugin: Invalid "hosts" entry for "catalyst_lab" group,
requires a dictionary, found "<class 'ansible.module_utils._internal._datatag._AnsibleTaggedList'>" instead.
...
[WARNING]: No inventory was parsed, only implicit localhost is available
```

**Root cause:** the `json` stdout callback was removed from Ansible core at the 2.10/2.11 collection
split and never carried into `community.general`; it survives only in the long-EOL, monolithic
`ansible==2.9` package, which nothing in this repository's stated support range targets. Separately,
the dynamic-inventory-script JSON contract (`_meta`/flat groups) is for an *executable* inventory
script Ansible runs and captures the stdout of; Ansible's real plugin auto-detection routes a plain,
non-executable `.json` file to the "yaml" inventory plugin instead, which requires the YAML plugin's
own nested `hosts: {<name>: {vars}}`/`children:` schema, not the script plugin's flat one. Both gaps
exist because PLAN.md's own architecture prose was written before either integration point was
verified against a real, currently-installed Ansible.

**Fix:** designed `internal/adapters/legacy/stdout_parser.go` against the real, always-present
`ansible.builtin.default` text callback at `-v` verbosity instead (real captured fixtures checked in
under `internal/adapters/legacy/testdata/`), and `internal/adapters/legacy/inventory.go` against the
real "yaml" inventory plugin's nested schema, verified end to end by actually running
`ansible-playbook` against the exact document `BuildInventoryJSON` produces
(`TestBuildInventoryJSON_AcceptedByRealAnsible`) and by the full container-to-container Release Gate
(`cmd/runner/ansible_release_gate_test.go`).

**Lesson:** `LESSONS_LEARNED.md` #93.

## 91. A test container the production code path never used, hiding a database configuration that existed nowhere

**Symptom.** `tests/e2e`'s Grand Integration Test started a real PostgreSQL container and
passed consistently. It looked like the most thorough test in the repository.

**Root cause.** No production binary could speak PostgreSQL at all. `cmd/controller` called
`ent.OpenEmbedded`, which is SQLite only, and `internal/ent/migrate` registered exactly one
dialect. The test reached PostgreSQL through `ent.Open("postgres", dsn)` plus
`client.Schema.Create(ctx)`, which is ent's automatic diff-and-apply, bypassing the versioned
migration system every real binary goes through. So the test exercised a dialect no deployment
ran, brought up by a mechanism no deployment used.

**Fix.** Phase 18 gave `internal/ent` a real dialect-agnostic `OpenDatabase` seam with genuine
SQLite and PostgreSQL adapters, a generated PostgreSQL migration set, and a shared conformance
suite run against both. The e2e harness now seeds through that same seam, and `Schema.Create`
appears nowhere.

**Lesson.** A container in a test proves nothing on its own. Ask which production call path
reaches it. If the answer is "none", the container is set dressing, and its presence actively
disguises the gap by making the test look thorough.

## 92. A dialect map that made a migration runner look portable while one statement inside it was not

**Symptom.** `internal/ent/migrate.Apply` was indexed by dialect name, which read as though
adding a dialect was a matter of adding a map entry.

**Root cause.** `applyOne` recorded each applied migration with
`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`. `lib/pq` rejects `?`
outright. Worse, it rejects it inside the same transaction as the migration's own DDL, so the
DDL rolls back too and the failure surfaces as broken DDL rather than as a wrong placeholder.
Verified empirically against a real PostgreSQL container: `pq: syntax error at or near ","`.

**Fix.** `migrationSource` now carries its own `insertVersion` statement beside the dialect it
belongs to. A test asserts every registered dialect has one and that it is parameterized.

**Lesson.** A lookup table keyed by dialect is not the same as dialect independence. Check
whether the statements the table's consumers execute are themselves portable, and prove it
against a real server rather than reading for it.

## 93. A compose file setting a configuration key no code read, in front of a service nothing used

**Symptom.** `docker-compose.yml` set
`DB_DSN=postgres://pleiades:password@postgres:5432/pleiades?sslmode=disable` on the controller
service, with a healthchecked `postgres` service and a `depends_on` gate.

**Root cause.** No Go code read `DB_DSN`. The controller read `DB_PATH`, defaulting to
`controller.db`, so the composed deployment ran on a SQLite file inside the container's
ephemeral filesystem while the PostgreSQL service beside it sat idle and every restart lost all
state. Found by grepping for the key rather than by anything failing, because nothing did fail.

**Fix.** The controller now resolves `DB_DSN` for real. The same audit found the composed
controller could not have started at all regardless, for three further reasons, each a
deliberate fail-closed startup check: `MASTER_ENCRYPTION_KEY` unset, neither `JWT_SECRET` nor
`JWKS_URL` set, and `RUNBOOK_DIR` defaulting to a directory the image never created. All are
fixed. The compose stack still cannot come up cleanly because its NATS healthcheck invokes a
binary the image does not contain, which is Phase 20's item and is not claimed as fixed here.

**Lesson.** Configuration that no code reads fails silently and forever. Grep every key a
deployment file sets against the code that is supposed to consume it; a key with no reader is a
bug even though nothing is red.

---

## 94. An unanchored `vendor/` gitignore pattern silently excluded embedded third-party assets, so the committed tree did not build

**Symptom.** None locally. Every build, test and gate passed on the machine the work was done on.
The commit was titled "add the dual-theme stylesheet and vendored assets" and contained the
stylesheet but none of the vendored assets.

**Root cause.** `.gitignore` carried a bare `vendor/` line, intended for Go's module vendor
directory. Git's pattern rules match a directory of that name at *any* depth, so it also excluded
`internal/ui/static/vendor/` — the ECharts and HTMX bundles the controller embeds and
redistributes, together with their licence and NOTICE files. `git add` prints nothing when it skips
an ignored path and exits zero, so nothing about the commit looked wrong.

The result was a tree that could not compile: `//go:embed app.css app.js chart.js vendor` fails at
build time with `pattern vendor: no matching files found` when the directory is absent. Local builds
stayed green throughout because the files were sitting on disk the whole time, ignored but present.

**Fix.** Anchored the pattern to the module root (`/vendor/`), which is the only place a Go vendor
directory ever exists, so the leading slash costs nothing and is what the line always meant. Then a
gate, because the failure shape — silent, invisible locally, fatal on a fresh clone — is one no
amount of local testing catches: `TestEmbeddedAssetsAreTrackedByGit` walks the embedded filesystem
and runs `git ls-files --error-unmatch` over every asset, so a file the binary embeds but the
repository does not contain fails the build. It caught a second file (`stream.js`) within the hour.

Proven rather than reasoned about: `git archive HEAD` into a clean directory, then
`go build ./internal/ui/static/`, which reproduced the failure exactly.

**Lesson.** A gitignore pattern is matched at every depth unless anchored, and `git add` reports
nothing when it skips what it ignores — so "I added the files and committed" is not evidence the
files are in the tree. More generally: **a file the binary embeds is a file the repository must
actually contain**, and the only honest check is against the committed tree, not the working one.
Anything else is a build that works on the machine it was written on.

---

## 95. A wildcard CORS header on an endpoint that had just become cookie-authenticated

**Symptom.** None yet, which is the point. `GET /api/v1/jobs/{id}/logs` sent
`Access-Control-Allow-Origin: *` on every response.

**Root cause.** The header was added when the web UI was a separate Vite application on another
port, and it was correct then: the endpoint accepted only `Authorization: Bearer`, a credential a
`fetch()` had to attach deliberately, so a wildcard origin exposed nothing a caller did not already
have a token for.

Phase 19 changed both halves of that premise at once. The UI moved same-origin into the controller,
so no cross-origin read was needed any more; and `/api/v1` gained a session cookie as a second
credential kind, so the endpoint became authenticated by something a browser attaches *ambiently*.
A wildcard origin on a cookie-authenticated stream is a standing permission for any site a
signed-in operator visits to read what their automation is doing to production.

It was not exploitable as written — browsers refuse to combine `*` with credentialed requests — but
it was one `Access-Control-Allow-Credentials` line away from being so, on the endpoint that streams
live output from privileged automation.

**Fix.** Removed the header entirely rather than narrowing it, since same-origin serving means there
is no legitimate cross-origin reader left. An e2e assertion against the real controller keeps it
gone.

**Lesson.** A security header is only correct relative to the authentication model underneath it,
and that model can change without the header being touched. This one went from correct to wrong
without anybody editing the line — the edit happened two packages away, in the middleware that
started accepting cookies. **When a new credential kind is added, every header that assumes the old
one has to be re-read**, and "ambient credential" is the property that flips a permissive CORS
policy from harmless to dangerous.

---

## 96. The credential-source generalization was built, tested, and never wired into the composition root

**Symptom.** `TestUI_LogStreamAuthenticatesWithTheCookieAlone` returned 401 against the real
controller binary, with a valid session cookie present.

**Root cause.** `api.IdentityMiddleware` and `session.CookieSource` had both been built specifically
so a browser could authenticate to the JSON API, which is the one defect that made a browser-based
log viewer impossible at all. Both were correct and both had tests. `cmd/controller` still called
`api.AuthMiddleware(evaluator)` — the Bearer-only wrapper — so the cookie source was never passed to
the router, and the entire point of the work was absent from the running binary.

The UI subtree worked, which made it worse: signing in, browsing and every write behaved correctly,
because `internal/ui/web` resolved the cookie itself. Only the `/api/v1` subtree, which is where the
SSE stream lives, was unaffected by the change.

**Fix.** Wired both sources into `RouterConfig.Auth`, Bearer first, and shared one `CookieCodec`
between the UI and the API so the two cannot disagree about the cookie's name.

**Lesson.** This is FAILURE_PATTERNS #52 in a different costume: code that is complete, correct and
unreachable from the composition root. The unit tests could not catch it, because there was nothing
wrong with the units. What caught it was an end-to-end test that drove the **real binary** and
asserted the capability the phase existed to deliver, rather than asserting that the pieces of it
work — which is what RULE 0 is asking for when it says a test only counts if it runs the path the
platform actually runs.

---

## 97. An inventory is a grant surface, so unvalidated membership is a cross-tenant privilege escalation with every individual step passing its own check

**Symptom.** None yet — found by adversarial review before the mechanism it exploits was wired up. Reported by all three attack lenses independently.

**Root cause.** Inventories were introduced as shareable containers, with sharing implemented as a RoleBinding at the new `ScopeInventory` level. That makes an inventory a *grant surface*: the resolver treats every device reachable through a shared inventory as in scope for the team it was shared with.

`SetStore.Create` and `Update` accepted arbitrary group and device ids and wrote them straight through. Nothing checked that a member belonged to the same organization as the inventory holding it.

The escalation needs no step that is individually suspicious. A caller holding `inventory:write` in their own tenant creates an inventory in their own organization (permitted), lists another tenant's device ids as its members (unchecked), shares it with their own team (permitted — it is their inventory), and is then legitimately authorized against hosts nobody granted them. Every permission check along the way passes, because each one is asking a question the attacker can honestly answer yes to.

Groups made it worse: a group has no organization edge of its own, so a group containing one foreign device smuggles that device in even when the direct device list is clean.

**Fix.** Validate membership at the write, which is the only place it can be stopped — by the time the resolver sees the containment it is a fact, and resolving it is exactly the correct behaviour. `assertMembersInOrganization` refuses any device belonging to another organization, and any group containing one. The API maps the refusal to 403 rather than 400: the submission is well formed and the caller is authenticated, they are simply not entitled. The error reports a count, never the ids — naming which devices belong to somebody else would answer, on that very request, the question the attacker was asking.

Devices with no organization at all are admitted deliberately: a single-tenant deployment has never populated that edge, they belong to no tenant, and refusing them would make the feature unusable for exactly the deployments most likely to adopt it first.

**Lesson.** **Ask what a new container grants, not just what it holds.** A collection that is merely descriptive can accept any membership; one that is an input to an authorization decision cannot, because its membership *is* a permission grant written in a different vocabulary. The tell is that sharing was implemented through the RBAC system — the moment a container feeds the resolver, every write to it is a privilege operation and belongs behind the same scrutiny as a role assignment.

Also: this was found by adversarially reviewing a *design* before implementing it, by agents told to break it rather than approve it. The same three lenses rejected the surrounding proposal outright. A review that had been asked "is this good?" would have said yes.

---

## 98. `scopeRule` discards the Role the resolver returned, so every RoleBinding's role is decorative

**Symptom.** None observable: `auth.NewScopeRule` has never been in a running admission chain, so no deployment has executed this path.

**Root cause.** `ScopeResolver.Resolve` returns `(Role, Effect, error)` and performs the full Section 18.4 walk — system, organization, inventory, group, device — with explicit Deny beating a broader Allow. `scopeRule.Check` calls it as `_, effect, err := r.resolver.Resolve(...)` and returns only the effect.

So the resolved role is thrown away. A binding granting `viewer` at a target and a binding granting `admin` at the same target produce an identical answer, and the `role` column on every RoleBinding row is decorative. Composed with `tokenScopeRule`, a caller holding `inventory:write` in their token plus any viewer-level Allow binding is authorized to delete.

**Fix.** Not yet applied, and deliberately so. The correct fix needs a decision this codebase has not made: `AdmissionRequest` carries a `RequiredScope` but no required *role*, so satisfying the role axis needs either a scope-to-minimum-role table or a required role on the request. Choosing one while the rule is unwired, in the same change that introduced an unrelated container, would be inventing policy in the wrong place. Recorded here so the phase that wires `NewScopeRule` addresses it deliberately rather than discovering it.

**Partial fix (2026-08-11).** The silence is fixed; the policy question is still open and still belongs to whoever wires the rule. `scopeRule.Check` now carries a doc comment stating in full that the resolved Role is reported and not enforced, what a correct fix would require, and why choosing it here would settle a policy question in the one place nobody would look for it. The discard itself is unchanged, and that is the point: an enforcement rule invented ahead of its first real caller is the failure recorded at #96 and #100, so the honest move was to make the gap legible rather than to close it speculatively. A first attempt did change the behaviour — returning an error on a clean Deny so the role reached the audit line — and was reverted, because `hateoas.go` documents relying on the distinction between "the chain could not reach a verdict" (error) and "it reached one" (Deny with a nil error), and collapsing that to surface a role nothing enforces would have traded a real signal for a cosmetic one.

**Lesson.** A function returning three values where the caller uses one is worth a second look, especially when the discarded one is the entire subject of the table it came from. This survived review because the call site reads naturally — `_, effect, err :=` looks like idiomatic Go, and nothing about it says "the role column is now meaningless".

---

## 99. A RoleBinding with `scope_id = 0` is a system-wide Allow, and nothing rejects one

**Symptom.** None observed; the rule is unwired.

**Root cause.** `ScopeTarget`'s own doc comment argues that zero-value fields "never match a real RoleBinding: ent primary keys are auto-increment starting at 1". That reasoning is sound for the *target* side and does not hold for the *binding* side, because nothing validates what goes into `role_bindings.scope_id`.

`ScopeResolver.Resolve` unconditionally folds an organization layer at `&target.OrganizationID` and a device layer at `&target.DeviceID`. When a target does not name one — a collection request, a runbook, anything outside the hierarchy — those fields are 0. A stored binding with `scope_id = 0` therefore matches, and because the device layer folds last under `policy.ModeOverride`, it beats every other layer including an explicit Deny at a real scope.

One row with a zero in a column with no positive constraint is a system-wide grant that outranks everything.

**Fix.** Not yet applied; it belongs with the phase that wires the rule, alongside #98. The shape is clear: reject a non-positive `scope_id` at any non-system scope, at the repository boundary and in the ent schema, and skip rather than match such a row when resolving.

**Fix (2026-08-11).** The resolver half is done, and it turned out to be a precondition rather than a follow-up. Organization-scoped *visibility* resolution passes a target naming an organization and nothing else, so `DeviceID` is 0 on every such call: resolving visibility through `ScopeResolver` before fixing this would have let one bad row grant global visibility across every tenant. `Resolve` now folds a containment level only when the target actually names a row there, via `appendScopeLayer`, which skips any level whose id is not positive. A request about an organization says nothing about a device, so a device-scoped binding can no longer answer it. System-scoped bindings are unaffected: they carry a nil `ScopeID` by design and name no row, and a separate test asserts they still apply to every target.

The correction to the entry above is worth recording too, because the original overstated the danger in one direction and understated it in another. A `scope_id = 0` row does **not** beat an explicit Deny: `combineScopeDecision` returns early once the accumulator holds a Deny, so a terminal Deny is genuinely terminal. What it did do is act as a blanket Allow for every check whose target named nothing at that level, which is worse in practice, because that is the shape of every organization-level question the platform asks.

Proven by `TestScopeResolver_ZeroScopeIDNeverMatchesAnUnnamedLevel`, and the test was mutation-checked: with the guard disabled, a `scope_type=device, scope_id=0` binding grants **admin** on an organization question. The repository-boundary rejection of a non-positive `scope_id` still belongs with the management surface that can write one.

**Lesson.** "The zero value cannot occur in practice" is an argument about one side of a comparison. Both sides need it, and the side that comes from a database column needs a constraint rather than a comment — a schema that permits the value will eventually contain it, whether by a migration default, a bad import, or a test fixture that escaped.

The second lesson is about sequencing. This was filed as a defect to fix later, in a subsystem nothing used. It became a precondition the moment a *different* feature decided to resolve through the same function, and nothing would have flagged that: the new caller looks entirely reasonable, and the latent row is in data rather than in code. When a dormant component acquires its first real caller, its recorded defects need re-reading as preconditions rather than as backlog.

---

## 100. A record action's affordance never entered the candidate set, so its control rendered for nobody, on every page, with no error anywhere

**Symptom.** The Runbooks view declares a `run` record action. The Run button did not appear on any runbook's detail page, for any caller, including an admin whose token carried every scope. Nothing errored, nothing logged, and every test passed. The conformance suite, the accessibility suite and the e2e suite were all green.

**Root cause.** `view.RecordAction` carries its own `*apispec.Endpoint`, with its own link relation and scope. That is the whole design: an action is gated by the same chain, against the same endpoint the router mounts, exactly as a CRUD operation is.

But the permitted set a template consults is computed in one place, and that place asked the wrong object:

```go
candidates := d.Ops.Candidates()   // internal/ui/web/handler.go
```

`Ops` holds List, Get, Create, Update and Delete. It has never held actions. So an action's relation never entered the candidate set, never came back from the generator, and `Affordances.Can(rel)` was false for it always. `DetailModel.Actions()` filtered every action out, correctly, from a set that could never contain one.

For Runbooks the mismatch was total: its operations declare `collection` and `self`, its action declares `execute`, and the three never intersect. The button was not refused. It did not exist.

Nothing caught it because every test asked a question the defect answered consistently. The conformance suite's affordance test compares what the UI renders against what the generator permits over *the same candidates* — both sides read `Ops.Candidates()`, so both sides omitted the action and agreed. A test that derives its expectation from the code under test cannot see a whole category go missing.

**Fix.** `Descriptor.Candidates()` unions the operations' affordances with the actions', and the web handler asks the descriptor rather than its `Ops`. `validateOps` gained the actions, because operations and actions now share one relation namespace: `Affordances` is keyed by relation, so two entries sharing one would make permitting either permit both, which on an action means offering an operation nobody granted.

The regression test asserts the rendered HTML contains the action's href, which is the only assertion that could have failed: it names the outcome a user experiences rather than a value the implementation computes.

**Lesson.** **When a feature is gated by a set, test that the set contains it, not that the gate works.** Every layer here was individually correct. The action carried a real endpoint, the filter applied the right rule, the generator evaluated what it was given. The defect lived in what was never put into the set, and absence is the one thing a consistency check between two derived values cannot detect.

The sharper tell: this was a *new optional part* added to an existing descriptor. Sections, charts and streams were all added the same way and all render, because each has a route that fails visibly when unwired. An action's only failure mode was silence, because a control that does not render looks exactly like a control the caller is not permitted to see — and "not permitted" is the answer this UI is designed to give quietly.

---

## 101. A required select whose only option source has no writer anywhere, so the create form it gates could never be submitted

**Symptom.** On a fresh `make ui-dev`, the Inventories create form renders an organization `<select>` with no options, and every submission is refused with "Choose the organization this inventory belongs to." There is no way to proceed from the UI. The whole Inventories feature is unreachable in the one environment built for reviewing it.

**Root cause.** Three individually reasonable decisions that nobody held together.

The schema makes `Inventory.organization` a required edge, correctly: an inventory belonging to no tenant would resolve against no organization scope, and whether that made it reachable by everyone or by nobody would depend on which way the resolver failed. The store refuses `OrganizationID == 0` for the same reason, and the view's `Bind` refuses `org < 1` to give the refusal a field to attach to.

And nothing in the repository creates an Organization. `Organization.Create` appears in exactly two test files. `tools/uidev/main.go` seeds six devices and no organization. There is no API endpoint, no UI view, no CLI command and no seeder.

So the required control is populated from `ListOrganizations`, which correctly returns an empty list, because the table is correctly empty, because nothing was ever built to fill it.

**Fix.** The management surface that can create an Organization, a Team, a User and a RoleBinding, plus a `uidev` seed that exercises it through real HTTP. Recorded before that landed, because the ordering is the lesson.

**Lesson.** **A required field is a dependency on a writer, and a `Kind: Select` says so out loud.** The validation was right, the schema was right and the store was right; what was missing was anything that could ever produce a valid value. This is the `init()`-that-nothing-imports failure (#52, #96) in a new medium: a complete, correct, well tested component with no path from the running system into it.

The generalizable check is cheap. For every required field whose values come from another table, ask what writes that table. If the answer is "a test", the feature does not work — and it will pass every test, because tests write their own fixtures.

---

## 102. HTMX is downloaded on every page and invoked by nothing, so half the request pipeline's fragment handling is unreachable

**Symptom.** None visible. Every page works. The UI is described, in its own phase plan and package doc comments, as "templ + HTMX".

**Root cause.** The vendored `htmx.min.js` is loaded by `layout.templ` on every page. A repository-wide search of the template set finds exactly one `hx-` attribute:

```
internal/ui/render/layout.templ:40:  hx-headers
```

on `<body>`, carrying the CSRF token. There is no `hx-get`, no `hx-post`, no `hx-target`, no `hx-swap`, and no call into `htmx.*` from `app.js`. Nothing on any page ever issues an HTMX request, so the token that attribute exists to attach has nothing to attach to.

The consequences run backwards through the server. `wantsFragment` has one caller. The `HX-Redirect` branch in `redirect` and the one in `requireSession` are reachable only by hand-crafting an `HX-Request` header, which is exactly what their tests do and exactly what no browser does. The security headers, the content security policy allowance, the vendored bytes, the checksum test, the provenance file and the licence notice all exist for a dependency the application does not call.

Every individual piece is correct. The plumbing is genuinely ready for the first element that opts in. What is wrong is the claim: the stack is templ, and HTMX is a payload.

**Fix (2026-08-11).** Connected, rather than removed. The deciding argument was that this is the part that matters once payloads start flying: a fan-out records its outcomes over seconds or minutes, and a page that froze at the instant it was opened, with nothing to say it had, is worse than no page.

`view.RefreshSpec` declares that a view keeps itself current, carrying an interval and an optional per-record `Active` predicate. `listRegion` and `detailRegion` were lifted out of their pages into their own components, so the first paint and every refresh render from one definition. `list` and `detail` now content-negotiate on HTMX's own request header and answer the same URL with the region alone, so a resource that declares a refresh still adds no route, no endpoint and no handler.

The stop mechanism is worth recording because it needs no bookkeeping: `hx-swap` is `outerHTML`, so the replacement carries its own trigger, and a fragment rendered while `Active` reports false simply carries none. A finished job costs one request, not one every five seconds until the tab is closed.

Two accessibility corrections came with it, both of which the original design would have got wrong by omission. A polled region never takes focus, because nobody asked for that swap and a timer has no intent. And a polled region announces only when its own summary actually changed, because "12 results" read aloud every five seconds is an obstacle rather than a feature, aimed at exactly the reader most likely to leave a running job open.

The canary left behind by the first pass is what caught the moment this changed: a test asserting a list request returns a whole document, with a comment saying that if fragment rendering was ever added, it and this entry needed revisiting. It failed on the commit that added it, which is what a canary is for.

**Lesson.** **A dependency that is loaded is not a dependency that is used, and "the plumbing is ready" is indistinguishable from "the plumbing is dead" without a caller.** This is the same shape as an `init()` nothing imports (#52), a credential source nothing wires (#96), and a record action whose relation never entered the candidate set (#100) — infrastructure ahead of any caller, correct in isolation, invisible in aggregate.

The specific tell here is cheap to check and worth making a habit: for any front-end library the server ships, grep the templates for a single call site. One `grep -o 'hx-[a-z-]*'` answered a question that four gates, a checksum test and a licence audit had all stepped around, because every one of them was verifying the file rather than its use.

---

## 103. A PATCH decoded an absent list and an explicit empty list identically, so renaming a record silently emptied its membership

**Symptom.** `PATCH /api/v1/teams/1` with body `{"name":"renamed"}` returned 200 and removed every member from the team. `PATCH /api/v1/inventories/1` with the same shape removed every group and device from the inventory.

**Root cause.** The write DTOs carried plain slices:

```go
type teamWriteDTO struct {
    Name  string `json:"name"`
    Users []int  `json:"users"`
}
```

`encoding/json` leaves a slice nil when its key is absent, and the handler assigned it unconditionally: `existing.UserIDs = body.Users`. So "the caller said nothing about membership" and "the caller said the membership is now empty" decoded to the same value, and the handler obeyed the second reading of both.

The store underneath is not wrong. Membership is replaced wholesale rather than merged, deliberately, because a merge makes removing the last member inexpressible: an empty submission would be indistinguishable from no change. That is correct at the store, which is handed a complete desired state. What was missing is that the HTTP layer owed it that complete state, and could not tell whether it had one.

It matters more on an inventory than on a team. An inventory is a grant surface (#97), so its membership is a permission written in another vocabulary; emptying it silently changes what every share of it covers, with nothing in the request saying so.

**Fix.** The lists became `*[]int`. A nil pointer means the caller did not mention that list and it is left exactly as it was; a pointer to a slice, empty or not, is a complete instruction and replaces it. Each list is independent, so naming devices does not clear groups. Regression tests assert all three cases per resource: omitted preserves, named replaces, explicit empty clears.

**Lesson.** **PATCH means partial, and a plain slice cannot express partial.** The verb is a promise that unmentioned fields are untouched, and any field whose absence must differ from its emptiness needs a type with three states. This is the request-side twin of the response-side defect recorded as LESSONS_LEARNED.md #98, found the same day in the same codebase: there `omitempty` collapsed nil and empty on the way out, here `encoding/json` collapsed them on the way in. Both were invisible because the Go code read naturally and the round trip through a struct never showed the loss.

The finding is also worth recording for how it was found: by an adversarial review agent that wrote a probe test into the working tree, ran it, and printed the before and after state. Reading the handler had not revealed it, twice, to two different readers.

---

## 104. A review agent wrote a file into the working tree, and CI failed on somebody else's scratch

**Symptom.** `make ci` failed at `go test ./internal/api/` on `TestProbe_PatchTeamWithoutUsersKey`, defined in `internal/api/zz_refute_probe_test.go`. Neither the test nor the file existed in any commit, in any plan, or in the session's own record of what had been written. By the time the failure was investigated the file was gone, so the failing test could not be located at all.

**Root cause.** A background workflow was reviewing the access surface adversarially while an unrelated `make ci` ran over the same working tree. The review agents were given the default tool set, which includes Write. One of them did exactly what a good reviewer does: it wrote a probe to test its hypothesis, ran it, confirmed a real defect, and cleaned up after itself.

Two independent problems. The agents had write access to a tree they were only meant to read, and they shared that tree with a concurrent verification run, so a transient file became a permanent-looking CI failure attributed to code that was fine.

**Fix.** Not yet applied as a mechanism, and the honest reason is that the finding was good: the probe caught #103, which two careful readings of the same handler had missed. Removing the ability to write a probe would remove the thing that worked.

The shape of the right fix is a worktree rather than a permission: `isolation: "worktree"` on the review agents gives each its own checkout, so probes are free to exist and cannot collide with anything. Recorded here so the next review workflow starts that way.

**Lesson.** **A concurrent verification run and a concurrent agent must not share a working tree.** A gate is only meaningful if the thing it measured is the thing you have, and a background process mutating the tree underneath it breaks that quietly: the failure names a file, the file is gone, and the natural conclusion is that the gate is flaky. It is not. Isolate the writer, not the write.

---

## 105. A guard ran after the deletions it was guarding, so a refusal destroyed the data it refused to destroy

**Symptom.** `DELETE /api/v1/teams/{id}` on a team holding the deployment's last system-scope Allow returned 409 with "refusing to delete the last system-scope grant", which is correct. The team survived, which is correct. Every *other* grant that team held was gone, permanently, and nothing said so.

**Root cause.** The deletion walked the team's grants and removed them one at a time, reusing the same per-binding guard `DeleteBinding` uses and asking it about each row as the loop reached it:

```go
for _, b := range held {
    if err := s.guardLastSystemGrant(ctx, b, Binding{}); err != nil {
        return err          // <- every grant walked past is already gone
    }
    ...delete b...
}
```

Two faults compounding. The guard was per-row rather than per-operation, so it asked "may this grant be removed" when the operation was "remove all of them"; and there was no transaction, so returning early left whatever the loop had already reached deleted. A team whose system grant sorted last lost everything before the refusal fired, and the API reported a clean refusal over a partial destruction.

The same method read its grants through the paged listing, capped at 200. A team holding more than that had exactly 200 destroyed and then failed a foreign key on the team row itself, which is the same defect arriving by a different route.

**Fix.** The whole set is read unpaged, the guard is evaluated once against the complete set before any write, and the writes run inside a real transaction that unwinds on any failure. Regression tests assert the refusal leaves every grant intact, that a team holding 201 grants deletes cleanly, that a team holding a system grant is still deletable while another stands, and that a deletion targeting a team that does not exist unwinds and reports `ErrNotFound` rather than a silent success.

**Lesson.** **A guard that can fire mid-operation must run before the operation, and the operation must be atomic.** "Refused" and "partially applied" are different outcomes, and a caller reading a 409 has no reason to suspect the second. Where a check is a precondition of a multi-row write, evaluate it against the complete set first and put the writes in a transaction: any other arrangement makes the error message a lie about what happened. The defect was found by an adversarial review agent and confirmed by executing the refusal and then reading the grants back, which is what distinguished it from the refusal working correctly.

---

## 106. Two opposite constraint violations arrived as one ent error type, so "still referenced" was reported as "already exists"

**Symptom.** Deleting a record something else still pointed at returned 409 "a record with that name already exists". The message describes a different operation than the one attempted, and names uniqueness as the problem when the problem was a dependency.

**Root cause.** `ent.IsConstraintError` is true for a uniqueness violation and for a foreign-key violation alike, and the type carries nothing distinguishing them. The mapping had one branch, written when the only constraint anything could hit was a duplicate name on create:

```go
case ent.IsConstraintError(err):
    return fmt.Errorf("%w: %q", ErrExists, name)
```

Once delete paths existed, every reference violation fell into it. The caller was told to pick a different name for a record it was trying to remove.

**Fix.** A separate `ErrInUse`, mapped to 409 with "something still references this record", selected by inspecting the driver's message for the wording SQLite and PostgreSQL each use. Inspecting a message is unlovely and is the only option ent offers here. An unrecognised constraint falls through to the uniqueness reading deliberately, because that is the safer of the two to be wrong about: it reports a collision rather than inventing a dependency that may not exist. Both branches and the fallback are covered.

**Lesson.** **When a library collapses two opposite failures into one type, the mapping to your own vocabulary is where they have to be separated, and the fallback is a decision.** A single branch is not "handling the constraint error", it is choosing one of its meanings silently. Pick the fallback for what it costs when wrong, and say in the code why that one.

---

## 107. A list rendered a foreign key, so the reader had to do the join

**Symptom.** The Teams list showed a column headed ORGANIZATION whose every value was an integer: `1`, `1`, `2`. Inventories did the same. The grants table read "operator, inventory 7, allow". The Users list showed `4, 9`. Nothing was broken, no test failed, and the pages were unusable without a second window open.

**Root cause.** Two separate mistakes that arrived at the same place.

The first was mechanical. Every one of those stores already eager-loads the referenced row: `ListTeams` calls `.WithOrganization()`, `ListUsers` calls `.WithTeams()`, the inventory list calls `.WithOrganization()`. The hydrators read `.ID` off the loaded row and discarded everything else:

```go
if row.Edges.Organization != nil {
    team.OrganizationID = row.Edges.Organization.ID
}
```

The name was in memory, on every row, and was thrown away. The fix cost zero additional queries.

The second was a reasoned decision, which makes it the more interesting half. The grants view's own doc comment argued against resolving the name:

> Not a join, for the reason the API's own version gives: resolving each scope id to its record's name would cost a query per row, and a deleted target would render as an empty string, which reads as "granted nowhere" rather than as "granted at a record that no longer exists".

Both observations were true. The conclusion did not follow from them. The answer to an expensive per-row join is a batch, not a primary key on the page; and the answer to an ambiguous blank is to say which of the two things it is, not to print an id instead.

There is a third contributing factor worth naming. The same session had just replaced a form control that asked an operator to type member user ids, on the explicit argument that "a control asking somebody to type a primary key is a control that will receive the wrong primary key". That argument applies with equal force to a column that answers with one, and it was not carried across, because forms and lists were being thought about as different problems.

**Fix.** `view.Field.References` names the registered view a field points at, and does two things: the cell renders the referenced record's name, and renders as a link to it, so the hierarchy is walkable rather than merely readable. `Row.Refs` carries the target id separately from the label, so the template never parses an id back out of text a human wrote. `view.CheckReferences` runs once after every view registers and refuses a reference to a view nobody registered, which cannot be checked inside `Register` because registration order is map iteration.

Three of the four cases were then free. The fourth, a role binding's `scope_id`, is a genuinely polymorphic reference with no foreign key by design, so it gets a batch resolver: collect the page's ids, group by scope type, one query per type. A deleted target keeps its id and renders as deleted rather than blank.

A conformance assertion over every registered view now fails any referencing field that renders empty or renders something that parses as an integer.

**Lesson.** **A list that renders a foreign key has not saved the reader a join, it has moved the join into their head.** The test is not whether the page is correct but whether it is usable without a second window: an operator should never have to learn that organization 1 is Network. When the name genuinely costs a query, batch it per page; when the target is gone, say so. And an argument made about a form control applies to the column that answers it, which is a connection worth looking for deliberately, because forms and tables feel like different problems and are the same one.

## 108. A replaced doc comment survived above its replacement, so one function documented two opposite policies

**Symptom.** `internal/api/access_bindings.go` carried two consecutive doc comments on one function:

```go
// grantedAt renders where a grant sits, for a reader rather than for a
// machine.
//
// Deliberately not a join. Resolving each scope id to its record's name
// would cost a query per row ... The id is the honest thing to show, and the
// scope type is what makes it legible.
// grantedAt renders where a grant sits, in words.
//
// It names the target rather than numbering it, for the reason
// FAILURE_PATTERNS.md #107 records ...
func grantedAt(b access.Binding) string {
```

The function names the target. The first comment says it deliberately does not, and explains why that is the better choice. Nothing failed: `gofmt` is content, `go vet` is content, and `golint`-style tooling has no opinion about a second paragraph that happens to start with the function's own name.

**Root cause.** The same session's own edit, made while fixing #107. The replacement text was inserted above the old comment instead of over it, and the old text stayed. It survived review because a reader scanning for the new comment finds it, immediately above the function, exactly where it belongs, and stops reading upward.

The reason it matters more here than in most codebases is that this repository's rules make doc comments load bearing. `.AGENTS/AGENTS.md` requires every exported symbol and every non-obvious decision to carry its reasoning, and the reasoning is how the next session decides whether a behaviour is deliberate. A comment that argues for the *previous* behaviour, sitting on a function that no longer has it, is worse than no comment: it reads as a warning that the current code is a mistake somebody already thought through and rejected.

**Fix.** Deleted the superseded paragraph. The surviving comment cites #107 by number, so the decision and its history stay reachable without keeping the old argument as prose.

**Lesson.** When a decision is reversed, the comment that argued for the old decision is not context, it is a contradiction, and it has to be deleted rather than pushed down. After any edit that changes what a function does, read the whole comment block above it from the top, not from the line the edit touched. A scripted or partial replacement that anchors on the first line of a doc comment will insert rather than replace, and the result compiles, formats and passes every check this project runs.

## 109. A testing library reached a production binary, because the package that imported it had no production caller until now

**Symptom.** None yet, which is the whole point of recording it. `go list -deps ./internal/adapters/legacy` pulls in eight `testcontainers-go` packages, a Docker client and their transitive dependencies. As of Phase 21, `cmd/runner` imports that package in order to route playbook dispatches, so all of it ships inside the Runner binary deployed to air-gapped and classified networks.

**Root cause.** `internal/adapters/legacy/docker_orchestrator.go` is a *non-test* file that imports `testcontainers-go`, and `testcontainers-go` is a direct `require` in `go.mod`. That was invisible for two phases because `legacy.NewAdapter` had **no production call site at all**: `gopls references` showed only `cmd/runner/ansible_release_gate_test.go` and the package's own test. A package that nothing imports contributes nothing to any binary, so the dependency sat in the module without ever sitting in an artifact.

Phase 21 is what changed that, and it could not avoid it. Go imports are static: the moment a composition root imports a package to construct one type from it, every transitive dependency of that package is linked in. There is no env-var toggle, no lazy construction and no interface indirection that avoids it, because the import is what pulls the code, not the call.

The reason it matters here more than it would elsewhere is the deployment target. This platform's stated audience is air-gapped, classified and financial-grade environments, where the set of code inside a binary is itself an auditable artifact. A testing library in that set is a larger `govulncheck` surface, a larger supply-chain surface, and a thing a reviewer has to explain.

**Fix.** Not fixed. The exposure was raised before it was created, and the decision was taken deliberately to compose both adapters now and replace the orchestration later: Phase 21 lands complete, and the Runner carries a test library until Phase 20 or 22 replaces `DockerOrchestrator`'s `testcontainers-go` calls with the Docker SDK directly. The composition root names this entry at the point of import so the cost is inherited knowingly rather than discovered.

Two things were done to bound it. The legacy adapter is composed **fail-open**: with no `PLAYBOOK_DIR` or no `ANSIBLE_RUNNER_IMAGE` the adapter is not constructed, the playbook kind is simply unroutable, and the Runner logs which kinds it can run at startup. And the routing decision itself lives in `internal/adapters/routing`, which depends on neither adapter, so removing the legacy import later is a change to one composition root rather than to the mechanism.

**Correction (2026-08-12).** The symptom paragraph above was false when it was written. "As of Phase 21, cmd/runner imports that package in order to route playbook dispatches" described wiring that did not exist: only the release-gate TEST imported the legacy adapter, `go list -deps ./cmd/runner` contained zero testcontainers packages, and the routing package had no production caller in any binary. The entry recorded the cost of a decision as though the decision had been executed, and the repository's own start-here doc repeated the claim. The wiring is real now (cmd/runner composes routing.Router over both adapters, fail-open on PLAYBOOK_DIR and ANSIBLE_RUNNER_IMAGE), so the exposure this entry prices is finally being paid as described. The wider pattern, four green layers that never composed, is #112.

**Lesson.** A dependency's blast radius is decided by what imports it, not by what calls it, and "this package has no production caller yet" is a temporary property that changes silently the first time somebody wires it up. When a non-test file imports a testing library, the cost is not paid until a binary reaches it, which means the decision gets made by whoever happens to need the package next, usually without knowing they are making it. Check `go list -deps` for a composition root's own binary, not for the module, and check it at the moment a new package enters that graph rather than at the moment it is written.

## 110. A composition root omitted an optional constructor option, and the feature it enabled was refused at run time with nothing failing at build time

**Symptom.** Every job launched from a template failed. Not a crash and not a 500: the launch was accepted with a 202 and a job id, the Worker picked the job up, and the job then reached the `failed` state with the reason "cannot resolve the inventory this job targets". The two chaos tests in `tests/e2e` caught it, both reporting `job state = "failed", want completed` from their baseline launch, which is the step that happens before either of them severs anything.

**Root cause.** `dispatch.NewWorker` takes its collaborators positionally and its optional ones as variadic `WorkerOption` values. Phase 21 added `WithSetStore`, which is what lets the Worker resolve the Inventory a job names into the devices it holds. `cmd/controller` was never given it.

Nothing could have failed earlier. The option is optional by signature, so omitting it compiles; the Worker's own package tests pass it, so its unit coverage was green; and the refusal it produces is *correct* behaviour, deliberately built in the same phase: a Worker that cannot resolve a job's target refuses the job rather than falling through to an unrestricted selector, because an unrestricted selector dispatches to every device the platform manages. The fail-closed design turned a wiring omission into a clean, well-reported, total feature outage, which is the right trade and also the reason nothing looked broken from inside any single package.

The near miss is worth naming. Had the selector failed *open* instead, this same omission would have dispatched every launch to the entire fleet, and the tests would have passed while doing it.

**Fix.** `cmd/controller` constructs the Inventory store above the Worker and passes `dispatch.WithSetStore(sets)`, with a comment at the call site saying what the Worker cannot do without it. The store was previously constructed a hundred lines further down, beside the UI's own handlers, which is why the option was easy to miss when the Worker was written.

**Lesson.** A functional option is a signature that cannot distinguish "deliberately not used" from "forgotten", so the compiler has nothing to say about either. When a phase adds an option that a new feature *requires*, the composition root is part of that feature and not a follow-up: wire it in the same change, and prove it from outside the process, because every test that constructs the collaborator itself will pass regardless. The generalisation of FAILURE_PATTERNS #52: an unwired dependency is invisible to the build whether it is a blank import, a registrar entry, or an option nobody passed.

## 111. An edit form rendered a control whose value the update then discarded, and the help text beside it had said "set once" since the day it shipped

**Symptom.** Five controls across three views. Open a team, press Edit, change ORGANIZATION from Network to Servers, submit. The page redirects to the team, reports no error, and the team is still in Network. Every grant it holds is still scoped exactly where it was. The same is true of an inventory's organization, and of a template's kind, definition and inventory.

**Root cause.** Each of the three `writer.Update` methods reads the field from storage and overwrites whatever the submission carried, which is deliberate and documented in each one: moving any of them re-scopes every RoleBinding pointing at the record, or re-tenants every job the template goes on to launch, and that is a migration rather than an edit. What none of them did was stop the form offering the control, because the field declaration had exactly one form-related flag, `InForm`, and one flag cannot say "offer this when the record is created and not afterwards".

So the field was declared once and rendered twice, and the second rendering was a lie. Two of the five carried help text reading "Set once", which made it worse rather than better: the sentence is true about the store and false about the control directly underneath it, and a reader resolves that contradiction in favour of the thing they can click.

This is the same defect the Templates launch form was rebuilt around three weeks earlier (LESSONS_LEARNED.md #102): a control whose value is then ignored is an affordance that does nothing. That rebuild fixed the launch path and did not generalise, because the launch form's fields are resolved per record through `RecordAction.FieldsFor` while a resource's own form fields come from the static declaration, and the two never met.

**Fix.** `view.Field.Immutable`. An immutable field appears in the create form, is absent from the edit form, and is reported as **undeclared** if a submission carries it to an update, so a post that smuggles the control is refused with a 400 rather than silently dropped. The renderer and the submission narrower read the same predicate, `Field.WritableOn(editing)`, off the same bit: `FormModel` derives the mode from whether it has a record id, which is also what decides where the form posts, so the set of controls and the operation cannot disagree about which one this is.

`Register` refuses `Immutable` on a field that appears in no form, because there it narrows nothing and reads as a promise about the record that nothing is keeping.

The three `Update` methods still read the field from storage. That is the third of three agreeing rather than redundancy: the form does not offer it, the narrower refuses it, and a caller reaching the writer directly still cannot move the record.

**Lesson.** When a write path deliberately ignores an input, the form that collects it is part of the same decision and has to be changed in the same breath, or the product has two answers to one question and shows the user the wrong one. Documentation is not a substitute: help text saying "set once" beside a working-looking select is read as a caveat, not as a contradiction. The general form is that a declaration used by more than one consumer needs to carry every distinction those consumers make, and a boolean that answers "does this appear in a form" cannot answer "in which form", so the first consumer to need the difference will paper over it locally, and the rest will keep shipping the old behaviour.

## 112. A feature shipped as four green layers that never composed, and every layer's own tests are why nobody noticed

**Symptom.** The playbook launch kind, Phase 21's flagship for the open Kind registry, could not work through the product by any path. The template form asked for a free-text "runbook id or playbook path" with no catalog to consult; a definition that named nothing was accepted at create (201), accepted at launch (202), and first failed at fan-out as a failed job. For the playbook kind specifically, four independent walls stood behind that one: nothing anywhere could list a playbook (the source had Get and no List, and no binary constructed it); the Controller's fan-out resolved every job through the NATIVE runbook source with no branch on kind, so a playbook job died as "runbook not found" before the Runner was consulted; the Runner composed only the native adapter, with routing.Router carrying zero production callers; and the two statements of the playbook reference grammar were disjoint, the template validator requiring a ".yml" suffix the resolver's `^[A-Za-z0-9_-]{1,64}$` rejects, so every definition that could be saved was one that could never resolve. The runbook kind carried a milder version of the same grammar split (253-character template bound over a 64-character resolver bound).

**Root cause.** Each layer was built against its own tests and none against the path. The routing package's tests exercised a Router nothing composed. The legacy adapter's release gate composed Router, adapter and playbook source BY HAND inside the test, so it proved the adapter executes real Ansible while proving nothing about any binary. The launch kind's validator was tested against its own grammar, the resolver against its own, and no test put one's output into the other. The plan's own verification item, "an e2e test launches one runbook and one playbook template and asserts each reached a different adapter", was the single test spanning the layers, and it was the one never built. FAILURE_PATTERNS #52, #96, #101 and #110 are all shapes of built-but-unreachable; this is the compound case, and its distinguishing feature is that redundancy of walls HID the defect: any one wall alone would have failed a smoke test, but with the form wall in front (nobody could even author a playbook template without typing a path by hand), no smoke ever reached the walls behind it.

**Fix.** One grammar per kind, owned by the resolving package and exported (runbook.ValidID, playbook.ValidID), with the kind validators delegating and an agreement test over a shared corpus. A launch.Catalog port listing and verifying definitions per kind, wired from the real sources in the composition root, consumed twice: the template store refuses a create whose definition does not resolve, and the Templates form's RUNS control became a picker over the same catalog with the kind derived from the choice. The fan-out worker prepares a job through a per-kind DefinitionSource map mirroring the Runner's routing map. cmd/runner composes routing.Router over both adapters, fail-open on PLAYBOOK_DIR plus ANSIBLE_RUNNER_IMAGE, refusing the half-configured state. And the missing test exists (TestGrandIntegration_EachKindReachesItsOwnAdapter): both kinds launched through the real API against the production binaries, each job's log stream carrying its own engine's output and not the other's.

**Lesson.** A vertical feature needs one test that enters at the top and leaves at the bottom before any of its layers can be called done, because layer tests compose the collaborators themselves and therefore cannot notice that nothing else does. When two packages state one contract (a grammar, a default, an encoding), one of them must import the statement from the other or a test must assert their agreement; two fresh statements are not duplication that will drift, they are disagreement from the first day, unnoticed exactly as long as nothing crosses the boundary. And a release gate that hand-composes the system it gates should say so in its name or its doc, because "the ansible release gate is green" was read, twice, as "the product runs ansible".

## 113. Two statements of one contract disagreed, and the reconciliation kept the wrong one

**Symptom.** A production Ascender job template stores `tripplite_python/tripplite_config.yml` in its Playbook field. This platform, immediately after a change whose stated purpose was fixing the playbook reference grammar, answered: `playbook "tripplite_python/tripplite_config.yml" is named by id, not by filename: write it as "tripplite_python/tripplite_config"` — advice naming a file that does not exist, in a form AWX never produces. No playbook belonging to any real customer could be named at all.

**Root cause.** Two packages stated the playbook reference contract independently and disagreed: the launch kind's `validatePlaybookPath` accepted project-relative `.yml` paths, and `internal/adapters/legacy`'s resolver accepted only flat `^[A-Za-z0-9_-]{1,64}$` ids. That much was correctly diagnosed (#112). The repair then unified them **onto the resolver's flat-id grammar**, because the resolver was the side that touched the filesystem and its grammar was the easier one to defend at a trust boundary.

That reasoning is the defect. Which of two disagreeing statements is *right* is decided by the system being compatible with, never by which is simpler to enforce or which side "owns" the boundary. The flat-id grammar existed because the resolver read one flat directory, and it read one flat directory because there is no Project entity to be relative to (`.SPECIFICATION/AWX_TEMPLATE_GAPS.md` §1) — so the repair propagated a *consequence of a missing feature* outward into the contract, and then wrote tests asserting it. Those tests are the worst part: `playbook_test.go` grew entries named `"filename"` and `"path"` listing `site.yml` and `playbooks/patch` as things that must be refused, which reads as deliberate design to the next person.

**Fix.** The grammar is project-relative paths, stated once in `internal/playbook.ValidateReference` and delegated to by the kind: non-empty, bounded, no NUL, not absolute, no backslashes, `path.Clean`-idempotent, no `..` climb, `.yml`/`.yaml` extension. `DirSource.Get` joins under the root and re-verifies confinement against the resolved absolute path; `DirSource.List` walks the tree returning relative paths, skipping dot-directories (`.git` above all) and the conventional role-layout directories a playbook never sits in. The e2e gate's fixture moved into a subdirectory so a resolver that cannot descend fails it.

**Lesson.** When two statements of one contract disagree, do not ask which is safer or which package owns it; ask which one an outside system already speaks, and make the other one that. Reconciling on the wrong side produces something strictly worse than the disagreement: the disagreement was at least visible as a bug that made nothing work, while the tidy wrong answer works perfectly for every value the codebase's own fixtures use and for none that a customer has. And when a contract looks unreasonably narrow, check whether it is encoding the absence of a feature rather than a real constraint — a flat namespace is what "we have no Project" looks like from inside the resolver.

## 114. A field made absent from the edit form was still demanded by the code that reads the submission, so every edit on three views failed

**Symptom.** Every inventory, team and template edit through the web UI answered 422 with an error naming a control the page had not rendered ("Choose the organization this inventory belongs to"). `make ci` was green.

**Root cause.** `view.Field.Immutable` (#111) removes a field from the edit form and refuses it as undeclared if a submission carries it. The three views' `Bind` functions still parsed those fields unconditionally, because `Bind` is one function serving both create and edit and had no way to know which it was serving. Adding `Immutable` to a field therefore silently converted a working edit path into one that could never succeed, and the writer's `Update` — which reads the immutable value from storage precisely so the submission need not carry it — never got the chance to run.

Nothing caught it because the conformance suite exercised create forms, validation refusals and CSRF, but never a *successful edit*: the one shape that traverses render → submit → narrow → validate → bind → update as a user does. Each half was individually correct and individually tested. The bug lived exactly in the seam, which is where the previous three findings in this file also lived.

**Fix.** `Values.Editing()` exposes the mode the narrower already tracked, and each affected `Bind` skips parsing an immutable field on an edit, leaving the zero value for `Update` to overwrite from storage. The general guard is `TestViewConformance_AnEditFormsOwnFieldsAreAnAcceptableSubmission`: for every registered writable view it fetches the edit form, parses the controls the server actually rendered along with their prefilled values (every selected option of a multi-select, not the first), resubmits exactly that, and requires acceptance. It is mechanical rather than per-view, so it covers views that do not exist yet. The faithful round-trip is also what makes it non-destructive: it writes a record's own values back over themselves, so the shared fixtures stay as seeded. An earlier draft collapsed multi-selects to one value and silently emptied a template's promptable fields, breaking an assertion three files away, which is the same class of bug the test exists to catch and a fair warning about writing round-trip tests carelessly.

**Lesson.** A change that narrows what a form renders is a change to what its handler may demand, and the two are usually in different files written months apart. Any framework flag that removes a control has to be paired, in the same change, with a check that the code reading that control tolerates its absence — and the check that actually holds is an end-to-end round trip of the surface, not a unit test of either half. "Submit exactly what was rendered and expect success" is a cheap, generic assertion that every form-driven UI should carry from its first view onward.

## 115. A checkbox's edit-form prefill used a different truthiness convention than the checkbox template itself checks for

**Symptom.** Found by code reading while building the Templates edit form's new per-field prompt checkboxes (B1), not by a live incident: `internal/ui/resources/templates/templates.go`'s `projector().Form` prefilled `allow_simultaneous` with `yesNo(tmpl.AllowSimultaneous)`, which renders `"yes"` or `"no"`. `internal/ui/render/field.templ`'s `KindBool` control renders the `checked` attribute only when the prefilled value is the literal string `"true"`. A template saved with `AllowSimultaneous: true` would therefore always render its edit-form checkbox unchecked, and an operator who opened it, changed nothing else, and saved would silently flip a true value to false.

**Root cause.** Two independent, un-reconciled statements of what a `KindBool` field's prefill string means. `yesNo` was written for `Cells` (list and detail-view display text, where "yes"/"no" is the right human-readable word) and reused for `Form` (prefill, which the checkbox template compares against a literal) without checking that the two consumers agreed on the convention. Nothing caught it because the seeded fixture's `AllowSimultaneous` happened to be `false` for the templates the conformance suite's `firstRecordID` reaches first, so the round-trip test (#114's own guard) never exercised the `true` case: the assertion is only as good as the fixture data it is run against.

**Fix.** `Form`'s prefill for `allow_simultaneous` uses `strconv.FormatBool` (or an equivalent literal `"true"`/absent-for-false) instead of `yesNo`, matching what the render template actually checks. `Cells` is untouched, since a list column showing "yes"/"no" is the correct, separate convention for that consumer.

**Lesson.** A helper written for one rendering context (a display cell) and reused for a different one (a form control's prefill value) carries an implicit assumption that the two contexts agree on what a value means — and `Cells` and `Form` do not have to, because one is read by a human and the other is compared against by a template's own conditional. When a field's Kind determines how a control is rendered (`view.FieldKind`'s whole reason for existing), the prefill value that control receives has to be produced in that Kind's own vocabulary, checked against what the render template for that Kind actually tests for, not against whatever a neighbouring consumer of the same domain field happens to expect. A fixture whose boolean fields are all `false` (or all the zero value generally) cannot exercise this class of bug at all; a seeded fixture used by a round-trip conformance test should include at least one record with every boolean field set true.

## 116. A resolver's output was correctly computed and never read by anything downstream of the function that computed it

**Symptom.** Found by code reading, tracing what happens to a template's execution fields (forks, limit, verbosity, extra variables) after B1 finally gave the Templates form real controls to set them. `launch.Template.Resolve` correctly folds a template's defaults, a saved configuration, survey answers and a launch's own overrides into `Resolved.Fields` and `Resolved.ExtraVars` — verified correct by this package's own tests. `internal/api/dispatcher.go`'s `LaunchTemplate` calls `Resolve`, receives `resolved`, and builds a `dispatch.Job` from it naming only `Definition`, `Kind`, `InventoryID` and `OrganizationID`. `resolved.Fields`, `resolved.ExtraVars` and `resolved.AllowSimultaneous` are read out of the return value and never referenced again anywhere in the codebase. Every field B1's new UI lets an author set was, until this session, inert: settable, validated, resolved correctly at launch time, and discarded before it reached a job record, the wire, or either execution adapter.

**Root cause.** The launch-configuration system (prompts matrix, per-field overrides, `Resolve`'s precedence folding) was built and thoroughly tested as a pure function of its own inputs and outputs, and every test asserting it asserted against its return value directly. Nothing tested, or could have caught by construction, whether a *caller* of that function used the whole return value. A resolver that computes three things and a caller that reads one of them both pass every test either half owns; the gap is only visible by reading the caller's own body field by field against the struct it received, which no automated check in this codebase does for a plain Go struct literal.

**Fix, partial this session.** `dispatch.Job` gained `Fields` and `ExtraVars` columns, and `LaunchTemplate` now stamps both from `resolved` (`.SPECIFICATION/AWX_PARITY_ROADMAP.md` Section 3b.1 has the full detail and what still has to be built: the wire and both adapters still do not read these values back off the job, so a launch's fields now reach the audit record but not yet a real execution). Tested end to end against a real ent store and a real dispatcher.

**Lesson.** A struct returned by a resolver is a checklist, not a report: before treating a resolve-and-persist path as complete, list every field the resolver's own type declares and grep for a second reference to each one downstream of the call site that received it. A field read exactly once — at the moment it comes out of the function that computed it — is a field on its way to being silently dropped, and no unit test of the resolver itself will ever show that, because the resolver was never wrong.

## 117. A job's completion state and tallies were fan-out publish outcomes, reported as though they were execution outcomes, while the real per-device outcome was already being reliably published to a subject nothing subscribed to

**Symptom.** Found by code reading, prompted by an adversarial review of what a job's `state` actually proves. A job whose every device failed its `ansible-playbook` run midway through still reaches `state = "completed"` with `failed_count = 0`: the Templates list's new Activity badge (B2, this session) would render it green.

**Root cause.** `internal/dispatch/worker.go`'s `Complete` call runs once the Controller's fan-out loop finishes, and its `dispatched`/`skipped`/`failed` counts answer "did the Controller succeed in publishing a dispatch message for this device," not "did the device's execution succeed" — a distinction `internal/ent/schema/job.go`'s own `state` field comment states correctly but which nothing surfacing the state to a reader (a badge, a status word) carries forward. Separately, and confirmed only by grepping every reference to `topology.ResultSubject`: `internal/runner/agent_wal.go` already durably WAL-buffers and reliably publishes a real per-device execution outcome (`internal/runner/wal.go`'s `ResultEntry`: device, outcome, reason) to that subject on every execution, and has done since PLAN.md Section 16's State Desync Mitigation was built. Nothing on the Controller side — not `internal/dispatch`, not `internal/api`, not `cmd/controller` — has ever subscribed to it. `ResultWAL`'s own doc comment says as much outright: the Controller-side half "has no consumer in this codebase yet."

**Fix.** Not built this session; sized and scoped in `.SPECIFICATION/AWX_PARITY_ROADMAP.md` Section 3b.2 as a design-then-build phase, because the state-machine question (what a job's state means once fan-out and per-device execution can each independently be incomplete, and what happens on a permanently lost result) needs an answer before the subscriber can be written, not after.

**Lesson.** A field's own schema comment can already state the honest scope of what it measures ("fan-out finished," not "execution succeeded") while every place that *renders* the field to a person quietly widens that scope back out, because a green badge reads as success to anyone who has not read the column's doc comment. When a system has two distinguishable notions of "done" (dispatched vs. executed, published vs. delivered, requested vs. confirmed), grep for every renderer of the status field whenever a second notion is introduced, not only the schema that defines it — and before building a reporting mechanism from scratch, grep for whether the data it needs is already being produced and simply has no reader, which is cheaper to find than to rebuild and was true here.

## 118. A security control's first working version cost 26x the thing it protected, which is how a control gets turned off

**Symptom.** Phase 22's secret-masking ruleset worked correctly on its first pass: every test in `internal/redact` passed, all three masking channels fired, and the negative control proved the rejected wrapping-handler design leaked where this one did not. Then the benchmarks ran.

A masked log line cost 25,207 ns/op against a 968 ns/op unmasked baseline, with 46 allocations where there had been none. With a thousand live secrets registered the number was 424,033 ns/op, roughly 0.4 milliseconds to emit one log line. Nothing was wrong. The control simply cost 26x on the hottest path in every binary, and the cost scaled with the number of secrets the process was protecting, so the busier a controller got the more expensive its logging became.

**Root cause.** Three independent things, none of which looks like a defect while reading the code.

Every masked string ran all five regular expressions unconditionally, including a PEM matcher with `(?s)` and a lazy wildcard, on lines that were almost always ordinary text with no secret shape in them at all. Every masked string called `Literals.Snapshot`, which rebuilt and re-sorted the entire secret set on each call, so a set of a thousand secrets was sorted once per log line. And the substring scrub allocated a claim table the length of the text and built a whole new string before discovering the text contained nothing to mask, which was the overwhelmingly common case.

Each is the obvious implementation of its step. The cost only exists in the composition, and only shows up when the steps are measured on the input distribution they actually see rather than on the input the tests use, which is deliberately full of secrets.

**Fix.** A prefilter on every pattern rule, carried in `rules.json` as data: a set of cheap lowercase substrings, at least one of which any match of that pattern must contain. `Masker.Text` lowercases once and skips any rule whose prefilter fails. A cached sorted snapshot in `Literals`, invalidated on mutation, so the sort happens once per `Add`/`Forget` instead of once per line. And a zero-allocation pre-pass in `maskLiterals` that scans for any occurrence before allocating anything.

Result: 3,410 ns/op with 1 allocation for a masked line, 884 ns/op for the pattern rules alone with zero allocations. A 7x improvement on the realistic case and 17x on the pattern floor.

The prefilter is the dangerous one, because a prefilter that does not actually hold for every match of its pattern silently disables its own rule and nothing fails. That is why each pattern rule also carries `samples` in the same file, and why three tests hold the two against each other: every sample must pass its own prefilter, must match its own pattern, and must actually come out masked through the real `Text` path. The evidence lives in the data file beside the rule rather than in test code, so the two cannot be edited apart, and the Python half gets the same examples.

**Lesson.** A security control's runtime cost is part of its correctness, not a separate concern to optimize later, because the failure mode of an expensive control is not slowness. It is deletion: somebody profiles, finds masking at the top, and turns it off or exempts the hot path, and the exemption is permanent. Benchmark a control against the *absence* of the control on the input distribution it will really see, which for a log-line scrubber is overwhelmingly lines with no secret in them, not the secret-dense inputs its correctness tests are built from. When the answer is a fast path that skips work, the fast path's own predicate becomes a new silent-failure surface, so it needs evidence that lives with it and a test that holds the predicate against the thing it is predicting.

## 119. A Runner whose NATS connection closes for good stays alive, stays healthy-looking, and silently stops doing any work

**Status: CLOSED 2026-08-23 by Phase 96a.** The second half of this entry, the NATS reconnect defaults in `internal/event` and `internal/lock`, is now fixed: `topology.DialOptions` sets `MaxReconnects(-1)` and `RetryOnFailedConnect(true)` and wires all four lifecycle handlers, and `internal/archtest`'s `TestEveryNatsDialCarriesTheSharedOptions` fails any dial that does not carry them. The give-up point this entry describes no longer exists at any outage length, and a disconnect is no longer silent. The original text below is unchanged; the paragraph after it records what was still open until Phase 96a and is now not.

**Original status when found: FOUND, NOT FIXED.** Discovered 2026-08-13 while verifying whether a test-harness container flake could reach production. It cannot; this can, and it is a different and worse thing. Recorded here rather than fixed in place because the fix changes the dispatch plane's failure behavior and the session that found it was building credentials, and folding an unrelated behavior change into that commit is how a change nobody reviewed in its own right ships.

**Symptom (predicted, not yet observed in a real deployment).** A Runner loses its NATS connection for longer than the client's reconnect budget. The process stays up. Its container keeps reporting healthy. It fetches nothing, executes nothing, and reports nothing, indefinitely. Capacity disappears from the mesh with no signal anywhere that says so, and the only visible evidence is a backoff-loop error line repeating in a log nobody is watching for that shape.

**Root cause.** Three things compose into it, and each is individually reasonable.

`nats.Connect(url)` is called with default options at all four production sites (`internal/event/nats.go`, `internal/lock/nats.go`, `cmd/runner/main.go`, `cmd/controller/main.go`). The nats.go defaults are `MaxReconnects: 60` and `ReconnectWait: 2s`, so the client gives up permanently after roughly two minutes of unreachability and closes the connection. That is a sensible library default for a request/response client and the wrong one for a long-lived worker whose entire job is to be attached to the bus.

No `ClosedHandler`, `DisconnectErrHandler` or `ReconnectHandler` is registered anywhere in the module, so nothing observes the transition.

`internal/runner/agent_run.go`'s `fetchLoop` backs off and retries on a fetch error rather than returning, which is correct for a transient error and indistinguishable from correct for a permanent one. `Run` therefore never returns, the process never exits, and no supervisor ever restarts it.

The Controller does not have this problem, and the difference is instructive: `cmd/controller/main.go`'s `readinessChecks` probes `nc.IsConnected()`, so an orchestrator sees it unready and restarts it. The Runner has no readiness surface at all, so the identical failure is invisible on one side of the mesh and self-healing on the other.

**FIXED 2026-08-16, Phase 20.** The third option named below, giving the Runner a readiness
surface of its own, is what shipped, because the two cheaper ones both answer the wrong
question. Exiting on a closed connection and retrying forever are each a policy about ONE
failure (the client gave up), and the entry's own diagnosis is broader than that: the Runner
had no way to say whether it was working, so any failure that left the process alive was
invisible. A surface answers all of them.

What was built: `internal/runner/heartbeat.go` writes a beat only while the durable consumer
genuinely answers, and `cmd/runner`'s new `healthcheck` subcommand turns that file's freshness
into an exit code. It is a file rather than a port because `cmd/runner` binds nothing and
should not start; it is the binary probing itself because the runtime image is distroless and
that binary is the only executable in it. The heartbeat is driven by the consumer answering
rather than by a ticker, which is the whole point: a ticker-driven beat keeps beating after
the connection dies and would have been a probe that cannot fail, which is the same defect
wearing a different hat.

Proven, not argued: `tests/e2e/runner_heartbeat_release_gate_test.go` severs a real broker
under a real running Runner and asserts the beat goes stale and `runner healthcheck` exits
non-zero, and `TestRunStopsBeatingWhenTheConsumerStopsAnswering` plus
`TestRunResumesWhenTheConsumerAnswersAgain` pin both directions in unit tests. The chart and
`docker-compose.yml` both wire the probe, so the failure now restarts the workload instead of
being invisible.

**Still open, and deliberately so:** the connection defaults themselves are unchanged.
`internal/event` and `internal/lock` build their own connections with the same `MaxReconnects:
60`, and the final paragraph below still applies to them. What changed is that the Runner's
silence is over; the library default that produces it is a separate fix with two more callers.

~~**Fix (sized, not applied).** Either register a `nats.ClosedHandler` that cancels the Runner's root context, turning a permanently dead connection into a process exit and letting the supervisor do what it is for; or pass `nats.MaxReconnects(-1)` so the client never gives up. The two are not equivalent and the choice is a real one: exiting surfaces the failure to whatever schedules the Runner, while retrying forever keeps a Runner that will recover on its own but leaves it invisible in the meantime. Giving the Runner a readiness surface of its own is the third option and the most work. Whichever is chosen, `internal/event` and `internal/lock` build their own connections with their own defaults and need the same treatment, or the fix covers one of three connections.~~

**Lesson.** A retry loop that cannot distinguish a transient failure from a permanent one converts an outage into silence, and silence is worse than the outage: an operator can see a crashed worker and cannot see an idle one. When a component's whole purpose is to stay attached to something, the library default for "give up" is almost never the right one, and the give-up path needs an owner that escalates rather than a backoff that absorbs. The tell here was structural and available without any incident: two processes connect to the same bus with the same defaults, one has a health probe that reads the connection and one has no health surface at all, and nobody had asked what the second one does when the first one's probe would have fired.

## 120. A process registered every value it was handed as a secret, and masked the ordinary ones out of its own output

**Symptom.** Phase 22's credential-injection release gate, run against a real ephemeral container for the first time, reported the injected environment as

```
REST_API_CONFIG = "********"
REST_API_TOKEN  = "********"
REST_API_URL    = "********"
```

Every value the playbook read back was the mask placeholder. The credential type declared exactly one secret input; the other two were an ordinary URL and a generated file path.

**Root cause.** The legacy adapter registers injected values with the process-wide masking set so that a module echoing one back is scrubbed. That much is right, and it is necessary: the Controller renders the credential, the Runner runs it, and they are different processes with different masking sets, so the Controller's own registration does not travel.

What the adapter cannot do is tell which values are secret. By the time an injection arrives it is a `map[string]string`, and a bearer token and a region are the same shape. Faced with that, the first implementation registered all of them, on the reasoning that masking too much is the safe direction.

It is not the safe direction, and the gate is what made that concrete. Registering an ordinary value scrubs that substring out of *every* later line the process writes, for the rest of its life. A Runner that has injected one credential naming `https://api.example.test` will thereafter render `connection to ******** refused` for an unrelated failure against an unrelated host. The output is corrupted permanently and nothing is protected, because the value was never secret.

The reasoning error is worth naming: "mask more" feels conservative because the risk being weighed is disclosure, and only disclosure. The cost of over-masking is not disclosure, so it does not appear on that scale at all, and a control evaluated on one axis will always be pushed to the end of it.

**Fix.** Secrecy is not recoverable downstream, so the side that knows says so. `credtype.Artifact` gained a `secrets` list, filled by the secret-tracking decorator that already computes exactly this set (the credential's own secret input values, plus any rendered value containing one). It crosses the wire as `wire.Injected.Mask`, and both adapters register exactly that and nothing else. Carrying the values adds no exposure the payload did not already have: they are the same bytes `Env`, `ExtraVars` and `Files` already carry.

The property is now held by a fuzz target rather than by a table, in both directions: a rendered value containing a secret must be declared secret, and a rendered value not containing one must not be. Both halves fail in opposite ways, and a table would only have covered the cases somebody thought of.

**Lesson.** When a value crosses a process boundary, every property of it that is not carried explicitly is gone, and "which of these is secret" is exactly the kind of property that looks recoverable and is not. Do not let the receiving side infer it. More generally: when a safety control has an obvious conservative direction, look for what that direction costs on an axis the risk assessment did not include, because a control with a free "safer" setting is usually one whose cost has simply not been measured yet. Here the cost was permanent corruption of the operator's own debugging output, and it took a release gate running the whole thing for real to make it visible; every unit test in the package passed, because none of them asserted that a *non*-secret value survives.

## 121. Strict-undefined turned a blank optional credential input into a total injection failure, and only real vendor data revealed it

**Symptom.** Nothing, for a whole stage. Every unit test passed, the fuzzers were
clean, both release gates were green, and the injector had been proven end to end
against a real `ansible-playbook` in a real container. The defect surfaced only when
the next stage transcribed a REAL AWX managed credential type (`controller`) into the
catalog and ran it: an operator authenticating with an OAuth token rather than a
password supplies no `username` and no `password`, and the entire injection failed
with an undefined-variable error. Not that one variable: the whole credential, so the
job could not run at all.

**Root cause.** `Credential.RenderVars` built the render namespace from the values the
credential actually held. The renderer is strict-undefined by design, so a template
referencing a declared-but-unsupplied optional input hit a name that was not in the
map, and strict-undefined did exactly what it was built to do.

The design reasoning behind strict-undefined was sound and remains so: an unreferenced
name silently becoming `""` is how a missing input injects an empty secret. What was
wrong was the assumption that the check had only one job. It has two, and they are
separated in time. Catching a name that is not a declared input is a check about the
TYPE, and `Injectors.Validate` already performs it at the moment the type is saved,
which is where Architecture Principle 5 wants it. Catching an input a particular
credential left blank is a check about the CREDENTIAL, and refusing there is wrong,
because a blank optional is legal by construction.

Every test written for the stage used a credential that supplied every input its
templates referenced. That is the natural thing to write when inventing a fixture, and
it is exactly the case that cannot fail. AWX's own data is full of the other case:
`controller` declares six inputs and requires one.

**Fix.** `RenderVars` now seeds every DECLARED input, using the schema rather than the
stored values, so an unsupplied optional renders empty and an undeclared name remains
impossible. That is also AWX's own behaviour, which matters more than the reasoning,
because what a migrated playbook observes is the resulting environment: AWX renders
under ordinary Jinja `Undefined`, so a blank optional input's variable is SET, to the
empty string.

Losing the accidental protection strict-undefined had been providing needed its own
replacement, because one real case still had to fail: a required input that was
prompted at launch and never answered. `Credential.checkReady` now demands a value for
every required input at injection time, with none of the three exemptions
`CheckValues` allows at save time (a default, an external reference, a launch prompt),
because by injection all three have already been resolved. The error names the input,
which the undefined-variable error never did.

Two related AWX behaviours were transcribed at the same time, for the same
observable-result reason: a boolean input renders in Python's capitalisation
(`True`/`False`, and `False` when unset), and an `ssh_private_key`-format input gains
a trailing newline if it lacks one.

**Lesson.** A validation rule that is correct at one moment can be wrong at another,
and a single implementation placed at the later moment will look correct for as long
as the fixtures happen to satisfy it. The general failure is a check that answers "is
this well formed?" being asked where the real question is "is this ready to use?".
Separate them explicitly, and write the fixture that only the later check can catch.

The second half is about where the defect came from. Every test in the stage was
written by the same person who wrote the code, against invented data, and invented
data encodes the author's own assumptions twice. The bug survived a fuzzer and two
container-backed release gates and was killed by transcribing twenty lines of somebody
else's real configuration. When a subsystem exists to be compatible with an external
system, take its fixtures from that system's own source early, not at the end as a
finishing step.

## 122. A build that compiles green produces a binary that cannot open its own default database, because the driver became a stub rather than a compile error

**Symptom.** None at build time, which is the whole problem. Phase 20's roadmap asks
for container images on "a distroless or scratch base". Both of those require a
statically linked binary, so the natural first move is `CGO_ENABLED=0`. That build
succeeds. `go build ./cmd/controller` prints nothing, exits zero, and produces a
working ELF binary that `file` reports as statically linked. Every test in the
repository still passes, because the container-backed tests run against Postgres.

The binary then fails the moment anybody runs it the way the documentation says to:

```
ERROR failed to open the controller database
  error="ent: migrating database \"ctl.db\": migrate: creating schema_migrations
  table: Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo to work.
  This is a stub"
```

`sqlite://controller.db` is the controller's DEFAULT `DB_DSN`. So the failure is not
reserved for an unusual configuration; it is what an operator gets for running the
image with no database configured at all, which is exactly what somebody trying the
product does first.

**Root cause.** `github.com/mattn/go-sqlite3` is a cgo package. When it is built with
cgo disabled, it does not fail to compile. It compiles a build-tagged alternative
whose functions return an error at run time saying the real driver is absent. That is
a deliberate and defensible choice by that library, since a hard compile failure would
break any program that merely links it without using it, which is most programs that
pull in a database abstraction. But its consequence here is that the compiler, the
type checker, the vet pass and the entire test suite are all blind to a change that
removes a database engine from the product.

The deeper cause is that the roadmap item named an implementation ("distroless or
scratch") rather than the property it wanted (a small, non-root, minimal-surface
image). Scratch and `distroless/static` demand a static binary. `distroless/base`
delivers every property that was actually wanted and does not.

**Fix.** Keep `CGO_ENABLED=1` and use `gcr.io/distroless/base-debian12:nonroot`, which
is glibc-based, distroless, non-root by tag, ships CA certificates, and costs roughly
20 MB more than `distroless/static`. The builder moves from Alpine to
`golang:1.26-bookworm`, because the old Alpine builder is musl and cannot produce a
binary that loads against glibc. The roadmap item was amended in place to record the
refusal and its evidence, rather than being quietly satisfied with a base that breaks
SQLite.

**Lesson.** A dependency that degrades to a run-time stub instead of a compile error
converts a build-configuration change into a run-time defect, and no amount of `go
build`, `go vet` or `go test` will see it if the tests all configure their way around
the default. Before changing any build flag that affects linkage, find the packages
whose behavior is selected by that flag, and run the resulting binary through its
DEFAULT configuration path, not through the configuration the test suite happens to
use. The test suite is the worst place to look for this class, because a suite that
provisions real infrastructure has already configured away the default it should be
defending.

The generalization beyond cgo: whenever a build knob changes which implementation gets
linked, the knob has moved a decision out of the type system, and something outside
the type system has to check it.

## 123. A test built its "nothing is listening here" address by releasing a port, and so picked the one address on the machine that would answer

**Symptom.** `go test ./internal/catalog/net/ssh/ -run TestPing_DialFailureIsReported`
failed on every run on a WSL2 development machine (6 of 6, and 39 of 40 in an earlier
sweep):

```
ping_errors_test.go:135: Ping() error = "net.ssh.ping: handshake with 127.0.0.1:44825:
  ssh: handshake failed: read tcp ...: read: connection reset by peer",
  want it to name the dial failure
```

The package is not listed in `flaky-packages.json`, so both `make ci` and
`make push-gate` failed on it, and the failure had nothing to do with the work in
flight: `git status --porcelain` on the package was empty, so it was byte-identical to
HEAD.

**Root cause.** The test needed an address whose TCP connect fails. It manufactured one
by opening a listener on `127.0.0.1:0`, reading back the port the kernel assigned, and
closing the listener immediately. That encodes an assumption: a port with no listener
refuses connections. The assumption is about the host's TCP stack, not about the code
under test, and under WSL2 it is false for precisely that port.

Probing this host with the same `net.Dialer` settings `ping.go` uses shows the shape of
it:

```
just-released by this process     127.0.0.1:44981   CONNECTED
never bound, high                 127.0.0.1:33333   FAIL  connect: connection refused
never bound, ephemeral range      127.0.0.1:45001   FAIL  connect: connection refused
never bound, low                  127.0.0.1:1       FAIL  connect: connection refused
just-released, after a 2s wait    127.0.0.1:44981   CONNECTED
```

The measured behavior is the table above: only the port this process itself bound and
released accepts a connection, and it still accepts one seconds later. The likely
explanation is that loopback is bridged between the Linux and Windows sides under WSL2,
so binding a port on the Linux side claims it on the Windows side too and releasing it
on the Linux side does not immediately release it over there. Either way the connect
succeeds against a socket with nothing behind it, and the connection dies as a reset as
soon as bytes are exchanged, which is during the SSH handshake. So the error came from
`Ping`'s handshake branch, and the assertion, which was watching the dial branch,
correctly reported that it never saw what it was waiting for.

This is worse than a coin flip. Every other loopback port on the machine refuses exactly
as the test expected. The port-selection strategy steered the test onto the single
address that would not. It is also invisible to whoever writes it, because "I just closed
it, so nothing can be listening" is a sound statement about the local kernel, and the
test was not talking to only the local kernel.

Two nearby tests make a related assumption and are unharmed by it, which is worth
recording so that nobody "fixes" them: `internal/transport/ssh`'s
`TestRealDial_TCPDialFailure` (dials `127.0.0.1:1`) and `cmd/pleiades`'s
`TestRunInventorySync_FailsOnUnreachableEndpoint` (closes an `httptest` server) both only
assert that some error came back. A reset instead of a refusal still fails them
correctly. The defect bites only where a test asserts *which* failure happened.

**Fix.** Stop deriving the address from host behavior. Pick addresses that cannot be
connected to by definition. The test is now table-driven over two of them:

- `127.0.0.1:0`. Port 0 is the sockets API's "assign me any free port" value for bind, so
  no listener anywhere can hold it and a connect to it never succeeds. It fails with no
  route and no resolver, and nothing leaves the host, which is what makes it hold up
  inside a network-less container.
- `192.0.2.1:22`, TEST-NET-1 from RFC 5737, reserved for documentation and not routed,
  under a one second context budget. This one is a real network-layer dial failure: an
  immediate "network is unreachable" where there is no route, an i/o timeout inside the
  budget where the packets are dropped. It is there so that the port 0 case is not the
  only evidence, since that case is settled entirely inside the host.

The assertions also gained the half that was missing. The old test only checked that the
message named "dial". It now also checks that the message does not name "handshake".
Every branch after the dial returns an error too, so a substring check for the expected
branch alone can pass on a run where the connection was actually established and failed
later. A negative control confirmed both halves are load bearing: re-running the old
just-released-port strategy through the new assertions fails on both, while the two new
addresses pass in the same test binary.

**Lesson.** A test that needs a condition to hold ("this address refuses connections",
"this file is absent", "this port is free") must obtain it from something that makes the
condition true, not from a sequence of steps that usually leaves it true. Releasing a
resource in order to prove it is unavailable is the recognizable shape of this mistake,
and it is a race even on hosts where it works, since another process can claim a released
port between the close and the dial. The question to ask of any such setup is what would
have to be true for it to be wrong, and whether anything in the test would notice. Here
nothing would, and the test instead reported a defect in the code it was testing.

The second half is about asserting on error paths. When several branches of a function
all return an error, a check that merely matches a substring of the expected message
cannot tell you which branch ran. Pin it from both sides: assert the marker of the branch
you want and the absence of the branches you do not. Without that, this failure reads as
"Ping stopped labeling its dial errors" when the truth was "a dial that could not
possibly succeed did", and those two hypotheses send the next reader to opposite ends of
the code.

---

## 124. A `//go:build ignore` file held a second copy of a pinned image, and no guard in the repository could see it

**Symptom.** None, for as long as it lasted, and that is the whole entry. Phase 20's
container hardening moved `docker-compose.yml` from `nats:2.14.4` to
`nats:2.14.4-alpine` and gave the broker three flags instead of one. Every test passed.
`make ci` passed. `tools/uidev/main.go`, which `make ui-dev` runs, kept starting
`nats:2.14.4` with `-js` alone, under a doc comment reading "runs the same NATS image
and flags docker-compose.yml uses, so the broker the UI is developed against is the one
it is deployed against". That sentence was true when it was written and false the
moment compose changed. Nothing reported it, because nothing could.

**Root cause.** Two layers, and only the second one matters.

The surface cause is an unavoidable second copy: the tool needed an image reference, so
it held a string literal.

The real cause is that the file carries `//go:build ignore`. That tag is right for what
the file is, a developer convenience invoked as `go run tools/uidev/main.go` that shells
out to docker and a compiler. What it also does, silently, is remove the file from
`go build ./...`, `go vet ./...` and every test in the module. A guard cannot check a
string it never compiles. So the one file in the repository with the weakest connection
to CI was also the file holding an unguarded copy of a value the repository had already
built a whole package to centralize, and the package doc of that package
(`internal/testsupport`) already said the pin should live there rather than at the call
site.

The same tag was also hiding two matching problems in the same commit's blast radius:
`docs/02-get-started.md` told a reader to `docker run ... nats:2.14.4 -js`, and the
changelog fragment told users the compose stack and the getting-started command "now pin
an exact NATS version", implying they agreed. Four distinct NATS strings existed in the
tree at once.

**Fix.** Structural, not a corrected literal. `//go:build ignore` excludes a file from
the default build but does NOT remove it from the module, so it can import
`internal/testsupport` like anything else, and now does:

```go
args := append([]string{"run", "-d", "--rm",
    "--name", name,
    "-p", fmt.Sprintf("%d:4222", natsPort),
    testsupport.NATSImage}, testsupport.NATSCommand()...)
```

`NATSCommand()` is new, and exists because the image agreeing while the flags differ is
the same defect wearing different clothes. It returns a fresh slice per call rather than
being an exported variable, since an exported slice is writable and a pin nobody can
rely on is not a pin. `TestComposeCommandMatchesPin` ties it to the compose file the way
`TestComposeImagesMatchPins` already tied the image.

The documentation copy got the same treatment rather than a corrected sentence:
`TestGettingStartedRunsThePinnedBroker` reads `docs/02-get-started.md`, finds the
`docker run` line a reader copies, and asserts the image and every flag after it match
the constants. Prose cannot import a constant either; that does not make it exempt.

Verified by running the real thing, not by reading the diff:
`PLEIADES_UI_ADDR=:8099 go run tools/uidev/main.go`, then
`docker inspect -f '{{.Config.Image}} {{json .Args}}'` on the container it started,
which answered `nats:2.14.4-alpine ["-js","-sd","/data","-m","8222"]`.

**Lesson.** A build tag that hides a file from the compiler hides it from every guard
built on the compiler. Before writing a literal in such a file, ask what would catch it
going stale; if the answer is "a person reading the doc comment", import the value
instead. The tag is a statement about how the file is invoked, never a licence to hold a
private copy of shared state.

---

## 125. A guard rejected only the literal tag `latest`, so `postgres:15-alpine` passed it for months under a doc claiming every image was pinned exactly

**Symptom.** None yet, and the entry exists because of when it would have arrived.
`internal/testsupport`'s package doc said "every image is pinned to an exact version,
never `latest`". `TestPinsAreNotFloatingTags` claimed to enforce that. `PostgresImage`
was `postgres:15-alpine`, which names a major only:

```console
$ docker run --rm --entrypoint postgres postgres:15-alpine --version
postgres (PostgreSQL) 15.19
```

The day upstream publishes 15.20, the advisory-lock tests, the ent conformance suite,
the migration generator, the end-to-end harness and the compose stack all move to it at
once, with no commit to bisect and nobody having decided anything. Two sibling guards
(`.dockerignore`'s exclusion test, the compose image test) had the same shape of hole in
the same commit, so this is a pattern rather than one bad line.

**Root cause.** The check tested the example instead of the rule. It read:

```go
if tag := ref[idx+1:]; tag == "latest" || tag == "" {
```

`latest` was the tag that caused the original incident, so `latest` became the test.
Everything else that floats, `postgres:15`, `nats:2`, `golang:1`, `15-alpine`, sailed
through, and the package doc beside it read as evidence that they could not. A guard
narrower than its own documentation is worse than no guard, because the documentation is
what people act on.

The same failure in the two siblings:

- `TestDockerignoreExcludesNonBuildInputs` compared allowances by exact prefix
  (`allowed == path || strings.HasPrefix(allowed, path+"/")`). `!.SPECIFICATION` failed
  it; `!.S*` re-admitted the identical tree and passed. The glob is the version somebody
  would actually write.
- `TestComposeImagesMatchPins` iterated its own two-entry table and never iterated the
  services it had just parsed, so a service ADDED to `docker-compose.yml` was unchecked
  in either direction. Adding a dependency is the normal way a new image arrives.

**Fix.** Each guard was rewritten to encode the rule, and each was then watched fail on
the regression it now claims to catch before being trusted to pass:

- The pin rule is now "the tag contains a release number with at least a major AND a
  minor component", checked by `rejectPin`, with `TestExactVersionRule` driving it over
  a table of good and bad references (including `postgres:15-alpine`, `nats:2`,
  `golang:1`, and a private-registry host whose port colon must not be mistaken for a
  tag separator). `PostgresImage` became `postgres:15.19-alpine`.
- The `.dockerignore` check matches patterns per path segment with `path.Match` instead
  of comparing prefixes, so a glob that re-admits a forbidden tree fails it.
- The compose check walks every service in the parsed file. A service that pulls an
  image must be pinned in `internal/testsupport`; a service that builds is exempt and
  says why; a service with neither is an error.

The rule's ceiling is written into the code rather than left implied: matching a version
number anywhere in the tag would accept a hypothetical `postgres:alpine3.22`. Anchoring
it to the front of the tag was tried and rejected, because it rejects
`version-10.3_p1-r0`, a genuinely immutable tag this repository depends on.

**Lesson.** When a guard exists because of one incident, it will be written against that
incident's literal and will silently be narrower than the rule everyone believes it
enforces. Write the rule down as a predicate, give the predicate its own table test with
the near-misses in it, and watch the guard fail once before believing it passes.

## 126. A private key mounted into a container was readable by nobody, and the message named the wrong problem

**Symptom.** `docker compose up -d --wait` reached a healthy stack. After `chmod 600` on
the certificate pair that `make dev-cert` writes into the gitignored `.dev-certs/`, the
controller logged this on every start and never served a request:

```
{"level":"INFO","msg":"controller listening","addr":":8080","scheme":"https"}
{"level":"ERROR","msg":"server failed","error":"open /etc/pleiades/tls/cert.pem: permission denied"}
```

`docker compose ps` showed `Restarting (1)`, and the file it named was present, was the
right file, and was readable by the user who was reading the logs.

**Root cause.** A bind mount carries the host file's numeric owner into the container
with no remapping. The controller image runs as UID 65532 (`gcr.io/distroless/base`'s
`:nonroot`), the host files are owned by whoever ran `make dev-cert`, so mode 0600 means
"readable by a UID that does not exist in this container". The directory matters as
much as the files: 0750 on `.dev-certs/` denies traversal, and the error is then still a
permission denial on a file whose own mode looks fine, which is the harder version to
read.

**Fix.** `tools/devcert` relaxes the pair to 0644 and the directory to 0755 after
generating them, with the reasoning in full at the one place it happens: these are
throwaway development certificates, generated fresh, valid for a week, in a gitignored
directory, and a real deployment mounts its key through its platform's secret mechanism,
which sets ownership correctly and needs none of this. `internal/testsupport` still
writes 0600, and `TestServingCertFilesAreOwnerOnly` pins that, so the relaxation stays a
deliberate act by one caller rather than a default.

**Lesson.** File modes on a bind mount are evaluated against the container's UID, not the
host's, so the correct mode for a secret on the host is the wrong mode for a secret a
container has to read. When a container must read a mounted file, decide the ownership
question at the same moment as the mount, and check the directory's execute bit as well
as the file's read bit. The alternative that keeps 0600 is not a chmod at all: it is
handing the file to the platform's secret mechanism, which is what production does.


---

## 127. A certificate meant to be reused was replaced on every restart, because one of its subject alternative names was the container ID

**Symptom.** The controller provisions a self-signed certificate when no real one is
configured, stores it on its data volume, and is supposed to present the same one on
every later start so a browser warning is a once-per-machine annoyance rather than a
once-per-restart one. It was not. `docker compose up`, then `down` without `-v`, then
`up` again, and the SHA-256 fingerprint on the wire had changed, with the startup log
reporting `"provisioning":"generated"` both times. The volume was intact and the old
`cert.pem` was still on it a moment before being overwritten, so every signal said the
storage was working and only the fingerprint said it was not.

**Root cause.** The reuse check refused a stored certificate that did not carry every
subject alternative name the caller asked for, which is the right rule for a name an
operator configured: adding a hostname has to take effect. The controller also adds the
machine's own hostname, and inside a container the hostname is the container ID.
`docker compose down` destroys the container, so `up` creates one with a new ID. Every
restart therefore found a stored certificate that was missing the "requested" name, and
regenerated. The check was working exactly as written; the input to it was a value that
changes by itself.

**Fix.** Split the two kinds of name in `internal/tlscert.Options`. `ExtraNames` are
required and a stored certificate missing one is replaced, which is what makes
`PLEIADES_TLS_AUTOCERT_HOSTS` take effect on the next restart. `OptionalNames` are put
ON a generated certificate and are never required of a stored one; `cmd/controller` puts
the hostname there. `TestEnsureReusesWhenOnlyAnOptionalNameChanged` feeds it two
different container IDs around one configured name and fails if the certificate changes,
with a control in the same test proving a changed CONFIGURED name still replaces it.

**Lesson.** A cache key must not contain a value that changes on its own. The rule
"replace what does not match what was asked for" is only safe when everything asked for
was asked for by a person; the moment a discovered fact about the environment joins the
list, the cache invalidates itself on a schedule nobody chose. Separate what a caller
requires from what a process noticed, and let only the first one throw work away. Also:
this was found by running the documented `down`/`up` cycle and diffing a fingerprint,
not by any test in the suite, because every unit test ran in one process with one
hostname. When a feature's whole promise is "the same thing next time", the test has to
change the thing that differs next time.

---

## 128. Every controller in a scaled deployment refused to boot at once, because the code that provisions a certificate counted its own writes instead of ending on a read

**Symptom.** One controller starting against an empty certificate directory worked every
time. Four starting at the same instant against the same directory, which is a Deployment
with replicas on a ReadWriteMany volume or `docker compose up --scale`, produced four
processes that exited before serving anything, each with

```
generated a serving certificate into /data/tls 3 times and could not read a usable pair
back: no usable certificate and key in place: tls: private key does not match public key
```

A second shape appeared in the same runs and looked unrelated: a controller logged
`controller listening scheme=https` and then died on `tls: private key does not match
public key` a moment later. Both were the same race, seen from two points in it.

**Root cause.** Three of them, stacked, and the third is the one that mattered.

The certificate and the key are two files. Each is published by a rename, so each is
atomic on its own, but two renames cannot be one step: a reader arriving between them sees
one writer's key beside another writer's certificate. That much was known and commented.

The recovery from it was a loop that ran "load, then generate" a fixed three times and
then returned a fatal error, WITHOUT loading again. Under contention every pass loaded a
pair that some other racer had half-replaced, so every pass generated, and the loop ended
on a generate whose result nobody read. The retry budget was not too small. Ending on a
write instead of a read is what turned a transient interleaving into a permanent refusal,
and no backoff or jitter existed to let the writers separate.

The listening-then-dying shape had the same root and one extra step. `Ensure` verified a
pair and returned two PATHS; the listener then handed those paths to
`ListenAndServeTLS`, which opened the files again. Everything between the check and the
second open was a window in which another controller could publish, so the process
verified one pair and served a different, mismatched one.

**Fix.** The loop now does load, CLAIM, load, generate, and the claim is the part that was
missing. Exactly one process may write at a time, decided by an `os.Mkdir` of a
`.generating` directory: mkdir either creates the entry or reports that it exists, in one
step, and unlike `O_EXCL` its exclusivity is unambiguous on the NFS-backed shared volumes
this failure needs to be fixed on. A process that loses the claim has learned that
somebody else is publishing a good pair right now, so it waits an exponentially backed off,
jittered moment and LOADS again rather than writing a second one. The claim is re-checked
by loading once more after it is won, so a racer that queued behind the winner reuses the
winner's certificate instead of replacing it. The pass after the last one is a load, so
the function's final act is always a read.

The listener no longer re-reads anything: `Ensure` returns the parsed `tls.Certificate`,
`cmd/controller` puts it in `TLSConfig.Certificates`, and `ServeTLS(listener, "", "")`
serves exactly the bytes that were verified. The listening line moved after a synchronous
`net.Listen`, so it reports something that already happened.

`TestEnsureUnderConcurrentStartups` runs eight goroutines off one start barrier and fails
if any refuses, if any ends up on a different certificate, or if more than one generated.
`TestEnsureAcrossRealProcesses` re-executes the test binary eight times against one
directory, because the coordination is a filesystem primitive and separate processes share
no memory at all, so a pass there can only come from the exclusive create.

**Lesson.** A retry loop's LAST action decides what its failure mode is. A loop that ends
on the operation it is retrying reports failure for a resource that, by then, usually
exists; a loop that ends on a read reports what is actually there. When the retries are
recovering from other writers, losing a race is not an error condition at all, it is the
strongest possible evidence that the thing being waited for is about to exist, and the
correct response to it is to look again rather than to try harder. Two more general points
fell out of the same bug: any check-then-use across two files needs the check to RETURN the
material, not a path to re-open it; and "log that we are listening" belongs after the
listener binds, or every failure prints a success line first.

## 129. A build context was measured from BuildKit's own progress line, which reports a cache delta rather than a size, so the measurement said 82 kB about 11.5 MB

**Symptom.** Phase 20's Release Gate has to assert that `.dockerignore` keeps the image
build context to a small fraction of the repository. The obvious measurement is the line
BuildKit already prints:

```
#7 transferring context: 82.00kB 0.1s done
```

Against a working tree of 384 MiB that reads as an excellent result. It is not a result at
all. The real context for the same build is 11.5 MiB across 1311 files, which the same
command reports on a machine that has never built this image before.

**Root cause.** BuildKit keeps a local cache of the build context on the daemon side and
synchronizes it against the client on each build. "transferring context" reports what
crossed the wire, so it is the DELTA since the previous build of the same context, not the
size of the context. A gate built on it would have reported a smaller and smaller number
the more often it ran, and would have passed unchanged if somebody deleted every line of
`.dockerignore`, because the second build after that deletion would transfer almost
nothing again.

**Fix.** Measure the context by building it. `FROM scratch` with a single `COPY . /` and
nothing else produces an image whose filesystem IS the filtered context, so
`docker export` of a container created from it yields the byte-for-byte answer, and the
same tar listing answers the second half of the question (which top-level directories got
in) with no extra work. `.dockerenv`, which docker creates inside every container it makes,
is the one entry that has to be subtracted. Measured this way the gate reports 11.5 MiB
across 1311 files against 384.4 MiB of working tree, or 2.99 percent, and it reports the
same thing on the tenth consecutive run.

The rejected alternative is worth naming because it looks principled: reimplementing
`.dockerignore`'s matching rules in Go and walking the tree. That measures whether the test
agrees with itself. `internal/testsupport/dockerignore_test.go` already reads the file and
checks its rules, which is the other half of this claim and is deliberately not the same
half.

**Lesson.** A progress indicator is instrumentation for a human watching a build, not an
API. Before asserting on a number a tool prints in passing, run the tool twice and check
that the number is the same both times; anything that shrinks on the second run is
reporting work done, not size measured.

## 130. A Kubernetes cluster created moments after a container image build lost etcd during CNI install, and the failure surfaced inside the test that installs the chart

**Symptom.** `kind create cluster` failed once in eight provisionings on the machine this
was written on, and the one failure was the run that came immediately after a
`docker compose build` in the same test binary. It always fails at the same step:

```
 • Installing CNI 🔌  ...
 ✗ Installing CNI 🔌
ERROR: failed to create cluster: failed to apply overlay network: ...
Command Output: clusterrolebinding.rbac.authorization.k8s.io/kindnet created
serviceaccount/kindnet created
daemonset.apps/kindnet created
Error from server: error when creating "STDIN": etcdserver: request timed out
```

Run on its own, the identical command succeeded every time. This is FAILURE_PATTERNS #61's
shape (the package that loses the race changes between runs) applied to a control plane
rather than to a container's port mapping.

**Root cause.** etcd is disk-latency sensitive and its default election and heartbeat
timings assume it is not competing for the same device as anything else. A cold image build
writes hundreds of megabytes of layers through the same daemon and the same filesystem, so
an fsync inside etcd that normally takes single-digit milliseconds does not return before
the API server's own request deadline. Nothing about the cluster, the chart or the images
is involved: the failure is complete before anything this repository ships has been
touched.

**Fix.** The gate provisions the cluster up to three times, deleting any partial cluster
between attempts and sleeping ten seconds, and if all three fail it SKIPS with a message
saying that nothing under test had been exercised yet. Every step after the cluster exists
(the image import, `helm install`, the readiness wait, the readiness document, the
bootstrap through `kubectl exec`) is run exactly once and fails hard, because a failure
there is about the artifact.

An image-pull failure is separated out and never retried: it is an absent prerequisite,
and retrying it only spends three times as long finding that out.

**Lesson.** Retrying is legitimate exactly where the thing being retried is not the thing
under test, and the boundary is a position in the test rather than a category of error. Ask
which step first touches an artifact this repository produces; everything before it is
setup that may be retried and then skipped, everything after it is evidence that must fail.

## 131. Four workloads collapsed into one object name at every legal release-name length between 49 and 53

**Symptom.** `helm install` of a release whose name was 49 to 53 characters long (Helm's own
limit is 53, so every one of these is legal) produced a release where the controller
Deployment, the runner Deployment, the PostgreSQL StatefulSet and the NATS StatefulSet all
carried the SAME `metadata.name`, along with their Services. Nothing failed. The API server
applied each object in turn, and the second Deployment replaced the first.

**Root cause.** `templates/_helpers.tpl` built a workload name by APPENDING the component and
then truncating the result:

```
{{- printf "%s-controller" (include "the-pleiades.fullname" .) | trunc 63 | trimSuffix "-" }}
```

The fullname prefix is itself truncated to 63. With a 49-character release name it comes to
62 characters (`<release>-the-pleiades`), so appending `-controller` and cutting at 63 keeps
the prefix plus a single `-`, which `trimSuffix` then removes. All four helpers returned the
prefix and nothing else. At 48 characters the bug was present but less visible: the names
stayed distinct and ended in `-c`, `-r`, `-p` and `-n`.

**Fix.** Truncate FIRST, append SECOND. One shared helper cuts the prefix to 52 characters
(63 minus the longest suffix, `-controller`) and then appends the component, so the component
word can never be the part that is lost. `tools/helm-lint` renders the chart at release-name
lengths 1, 20, 48, 49, 52 and 53, twice at each length to cover both branches of the fullname
helper, and asserts that no two objects share a kind and name, that each workload name still
ends with its component, and that every name fits the limit its kind is held to. Restoring
the old helpers makes that check produce 29 findings.

**Lesson.** A name built by appending then truncating puts the meaningful half at the end of
the string, which is exactly the half a length limit removes. Cut the part nobody reads.

## 132. A PodDisruptionBudget that silently did not exist, because the template chose its field by truthiness

**Symptom.** An operator who set `controller.podDisruptionBudget.enabled=true` and cleared
`maxUnavailable` (which the chart's own refusal message told them to do, in order to use
`minAvailable`) rendered NO PodDisruptionBudget at all in some combinations, and one carrying
no budget field in others. `kubectl get pdb` was empty in a release whose values said
disruption protection was on.

**Root cause.** `{{- if .Values.controller.podDisruptionBudget.minAvailable }}` asks whether a
value is TRUTHY. The chart's own default for that key is `""`, and `0` is falsy too, so
"unset" and "deliberately zero" were indistinguishable, and the else branch emitted an empty
`maxUnavailable` when the operator had cleared it. The validation had the same shape:
`and .minAvailable .maxUnavailable` never fired for `minAvailable: 0`.

**Fix.** A `the-pleiades.isSet` helper that answers "did the operator state this value",
treating nil and `""` as unset and everything else, including `0` and `"0"`, as set. The
template emits whichever field is set, `_validations.tpl` refuses both-set and neither-set
with the reason, and `tools/helm-lint` asserts the COUNT of budgets a profile must produce,
because an absent object passes every rule that iterates over rendered objects.

**Lesson.** Truthiness is the wrong question for any setting where 0 is a legal answer. Ask
whether the value was stated, and check the absence of an object as its own assertion.

## 133. A reinstall with a different database password installed cleanly and crash-looped forever

**Symptom.** `helm uninstall` followed by `helm install` under the same release name with a
different `postgresql.auth.password` reported every object created and every hook succeeded.
PostgreSQL came up healthy. The controller then failed authentication, exited, was restarted,
and repeated that indefinitely. Nothing in helm's output, the chart, or the database's own
logs contained the word "password".

**Root cause.** A StatefulSet's `volumeClaimTemplate` claims are deliberately NOT deleted by
Kubernetes when the StatefulSet goes away, so the database volume survives `helm uninstall`
(which is the right behavior: destroying a database on uninstall would be worse). PostgreSQL
applies `POSTGRES_USER`, `POSTGRES_DB` and `POSTGRES_PASSWORD` only when it initializes an
EMPTY data directory, and ignores all three on a volume that already holds a database. The
new password was therefore believed by the controller and ignored by the database.

**Fix.** The StatefulSet stamps a sha256 fingerprint of the three credentials onto its
`volumeClaimTemplate` metadata. Kubernetes copies a volumeClaimTemplate's annotations onto
the claim it creates (verified against a real cluster before the fix was written), and the
claim outlives the release, so `_validations.tpl` looks the claim up with `lookup` and
refuses to render when the stamp and the values disagree. The refusal names the claim, says
which choice keeps the data and which destroys it, and prints the exact `kubectl delete pvc`
command. It refuses only a PROVEN mismatch: an unstamped claim, or a release whose password
comes from `secrets.existingSecret` and is therefore never seen by the chart, produce no
refusal, because the chart has no evidence.

Two boundaries came with it. `lookup` needs an API server, so the check does nothing under
`helm template` and everything under a real install, upgrade or `--dry-run=server`, which is
why the proof lives in `tests/e2e/packaging_kind_test.go` rather than in `tools/helm-lint`.
And `volumeClaimTemplates` is immutable on a live StatefulSet, so toggling
`secrets.existingSecret` on a running release (the one case that changes the stamp without
changing the credentials) has to go through uninstall and install, which keeps the volume.

**Lesson.** When a resource deliberately outlives the release that created it, the release
needs a way to recognize it on the way back in. Stamp an identifier on the surviving object,
compare it before rendering, and refuse only on a proven mismatch rather than on a suspicion.

## 134. A lock over a cheap, idempotent write turned one slow or dead controller into every other controller refusing to start

**Symptom.** Four separate reports, all filed against the same code and all looking
unrelated. A controller SIGKILLed while provisioning a certificate made every other
controller in that directory refuse to start for two minutes. A directory that became
unwritable while a controller was provisioning made the refusal outlive the fault. A
controller that was merely SLOW made the others exhaust a fixed eight-attempt budget and
exit. And during a renewal a slow writer split the fleet across two certificates, with each
straggler passing its own healthcheck so nothing restarted it.

**Root cause.** One cause wearing four hats: the writers were serialized by an exclusive
claim (an `os.Mkdir` of `.generating`), so every failure mode of a claim holder became a
failure mode of every other process. The claim existed for one reason: a certificate and its
key were two files, `rename(2)` replaces one directory entry, and there is no call that
replaces two, so a reader arriving between two renames could see one writer's key beside
another's certificate. Tuning the claim could only move which of the four faces appeared;
raising the retry budget produces a fifth.

**Fix.** Delete the lock and remove the reason for it. The certificate and its key are now
published as ONE file (`serving.pem`), so publishing is exactly one rename, which is atomic
everywhere. `Ensure` is three steps and no loop: load what is published; if it cannot be
served, mint a replacement and publish it; then load again and serve whatever is there now.
N controllers may all generate and all publish, the last rename stands, and every one of them
reads that winner. Nobody waits, so nobody can be blocked by a process that is slow or gone,
and a loser's wasted keypair costs a few milliseconds of CPU.

**Lesson.** Before serializing writers, ask what the write costs and whether any two results
are interchangeable. Generating a self-signed certificate is milliseconds and any valid pair
is as good as any other, so the correct number of writers is "however many turn up". A lock
is worth its failure modes only when the work is expensive or the results differ.

## 135. A provenance record kept as one read-modify-write file lost a concurrent writer's entry, and the process serving that certificate failed its own healthcheck

**Symptom.** Sixteen controllers started against one certificate directory. Every one of them
provisioned and served a valid pair, and roughly one run in three had a controller whose OWN
healthcheck rejected the certificate it was presenting: "x509: certificate signed by unknown
authority" against a listener that was working perfectly. An orchestrator would have killed
it.

**Root cause.** The healthcheck verifies the local listener against the certificates this
deployment has provisioned, which were recorded in a single `provisioned.pem` that each
writer read, prepended itself to, and wrote back. That is a read-modify-write: a writer whose
read happened before another writer's write erased that other writer's entry. The certificate
was real, published and served; the record of it was gone.

**Fix.** One file per certificate, named after the certificate's own SHA-256 fingerprint, in
a `provisioned/` directory. No writer's file is ever any other writer's file, so nothing can
be lost, on any filesystem, with no coordination. Pruning keeps the newest sixteen and never
touches a record younger than an hour, because a count-only rule would have deleted eleven of
those sixteen entries while eleven processes were still serving those exact certificates,
which is the same failure arriving through the cleanup path.

**Lesson.** "Two processes cannot lose one another's work" is a property that has to hold for
every file a design writes, not just the important one. A read-modify-write on a shared file
is a lost update waiting for contention, and the cheapest fix is usually to stop sharing the
file: name it after its content and the collision cannot happen.

## 136. An ownership check keyed on the certificate destroyed the private key beside it whenever the certificate was absent

**Symptom.** A directory holding an operator's `key.pem` and no `cert.pem`, which is what a
half-finished secret mount looks like, had the key silently overwritten by a generated one on
the next start. The key existed in exactly one place.

**Root cause.** `ownsStoredCertificate` asked its question only of `cert.pem` and returned
"this package may write here" whenever that file was missing or unreadable, on the reasoning
that there was nothing to destroy. The key was never part of the question, so its presence
proved nothing and its absence was never checked.

**Fix.** Ask the question of every piece of material present, and prove each one the only way
it can be proven. A serving bundle carries its own provenance block. A `cert.pem` is proven
by the provenance records beside it. A private key carries nothing that names its author, so
it is proven by belonging to a certificate that is itself recorded; a key that belongs to no
recorded certificate is an operator's and is never written over, and the only line in the
package that deletes key material demands that same proof first.

**Lesson.** An "is there anything to destroy?" check must enumerate what could be destroyed,
not one representative of it. If a piece of material cannot prove its own authorship, the
absence of proof is a refusal, never a permission.

## 137. Every kind got one name budget, so the fix for a name collision created two StatefulSets that install cleanly and produce no pods

**Symptom.** A Helm release installed under a 40-character (or longer) release name reported
`STATUS: deployed`. Every object was created. Both StatefulSets sat at `0/1` ready forever,
the controller and every runner went into `CrashLoopBackOff` behind them because there was no
database and no broker, and nothing in the install output, in `helm status`, or in
`kubectl get statefulset` named a cause. The only evidence anywhere was a `FailedCreate`
event on each StatefulSet.

Observed by really installing the chart into a real cluster:

```
metadata.labels: Invalid value:
"oaaaa...aaaa-postgres-fc79dd66c": must be no more than 63 bytes
```

**Root cause.** This is the second defect in one line of arithmetic, and the first one is
worth restating because the second was its fix.

The first defect was a name collision. The naming helpers appended the component word and
then truncated the RESULT to 63, so a release name of 49 to 53 characters, all legal to Helm,
cut the suffix away entirely and rendered the controller, the runner, the PostgreSQL
StatefulSet and the NATS StatefulSet under one identical name. The fix inverted the order:
cut the release-scoped prefix to a budget first, then append the component, so a long release
name loses characters from a part nobody reads.

That budget was one number, 52, applied to all four workloads, derived as 63 minus the
longest component word. 63 is the DNS label limit, and it is the right ceiling for a Service,
whose name the API server rejects outright past it. It is the wrong ceiling for a
StatefulSet, and wrong in the direction that hides.

A StatefulSet's own name is only held to the 253-character subdomain limit, so the object is
accepted. What is not accepted is its pods. The StatefulSet controller labels each pod with
`controller-revision-hash`, whose value is the ControllerRevision name,
`<statefulset name>-<hash>`. A label value may not exceed 63 bytes. The hash is a `uint32`
printed in decimal and then re-encoded character for character, so it is 1 to 10 characters
long and its length changes with the pod template. So the real ceiling is 63 minus one dash
minus 10, which is 52 for the object name, which is 11 characters below the budget every kind
was sharing.

Two other limits sit inside that one and are the ones people reach for first: a pod is named
`<statefulset name>-<ordinal>`, and that string is both the pod's `spec.hostname`, which the
API server validates as a DNS label, and its `statefulset.kubernetes.io/pod-name` label. Both
permit a 61-character name at one digit of ordinal. Naming those two as the reason and
stopping there would have produced a budget 9 characters too generous and a defect that
appeared only for the longest release names.

The reason it survived is the shape worth remembering. The rule written to prevent a
recurrence read every object's name and held it to 63 if its kind was one of Service,
StatefulSet or Deployment. That is a rule about the names the chart WRITES. Every failure
here was in a name Kubernetes DERIVES from those, and no length of release name could make
the written names fail. The tests passed at every probe length, the chart rendered, `helm
lint` was clean, and a server-side `kubectl apply --dry-run=server` of the whole render was
clean too, because a dry run creates no pods.

**Fix.** The budget is per kind, and each ceiling is derived from what Kubernetes appends
rather than from the DNS label limit directly: 63 for the two Deployments and the Services
that share their names, and 63 minus 11 for the two StatefulSets. `helm-lint` now asserts the
derived names as well as the written ones, at eight release-name lengths: the
`controller-revision-hash` label, the pod name at the highest ordinal the replica count
produces, that pod name as a DNS label under the governing Service, the fully qualified
record in the longest namespace a cluster permits, and, for Deployments, that two workloads
stay distinguishable after Kubernetes truncates a generated pod name's base at 58 characters.
Both directions were proven against a real cluster: the pre-fix chart installed at a
53-character release name produces two StatefulSets with zero pods, and the fixed chart at
the same release name reaches every pod Running and Ready.

**Lesson.** A name limit belongs to a KIND, not to a cluster, and the limit that binds is
usually not on the name you wrote. Kubernetes derives other names from an object's name
(pods, ReplicaSets, ControllerRevisions, label values, DNS records), and each derivation has
its own ceiling; the tightest of them is what the object name is really held to. When a check
validates a name, ask what is built from that name and validate that too, because the
derived-name failures are the quiet ones: the object is created, the install reports success,
and the workload simply never starts. A dry run cannot see any of it, since a dry run creates
no pods.

## 138. A rule that never replaced material it could not prove it wrote made a directory no controller could ever start in again

**Symptom.** A controller reported, on every start, that its own certificate directory
"holds certificate material this controller did not write and cannot serve", and told the
operator to set `TLS_CERT_FILE` or move the material aside. Every other controller sharing
the directory said exactly the same thing. Nothing recovered without a human deleting a file
the message had just warned them not to touch. Four states produced it and this package
itself produces three of them:

1. `serving.pem` of zero length, or holding bytes that are not PEM, or cut off part way
   through the certificate. That is what a process killed mid-write, or a volume that ran out
   of space, leaves behind. The directory's own provenance records proved this package had
   written the file, and it was still refused.
2. A directory holding only `cert.pem`. A certificate with no key beside it can be served by
   nobody and is a secret to nobody.
3. Any read failure that was not "no such file": a permission error, a directory where the
   file should be, a named pipe. Every file this package writes is 0600, so two controllers
   running as two users produced this against each other, and the message blamed a secret
   nobody had put there.
4. A legacy `provisioned.pem` that existed and could not be read. That one did not stop the
   start: it made `AnchorsForDir` return an error instead of the anchors it had already
   gathered, so a controller that provisioned perfectly and served perfectly failed the
   container healthcheck it ships with, forever, over a file nothing else in the process
   reads. An orchestrator answers that by killing a healthy process.

**Root cause.** One rule, "never replace material I cannot prove I wrote", applied to
everything in the directory. It is the right rule for exactly one thing, a private key,
because a private key exists in exactly one place. Applied to bytes that do not parse it
protected nothing, because nothing can serve them and nobody can lose them. Applied to a
certificate it protected something public that could not be served without the key that was
not there. Applied to a file that could not be READ it stated a conclusion ("did not write")
where the honest answer was "unknown", and named a setting that would not have fixed it.
The rule also conflated two different promises: refusing to overwrite a file, which costs
nothing, and refusing to start, which costs a deployment its availability.

**Fix.** The question is asked about a private key and nothing else. Publishing is blocked by
a parseable private key this package cannot account for, or by a file that exists and cannot
be read; every refusal names the file, quotes what the operating system said, gives the uid
this process runs as, and states both ways out. Bytes that do not parse and a certificate
with no key beside it never block a start, and are still never overwritten, because
`certFileIsOurs` keeps the stricter rule at the write itself. The unreadable legacy record is
now advisory to the anchor builder: `readProvisioned` returns the certificates it did gather
alongside its error, ownership stops on the error and the anchors carry on without it.

**Lesson.** A protection has to name what it is protecting. "Material" was four different
things wearing one word, and the one of them worth a refusal was the private key. Before
writing a rule that can refuse forever, list the states it will refuse in, and check how many
of them the code itself can produce: three of these four were ours.

## 139. The trust anchors could not admit what a running replica was still presenting

**Symptom.** A controller that had been up for over an hour failed its own container
healthcheck, on every probe, while serving a certificate that worked. The trigger was one
sibling publishing one new certificate. An orchestrator answers a failing healthcheck by
restarting the process, so a perfectly healthy replica was killed.

**Root cause.** Two holes in the same set. The provenance records are the healthcheck's trust
anchors, and a record could disappear while the process presenting that certificate was still
running. `pruneProvisioned` kept the sixteen most recent records and deleted the rest once
they were an hour old, so the seventeenth certificate a directory ever saw evicted the first,
and an uptime longer than the grace window was all it took. Separately, a replica that REUSED
what it found wrote no record at all: its anchor was another writer's file, so a directory
whose records had been lost (an operator tidying up, a restore that missed a subdirectory)
left that replica trusting nothing of its own.

**Fix.** A record is removed only once the certificate in it has expired past a clock-skew
grace, because that is the only moment at which no process can legitimately still be
presenting it. Neither number in the old rule could be tuned to be right: any count is wrong
for a fleet one replica larger, and any age is wrong for an uptime one hour longer. And
`settle` now records what it is about to serve, so every replica puts its own answer into the
set, in a file named after its own fingerprint that no other writer will ever open.

**Lesson.** When a set decides whether a live process is healthy, membership has to be
governed by a fact about that process, not by housekeeping convenience. "The sixteen most
recent" and "younger than an hour" are facts about the directory. "Its certificate has not
expired" is a fact about what a replica can still be doing.

## 140. A log field named for a certificate carried the path of the private key

**Symptom.** The startup WARN line reported `cert_file` as the path of `serving.pem` on every
reuse, and the documentation described that field as a certificate path. An operator
following it into a `curl --cacert`, a ConfigMap or a copy to a colleague would have handed
this server's private key to something that only ever wanted the certificate.

**Root cause.** `ServingCert.CertFile` was set to the file the material was READ from. On the
publish path that was `cert.pem` and the field was right; on the reuse path the material is
read from the serving bundle, which is one file holding the certificate AND the key, so both
fields named it. Nothing was wrong with the value, and the name was a promise the value could
not keep.

**Fix.** `CertFile` is settled by reading `cert.pem` back and confirming it holds the
certificate being served, and `ServingCert.AnchorFile` reports whether a key-free copy exists
at all, so a caller with nothing safe to print prints nothing. The controller renamed its
fields to what they carry: `serving_file` for the material, `trust_anchor_file` for the copy
with no key in it.

**Lesson.** A field name is an instruction to whoever reads the log. If the name says
"certificate" then every value it can ever hold has to be a certificate and nothing else, or
the field has to be able to be absent.

## 141. The Kubernetes gate's cluster name is a constant it deletes on sight, so any second actor's cleanup is a live run's outage

**Symptom.** `make ci` failed at `TestPackagingReleaseGate_KubernetesInstall`, in
`assertALongReleaseNameStillProducesFourWorkingWorkloads`. Every assertion before it had
passed against a real cluster: the chart installed, `/readyz` reported its database and
broker, `bootstrap-admin` ran through `kubectl exec`, and the runner Deployment reached
Available with its in-pod `runner healthcheck` reporting an 8-second-old heartbeat. Then the
second install, at a 53-character release name, sat at `Available: 0/1` for its full
8-minute budget, after which every `kubectl` and `helm` call returned `connection refused`
against the API server. Run alone afterwards, the identical test passed in 259 seconds and
that same install completed in 67.

**Root cause.** The cluster's name is a constant, `kindClusterName = "pleiades-release-gate"`
in `tests/e2e/packaging_kind_test.go`, and `createKindCluster` opens by deleting any cluster
of that name. That delete-first is deliberate and correct for what it was written against: a
test binary killed before its `t.Cleanup` runs leaves the cluster behind, and `kind create`
refuses a name that already exists. What it cannot do is tell an ABANDONED cluster from a
LIVE one. The name is a shared global with no ownership marker, so any second actor holding
it, a concurrent `make ci`, a `make push-gate` overlapping a manual run, or a person tidying
up, destroys the first one's cluster mid-install.

The expensive part is not the lost run, it is where the failure surfaces. A test whose
infrastructure is removed reports the assertion that was executing, never the removal. This
one pointed at the long-release-name boundary, which is exactly where this chart has had two
real defects before (#137 is one of them), so the failure was credible in every particular
and was about none of them.

**Fix.** Partial, and the halves are worth separating.

What is fixed: `tools/breakglass` and `make break-glass`, the recovery path for leftover
infrastructure, refuses to run while a test run is live rather than cleaning underneath it.
It asks two independent questions, because neither covers the other's window: whether a
testcontainers reaper is running, and whether a `go test` process has its working directory
inside this repository. Its own removals are positively attributed to this repository first,
so a developer's long-lived cluster is listed and left rather than pruned. Verified against a
genuinely live `make ci`: it refused, exited 1, and the gate's cluster survived.

The gate itself was fixed in the same session, and its two halves separate the same way.

The NAME now carries an owner. `kindClusterPrefix` is the shared prefix and each run's cluster is
`<prefix>-<pid>`, so no two runs can name the same cluster and `kind create` cannot collide with a
live sibling. The unconditional delete-first is gone because there is nothing left for it to do.

The RECLAMATION now asks about the owner rather than about the name. `reclaimAbandonedClusters`
deletes a prefixed cluster only when the process id in its name no longer names a running process,
which is the one fact separating a cluster nobody will ever use again from one somebody is using
right now. A cluster whose owner is alive is left alone and logged even though it is almost
certainly in the way. PID reuse can make a dead cluster look alive; that is the safe direction,
since the failure is a leak a later run or `make break-glass` clears, where the opposite error is
the incident above. `tools/breakglass` applies the identical rule against the same prefix.

That rule's test caught a real bug on its first run: a pid of 0 was read as "dead, therefore
reclaimable", when `os.Getpid` never returns 0 and such a name proves nothing whatever.
Unattributable is now its own answer, distinct from abandoned, so an anomaly is left alone instead
of deleted. The live case is asserted against the test process's own id, so it cannot pass by
picking a number that happens to be free.

**Lesson.** Infrastructure identified by a constant has no owner, so every cleanup of it is a
race with every user of it, and the loser's failure is reported against whatever assertion
happened to be running. See LESSONS_LEARNED.md #129.

## 142. An unquoted chart value let an operator-supplied string add fields to objects the chart never wrote

**Symptom.** None, until somebody looked. The chart rendered, `helm lint` passed, `kubectl apply
--dry-run` accepted the output, and every existing helm-lint profile was green. The defect was found
by the Schema/Injection Hardening audit asking what happens to a hostile value rather than a
plausible one.

**Root cause.** `templates/controller-deployment.yaml` interpolated
`.Values.externalDatabase.existingSecret` and `.existingSecretKey` into the `secretKeyRef` that
supplies `DB_DSN`, unquoted, and `values.schema.json` declares both as plain `string` with no
pattern. A YAML plain scalar may continue onto following lines, so a value whose continuation is
indented to exactly the depth of the key it was rendered under stops being a value and becomes
structure. Demonstrated, not theorised:

    externalDatabase:
      existingSecret: "innocent-name\n                  optional: true"

rendered as

    secretKeyRef:
      name: innocent-name
      optional: true
      key: DB_DSN

The consequence is worse than the injection itself. `optional: true` means a Secret that is missing
no longer blocks the pod: `DB_DSN` is simply absent, and `cmd/controller` falls back to its
documented default of `sqlite://controller.db`. The operator gets a controller that passes every
probe and reports itself healthy while running on an empty container-local database instead of their
PostgreSQL, and loses whatever it writes at the next restart. A silent switch to the wrong database
is the failure this repository's whole fail-closed configuration convention exists to prevent, and
it arrived through a template rather than through code.

The same class was present on every string-valued interpolation in the chart: `imagePullPolicy`,
`claimName`, `storage`, `type`, `accessMode` and the runner's volume `name`. Only the two secret
fields had both an unconstrained schema type and a security-relevant destination, but all of them
could break a render.

**Fix.** Every string-valued interpolation is piped through `quote`. The numeric ones deliberately
are not: quoting a port, a replica count or a probe interval turns an integer into a string and
Kubernetes rejects it, so a blanket rule would have been its own outage.

The regression guard is `checkValueInjection` in `tools/helm-lint`, and it is dynamic rather than
static on purpose. A static rule would have to assert that every interpolation is quoted, which is
false and must stay false for the numeric ones, and deciding which is which needs a schema that does
not describe every value. So the guard renders the chart with the real payload and fails only if the
payload becomes a mapping key. Either safe outcome passes: refusing to render, or rendering the
payload inertly as a quoted scalar. Verified by removing the quote and watching it fail with the
exact finding, then restoring it.

**Lesson.** A template is an injection surface with no type system in front of it. Ask what a value
does when it contains a newline, not only when it contains the wrong word.

## 143. A Collection may import only pkg/, so the one SSH module hand-rolled the security-critical dial the transport layer already owned

**Symptom.** `internal/catalog/net/ssh/ping.go` contained its own dial loop, its own
`buildAuthMethod`, its own `hostKeyCallback` and its own `shellQuote`, all near-copies of
`internal/transport/ssh`. Its doc comment stated the consequence plainly: the method inherits
none of that package's circuit breaker or retry-with-backoff, and called the tradeoff
"acceptable for a lightweight, read-only diagnostic method, and would need revisiting if this
package grew a second, write-capable method."

**Root cause.** `internal/archtest`'s `TestCatalogPackagesImportOnlyPkg` correctly forbids a
Collection package from importing anything in this module outside `pkg/`, so no Collection can
reach `internal/transport/ssh` no matter how much of the same work it needs. Nothing under
`pkg/` did that work, so the only way to write an SSH-backed module was to write it again. The
rule is right and the gap under it was the defect.

**Fix.** The mechanism moved to `pkg/remoteexec`: dial with retry and backoff, the per-target
circuit breaker, fail-closed known_hosts verification, turning secrets into exactly one
authentication method, and the POSIX quoting that makes a command line safe.
`internal/transport/ssh` became a thin adapter over it and kept its `transport.Transport`
identity, its `credential.Credential` translation and its `Options` surface unchanged; its
container tests against a real, independent sshd pass unmodified, which is what proves the move
preserved behavior. `net.ssh.ping` was refactored onto the same primitive and its existing tests
pass unchanged, which is what proves the primitive is usable from a Collection.

**Lesson.** A layering rule that forbids reaching for shared code is only half a design. The
other half is a home for that code on the allowed side of the line, and the cost of not building
it is not duplication in the abstract: it is a second implementation of host key verification.

## 144. The Crawl tier handed every Collection method an empty secret set, so no method needing a credential could run from the CLI

**Symptom.** `pleiades run` against a runbook naming `net.ssh.ping` failed with "no usable
authentication method", and against any `net.catalyst.*` method with `no "username" secret
available`, on a device whose credential was in `.pleiades/credentials.yaml` the whole time. The
Walk tier was unaffected.

**Root cause.** `cmd/pleiades/run.go` passed `engine.NewDeviceRunbookContext` as the executor's
context constructor. That function ignores its device argument and returns
`engine.NewRunbookContext(nil)`, so `InjectSecrets` always returned an empty map. Its own comment
explained why, and the reason had expired: "which is empty here because no method in the catalog
needs a device secret yet" stopped being true the moment `net.ssh.ping` landed, and nothing
connected the two changes. The credential store was already constructed two lines away, for the
`ssh_exec` transport path only.

**Fix.** `engine.RunbookContextFunc` now takes a context and returns an error, and
`engine.NewCredentialRunbookContext(store)` resolves each device's stored credential and flattens
it into the context. A device with no stored credential is not an error and yields an empty set,
matching the Walk tier; any other lookup failure is reported, because an unreadable store and an
absent entry must not look alike.

**Lesson.** A comment that says "this is empty because nothing needs it yet" is a dependency
between two changes with nothing to enforce it. The first feature that needs the thing will not
find the comment.

## 145. Two collection methods' documentation had already drifted from the data the catalog is generated from

**Symptom.** `net.catalyst.site_facts` and `net.catalyst.tag_facts` each carried a `Doc.Examples`
entry in their registered manifest that `internal/forge/catalogdata` did not have. Nothing
failed, and nothing would have failed until somebody regenerated the catalog.

**Root cause.** `catalogdata` is the source `forge new-collection` is driven from, but the
scaffold template only ever emits `Doc.Summary`; every other documentation field on an
implemented method is hand-written into the generated file afterward. That makes the two copies
unavoidable, and no test compared them. The consequence is worse than stale prose: a
regeneration would silently drop those Examples, and `tools/gendocs`'s own
`TestImplementedMethodsHaveCompleteDocs` requires at least one Example on an implemented method,
so a from-scratch regeneration would produce a tree that fails its own completeness gate for a
reason nothing in the diff would explain.

**Fix.** `internal/archtest`'s `TestCatalogDataDocsMatchTheRegistry` compares every catalogdata
entry's `Doc` against the registered manifest's, field by field so a failure names which field
drifted. It found both entries on its first run; both were synced.

**Lesson.** When two copies of something are unavoidable, the test comparing them is not
optional, it is the thing that makes the duplication safe. Write it at the moment you create the
second copy, not after somebody notices the drift.

## 146. A circuit breaker latched half-open forever, so a device that was briefly down was never dialed again

**Symptom.** After a target's circuit opened and its cooldown elapsed, every subsequent
connection returned "circuit open, too many recent failures" with no dial attempted, forever.
The device could come back and nothing would notice.

**Root cause.** `Allow` is a transaction, not a query: on an open circuit past its cooldown it
hands out the single half-open probe and mutates the state to record that it did. Two calls sat
on one dial path, one in `Connect`'s early fast-fail check and one inside the retry loop. The
first consumed the probe and did not dial; the second saw a probe already in flight and refused.
Because only a real dial produces a `RecordSuccess` or a `RecordFailure`, nothing ever resolved
the half-open state, and the half-open branch has no cooldown re-check. Reachable with stock
defaults: five consecutive dial failures, which is two `exec.command` tasks, and the process
holds one Runner for its whole life through `remoteexec.Shared` or a composition root.

**Fix.** Split the two questions. `Permitted` looks without consuming and is what `Connect`
calls; `Allow` claims the probe and is called only by the code about to dial.
`TestConnect_ProbeSurvivesToTheDial` asserts on DIAL COUNTS across a real cooldown boundary,
because the wedge produced a perfectly reasonable-looking error every time.

**Lesson.** A method named like a predicate that is really a transaction will be called twice by
somebody. Name it for what it does, or make the read-only form the one that is easy to reach.
`LESSONS_LEARNED.md` #135's correction records how a mutation pass rationalized this instead of
finding it.

## 147. The idempotence guard looked in a different directory from the command it guarded

**Symptom.** A task with `chdir: /opt/app` and `creates: VERSION` re-ran on every execution even
after `/opt/app/VERSION` existed. The mirror case was worse: `chdir: /opt/app` with
`removes: stale.lock` reported "skipped, since stale.lock does not exist" and left
`/opt/app/stale.lock` in place while the run reported success.

**Root cause.** The command was built as `cd '<dir>' && <argv>` while the existence check ran a
bare `test -e <path>` on its own session, so a relative path resolved against the SSH login
directory. Ansible's own command module changes directory before evaluating `creates`, and this
package's doc comment claimed to mean what Ansible means. Every test used absolute paths, so
nothing noticed.

**Fix.** The working directory is resolved once, before the guard, and threaded into it, so both
run under the same `cd`. A directory that cannot be entered now reports a distinct exit status
and becomes an error rather than an absence, because reading it as "not there" is exactly what
made `removes` skip real work.

**Lesson.** When a predicate and the action it gates are computed separately, enumerate the
context each depends on and check they get the same one. And a predicate that can fail to
evaluate has three outcomes, not two.

## 148. A quoted directory beginning with a dash was parsed as a cd option, so the command ran in the home directory

**Symptom.** `chdir: "-P"` did not fail. `cd '-P'` is identical to `cd -P`, a valid flag with no
operand, so the shell changed to the home directory and the command ran there and reported
success.

**Root cause.** The value was correctly single-quoted, which defeats word splitting and every
form of expansion, and does nothing about option parsing. The `&&` already in place to stop a
bad directory could not help, because `cd` had not failed.

**Fix.** `cd -- '<dir>'`, with a test for both halves: a dash-named value is refused, and a
directory genuinely named `-P` still works, since "reject everything with a dash" is a different
and also wrong fix.

**Lesson.** Quoting and option termination are two separate defenses against two separate
grammars. Interpolating a value as a command's operand needs both.

## 149. A large standard input a remote command never read turned a successful command into an opaque failure

**Symptom.** `exec.command` with a `stdin` payload over roughly 256 KiB, against any command that
exits without draining its input, returned "EOF" with no exit status, no stdout and no stderr
recorded at all. The same payload into `cat` succeeded, which is what isolated the cause to
unconsumed input rather than size.

**Root cause.** `x/crypto/ssh`'s `Session.Wait` returns the standard input copy's error whenever
the command's own exit status was clean. With `session.Stdin` the library owns that copy, so a
command that exits successfully while the copy is still writing produces an `io.EOF` that is
reported in place of the real result, and the caller then returned before recording any stats.

**Fix.** Own the copy: write through `session.StdinPipe` on a goroutine whose error is
deliberately dropped, since a remote that stopped reading has not failed, it has finished. The
goroutine is reaped by a `WaitGroup` deferred to run after the session close that unblocks it.

**Lesson.** Worth recording separately: the regression test written beside the code passed
against the broken version, because an in-process handler never develops the same timing. Only
restoring the broken code showed that, and the working test had to live a layer up, against a
real shell. `LESSONS_LEARNED.md` #136.

## 150. The shipped runner container sets no HOME, so every SSH Collection method fails host key verification unless the task opts out

**Symptom.** In the published runner image, any Collection method that opens an SSH connection
fails at `Connect` with "no known_hosts path configured and the home directory could not be
determined", before any dial. The only way to make one work is
`insecure_skip_host_key_verify: true`, which means the escape hatch is not an edge case in
production, it is the only path that runs.

**Root cause.** Host key verification resolves `$HOME/.ssh/known_hosts` when no path is
configured, and Go's `os.UserHomeDir` on Linux reads `$HOME` and errors when it is empty; it does
not consult `/etc/passwd`. `Dockerfile.runner` sets `WORKDIR /app` and `USER 65532:65532` on a
distroless base and no `ENV HOME`, and `docker-compose.yml` sets none either. Nothing supplies a
known_hosts file to the container in the first place, so even with `HOME` set the file would be
absent.

**Why no test caught it.** Every SSH release gate manufactures the affordance it is testing
against: `cmd/runner/ssh_mesh_release_gate_test.go` and `cmd/pleiades/ssh_release_gate_test.go`
both set `HOME` to a temp directory and write a real known_hosts into it, which is right for
proving verification works and is exactly what hides the fact that the shipped image provides
neither. The gate proves the mechanism; nothing proves the deployment supplies its inputs.

**Fix.** Applied 2026-08-16, in three layers, after being recorded unfixed for one session.

The design question turned out to have two halves that were being confused. Where the host keys
COME FROM is a fleet-management question with no cheap answer. Where the file IS is a property of
the process, and every SSH tool ever written answers it with a setting. Only the second half was
blocking, so only the second half was solved: `pkg/remoteexec` gained `KnownHostsEnv`
(`PLEIADES_KNOWN_HOSTS`), resolved per connection between the caller's explicit path and the home
directory, which is OpenSSH's own layering and AGENTS.md's hierarchical-policy principle.

It went in `pkg/remoteexec` rather than a composition root because that is the only place that
reaches the code that needs it: under the Walk tier a Collection method runs in a per-task child
process with no composition root and no argument it controls, and it builds its own Options from
task parameters. One variable read in one place fixed all four call sites, which had all been
passing an empty path. Deliberately a PATH and never a POLICY: there is no variable that turns
verification off, because a variable set once is forgotten while a task parameter sits in the
runbook where review can see it.

`Dockerfile.runner` declares the variable and ships an empty `/app/ssh` to mount over, and
nothing is baked in: host keys in an image mean rebuilding to add a device, and an empty
known_hosts would be worse than none, since it parses and matches nothing and would fail per
device instead of once about a mount nobody made. `helm/the-pleiades` takes a ConfigMap or Secret
through `runner.knownHosts`, and `docker-compose.yml` carries the mount commented with both
shapes and with the reason it is not uncommented (a bind mount whose source is missing creates a
root-owned directory on the host).

**How it is proven, and the part that matters.** The unit tests on resolution order prove
nothing about the place this had to work, so the real evidence is
`TestSSHMeshReleaseGate_HostKeyVerifiedFromTheEnvironment`: a real dispatch over real NATS,
through the real Agent and DAG executor, into the spawned child process, dialing a real sshd
container whose key was captured the way `ssh-keyscan` captures one, with no known_hosts under
`$HOME` and no insecure flag anywhere. Its negative control removes the variable and requires
both a failure and an error naming the variable. `$HOME` is set to an empty directory rather
than unset, because emptying it also takes away what the Docker client reads and the test starts
containers. `TestPackagingReleaseGate_ImagesRunUnprivilegedWithNoShell` asserts the built image
declares the variable and carries the directory; mutating away either half fails it.

**Lesson.** A fail-closed default is only as good as the deployment's ability to satisfy it. When
a test sets up the thing production is supposed to provide, it has stopped testing whether
production provides it, and the honest place to notice that is the packaging, not the test.
Separately: when a fix looks blocked on a hard design question, check whether the hard question
is actually load-bearing. This one had a cheap half and an expensive half welded together in the
write-up, and the cheap half was the whole outage.

## 151. Every Collection manifest declares a required capability that nothing enforces at run time

**Symptom.** `exec.command` declares `RequiredCapabilities: [CommandExecCapable]` and will run
against any SSH-reachable device, including a Cisco switch. The same is true of every method in
the catalog.

**Root cause.** `Manifest.RequiredCapabilities` has no run-time reader. The Controller's admission
path consults `engine.ActionCapability`, a two-entry table naming only `ssh_exec` and
`ios_backup`, so a runbook of Collection tasks is dispatched with an empty requirement set and
`CapabilityAdmits` loops zero times. The Crawl tier's `collectionActionExecutor` checks status and
nothing else, and `internal/validate`'s capability rule keys off the same two-entry table. The
field is read by the documentation generators, by registration's name-exists check, and by
`internal/archtest`. That is all.

**How it was found, which is the part worth keeping.** Not by reading the field's definition,
which says plainly what it is for, but by a reviewer tracing what a comment claimed. A comment in
freshly written code asserted "admission has already checked that the device declares
CommandExecCapable" as the justification for an optional type assertion. The assertion was the
right shape for a different reason; the stated reason was false. It is now corrected in place,
which is the only reason this entry exists at all.

**Fix.** Not applied. The gap is a design decision, not an oversight to patch: enforcing manifest
capabilities means deciding what happens on the Runner, whose device adapter reports capabilities
as a membership test with no structural check, and where a static Go method set cannot express a
per-device capability set. `docs/03-migrating-from-ansible.md` already tells users the capability
column is documentation rather than a check, so the product is not lying to anyone; the code
comments were.

**Lesson.** A declared constraint with no enforcement is a comment, and it will be cited as a
guarantee by the next person who writes code near it. Either enforce it or say in the field's own
doc comment that nothing does.

## 152. The chart linter could not see a volumeMount naming a volume that does not exist

**Symptom.** A `volumeMount` whose `name` matches no volume in the pod renders cleanly, passes
`helm lint`, passes `tools/helm-lint`, and is then rejected by the API server at apply time. The
first thing that notices is an install.

**Root cause.** `tools/helm-lint` decoded `volumes` (to refuse a `hostPath`) and never decoded
`volumeMounts` at all, so the two sides were never compared. Nothing else in the pipeline can
compare them either: `helm template` produces text, and `helm lint` checks that a chart is well
formed rather than that what it produces is valid.

**How it was found.** By trying to justify a comment. A new render profile was added for the
runner's host key mount, and its comment claimed the profile would catch a mount and volume that
had drifted apart. Renaming the volume to check that claim produced a clean run, so the comment
was false. Fixing the linter was cheaper than softening the comment and protects the chart's four
other volumes as well.

**A false positive on the first run, which is part of the entry.** The check immediately reported
both database StatefulSets as broken. A StatefulSet declares storage in `volumeClaimTemplates`,
which is a different part of the object, so a mount against a claim looked like a mount against
nothing. A checker that is wrong about correct charts gets switched off, so this is a unit test
of its own rather than a line in a commit message.

**Fix.** `checkVolumeMounts` in `tools/helm-lint/checks.go`, reading pod volumes and
`volumeClaimTemplates` together. It runs the direction that fails: an unused volume is wasteful
and legal, a mount with no volume cannot start.

**Lesson.** When a comment claims a test catches something, break the thing and watch it get
caught. Two of this session's findings came from that one habit, and neither came from reading.

## 153. break-glass refuses forever under an agent harness, because its liveness guard matches the harness's own shells

**Symptom.** `make break-glass` reports "a test run appears to be using this state right now" and
lists eight `/bin/bash -c source /root/.claude/shell-snapshots/...` processes, then removes
nothing. No test is running. It never stops refusing, because those shells are the agent's
persistent shell pool and outlive every command.

**Root cause, sharper than it first looked.** The processes were not idle harness shells. They
were stale wait loops left by earlier sessions, each of the form
`until ! pgrep -f "make push-gate"; do sleep 15; done`. That pattern matches the loop's OWN
command line, so `pgrep` always finds something, the condition is never true, and the loop cannot
exit. Eleven of them had accumulated, the oldest running 31 hours. break-glass then sees eleven
live processes whose command lines name this repository's test commands and correctly concludes
something is using the state.

So there are two defects stacked. The self-matching `pgrep` is the one that creates the
processes, and it belongs to whoever writes the wait loop, not to break-glass; a loop waiting on
a command must exclude itself (match the real process, or check a pid, or `pgrep -f "[m]ake
push-gate"`). break-glass's own heuristic is then right for a human at a terminal and wrong in
the presence of any long-lived process that merely mentions the command.

**Fix.** None applied to the tool, deliberately. Kill the stale loops, which do nothing but
sleep. Then verify no real run is live (`pgrep -af "go test"`, `docker ps`), and check what would
go with
`make break-glass BREAK_GLASS_FLAGS="-force -n"` before running `-force`. The dry run is the
important half: it attributes every removal to this repository first, and on the run that found
this it correctly left Docker Desktop's own kind cluster alone.

**Lesson.** Two of them. A wait loop whose predicate can match the loop itself never terminates,
and `pgrep -f` on a string that appears in your own command line is the standard way to write
that bug. And a liveness heuristic keyed on "is something running that looks like it uses this"
inverts under any long-lived process that merely mentions the command, so read its evidence
before overriding it rather than deleting the guard.

## 154. A zero-value sentinel collided with a meaningful zero, turning "reject every session" into "accept them all"

**Symptom.** Three tests that prove a Collection method reports a connection failing partway
through a task started reporting "a connection failure partway through the task was reported as
success". Nothing about those methods had changed.

**Root cause.** The SSH test harness moved from one package's test file into
`pkg/remoteexec/remoteexectest`, and its session cap moved from a bare function argument into an
`Options` struct field. The old argument said "negative means unlimited". The new field's doc said
"zero means the default, which is unlimited", so `Options{}` would be a normal server. But zero is
a MEANINGFUL budget for this type: it means reject the very first session, which is precisely how
those tests reach the branch where authentication succeeds and the session does not. So every
caller asking for "reject everything" silently got an unlimited server, and the tests passed
through the happy path instead of the branch they were written for.

**Why it was caught.** Only because the move was verified by running the moved-onto tests
unchanged, which is the same evidence the `pkg/remoteexec` extraction was held to. Reading the
diff would not have found it: both the field and its default read perfectly sensibly on their own.

**Fix.** `SessionLimit *int`, with nil meaning unlimited, plus a `Limit(n int) *int` helper so no
caller writes a temporary. The zero value of the struct is now unambiguous and zero is expressible.

**Lesson.** Before making a struct field's zero value mean "unset", check whether zero is a value
the field can legitimately take. When it is, a pointer, a separate presence flag or a distinct
sentinel type is the fix; an int with a documented default is a silent behavior swap. Note the
shape of the failure: the code did the OPPOSITE of what the caller asked, and every affected test
still passed its setup, so the only signal was an assertion much later.

## 155. chmod before chown threw away the setuid bit the task had just asked for, and the module could never converge

**Symptom.** A task setting a special-bit mode together with an owner or group (a setuid helper, a
setgid shared directory's files) reported success and changed, and the device did not have the
special bit. Every later run reported changed again, forever, because the requested state was
never reached.

**Root cause.** `pkg/remotefile.Apply` sent `chmod` first and `chown`/`chgrp` second. Linux clears
`S_ISUID` and `S_ISGID` on a regular file whenever its owner or group changes, which is a
deliberate kernel protection: a setuid binary that changed hands would otherwise run as its new
owner. So the chown undid the chmod that had just run. Verified at a real shell before the fix:

    chmod 4755 f; chown daemon f   ->  755
    chmod 2755 f; chgrp daemon f   ->  755
    chgrp daemon f; chmod 2755 f   ->  2755
    mkdir d; chmod 2775 d; chown daemon d  ->  2775   (directories are immune)

Ansible orders owner, then group, then mode for exactly this reason
(`module_utils/basic.py`'s `set_fs_attributes_if_different`), which is the kind of detail the
superset rule exists to inherit rather than rediscover.

**Why no test caught it.** No test in the package combined a special-bit mode with an ownership
change; every mode fixture was an ordinary 0644 or 0600, on which the reordering is invisible.
Directories are immune, so `file.directory`'s tests could never have found it either. The
`file.permissions` tests were thorough, mutation-tested and all passed against the defect.

**How it was found.** An adversarial review pass whose whole assignment was convergence, run over
five freshly written modules after they were integrated green. It was reported with a shell
transcript rather than an argument, which is why it took one command to confirm rather than a
debugging session.

**Fix.** Ownership first, mode last, with the reason written at the call site. A side effect worth
knowing: `file.permissions` can no longer reach its own partial-failure branch, because a refused
chown now leaves the mode untouched, so there is no partial state to report. That branch is kept
anyway (the case it reports is real, just no longer reachable in the test environment) and the
package's coverage floor moved 100.0 to 99.5 with that written justification.

**Lesson.** Two. Ordering between two operations that each succeed is invisible to a test suite
that never combines them, so when a helper applies several attributes, test the combination and
not only each attribute. And where this platform claims to be a superset of Ansible, read what
Ansible's own module actually does before writing the equivalent: the ordering here is not
folklore, it is documented behavior that exists because someone hit this.

## 156. The manifest declared an inverse that could not be right, because the true inverse is a property of the run and not of the method

**Symptom.** No failure in a test. A design that was internally consistent, enforced at
registration, documented on every generated page, and wrong in a way that would have caused data
loss the first time anything acted on it.

**What it was.** `Manifest.Inverse` named the FQCN that undoes a method plus the prior-state keys a
rollback would feed it. `file.directory` declared `Method: "file.remove"`, `Captures:
["exists","kind","mode","owner","group"]`.

**Root cause.** A method's inverse is not a property of the method. It is a property of the run.
Three ways the static form breaks, each different:

- `svc.start` against a service that was ALREADY RUNNING must undo to nothing. Declaring "stop"
  tells a rollback to stop something the run never touched.
- `file.directory` that FOUND a directory and only fixed its mode must undo to the old mode, not to
  a removal. The static declaration named a removal, so a rollback would have deleted a directory
  the run did not create, along with everything in it.
- `http.request` is read-only or destructive depending on a parameter, so one declaration covering
  every invocation can only describe the worst case and is useless for the common one.

The shape also pushed reconstruction logic into the rollback engine: given an FQCN and a list of
captured keys, something has to know how to assemble a call, which means the engine has to know
something about every method in the catalog.

**How it was found.** Not by testing. The user pointed at `http.request` and said it was too
generic for the field to be useful, and proposed declaring reversibility as a true/false statement
with the actual inverse emitted in the output as nested data. That reframing is what exposed the
other two cases, which were latent in already-merged code.

**Fix.** Split whether from what. `Manifest.Reversibility{Reversible bool, Notes string}` answers
once, at registration, with the reason required when false. `sdk.RecordInverse` emits the concrete
instruction per run: an FQCN plus already-resolved parameters plus a human-readable description,
written to an `inverse` stat. The emitted instruction is a task, so undoing a run is running more
tasks through the same dispatcher, with the same capability checks and audit trail, and a rollback
engine needs no second execution path and no per-method knowledge.

A converged run emits NOTHING, and that absence is now meaningful: it is how the journal says
undoing this means doing nothing. The old shape had no way to express that.

**Lesson.** Before putting a property on a manifest, ask whether it is a property of the METHOD or
of the RUN. A run-dependent value declared statically has to collapse every case into one, and the
case it collapses to is usually the most destructive one. The tell here was that the field needed a
`Captures` list at all: a declaration that has to name the data someone else will need in order to
interpret it is not a declaration, it is half of a computation, and the half that is missing is the
half that knows the answer.

## 157. A repo-wide uniqueness test fails while worktree-isolated agents' checkouts are on disk

**Symptom.** `internal/redact`'s `TestRulesetHasExactlyOneCopy` failed reporting 19 copies of
`rules.json`, having found one in each of 18 leftover `.claude/worktrees/*` checkouts plus the real
one. Nothing in the change under test touched that package.

**Root cause.** A worktree is a full checkout of the repository. Any test that scans the tree and
asserts something is unique counts every worktree's copy. The Workflow tool auto-removes a worktree
only when it is UNCHANGED, and every one of these had files synced into it, so none qualified for
cleanup.

**Fix.** Remove the worktrees (`git worktree remove --force`, then `git worktree prune`). The
branches survive that, so committed work is still reachable.

**The mistake made while fixing it, which is the more useful half.** The worktrees were deleted to
make the test pass, BEFORE verifying they held nothing unintegrated. It happened to be safe, since
copying outputs to a directory outside the repository was part of the workflow's contract, but that
was luck rather than a check. Reconciled afterward: every one of 37 agent files diffed byte
identical against its copy in the main tree, and all 20 worktree branches were zero commits ahead.

**Lesson.** Reconcile before deleting, not after. And when a test fails in a package the change did
not touch, the cause is more often the environment than the code, so look at what the tooling left
behind before looking at the diff.

## 158. A new module was added as a bare transport-action name instead of an FQCN, by copying the oldest thing in the codebase

**Symptom.** WinRM execution shipped as `winrm_exec`, a bare underscored name registered in
`engine.ActionCapability` beside `ssh_exec` and `ios_backup`, and it was written into four example
runbooks and a Release Gate before anyone noticed. A module name in this catalog is
`xxx.xxx.xxx`: `net.cli.command`, `svc.systemd.start`, `pleiades.builtin.set_metadata`. The name
shipped was not merely un-namespaced, it was not in the shape at all.

**Root cause.** Two existing patterns sat side by side and the wrong one was nearer. `ssh_exec` is
the Phase W6 transport-action path and is the oldest dispatch mechanism in the module;
`internal/catalog/*` is the current one, with a registry, manifests, capability requirements,
reversibility and generated docs. Reaching a Windows host looked like "what `ssh_exec` does, but
WinRM", so `ssh_exec` became the template. Nothing in the code says "this is legacy": the
transport-action path is load-bearing, well-documented and passing tests, which is exactly what
makes it a convincing thing to copy.

The same session had already built the correct pattern for a different namespace, `svc.start`
resolving to `svc.systemd.start`, without recognising it applied here. The generic/concrete pair
was the answer to "how does one FQCN reach two platforms" and it had been written that morning.

**Fix.** `pkg/winrmexec` holds the implementation, because a Collection may import only `pkg/`.
`exec.winrm.shell` was scaffolded with `pleiades forge new-collection` and hand-completed, giving
it the manifest, capability requirement, reversibility answer and generated reference page every
other method has. `winrm_exec` was removed entirely, and with it `transport.Shell`,
`transport.ShellTransport`, `WinRMTarget`, the executor's shell dispatch and
`internal/transport/winrm`: all of that existed only to serve the wrong name.

**The general rule.** When extending a subsystem, find out which of its conventions is CURRENT
before copying the one that is nearest. Age is invisible in source: a legacy path that still works
looks exactly like a recommended one. The tell here was available and ignored, that everything
generated into `internal/catalog` had a three-segment name and the thing being copied did not.
Second tell: new code should come out of the generator when a generator exists. `pleiades forge
new-collection` would have produced the right shape by construction, and hand-writing the file was
what made the wrong shape possible.

## 159. netsh converting an adapter to the address it already holds by DHCP leaves it with no address at all

**Symptom.** `netsh interface ipv4 set address name=Ethernet static <ip> <mask> <gw>`, where `<ip>`
is the address the interface currently holds from its own DHCP lease, left a lab VM unreachable
twice. From the console: `DHCP Enabled: No`, and the only IPv4 address an APIPA `169.254.45.78`.
DHCP was disabled and nothing was bound.

**Root cause.** netsh performs the conversion in two steps, disabling DHCP and then binding the
static address, and the bind can fail while the disable has already taken, because the address
being bound is still held by the lease that was just released. The result is neither
configuration. It is timing-dependent rather than deterministic: the first conversion in a session
tends to succeed, and one run immediately after reverting to DHCP tends to fail, because the lease
has just been reissued.

**Two wrong diagnoses, recorded because the wrongness was the expensive part.** First guess was a
Windows Firewall profile flip: changing the address makes Network Location Awareness re-identify
the network, it is classified Public with no domain controller, and `WINRM-HTTP-In-TCP` covers
Domain and Private only. That is all true and it is not what happened. Second guess was the
opposite, that ARP failing proved nothing was at the address and so filtering was ruled out, which
was correct reasoning stated with more confidence than the evidence carried. The firewall theory
was then disproven properly: the Public rule was enabled for the second outage and the host went
dark anyway.

**Fix.** Convert to an address OUTSIDE the DHCP pool, reserved for the host. If the leased address
must be reused, release the lease first and accept the gap. Never run the conversion twice
back-to-back against the same address.
`cmd/pleiades/winrm_static_ip_release_gate_test.go` converts exactly once per run and registers its
revert as `t.Cleanup` before the change, so the adapter is restored even on failure or panic.

**The general rule.** A remote change that reconfigures the transport it arrived on cannot report
its own success, so it needs a verification path that does not depend on the change having worked.
IPv6 is that path on Windows: IPv4 and IPv6 are configured independently and WinRM listens on both,
so a host that has lost IPv4 usually still answers on 5985 over IPv6. That was confirmed working
during the second outage and is why the example inventory carries the same machine twice.

## 160. A "skip what already exists" flag applied per file resurrected fifteen stub tests underneath real implementations

**Symptom.** `go generate ./internal/forge/catalogdata` had never worked on an already-generated
tree: `forge new-collection` refuses to overwrite, so the second entry it reached failed the whole
run. Adding `--skip-existing` fixed that and reported "wrote 15 new file(s)" on a tree where every
method already existed. The fifteen were generated starter tests for `svc.*`, `svc.systemd.*` and
`net.catalyst.*`, and `go test ./internal/catalog/svc/...` immediately failed eleven times with
`Manifest.Status = implemented, want declared`.

**Root cause.** The skip was decided per file, and the unit a `forge new-*` subcommand generates is
per entry. A Collection method is two files, a source file and a starter test, and they are only
coherent together. Every one of the fifteen was a method whose implementation had been
hand-completed and whose generated starter test had been deliberately deleted, replaced by a real
test under a different name (`svc_test.go`, `systemd_test.go`, `catalyst_test.go`). The
implementation file existed, so it was skipped; the starter test did not, so it was written. What
landed was a generated test asserting the method is declared and returns "not implemented",
sitting next to an implementation that is neither.

The deeper mistake was reading the refusal as being about files. `writeGeneratedFile` refuses per
file because that is the level it operates at, and inverting a per-file refusal gives a per-file
skip, which is a mechanical transformation rather than an answer to "what does skipping mean here."
Skipping means "this entry has already been generated, leave it alone," and the evidence for that
is any of its files existing, not each of them separately.

**Fix.** `firstExistingFile` checks the whole set before writing any of it, and each subcommand
skips the entry as a unit and says so. The per-file refusal is unchanged for the normal path, where
it is still the right answer.

**Lesson.** When you invert a guard, re-derive what the inverted rule should mean rather than
negating the condition where it happens to be written. A refusal is allowed to be finer-grained
than the permission that replaces it, because refusing wrongly costs an error message and skipping
wrongly costs silence. Also: this was found by running the generator against the real repository
and reading `git status`, not by the tests, which is the argument for running a code generator
somewhere it can do damage you can still see.

## 161. A scaffolder's placeholder for an unsupported arity emitted a function signature that could not satisfy the type it was meant to produce

**Symptom.** `internal/forge/filterscaffold`'s `Reminder` renders a `*Binding` function alongside
the `cel.Function`/`cel.Overload` block it pastes into `internal/engine/cel_filters.go`. For a
one- or two-argument filter this compiles as-is (`cel.UnaryBinding`/`cel.BinaryBinding`, both fixed
arity, matching the generated `func fooBinding(arg0 ref.Val) ref.Val` shape exactly). For a
three-argument filter (`bindingFuncFor`'s documented arity-two ceiling), the generator still emitted
individual named parameters -- `func fooBinding(arg0 ref.Val, arg1 ref.Val, arg2 ref.Val) ref.Val`
-- registered against `cel.FunctionBinding(fooBinding)`. Phase 53's `RegexExtract`, this codebase's
first three-argument filter, hit this and was hand-rewritten from scratch as a real
`func(...ref.Val) ref.Val` with an arity check and indexed access, recorded at the time only as "the
forge has no typed `OverloadOpt` past arity two," not as "the generator's own stub does not compile
against the value it is registered as."

**Root cause.** `cel.FunctionBinding`'s real Go type is `func(...ref.Val) ref.Val`, a variadic
slice parameter, not a fixed tuple. `bindingFunc`'s per-parameter loop built `argN ref.Val` for
every `Param` regardless of count, correct for the two typed `OverloadOpt`s (`UnaryBinding`/
`BinaryBinding`, whose own Go types are the fixed two- and one-argument shapes the loop happens to
produce) but silently wrong for the untyped one: three individually named `ref.Val` parameters is
not assignable to `func(...ref.Val) ref.Val`. Nothing caught this the first time because the fix
was applied by hand, off the template, before the generated stub was ever asked to compile as a
`cel.FunctionBinding` value on its own.

**Fix.** `bindingFuncFor` now returns `cel.FunctionBinding` (not a `/* TODO: arity */` comment) for
arity three and above, and `bindingFunc` emits the real shape for that case: `func fooBinding(args
...ref.Val) ref.Val`, a generated `if len(args) != N { return types.NewErr(...) }` arity check, and
`args[i]` in place of each `argN`. Verified against `Reminder()`'s own output for a three-argument
config, byte for byte matching `RegexExtract`'s own hand-written binding, before either of Phase
54's two three-argument filters (`FilterListByKV`/`ExcludeListByKV`) was written.

**Lesson.** A generator's placeholder for a case it cannot fully handle should still be checked
against the real type it is meant to satisfy, not just "looks like the pattern for the cases that
do work." The first occurrence of an unsupported shape being hand-patched off the template hid the
question of *why* the generator couldn't produce it; only reading the generator's own source before
writing a second and third occurrence surfaced that the gap was a real bug (a signature mismatch),
not merely missing coverage. Three occurrences of the same one-off is the threshold this codebase
has already used elsewhere (Phase 51's `celToStringList`, Phase 52's structural types) to decide a
capability belongs in the tool rather than in the hand; this is the same judgment applied to a
scaffolder's own binding-generation logic rather than to its type table.

## 162. A doc comment assumed the stdlib PEM encoder validated its own block type, and it does not

**Symptom.** Phase 56's `pkg/filters.DERToPEM(der, blockType string) string` wraps caller-supplied
base64 bytes in a PEM block of the caller-supplied `blockType`, calling
`pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: raw})` directly. The first version of this
function's doc comment claimed "`pem.Encode` rejects a newline in `Type`, to prevent a caller from
smuggling an extra PEM header or body into what looks like a single block's type line" -- written
before checking whether that claim was actually true. It was written down as an assumption because
Go's `encoding/pem` package does validate *something* about a `Block` before encoding it (a
`Headers` map key containing a colon), which reads, at a glance, like the kind of package that would
also validate `Type`. A test asserting `DERToPEM(der, "CERT\nIFICATE")` returns `""` failed: the
function instead returned a PEM string with a literal embedded newline splitting `-----BEGIN
CERT` from `IFICATE-----`, meaning a `blockType` containing a raw `-----END X-----\n\n-----BEGIN Y---
--` sequence would let a caller smuggle an entire second, attacker-chosen PEM block into what a
downstream reader (another `filters.*` call, or code outside this platform entirely) would trust as
one clean block.

**Root cause.** Reading `encoding/pem`'s own source (`src/encoding/pem/pem.go`'s `Encode`) rather
than trusting the assumption: the function's *only* validation is `for k := range b.Headers { if
strings.Contains(k, ":") { return error } }`. `b.Type` is written into the output completely
unvalidated, as `out.Write([]byte(b.Type + "-----\n"))`, with no check for a newline, a null byte, or
anything else. The doc comment's claim was invented to sound plausible rather than verified against
the actual source, and the function that would have caught it -- a test asserting the newline case
specifically -- had not been written yet when the doc comment was.

**Fix.** `DERToPEM` now validates `blockType` itself against `pemBlockTypePattern`
(`^[A-Z0-9 -]+$`, the character class every real PEM label this codebase has reason to produce
already fits: `CERTIFICATE`, `PUBLIC KEY`, `RSA PRIVATE KEY`, `X509 CRL`), refusing anything outside
it before ever calling `pem.EncodeToMemory`. The doc comment was rewritten to state what was
actually verified, and a new test
(`TestPEMToDER_DERToPEM_RoundTrip/der_to_pem_block_type_injection_blocked`) constructs a real
injection payload (`"CERTIFICATE-----\n\n-----BEGIN EVIL"`) and asserts it is refused, not just that
a bare newline is. `coverage-floor.json`'s Phase 56 entry documents the resulting `encoded == nil`
branch in `DERToPEM`/`SSHPublicKeyToPEM` as provably unreachable now that this validation runs first
(the only way `pem.EncodeToMemory` can still fail, a colon in a `Headers` key, is unreachable since
neither call site ever sets `Headers`).

**Lesson.** A doc comment describing what a called stdlib function does is a claim, not a fact,
until the function's own source (or its documented contract) has actually been read. This is the
same discipline `.AGENTS/AGENTS.md`'s LSP-over-grep mandate asks for applied one level up: querying
the real tool (here, reading the real source) instead of narrating what a function "probably" does
based on how safe-sounding packages usually behave. The finding surfaced only because a test was
written for the specific case the doc comment claimed was handled; a test suite that only exercises
the happy path (a well-formed `blockType` like `"CERTIFICATE"`) would have shipped this exact
PEM-injection vector with a doc comment actively asserting it was closed.

## 163. A first-draft `GzipDecompress` had an input cap but no output cap, so a 1 KiB compressed value could allocate 64 MiB

**Symptom.** Phase 58's `pkg/filters.GzipDecompress(data []byte) string` bounds its own *input*
(`len(data) > MaxStructuredInputBytes`, 1 MiB) before ever calling `gzip.NewReader`, matching every
other function in this package's own "bound input length before parsing" invariant
(`pkg/filters/filters.go`'s package doc). The first draft then decompressed with a bare
`io.ReadAll(gzipReader)`, trusting that input cap to also bound the *output*. It does not: gzip's own
format allows extreme compression ratios for pathological input (a long run of one repeated byte
compresses to a few hundred bytes regardless of how long the run is), so a compressed value
comfortably under the 1 MiB input cap can still decompress to tens or hundreds of megabytes. This
phase's own Schema/Injection Hardening checklist item ("bounds input length before parsing... no
unbounded allocation") was written broadly enough to cover this, and a deliberate test
(`TestGzipDecompress/decompression_bomb_refused`, constructing a real 64 MiB payload of one repeated
byte and confirming its compressed form is well under the input cap before asserting it gets refused)
caught the gap during this phase's own audit pass, not in review of someone else's code.

**Root cause.** `MaxStructuredInputBytes` (and `MaxInputBytes` before it) was designed for exactly
one shape of risk: a caller-supplied *flat* value whose byte length is invisible to the CEL engine's
own per-call cost accounting (`pkg/filters/filters.go`'s own justification for why the cap has to
live in the function, not the engine). Gzip decompression is a second, structurally different risk
neither cap addresses: the *decoded* size is not proportional to the *encoded* size at all, so
capping the thing you can see (the compressed bytes) says nothing about the thing you cannot see yet
(the decompressed bytes) until decompression has already happened.

**Fix.** A new, separate constant, `maxGzipDecompressedBytes = 16 << 20` (16 MiB), wraps the
`gzip.Reader` in `io.LimitReader(r, maxGzipDecompressedBytes+1)` before `io.ReadAll`, and the result
is checked against the cap a second time after reading (the `+1` distinguishes "read exactly the cap"
from "there was more data past it," since `io.ReadAll` against a `LimitReader` cannot itself report
which case occurred). Exceeding the cap returns `""`, this package's own established sentinel for
malformed/refused input, rather than a partial, silently truncated string. The value (16 MiB) was
chosen as generous headroom over any ratio real log or config text achieves against a 1 MiB
compressed input, while still refusing to allocate without bound for a crafted one; the constant's
own doc comment records this reasoning and cites this entry.

**Lesson.** An input-length cap and an output-length cap are not the same control, and a function
whose stated job is decompression is exactly the shape where conflating them is dangerous: the
premise of the tool is that a small input legitimately produces a large output, which is precisely
what a decompression-bomb check has to catch without also breaking the legitimate case. This is a
general instance of PLAN.md Section 36's own decision to give Phase 52's document-shaped filters
their own separately-justified `MaxStructuredInputBytes` rather than reusing the flat-scalar
`MaxInputBytes` unchanged: a new function whose risk shape genuinely differs from every existing cap
needs its own cap, not a reuse-by-default of whichever one is already in scope. The catch came from
writing an adversarial test for the checklist's own stated concern (unbounded allocation) rather than
only testing the round-trip happy path, the same lesson entry #162 draws from a different angle.


## 164. A recurrence walk that ran in the target time zone let DST normalisation feed back into its own iteration state

**Symptom.** `FREQ=HOURLY` anchored just before a spring-forward transition
produced the same instant over and over: `00:00`, `01:00`, then `01:00`,
`01:00`, `01:00`... forever. The rule appeared to fire, then to stall.

**Root cause.** The expansion walked candidate periods by constructing each
next period with `time.Date(..., loc)` from the previous one's calendar fields.
For a wall-clock reading inside a daylight saving gap, `time.Date` does not
return that reading -- it normalises to a real instant, whose wall clock is a
different hour. That normalised hour was then read back out and used to build
the next period, so the walk got stuck at the transition instead of stepping
through it. The bug is not in the arithmetic; it is that the iteration state
passed through a lossy transformation on every step.

**Fix.** Walk in civil (wall-clock) time, carried as a `time.Time` anchored in
UTC because UTC has no transitions and therefore no normalisation, and convert
to a real instant only at the moment of emission. The location is now consulted
exactly once per occurrence, at a point where its result cannot influence
anything downstream.

**Lesson.** A value that a zone-aware constructor may silently adjust must not
be fed back into the loop that produced it. Keep zone-naive arithmetic and
zone-aware resolution in separate phases, with the conversion at the boundary.

## 165. Go's time.Date and PEP 495 fold=0 resolve a nonexistent wall clock to different instants

**Symptom.** A daily rule at a time inside a spring-forward gap produced an
instant one hour earlier than the same rule expanded by python-dateutil, which
is what AWX uses. Every other case in a 36-case parity corpus agreed exactly.

**Root cause.** For a wall-clock reading that does not exist, the two
implementations pick opposite offsets. Go's `time.Date` uses the
POST-transition offset, which moves the resulting wall clock backwards (02:30
in New York on 2024-03-10 comes back as 01:30 EST, 06:30Z). PEP 495's `fold=0`,
which `zoneinfo` and `dateutil` follow, keeps the wall clock and applies the
PRE-transition offset, yielding 07:30Z, which renders as 03:30 EDT. Neither is
wrong; they are different documented conventions, and nothing in either
standard library's documentation puts them side by side.

**Fix.** An explicit `localize` step that detects the gap by round-tripping the
wall clock, and on a mismatch recovers the pre-transition offset (by asking what
the same wall clock meant a day earlier) and places the instant accordingly.
Asserted against the generated dateutil corpus rather than reasoned about.

**Lesson.** When matching another implementation's date arithmetic, the gap and
fold cases are where the conventions differ, and the difference is invisible in
every other test. Establish the target's behaviour by running it, not by
reading a specification both implementations claim to follow. Also note the
consequence for display: a nonexistent local reading like `02:30-0500` can be
represented by Python and cannot be represented by a Go `time.Time` at all, so
a parity test comparing formatted local strings must compare instants instead
and treat the rendering difference as expected.

## 166. A concurrency test passed because most of its workers crashed before reaching the code under test

**Symptom.** A test driving eight concurrent scanners at one schedule asserted
exactly one job was launched, and passed. Its log was full of
`database table is locked` errors.

**Root cause.** The fixture used SQLite's shared-cache in-memory mode, which
answers a contending writer with `SQLITE_LOCKED` immediately. `_busy_timeout`
retries `SQLITE_BUSY` and does not cover `SQLITE_LOCKED`, so seven of the eight
workers failed before ever attempting the claim. The assertion held because
only one worker got far enough to launch -- not because the unique index that
is the actual duplicate-fire guard adjudicated anything. The test would have
passed with the guard removed entirely.

**Fix.** Move the fixture to a file-backed database in WAL mode, where writers
queue instead of failing, so the workers genuinely contend. Then strengthen the
assertion beyond the launch count: check that the occurrence exists exactly once
and that no occurrence was left in the intermediate `claimed` state, so a
future regression to lock-failure cannot pass again.

**Lesson.** A concurrency test that passes is not evidence until you have
confirmed the workers actually reached the contended operation. Read the test's
own log output on a passing run; errors on a green test are a signal that the
assertion may be measuring something other than what it names.


## 167. A view's create form rendered no controls at all because every field omitted `InForm`, and the whole conformance suite still passed

**Symptom.** A newly implemented view's list rendered correctly, with real rows
and real data. Its create page returned 200 and rendered the page chrome, the
heading, and working Save and Cancel buttons -- and not one form control. Eight
declared fields, zero inputs.

**Root cause.** `view.Field` gates form rendering on an explicit `InForm bool`,
separately from `InList`. The new view set `InList` on the fields it wanted as
columns and never set `InForm` on any of them, so `Field.Writable()` returned
false for all eight and the renderer omitted every control. The declaration
read as complete because the fields, kinds, labels, help text, validation
bounds and option sources were all present and correct; the one flag that makes
any of it appear was the one absent thing.

**Why the tests did not catch it.** Nothing in the conformance suite asserts
that a writable resource renders any controls. `TestViewConformance_EveryViewRendersItsList`
renders the list, which was fine. The edit-form conformance test checks the
properties of controls that ARE rendered, so a form with none vacuously
satisfied it. `view.Register` accepts a descriptor whose Handlers are writable
but whose fields are all non-form, because that is a legitimate shape for a
resource written entirely through an action rather than a form. Every layer was
individually correct and the composite was useless.

**How it was actually found.** By rendering the page and reading it -- RULE 0
applied to the UI rather than to an executor. A grep for `<input|<select|
<textarea` in the returned HTML found only the CSRF hidden fields.

**Fix.** Set `InForm: true` on every field the form should carry.

**Lesson.** A "renders 200" assertion proves a page did not crash, not that it
contains anything. For any writable view, assert that the create form actually
carries a control for each field it means to collect -- an empty form is the
UI's version of the shipped-but-unreachable failure this repository has
recorded repeatedly, and it passes every test that only inspects what is
present.

## 168. A hop chain's `Connect` kept closing only its last leg, leaking every earlier hop's connection

**Symptom.** A real-container goleak test for the new N-hop SSH mechanism
(`TestSSHContainer_HopChain_EmptyRouteIsUnchanged`) failed with a background
`golang.org/x/crypto/ssh` goroutine still running after the test's own `Conn`
was closed: a `(*handshakeTransport).kexLoop`, `(*mux).loop`, and
`(*Client).handleChannelOpens` set, the exact shape a live, never-closed
`*ssh.Client` leaves behind.

**Root cause.** `pkg/remoteexec.Runner.Connect`'s per-hop loop dialed each leg
in turn, holding only the single most recently dialed `*ssh.Client` in a local
variable that got overwritten on every iteration
(`client = next`). The returned `Conn` wrapped that one, final client and
nothing else, so `Conn.Close()` closed only the target's own connection. Every
earlier hop's client (the bastion, and any jump host nested behind it) was
still a fully live, authenticated SSH connection with its own background
goroutines, referenced by nothing after `Connect` returned, and therefore
never closed by anything.

**Why it was not caught earlier.** The mechanism's own happy-path tests
(dialing through a bastion, running a command, checking the result) all
passed, because a leaked client still forwards traffic correctly for the
tunneled connection built on top of it -- the bug has no effect on
correctness, only on cleanup. It surfaced only because a goleak check was
added to the same test, specifically because Phase 72's own Release Gate
required proving a torn-down hop chain leaves no goroutine behind.

**Fix.** `Conn` gained a `chain []*ssh.Client` field (every client dialed to
reach the target, in dial order), and `Close` walks it in REVERSE:
innermost (target, or the last hop) first, then back out to the first hop.
Reverse order matters, not just completeness -- a hop's client owns the
tunneled connection the next hop was built on, so closing the first hop while
a later one is still "open" at the Go object level would sever a connection
out from under something still using it. `Connect` itself also gained a
`defer` that closes every already-dialed client in the same reverse order if
a LATER leg's dial fails, so a chain that fails partway through does not leak
the legs that succeeded before the failure.

**Lesson.** A multi-resource acquire-in-a-loop (N connections dialed one at a
time, where each later one depends on an earlier one staying open) needs a
single owner that tracks the WHOLE chain, not just the most recent success --
"keep the latest thing in a variable" is a pattern built for a single
resource, and silently drops every earlier one the moment a second resource
enters the picture. Whenever a loop dials, opens, or acquires more than once
before returning success, ask specifically what closes the N-1 things that
were NOT the last one, and prove it with a leak check against something the
loop actually iterates more than once, not just a single-hop case.

## 169. A goleak check's baseline depended on which other tests happened to run first in the same binary

**Symptom.** A new real-container chaos test
(`TestSSHHopChain_SeveredBastionMidTunneledCommandSurfacesANamedError`)
passed when run as part of the package's full test suite, but failed with a
`net/http.(*Transport).dialConn`-owned goroutine leak when run alone via `go
test -run TestSSHHopChain`.

**Root cause.** The test called the package's existing `verifyNoLeaks(t)`
helper, which checks against a package-level `sharedContainerGoleak`
variable: a `goleak.IgnoreCurrent()` snapshot populated once, by
`sync.Once`, inside `requireSSHContainer` -- a function this new test never
called, because it built its own dedicated container and network rather than
using the package's shared one. When some OTHER test in the same binary had
already called `requireSSHContainer` first, `sharedContainerGoleak` held a
real baseline and the check passed. When this test ran alone, nothing had
ever populated it, so `goleak.VerifyNone` ran with no baseline exceptions at
all and correctly, but uselessly, flagged testcontainers-go's own background
HTTP transport goroutines (idle keep-alive connections to the Docker daemon
and, in this test, the toxiproxy control API) as if the test's own SSH code
had leaked them.

**Why it was not caught immediately.** The full-package run order happened to
put a `requireSSHContainer`-calling test first, so the bug was invisible
until the new test was run in isolation, which is exactly how a developer
iterating on one new test, or CI running `-run` against a single package,
would actually invoke it.

**Fix.** The test takes its OWN local `goleak.IgnoreCurrent()` snapshot,
after its own container, network, and proxy are already up and a baseline
connection has already completed a full dial-and-close cycle, and verifies
against that local snapshot rather than the package-level shared one.

**Lesson.** A shared, `sync.Once`-populated test baseline is only a
correctness dependency, not just a convenience, for every test that reads it
-- and a test that does not itself run the code path that populates it is
implicitly depending on some OTHER test having already run first in the same
binary. Any goleak (or similar) check taken against a package-level shared
snapshot needs either a guarantee that the populating call always runs first
(order-independent, e.g. `TestMain`), or its own local snapshot taken after
its own setup, the same way this fix landed. "It passed in the full suite"
is not evidence a leak check is correct; run the specific test alone before
trusting it.

## 170. Three `StatusImplemented` Collection methods required a capability zero device types could structurally satisfy

**Symptom.** `container.docker.run`, `container.docker.stop` and
`container.docker.remove` were `StatusImplemented`, fully coded, fully
tested, and their own `RequiredCapabilities` correctly named
`capability.NameDocker` (`internal/catalog/container/docker/{run,stop,remove}.go`).
Every real invocation of any of the three, against any real device in this
repository, would nonetheless have been refused by
`engine.checkMethodCapabilities` before the task ever ran.

**Root cause.** `checkMethodCapabilities` (`internal/engine/collection_action.go:106`)
calls `device.HasCapability(required)`, which is `Declares(name) &&
capability.Implements(item, name)` on every concrete device type
(`linux.Server.HasCapability`, and its siblings, all the identical
one-liner). `capability.Implements` is a plain Go type assertion against
the interface `DockerCapable` binds to
(`_, ok := item.(DockerCapable)`), and `DockerCapable.DockerSocketPath()
string` (its name before this fix) had **zero implementers anywhere in the
module** -- confirmed empirically with a throwaway probe against a real
`linux.Server`, with a real capability it does implement
(`SSHTransportCapable`) as a non-vacuous control:
`HasCapability(DockerCapable) = false`,
`HasCapability(SSHTransportCapable) = true`. Every real Linux, Windows,
Cisco, AWS or Catalyst Center device in the codebase would fail the same
way.

**Why it was not caught earlier.** `docker_test.go`'s own suite built its
target device as `&inventorytest.Stub{Caps:
[]capability.Name{capability.NameDocker}}`, and `Stub.HasCapability` (`pkg/inventory/inventorytest/stub.go`)
deliberately skips the structural half of the check -- its own doc comment
says a test double declaring a capability "is asserting the behavior it
wants, not classifying a real device." So every test exercised exactly the
one code path that could never actually fail this way, which is RULE 0's
own thesis stated as a bug instead of a warning.
`linux/server.go:89-99`'s own comment already recorded the identical class
of defect happening once before, for `POSIXFileSystemCapable` and
`FactGathererCapable` (fifteen working methods silently refused until the
gap was declared); Docker was simply the next capability nobody had
audited against a real device type.

**Fix.** `DockerCapable.DockerSocketPath() string` renamed to
`DockerEndpoint() capability.SocketAddress` (a new named string type, so a
Windows named pipe can never be mistaken for a POSIX path). A real
container-host device type, `container.Host` (scaffolded through the real
`pleiades forge new-device` CLI, then hand-completed with `SSHHost`/
`SSHPort`/`DockerEndpoint`/`IPAddress`), wired into
`internal/inventory/builtins.go`. A new systemic guard,
`internal/archtest.TestImplementedCollectionCapabilitiesAreSatisfiable`,
now fails the build if any `StatusImplemented` Collection method's
`RequiredCapabilities` names a capability no registered device type
structurally implements -- proven to actually catch this exact defect by a
negative control (temporarily un-wiring the new device type reproduces the
three failures verbatim).

**Lesson.** A capability manifest and a capability-satisfying device type
are two independent claims, and nothing before this connected them except
a runtime check every existing test bypassed. `RequiredCapabilities` being
well-formed (`TestCollectionManifestsNameKnownCapabilities`, which only
checks the name is registered) is not the same claim as
`RequiredCapabilities` being satisfiable, and a `StatusImplemented` badge
on a method proves neither. See [[171]] for what the same new guard found
next.

## 171. A guard built for one capability gap found two more the same way, and three others already disclosed

**Symptom.** Running the new `TestImplementedCollectionCapabilitiesAreSatisfiable`
sweep (entry 170) for the first time did not report one failure, it
reported nineteen, across six capabilities: `PackageManagerCapable`,
`AptCapable`, `DnfCapable`, `PosixAccountCapable`, `FirewalldCapable`, and
`NetworkAddressableCapable`.

**Root cause.** Four of the six were not new information: `pkg/apt/apt.go`,
`pkg/dnf/dnf.go`, `internal/catalog/identity/user/user.go` and
`identity/group/group.go` each already carried an honest "the capability
this cannot reach yet" section in their own package doc comment, calling
the gap "settled, intentional architecture" (package-manager family and
POSIX account management are genuinely per-distro/not-yet-collected
classification data, unlike a service manager, which has a safe universal
default). The other two were not disclosed anywhere.
`fw.firewalld.{allow,deny,reload}` required `FirewalldCapable`
(`FirewalldZone() string`, structurally unimplemented by any type) with no
caveat in `internal/catalog/fw/firewalld/firewalld.go` at all.
`pleiades.builtin.wait.port` required `NetworkAddressableCapable`
(`IPAddress() string`) with no caveat either, despite that accessor being
trivial, already-known data every network-reachable device type already
stores under its own name (`SSHHost`, `WinRMHost`).

**Why it was not caught earlier.** Same as entry 170: every method's own
tests used `inventorytest.Stub`, and nothing before this sweep ever
constructed a fully classified instance of every real device type and
asked whether each one's own `HasCapability` could ever return true.

**Fix.** Split by whether the gap was genuinely architectural.
`NetworkAddressableCapable` was fixed for real: `IPAddress()` added to
`linux.Server`, `windows.Server`, `cisco.Router`, `cisco.Switch` and the
new `container.Host` (each delegating to its existing host accessor), and
`capability.NameNetworkAddressable` added to each type's baseline
capability set, since this is universal data, not an optional per-vendor
choice -- there was no honest architectural reason to leave it
unsatisfiable. `FirewalldCapable` was documented rather than fixed,
matching the `AptCapable`/`DnfCapable` precedent exactly (firewalld is
genuinely optional per-distro software; not every Linux server runs it,
so it cannot join the unconditional baseline the way `SystemdCapable`
did): `firewalld.go` gained the identical "capability this cannot reach
yet" section `apt.go` already carries. All five genuinely-architectural
capabilities (`PackageManagerCapable`, `AptCapable`, `DnfCapable`,
`PosixAccountCapable`, `FirewalldCapable`) were then added to a new
`acceptedUnsatisfiableCapabilities` allowlist in
`internal/archtest/registry_sweep_test.go`, each entry citing the exact
doc comment that discloses it -- matching `gosec-waivers.json`'s
established per-entry-reason convention rather than a blanket
suppression. A companion test,
`TestAcceptedUnsatisfiableCapabilitiesAreNotStale`, fails if any
allowlisted capability ever becomes satisfiable for real, so the
exemption cannot silently outlive its own justification (proven by a
negative control: temporarily allowlisting the now-fixed
`NetworkAddressableCapable` makes the staleness test fail immediately).

**Lesson.** A systemic guard built to catch one specific, already-known
bug should be run for real before assuming its blast radius is exactly
one bug wide -- this one surfaced a whole capability-satisfiability class
at once, most of it already honestly accounted for in source, some of it
not. And an allowlist for a real, accepted exception needs the same
per-entry-reason discipline and the same staleness guard `gosec-waivers.json`
and `flaky-packages.json` already established for this project's other two
waiver mechanisms -- a bare list of exempted names, with no citation and
no drift check, would have been a second, unaudited way for exactly this
class of bug to hide again.

## 172. A byte-stream "read until quiet" loop treated a closed connection as a hard failure instead of a valid end of output

**Symptom.** `pkg/serialtcp.Exec`'s own real test suite, run against a
real local TCP server (`net.Listen`), failed three of six tests with
`serialtcp: EOF` the first time it ran: `TestExec_RoundTripsThroughARealTCPConnection`,
`TestExec_ConnectionLifecycleOpensAndClosesCleanly`, and
`TestExec_DefaultsWhenOptionsIsZeroValue`.

**Root cause.** `readUntilQuiet` accumulated bytes from `net.Conn.Read`
until one call returned a `net.Error` with `Timeout() == true` -- the
documented signal a per-call `SetReadDeadline` had elapsed with nothing
new to read, meaning "no more output is coming for now." The test
double's own handler (`echoServer`'s callback) wrote its canned response
and then returned, which ran its `defer conn.Close()` almost
immediately. The client's second `Read` call, made to see whether more
output was coming, therefore saw `io.EOF` (the far end closed the
connection) rather than a timeout, and `readUntilQuiet` treated any
non-timeout error, `io.EOF` included, as a hard failure.

**Why it was not caught before running the real tests.** The design was
reasoned out and written directly from the analogous, already-verified
`pkg/serialexec` shape (a local serial line's termios-based `(0, nil)`
timeout contract), and the difference between the two protocols' actual
"no more data" signals was assumed to be only the shape of a timeout
(`net.Error.Timeout()` versus `(0, nil)`), not that TCP has a SECOND,
equally normal termination signal — the far end closing the
connection — that a local serial line has no equivalent of at all. A
real socat PTY pair was used to verify `pkg/serialexec`'s identical
timeout logic before writing any code (see `pkg/serialexec`'s own doc
comment), but `pkg/serialtcp`'s design was written from that verified
precedent by analogy rather than independently checked against a real
TCP connection first — the gap was found only once the real tests, which
happened to write a test double that closes promptly (a realistic
console-server behavior, not a contrived one), were actually run.

**Fix.** `readUntilQuiet` now treats `io.EOF` identically to a read
timeout: both mean "stop reading, return what was accumulated," not an
error. A raw byte pipe has no session semantics to say whether the far
end closing the connection was deliberate, and `transport.Result.ExitStatusUnknown`'s
whole premise is that this package cannot know more than "here is what
came back before it stopped" — an EOF is exactly as valid an answer to
that question as a quiet period is.

**Lesson.** Porting a verified design from one protocol to a sibling
protocol by analogy is not the same as verifying the port itself: two
protocols that share a shape (write-then-read-until-quiet) can still
disagree about which underlying signals mean "quiet," and the honest
move is to write the real test for the NEW protocol and run it before
trusting the port, not just to reuse the reasoning that verified the
original. This is also a second instance of a shape this project's own
`FAILURE_PATTERNS.md` already knows: a test double's realistic behavior
(closing the connection promptly, which a real console server or web
server might well do) found a real production bug a less realistic
double (one that kept the connection open forever) would have hidden.

## 173. Two shipped Adapter packages carried real logic at 0.0% test coverage, hidden one layer below a fully-tested primitive

**Symptom.** While building `internal/transport/telnet` (Phase 73
Workstream E) and reaching for the same "thin `internal/transport/*`
Adapter over a `pkg/` primitive" shape `internal/transport/ssh`,
`internal/transport/serial`, and `internal/transport/serialtcp` already
established, a routine `go test ./internal/transport/serial/...
./internal/transport/serialtcp/... -cover` check (done before writing
the new package's own test, to see what the established bar actually
was) showed both existing packages at `0.0% of statements` — not "low,"
zero. Neither had a test file at all.

**Root cause.** Workstream D (the phase immediately before this one)
built exhaustive, real, non-mocked RULE 0 evidence for the two `pkg/`
primitives underneath these adapters (`pkg/serialexec` at 96.3%,
`pkg/serialtcp` at 95.0%, both against real fixtures: a socat PTY pair
and a real `net.Listen` server respectively) and treated that as
sufficient, because the adapter's own job — a type assertion on
`transport.Target.Endpoint` plus a field-for-field translation of the
result — looked too thin to need its own test. It was still real,
reachable, untested code: the type assertion's failure branch (a
binding-configuration bug reaching the wrong `Endpoint` kind) and the
error-wrapping branch (a real `pkg/` failure reaching the caller) had
never executed under `go test` at all, only been read and reasoned
about.

**Why it was not caught at the time.** Workstream D's own verification
sweep ran `go build ./...`, `go vet ./...`, `gofmt -l`, and a full `go
test ./...` pass, and all of it stayed green — a missing test file
produces no build error, no vet warning, and no failing test, only a
silent gap in a coverage percentage nobody explicitly checked for those
two specific packages in isolation. The full-repo test run's own summary
line reports `[no test files]` for a package with none, easy to
overlook among 140-plus other package lines that legitimately say `ok`.

**Fix.** Added `internal/transport/serial/serial_test.go` and
`internal/transport/serialtcp/serialtcp_test.go`, each proving three
things the pkg/ layer's own tests cannot: a real round trip through
`New(...).Exec(...)` (not the pkg/ function directly) against the same
real fixtures Workstream D already established (a local copy of the
socat-PTY helper for serial, a real `net.Listen` server for serialtcp),
the type-assertion error path (an `Endpoint` kind the Adapter does not
understand fails with a clear, named type error, not a panic), and the
error-wrapping path (a real underlying failure reaches the caller
wrapped, not silently absorbed). Both packages went from 0.0% to 100.0%
coverage. `internal/transport/telnet`, built alongside this fix, got the
identical three-test shape from the start rather than repeating the gap
a third time.

**Lesson.** "The layer underneath is exhaustively tested" is not
evidence that a thin wrapper above it is tested — a delegation function
still has its own branches (the type assertion, the error wrap), and
each one is reachable code that can be wrong independently of whatever
it delegates to. Before treating a new package as done, run `go test
-cover` on it *and* on every sibling package the same session's own
work sits beside, not just the new one being written — this gap would
have been caught a full workstream earlier by the same one-line check
that found it here.

## 174. A frame's announced size was allocated before it was checked against the output cap

**Symptom.** None yet observed in production — found during Phase 73
Workstream H's own Schema/Injection Hardening audit, reading
`pkg/dockerexec`'s `readDemux` deliberately rather than in response to a
failure. `TestReadDemux_NeverPanicsOnAdversarialInput`'s own existing
adversarial case, `{1, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF}` ("announces
~4GiB payload, delivers none"), was passing — but only because
`make([]byte, size)` for a ~4 GiB `size` happened to succeed against
this environment's available virtual address space rather than because
the package actually prevented the attempt.

**Root cause.** `readDemux` parses Docker's own exec-attach stream
framing: an 8-byte header (stream type, 3 reserved bytes, a 4-byte
big-endian payload length) followed by that many payload bytes, read in
a loop until a clean EOF. The pre-fix code read `size` off the wire and
immediately called `payload := make([]byte, size)`, THEN read the
payload, THEN appended it and only checked the accumulated total against
`maxOutput` afterward. `size` is remote-controlled (nominally by the
Docker daemon, which this package presumes non-hostile, but a corrupted
or truncated stream produces the identical bytes a hostile one would),
so a single frame naming a size near `math.MaxUint32` forced a
multi-gigabyte allocation attempt regardless of `maxOutput`, before the
cap this package exists to enforce ever ran.

**Why it was not caught building the feature.** The cap was written and
tested from the accumulation side — "does the running total exceed
`maxOutput` after this frame" — which is the natural way to reason about
a *stream* of many small frames arriving over time (the realistic case
this package's own real-daemon tests exercise). A single frame whose
*announced* size alone already exceeds the whole cap is a different
shape of the same threat, easy to miss when the check is written as "the
total so far" rather than "the total this frame could possibly add."

**Fix.** The `maxOutput` check now runs against `len(stdout) +
len(stderr) + size` BEFORE `make([]byte, size)` is called, so a frame
whose announced size alone would exceed the cap is refused without ever
allocating or attempting to read its payload. Added
`TestReadDemux_RefusesAnAnnouncedSizeExceedingTheCapBeforeAllocating`,
which proves this by supplying a reader that has the 8-byte header and
nothing else: the old code would have surfaced a truncated-payload read
error (proof it tried to read the payload), while the fixed code
surfaces the output-exceeded error immediately.

**Lesson.** This is the same shape `pkg/filters.GzipDecompress` already
taught this codebase once (an input cap with no matching output cap
allowing decompression bombs) applied to a different mechanism: a cap
checked only against what has already been accumulated, never against
what one single unit of remote-controlled input claims it is about to
add, leaves the allocation-before-validation gap wide open no matter how
tight the accumulated-total check is. Any loop that reads a
remote-controlled length field and then allocates based on it needs the
bound check before the allocation, not after.

## 175. A protocol with no length field to lie about still had an unbounded accumulator, because the missing terminator is the same risk in a different shape

**Symptom.** None yet observed in production — found during the same
Phase 73 Workstream H audit that found #174, by asking the identical
question ("is there a remote-controlled loop here that grows without
bound") of `pkg/rfc2217`'s COM-PORT-OPTION subnegotiation parser
(`iacFilter.feed`) once #174 had already shown the general shape was
worth checking for elsewhere in the same phase's new packages.

**Root cause.** RFC 2217 subnegotiation frames are `IAC SB <option>
<payload...> IAC SE` — delimited by a terminator, not a length prefix,
so at first read this looked like it could not have #174's exact defect
(there is no length field to allocate against). But `iacFilter.feed`
accumulated every non-IAC byte between `SB` and `SE` into `sbPayload`
with no upper bound at all: a subnegotiation that simply never sent its
own `IAC SE` — a compromised or malfunctioning access server, or a
machine-in-the-middle — would grow `sbPayload` for as long as the
caller's own `Options.ReadTimeout` window allowed a byte stream to keep
arriving, which on a fast local network is enough time to accumulate a
meaningful amount of memory before the deadline ends the call.

**Why it was not caught building the feature.** `FuzzIACFilter`'s own
doc comment already (incorrectly) described this framing as
"length-prefixed," apparently written by analogy to the plan's own
language for this boundary rather than checked against what the parser
actually does — RFC 2217 has no length field anywhere in this framing.
That mischaracterization meant the fix this doc comment implied
(validate a length field before trusting it) did not exist and could
not, since there is no such field; the REAL risk — an attacker
withholding the terminator instead of lying about a length — was a
different question nobody had asked yet, because the doc comment's own
wrong premise made it look already covered.

**Fix.** Added `maxSBPayload` (256 bytes — every real COM-PORT-OPTION
payload this protocol defines is at most 4 bytes, so this is headroom,
not a tight fit) and a bound check at both places `sbPayload` grows (the
plain-byte path and the escaped-`0xFF` path), returning a genuine error
the moment the cap is exceeded rather than continuing to accumulate.
Added `TestClient_UnterminatedSubnegotiationPayloadFailsClosedRatherThanGrowingWithoutBound`
and a new fuzz seed exercising a long, unterminated payload past the
cap. Corrected `FuzzIACFilter`'s own doc comment to state plainly that
this framing is terminator-delimited, not length-prefixed, so the next
reader is not misled into thinking a length-field check already covers
this class of risk here.

**Lesson.** "This protocol has no length field, so it cannot have the
length-field bug" is too narrow a defense: an unbounded accumulator
reachable by withholding a terminator is the identical resource-risk
shape as one reachable by lying about a length, just triggered by an
absence instead of a false value. When auditing a parser for one
package's version of a known bug shape, check every sibling parser added
in the same phase for the shape itself, not for the literal mechanism
(a length field) the first instance happened to use.

## 176. A named integer type built specifically to prevent one silent-misread class still allowed a different silent-truncation class

**Symptom.** `make gosec` reported three unwaived G115 (integer overflow
conversion) findings in `pkg/rfc2217/rfc2217.go`: `uint32(baud)` at two
call sites in `setBaudRate`, and `byte(dataBits)` in `setDataSize`. Found
running the phase's own release-gate tooling, not by manual review.

**Root cause.** `serialline.BaudRate` is `type BaudRate int` — a named
type whose own doc comment states its purpose is "a device property
misread as a port number cannot silently become a line rate," but
nothing about the named type itself bounds its value, and RFC 2217
sends a baud rate as a 4-byte wire value: `uint32(baud)` for a negative
`BaudRate` (or one exceeding `math.MaxUint32`, reachable on a 64-bit
build where `int` is 64 bits) wraps to an unrelated positive value
instead of failing. `Config.DataBits` is a plain `int` sent as one wire
byte; `byte(dataBits)` for any value outside 0-255 truncates silently
the same way. Both values are hydrated from `pkg/inventory.Properties`
(a device's own configured line settings), which rejects a value that
fails to *parse* as an integer but enforces no range — so a
misconfigured or malicious property value reaches these conversions
unbounded.

**Why it was not caught building the feature.** `Parity` and `StopBits`
(the same package, same file) both ship a `Valid()` method precisely
because their own wire encodings have a small, fixed set of legal
values — the pattern was already established for the two fields where
it was obvious. `BaudRate` and `DataBits` have no comparable fixed
vocabulary (the package's own doc comment says so explicitly for
`DataBits`: "no fixed vocabulary of named values fits it"), which made
it easy to reason that they therefore needed no bound at all, rather
than recognizing they still need a *range* bound even without a fixed
*set* of legal values.

**Fix.** `setBaudRate` refuses `baud <= 0 || baud > math.MaxUint32`
before constructing the wire value; `setDataSize` refuses `dataBits < 0
|| dataBits > 255` before the `byte()` conversion. Both return a clear
error naming the offending value rather than silently sending whatever
the conversion happened to produce. Added
`TestClient_SetLine_RefusesOutOfRangeBaudRate` and
`TestClient_SetLine_RefusesOutOfRangeDataBits`, each proving the
refusal happens before any subnegotiation is sent (a fake access server
that would hang forever if actually asked to negotiate).

**Lesson.** A named type with a doc comment about preventing one kind of
mistake is not the same as a validated type: `BaudRate`'s own doc
comment is about a Go-level type-confusion mistake (a port number in a
baud-rate field), not about the value's own numeric range once it
reaches a specific wire encoding. Every named integer type that gets
converted to a narrower wire representation (a byte, a uint32) needs
that conversion's own bound checked at the conversion site, regardless
of whether the type's own documentation already claims to prevent a
different, adjacent class of mistake — `make gosec`'s G115 rule is what
actually catches the gap between "has a named type" and "is validated
for this specific narrowing," and running it before considering hardening
work finished is what closed it here.

## 177. A "nothing is listening here" test address was built by releasing a port again, the exact recurrence entry #123 already named

**Symptom.** `go run ./tools/coverage-check` (a full `go test ./... -race`
sweep) failed once, non-deterministically, on
`TestDialThroughHops_UnreachableTargetThroughBastionFailsWithChannelError`
(`pkg/remoteexec/tunnel_test.go`, Phase 73 Workstream G): "expected a
channel-open failure against an address nothing is listening on," with
`DialThroughHops` returning no error at all. The same package's own
targeted, repeated reruns (`-count=20`) after the fix all passed,
confirming the failure was a real race, not a one-off environment fluke
unrelated to the test's own construction.

**Root cause.** This is `FAILURE_PATTERNS.md` #123's exact bug shape,
reintroduced in a new test written after that entry already existed: the
test built its unreachable address by opening a `net.Listen("tcp",
"127.0.0.1:0")`, reading back the assigned port, and closing the
listener immediately, assuming a released port refuses connections. On
this project's own WSL2 development host, a just-released loopback port
keeps accepting connects for a period afterward (the Linux and Windows
sides of loopback are bridged, and release does not propagate
immediately), so the dial the fake bastion's own `direct-tcpip` handler
made sometimes succeeded instead of failing, and `DialThroughHops`
returned a live (if useless) connection with no error.

**Why it was not caught writing the test.** #123's own fix and lesson
were already committed to this exact file when this new test was
written, but the lesson was not consulted at the point a new "build an
address nothing is listening on" need arose — the earlier entry's own
final sentence names exactly this failure mode ("a race even on hosts
where it works, since another process can claim a released port between
the close and the dial") and was not applied by analogy to a new,
unrelated package reaching for the identical construction independently.

**Fix.** Replaced the open-then-close listener with the literal constant
`"127.0.0.1:0"`, #123's own established fix: port 0 is the sockets API's
"assign me any free port" value for `bind`, so nothing can ever be
listening on it and a connect to it fails for a reason no host-specific
timing can undo. Verified with 20 repeated runs (`-count=20`), all
passing, where the prior construction had already been observed to fail
once in the wild.

**Lesson.** A documented failure pattern in this project's own
`FAILURE_PATTERNS.md` is not self-enforcing just by existing: a new test
in an unrelated package can independently re-derive the same plausible-
looking, subtly-wrong construction unless the pattern is actively
checked against before writing a "this address must refuse connections"
fixture, not just recorded for whoever happens to hit the failure and go
looking. Grepping this file for "listening" or "released" before writing
a new such fixture is cheap; discovering the recurrence via a
nondeterministic CI-equivalent failure is not.

## 178. Stream shape is re-asserted by three composition roots from compile-time constants, so the least-qualified process silently wins

**Symptom (latent -- found by inspection while designing Phase 96, not by an
observed failure; there is no test that would catch it and no log line that
would report it).** Any operator-chosen JetStream retention setting -- the
dedup window, `MaxAge`, replicas -- is silently reverted the next time a
Runner process starts, with no error surfaced anywhere and no way to tell
from the outside that it happened.

**Root cause.** Stream configuration is not written once at provisioning
time. `internal/topology.EnsureStream` calls
`js.CreateOrUpdateStream(ctx, StreamConfig())` (`internal/topology/stream.go`),
and `StreamConfig()` returns **compile-time constants**
(`streamMaxAge`, `streamDuplicateWindow`, `Replicas: 1`). Three separate
composition roots reach that function on every process start, each through
`event.NewNatsBus`: `cmd/controller`, `cmd/runner` and `cmd/demo`. So the
stream's shape is whatever the most recently started binary was compiled to
believe, which is last-writer-wins over shared infrastructure with no owner.

The ordering makes it worse rather than better. The Runner is the binary most
likely to be an older build, deployed at the edge and upgraded last --
precisely the process whose opinion about fleet-wide retention should carry
the least weight, and precisely the one that will restart most often on an
unstable link.

This was invisible for as long as the constants were the only source of
truth, because every writer agreed. It only becomes a defect the moment
anything makes retention configurable, which is what Phase 96 does -- so it
is a pre-existing latent defect promoted to a load-bearing one, not a defect
that phase introduces.

**Fix.** Give the stream exactly one owner. The Controller keeps
`CreateOrUpdateStream`; Runners **attach** with `js.Stream(ctx, StreamName)`
and fail loudly when it is absent. That converts the failure mode from
"silently reshaped by whoever restarted last" into "stream missing, refuse to
start", which is the same fail-closed instinct
`internal/transport/ssh/known_hosts.go` already applies to a missing
`known_hosts` file. Guard it with an `internal/archtest` rule so a
non-owning root cannot regain the ability to reshape the stream. Filed as
checklist items in Phase 96.

**Lesson.** An idempotent-looking provisioning call
(`CreateOrUpdate*`, `Ensure*`, `migrate`, `seed`) reachable from more than one
composition root is not idempotent -- it is last-writer-wins, and the fact
that every writer currently agrees is a property of them sharing a compile,
not a property of the design. Before making any such value configurable, ask
which single process owns it and make every other process a reader.

## 179. Two fully-built shared primitives had zero production callers, and both had been "finished" for phases

**Symptom.** While looking for somewhere to put a longer deduplication
window, `grep -rn "NewIdempotentBus" --include="*.go" .` returned only the
symbol's own definition and three doc-comment mentions. No composition root
called it. The same question asked of `pkg/policy` found the answer written
into the package's own doc comment: "This is written down here, not built,
because no phase consuming it exists yet."

**Root cause.** Both are Section 25 "shared primitive" contracts, built ahead
of a consumer on the reasonable theory that a later phase would need them.
`event.NewIdempotentBus` ships with its `DedupStore` port and **both**
adapters (`dedup_inprocess.go`, `dedup_nats.go`), full doc comments and
tests. `pkg/policy` ships the whole hierarchical System -> Inventory -> Group
-> Device resolver with `Override`/`UnionSlices`/`IntersectSlices`, plus fuzz
and benchmark tests. Everything about both reads as complete, and a
maintainer searching for "do we have deduplication" or "do we have a policy
resolver" gets a confident yes.

The tests are what hid it. Each package's own tests exercise its API
thoroughly, so coverage is high and CI is green, and neither `go vet`,
`make coverage` nor `internal/archtest` has any notion of "exported, tested,
and reached by nothing that ships". A port with no callers is invisible to
every automated gate this repository runs.

**Fix.** No code change for either yet; both are now filed against the phases
that would consume them -- `pkg/policy` as Phase 102's explicit first
consumer ("do not invent a second resolver"), and `NewIdempotentBus` as a
named finding in Phase 96, including the further catch that the Runner pulls
dispatch from a raw `jetstream.Consumer` rather than through
`Bus.Subscribe`, so the decorator would not cover the dispatch path even once
wired.

**Lesson.** Gate 2 already says it -- "a port with no callers is not an
implemented pattern, it is a decoration" -- and it still happened twice,
because a build-ahead primitive passes every check a real one does. When
consuming a shared primitive, verify it has at least one existing production
caller before assuming the mechanism works; when building one ahead of its
consumer, say so in the package doc the way `pkg/policy` honourably did, so
the next reader is not misled by its completeness.

## 180. A sync plugin's two dependencies arrived through constructor options only its tests ever passed, so the CLI built it broken every time

**Symptom.** `pleiades inventory sync --plugin aws` failed 100% of the
time, from the day the plugin landed, with `sync plugin "aws": no region
configured, use WithRegion`. Three separate test suites were green the
whole time: the plugin's own package suite, a real-LocalStack
integration suite, and `internal/inventory/plugins`' shared conformance
suite, which drives every plugin through one identical set of
assertions.

**Root cause.** The plugin took its two dependencies as functional
options, `aws.WithRegion` and `aws.WithCredentialStore`. `gopls
references` on both returns test files and nothing else. The registry's
own `Descriptor.New` was `func() Plugin`, taking no arguments, so the
instance every real caller got had `region == ""` and `creds == nil`,
and `Connect` refused before touching the network.
`cmd/pleiades/inventory.go`'s `buildSyncPlugin` did wire one plugin, via
a `desc.Name == catalystcenter.Name` type switch whose own doc comment
had already predicted its successor: "when a third plugin needs it, this
becomes an optional interface the plugin asserts rather than a longer
switch." The third plugin arrived and the switch was not extended.

**Why the conformance suite did not catch it.** Because it constructed
the plugin the same way the plugin's own tests did:
`awsplugin.New(awsplugin.WithRegion(...), awsplugin.WithCredentialStore(...))`.
Every suite built its subject correctly and independently, so none of
them was exercising the arrangement the product ships. That is RULE 0's
thesis in a shape the rule's usual example (a mocked transport) does not
cover: nothing here was mocked, and the wiring was still fictional.

**Fix.** The dependency became a parameter of every constructor rather
than an option one caller might remember. `Constructor` is now
`func(Deps) Plugin`; `Deps` carries the credential store. A
per-deployment value an operator types is now declared data on the
descriptor (`Descriptor.Settings []SettingSpec`) supplied as
`--set key=value` through `Config.Settings`, so `aws` declares `region`
and `syncplugin.Open` refuses by name, with the setting's own
description, before anything dials. `Descriptor.RequiresCredentials`
makes "the composition root forgot to wire the store" a refusal in
shared code instead of a per-plugin string inside `Connect`.
`buildSyncPlugin` and its type switch are gone; `cmd/pleiades` and the
conformance suite both call `syncplugin.Open`.

**Guards.** Three, of decreasing generality.
`internal/archtest.TestCompositionRootsBuildPluginsThroughTheRegistry`
fails if any `cmd/` package imports an individual plugin package, which
forbids the type switch that made a per-plugin arrangement expressible
at all, and which would have failed on the tree that shipped this defect.
`TestEveryRegisteredPluginOpensFromTheSharedPath` opens every registered
plugin through `Open` with only what a user can supply, and again with
no store, so `RequiresCredentials` cannot become a field nobody reads.
The conformance suite now constructs through `Open`.
`tests/e2e/inventory_sync_cli_test.go` runs the real binary against a
real LocalStack and reads the `inventory.yaml` a user would open.

**Lesson.** See `LESSONS_LEARNED.md` #154.

## 181. Four capabilities, three transports and three fqcns shipped with no device type able to satisfy any of them, in the same commit that added the guard against exactly that

**Symptom.** `serial_exec`, `serialtcp_exec` and `telnet_exec` were
refused for every device the platform can build, twice over:
`validate.CapabilityRule` rejected the runbook because no device had
`SerialCapable`/`RawPassthroughCapable`/`TelnetCapable`, and
`engine.SerialTarget`'s type assertion would have failed anyway. The
transports behind them are real, tested against real containers, and
documented in `docs/10-running-in-production.md` as usable.

**Root cause.** No production device type implemented any of the four
serial-family capability interfaces. The only implementers in the module
were stubs in `internal/engine/action_ssh_test.go` wrapping
`inventorytest.Stub`, which deliberately matches a capability by name and
skips the structural assertion a real device type performs. So
`TestSerialTarget` proved `SerialTarget` reads the accessors it is
handed; it could not prove any device the platform can hydrate has them.

**Why the existing guard did not catch it.** Phase 73's Workstream A had
just fixed this exact class for `DockerCapable` and added
`internal/archtest.TestImplementedCollectionCapabilitiesAreSatisfiable`
against recurrence. That sweep walks `catalogdata.Collections` and skips
anything not `StatusImplemented`. A transport fqcn is not a Collection
method and appears nowhere in that table, so the entire transport
dispatch surface was outside its coverage, and the defect shipped in the
same commit as the guard.

**Fix.** A real `console_device` type
(`internal/inventory/devices/console`), scaffolded through the actual
`pleiades forge new-device` CLI and hand-completed, hydrating every
accessor from `pkg/inventory.Properties` the way `linux.Server.SSHPort`
already does. It declares each of the four per record rather than all
four unconditionally, because they are alternative ways to reach one
device rather than four facts about it: a switch on a terminal server is
not also on the local host's `/dev/ttyUSB0`, and declaring otherwise
would let `validate` pass a `serial_exec` task that then dials an empty
device name. A line setting that does not parse is refused at
construction rather than defaulted, since a wrong parity does not fail a
serial line, it silently corrupts every byte crossing it.

**Guards.** `TestDispatchableTransportCapabilitiesAreSatisfiable` and
`TestBoundTransportCapabilitiesAreSatisfiable` cover
`engine.ActionCapability` and the real `NewDefaultTransportBindings`
registry, both negative-controlled by a permanent synthetic case rather
than a one-off manual un-wiring. Running them for the first time
reproduced all three failures verbatim.
`TestRegisteredCapabilitiesAreReachable` is a third sweep covering the
class neither of the other two can see: a capability nothing requires
and nothing satisfies, which refuses nothing and misleads only a reader
of the published vocabulary. It found `FileTransferCapable`, which
`docs/03-migrating-from-ansible.md` was telling migrating users that
`archive.extract` requires, when that method requires
`POSIXFileSystemCapable`.

**Entry #179, merged from `main` alongside this one, is the same class
seen from the other end and states the gap these sweeps close.** It found
two fully-built shared primitives with zero production callers and said
outright what was missing: "neither `go vet`, `make coverage` nor
`internal/archtest` has any notion of 'exported, tested, and reached by
nothing that ships'. A port with no callers is invisible to every
automated gate this repository runs." That was true when written. The
sweeps here give three of those registries such a notion (Collection
capabilities, transport fqcns, sync plugins), and
`TestRegisteredCapabilitiesAreReachable` covers the specific shape #179
describes: a name that is complete, tested, and required by nothing.
Neither entry closes the general case, and #179's own subjects
(`event.NewIdempotentBus`, `pkg/policy`) are still unreached, so the
honest reading is that this class now has partial coverage rather than a
guard.

**Lesson.** See `LESSONS_LEARNED.md` #155.

## 182. The one JetStream KV bucket whose shape was declared outside internal/topology was the lock bucket, and two documents claimed otherwise

**Symptom.** No runtime symptom, which is the point of recording it.
`internal/topology`'s package doc calls it "the single owner of every
NATS JetStream subject, stream, consumer, and retention/replica setting,"
and `internal/archtest/layering_test.go`'s own comment repeats that it is
"the one place jetstream.StreamConfig/ConsumerConfig/KeyValueConfig
shapes are declared." Both sentences were false when written.

**Root cause.** `lock.NewNatsLockManager` built a
`jetstream.KeyValueConfig` literal inline for the `Pleiades_Locks`
bucket, which carries both the leader-election leases `cmd/controller`
holds and the per-device execution leases `cmd/runner` takes. Both
binaries call that constructor on startup, and
`CreateOrUpdateKeyValue` reaches `CreateOrUpdateStream`, which issues an
unconditional `UpdateStream` first, so the bucket's shape is whatever the
most recently started binary was compiled to believe.

**Correcting the original report.** The audit that surfaced this called
it "the same shape" as a prior multi-declaration stream-drift finding.
That characterization is wrong and would send a reader hunting for a
second config literal that does not exist: there was exactly one
literal, reached through one constructor, so the two roots could not
disagree within a build. The real exposure is narrower and version-skew
shaped: `docker-compose.yml` ships controller and runner as separate
images, so a rolling upgrade can run a build whose `lockBucketTTL`
changed against one where it did not, and a lowered bucket TTL lands as a
lowered stream `MaxAge`, expiring live lock entries and releasing a
device lease that two runners could then both take.

**This entry was itself half wrong, and #178 is what corrects it.** As
first written it said several roots reshaping one JetStream object at
startup was "this codebase's deliberate pattern," citing
`topology.EnsureStream` doing exactly that for the main stream from
three composition roots, and concluded that what made it safe was having
the shape written down once. Entry #178, found independently while
designing Phase 96 and merged from `main` after this was written, reaches
the opposite and correct judgement about the same code: last-writer-wins
over shared infrastructure with no owner is a latent defect, and the
Runner (the binary most likely to be an older build, deployed at the edge
and upgraded last) is precisely the process whose opinion should carry
the least weight. The reasoning here was not wrong about the mechanism,
only about whether to accept it, and it was wrong for the ordinary reason:
an existing pattern was read as an endorsement of itself. Both entries
describe the same unchanged code, since Phase 96 is planning at the time
of this merge.

**Fix.** `topology.LockBucketConfig()` plus `LockBucketName`,
`LockMarkerTTL` and `LockBucketTTL`, sitting beside the dedup bucket's
existing config; `internal/lock` calls it. That gives the shape one
declaration, which is a prerequisite for #178's fix rather than a
substitute for it: giving the bucket one *owner* (a writer that
provisions, and attachers that fail closed when it is absent) is the
other half, and it belongs with the identical change to the stream
rather than being done differently here first.

**Guard.** `internal/archtest.TestOnlyTopologyDeclaresJetStreamShapes`
parses every non-test Go file in the module and fails on a
`jetstream.StreamConfig`, `ConsumerConfig` or `KeyValueConfig` composite
literal outside `internal/topology`. Negative-controlled twice: against
synthetic source in the same file, and against a real probe file
temporarily added to `internal/lock`, which it reported by path.

**Lesson.** A doc comment asserting exclusive ownership of a pattern
("this is the one place X is declared") is a claim about the whole
repository that no reader can verify by reading the file making it, and
that no compiler checks. Either write the AST rule that enforces it in
the same commit, or write the weaker sentence that is actually true.

## 183. Entry #177's fix reached one of ten sites carrying the identical construction, and the sweep that went looking for the rest missed half of them too

**Symptom.** A full `go test ./...` sweep failed once on
`internal/transport/serialtcp.TestExec_UnreachableConsoleServerThroughBastionFailsWithChannelError`
("expected a channel-open failure against an address nothing is
listening on"), then passed on every isolated rerun. The package is not
in `flaky-packages.json`, so `make push-gate` would have blocked a push
on it, at random. A later `make coverage` run (a second full sweep) then
failed on `internal/transport/telnet`'s equivalent test, which the first
sweep had passed.

**Root cause.** Exactly entry #177, in files #177's own fix did not
touch. #177 was found in `pkg/remoteexec/tunnel_test.go` and fixed
there alone. The identical open-a-listener, read-its-port, close-it,
dial-the-number-again construction was live in nine more places, all
written in the same phase. A just-released loopback port keeps accepting
connects on this project's WSL2 host, so the dial sometimes succeeds and
the failure the test exists to observe never happens.

**The part worth recording is the second miss, not the first.** After
the `serialtcp` flake, a grep went looking for the rest and found four
sites, which were fixed. That grep matched on the *expression shape*
(`Addr().(*net.TCPAddr).Port` near a `Close`), and it missed five more:
`internal/transport/telnet` had two (one of which flaked on the very
next full run), `internal/transport/serialtcp` itself had a second one
in the same file that had just been edited, `pkg/remoteexec/hop_test.go`
spelled it `Addr().String()`, and `pkg/tftpxfer` used
`LocalAddr().(*net.UDPAddr).Port`. Searching for the *intent* instead
("nothing is listening", `deadListener`, "listening now") found all of
them in one pass. A grep written from the shape of the instance in front
of you finds instances that look like that one; a grep written from what
the code is trying to say finds the rest.

**Fix.** The literal port `0` at eight of the ten sites, #123's and
#177's established fix: port 0 is the sockets API's "assign me any free
port" value for `bind`, so nothing can ever be listening on it. Each
site cites the entries by number, so the next reader finds the reasoning
at the code rather than by searching. Three direct-dial assertions were
also strengthened from "an error occurred" to "the error names the
address," which is what their own doc comments already claimed.

Two sites did not take that fix, and both are recorded rather than
forced. `pkg/tftpxfer`'s test asserts a *timeout budget* bounds the
call: a closed UDP port answers with an ICMP port-unreachable that ends
it early, and an invalid address fails validation earlier still, so
either would make it pass without the budget bounding anything. It now
holds a real UDP socket open and silent, which is the case the budget
exists for and also stops any other process taking the port.
`internal/catalog/pleiades/builtin/wait`'s `portClosedPort` needs a
concrete port that is closed now and bindable later, because its tests
prove `wait.port` notices a port opening, and port 0 cannot express
that. It was first left alone under a comment calling it a considered
exception, and the very next full sweep failed on it ("a closed port was
reported as open by the bash prober", against a prober that was working
correctly), which is the third time in this session that leaving one of
these alone cost a run. It now closes the race by checking rather than
by construction: bind, release, and confirm the port actually refuses a
connection before handing it back, retrying a bounded number of times
and failing with a message naming this fixture if it cannot. Verified
with `-count=8 -race` across all seven affected packages and
`-count=6 -race` on the wait package.

**Lesson.** See `LESSONS_LEARNED.md` #157.

## 184. t.TempDir plus a Unix socket overruns macOS's sun_path, and bind reports "invalid argument" rather than anything about length

**Symptom.** CI's `macos-latest` leg failed nine tests in `pkg/dockerexec`
with `net.Listen: listen unix
/var/folders/df/djsxfhc17x95674wsm_g8s980000gn/T/TestExec_RoundTripsAgainstARealFakeDaemon3888082138/001/docker.sock:
bind: invalid argument`. Every one of them passed on Linux, locally and
in CI, every time.

**Root cause.** `sockaddr_un.sun_path` is 104 bytes on macOS and the BSDs
and 108 on Linux. `t.TempDir` builds its directory name out of the
calling test's own NAME, and macOS puts `TMPDIR` under a 49-character
`/var/folders/<2>/<28>/T/`. Prefix plus a descriptive Go test name plus
`t.TempDir`'s random suffix plus its `/001/` subdirectory plus the socket
filename runs past 104. Linux's limit is four bytes larger and its
`TMPDIR` is `/tmp`, so the identical code has roughly 44 bytes of
headroom there and the bug cannot appear. `bind` returns `EINVAL`, not
`ENAMETOOLONG`, so the error names nothing about length and reads like a
malformed address.

The construction was live at five sites in four packages
(`pkg/dockerexec`, `internal/catalog/container/docker`,
`internal/catalog/file` twice, `internal/tlscert`) — entry #181's lesson,
demonstrated again in the very next phase. One of the five,
`internal/tlscert`, wrapped the failure in `t.Skipf("this platform cannot
create a unix socket")`, so on macOS it would have gone on silently
skipping real evidence rather than failing: a second copy of #164's
skip-shaped hole, arrived at from a different direction.

**Fix.** A `shortTempDir` helper at each site (`os.MkdirTemp("", "px")`
with a `t.Cleanup` removal), which keeps the whole path near 70 bytes on
either platform because the name no longer carries the test's. The
failure was reproduced on Linux BEFORE fixing it, by pointing `TMPDIR` at
a 52-character path — Linux's 108-byte limit less macOS's 104 is exactly
the 4 bytes that make a 49-character macOS prefix equivalent to a
53-character Linux one — which produced the identical nine failures and
the identical error string, and then proving them green under the same
`TMPDIR` afterwards.

**Lesson.** A platform constant that differs by four bytes between two
OSes produces a bug only one CI leg can ever see, and whether it fires is
decided by something as arbitrary as how descriptive a test's name is.
Nothing that builds a Unix socket path may derive it from a test name.
And a cross-platform failure that "cannot be reproduced locally" usually
can be: find the limit the remote platform is hitting and simulate it,
rather than treating the remote leg as the only oracle and fixing blind.

## 185. syscall.Stat_t in a test file is a compile error on Windows, and the ok guard beside it reads exactly like it already handles that

**Symptom.** CI's `windows-latest` leg failed `go vet ./...` with
`undefined: syscall.Stat_t` in `internal/catalog/file` and
`internal/catalog/file/line`. Nothing was wrong on Linux or macOS.

**Root cause.** Six sites wrote `sys, ok := info.Sys().(*syscall.Stat_t)`
followed by `t.Skip("this platform does not report POSIX owner and group
ids")`. That skip makes the code look platform-aware, and it is a RUNTIME
guard for a COMPILE-TIME problem: `syscall.Stat_t` does not merely go
unpopulated on Windows, it does not exist there, so the entire test
package fails to type-check and no line of the guard ever runs. A comment
that names the right concern is not the same as handling it, and here the
wrong handling was actively reassuring.

**Fix.** `posixOwnerIDs(fs.FileInfo) (uid, gid int, ok bool)` in a
build-tagged pair per package — `ownership_posix_test.go` under
`//go:build !windows` doing the assertion, `ownership_windows_test.go`
returning `ok == false` — with every caller keeping its existing skip,
which now means what it says. Verified with `GOOS=windows go vet` and
`GOOS=darwin go vet` over the affected packages, not just the native
build.

**Lesson.** Only a build tag can express "this identifier does not exist
on that GOOS". A type assertion's `ok` result cannot, however plausible
the skip beside it reads. This is the same family as the platform-suffix
trap `.github/workflows/ci.yml`'s own comment describes (#51), approached
from the other side: there a file silently never compiled, here a file
silently never could. The advisory Windows leg exists to catch exactly
this, and it only works if somebody reads a leg that is allowed to be
red.

## 186. The bastion proof's console server was published to the host, and publishing it is precisely what let a different Docker network reach it

**Symptom.** CI's `ubuntu-latest` leg failed
`TestBastionProof/DirectDialToConsoleServerFails`: a direct TCP dial from
the test process to the console server's container address SUCCEEDED,
where the test asserted it must fail because that container is attached
only to the isolated management network. Green on the author's Docker
Desktop/WSL2 host every single time.

**Root cause.** Two independent defects, the first hiding the second.

(a) The assertion was environment-dependent rather than a property of the
topology. Under Docker Desktop the daemon runs inside a VM whose bridge
subnets the host cannot route to, so the dial failed and looked like
evidence. Under a native-Linux daemon — GitHub's `ubuntu-latest`, the
only leg that runs this package at all — the host routes to every bridge
network directly and the dial succeeds. Host-to-bridge routability is a
property of how the daemon is installed, so no assertion about it can
prove anything about the topology.

(b) Chasing (a) surfaced the real one. The console server had a published
host port the entire time (`0.0.0.0:35480 -> 7000/tcp`), while the file's
own doc comment claimed it had "NO port published to the host at all" and
its readiness strategy was described as deliberately avoiding one. Cause:
testcontainers-go publishes the IMAGE's own `EXPOSE` ports when the
`ContainerRequest` declares no `ExposedPorts` of its own (`lifecycle.go`,
"Expose ports automatically if the container request exposes zero
ports"). Asking for no ports is what produced a binding. Compounding it,
`wait.ForListeningPort(...).SkipExternalCheck()` does not avoid a mapping
either: `SkipExternalCheck` only suppresses the dial FROM the host, while
`HostPortStrategy.WaitUntilReady` still blocks on `target.MappedPort`
first — so that readiness gate can only ever be satisfied by a published
container, and it silently required the exact thing the test existed to
disprove.

The publish was not cosmetic. Measured directly, with two networks and
two otherwise-identical containers: the published one was reachable from
a container on the OTHER bridge network, the unpublished one was not. A
published port installs an ACCEPT rule in the daemon's `DOCKER` chain
that matches traffic arriving from ANY bridge, not just from the host, so
it punches straight through the inter-network isolation this entire file
exists to demonstrate.

**Fix.** `EXPOSE 7000` removed from `testdata/consoleserver/Dockerfile`,
with a comment recording why it must not come back. Readiness swapped to
`wait.ForExec` reading the listening socket out of `/proc/net/tcp` inside
the container, which needs no host mapping — the same check
`wait.ForListeningPort` performs internally, minus the mapped-port
precondition. The host-routing-dependent control was replaced by two that
hold wherever the daemon runs: the console server has no host port
binding at all, and it is unreachable from a host attached only to the
outer network BY ADDRESS. That last distinction matters — the
pre-existing one-hop control dialed the DNS alias `consoleserver`, which
only the management network's embedded DNS answers, so on its own it
proved the name does not resolve and not that there is no route.

**Lesson.** Three. A control assertion has to fail for the reason it
claims: "the dial failed" and "there is no route" are different
propositions, and the gap between them only becomes visible on a
differently-installed daemon. A negative claim in a doc comment ("no port
published") is not an assertion — this one was false for the entire life
of the file, and the test that supposedly proved it never looked at a
port mapping. And a readiness strategy is part of the topology, not
scaffolding around it: this one quietly demanded a host mapping, which
was exactly what the subject under test forbade.

## 187. One test asserted a fact was present where its twelve siblings asserted a value, so it alone had no environmental guard and was the only one to fail off Linux

**Symptom.** `make test-no-docker` failed on the CI matrix's `macos-latest`
leg, in `internal/catalog/facts`, on one test:

```
--- FAIL: TestGather_FilterMatchesAsAGlob
    gather_test.go:438: ansible_distribution fact is missing, want the glob to have matched it
    gather_test.go:438: ansible_distribution_version fact is missing, want the glob to have matched it
    gather_test.go:442: facts = map[], want only the two the glob matches
```

`build` and `vet` were clean on all three legs, and the whole package
passed on `ubuntu-latest`.

**Root cause.** Not what it looks like, and the first diagnosis offered for
it was wrong in a way worth recording, because it was plausible and would
have made the test strictly worse.

`facts.gather` is a remote SSH method. Its test suite deliberately points
it at a real in-process SSH server that hands every command to a real
`/bin/sh` on the machine running the test (`pkg/remoteexec/remoteexectest`),
which the file's own header states is RULE 0's doing: "a server returning
canned bytes would only prove the canned bytes were canned". So the
"device" being probed on the macOS leg is the macOS runner, and the method's
os-release probe is one `cat /etc/os-release`, a file macOS does not have.

The method therefore behaved **correctly**: it emitted no fact the device
could not answer, which is the contract its own manifest advertises
("Returned: when /etc/os-release names one"). Nothing was broken in the
product, and nothing was broken in the transport.

What was broken was one test's assertion shape. Twelve of the thirteen
tests in the file reach their expected values through `gatherReadFile`,
`gatherCommandOutput` or `gatherOSReleaseValue`, each of which `t.Skipf`s
when this machine cannot answer, so each carries an environmental guard as
a side effect of computing what it expects. `TestGather_FilterMatchesAsAGlob`
asserted only that two keys were **present**, never comparing a value, so it
touched none of those helpers and had no guard to inherit. It was the only
test in the file that could fail rather than skip on a host without
`/etc/os-release`, and it did.

The wrong diagnosis was that the gatherer was "likely executing against the
live runner's OS rather than a mocked dataset", with the recommended fix
being to inject a static map of fake facts. That inverts the design: the
live execution is the point, and a static map cannot express what this test
actually pins, which is that the glob skips the **commands sent** rather
than filtering the output on the way back. The second suggestion, asserting
a portable fact like `ansible_system` instead, does not apply either ,
`facts.gather` does not emit `ansible_system`, and the test is specifically
about one command answering two facts, which only the os-release pair does.

**Fix.** The glob test now resolves both expected values through
`gatherOSReleaseValue` before the round trip and compares against them. That
is one change with two effects: it inherits the same skip its siblings have,
and it is strictly stronger on Linux, where a glob selecting the right two
keys but filling them with the wrong values previously passed.

The reproduction did not need a Mac. Hiding `/etc/os-release` in a mount
namespace on Linux, with `/etc` otherwise intact, produced the CI failure
byte for byte:

```
go test -c -o /tmp/facts.test ./internal/catalog/facts
cp -a /etc /tmp/etc-noosrelease && rm -f /tmp/etc-noosrelease/os-release
unshare -rm sh -c 'mount --bind /tmp/etc-noosrelease /etc; /tmp/facts.test -test.v'
```

Before: 10 pass, 2 skip, 1 fail. After: 10 pass, 3 skip, 0 fail.

**Lesson.** The fix closed the failure and opened the real question, which
is the second half of this entry and is recorded as
`LESSONS_LEARNED.md` #158: once the test skips politely, macOS reports `ok`
for the package while three of fourteen tests never ran, including the
happy path. `TestGather_LinuxAnswersEverythingThisSuiteWouldOtherwiseSkip`
was added in the same commit so that the identical skip is a **failure** on
Linux, where these facts are the whole reason the evidence exists. It is
controlled by the same namespace recipe above, which turns it red on
demand.

## 188. Nine packages could not run their own tests twice in one process, and no gate in this repository could ever have noticed

**Symptom.** `go test -count=2` over every non-Docker package failed in
nine of them: twenty-four tests, two of which were not failures but hard
panics that killed the test binary and took every later test in the
package with them.

```
--- FAIL: TestCollectionActionExecutor_InvokesRegisteredMethod
    collection_action_test.go:73: registering enginetest.invoked: duplicate registration for "enginetest.invoked"
panic: collection: duplicate registration for "test.must_register_duplicate" [recovered, repanicked]
panic: syncplugin: duplicate registration for "stub_must_register" [recovered, repanicked]
```

Every one of them passed at `-count=1`, which is `go test`'s default and
therefore what every target in the `Makefile` had always measured.

**Root cause.** Two causes, not one, and separating them was the whole
diagnosis.

*Cause A, seven packages.* A test registers into a process-global registry
and never removes the entry, because `pkg/registry.Registry[T]` had no
unregister, reset or restore of any kind, and neither did any of its
consumers -- a repo-wide search for `func Reset|Unregister|Clear` returned
one unrelated hit. The registry outlives the test, so a second iteration
re-registers a name the first iteration left behind. `Register` returns
`duplicate registration for %q`; `MustRegister` panics.

`internal/launch` deserves its own note, because it had visibly tried to
solve this and the attempt looked right. `unknownkind_test.go` named its
kind after `t.Name()`, which de-duplicates the two tests in that file
against each other. It does not de-duplicate a test against itself:
`testing.matcher.fullName` appends a `#01` suffix for SUBTEST names only
(`$GOROOT/src/testing/match.go:89-91`), so a top-level test re-run by
`-count` computes the identical name and the identical key.

*Cause B, two packages, unrelated mechanism.* `internal/ui/resources`
opens its fixture database on a FIXED shared-cache in-memory DSN
(`file:uiaccessfixture?mode=memory&cache=shared&_fk=1`) and deliberately
never closes it, behind a `sync.Once` that seeds once per PROCESS. Four
tests then created records with hard-coded names, so iteration two
re-inserted a name iteration one had committed and hit a real unique
index. The handler answered 500 and the test reported only the status
code, so the actual cause (`access: a record with that name already
exists`) was invisible until the handler's own log line was surfaced.
`internal/ui/web` was a package-level spy (`actionCalls`) that one test
cleared on entry and never on exit, read by an earlier-declared test that
never cleared it at all.

**What made it invisible.** Nothing here is exotic; the reason it survived
is that no gate could see it. `go test` defaults to `-count=1`, so `make
test`, `make test-race`, `make test-no-docker` and every CI leg measured
exactly one iteration forever. Coverage cannot see it either, since the
lines do run. `go vet` has no opinion. Two of the nine were panics, which
means the class was not merely latent -- it was already destroying test
binaries, and only ever on a run nobody performed.

**Fix.** `pkg/registry.Registry[T].SnapshotForTest()` returns a restore
closure; the six packages owning a package-level table re-export it as
`SnapshotForTest()`, and each offending test gained one
`t.Cleanup(...)` line. No assertion changed. Cause B got per-invocation
names (`uniqueName`, which counts rather than using `t.Name()`, since
`t.Name()` repeats across iterations) and one precondition reset.

Two guards, because neither covers the other's cause. `make test-repeat`
runs the non-Docker set repeatedly and is wired into `ci` and `push-gate`;
it is the only thing that can catch Cause B, since a 500 from leaked
fixture state is invisible to any AST rule. And
`internal/archtest/testseam_test.go` forbids production code from calling
anything named `*ForTest`, since the seam had to be exported surface rather
than an `export_test.go` (a `_test.go` file cannot be imported across
package boundaries, and four of the seven packages isolate a table
`pkg/collection` owns).

The gate is set at `-count=3`, and the third iteration is the part worth
recording. `-count=2` catches everything above, and it is VACUOUS against a
third cause found by going one step further: `pkg/remoteexec.Shared`
memoizes one Runner per Options for the life of the process, its circuit
breaker counts consecutive failures with no window and no decay, and one
`net.ssh.ping` spends three dial attempts against a threshold of five. So a
test whose whole subject is a dial failure passes at 1 and at 2 and fails
from 3, where the error stops naming the dial failure and says "circuit
open" instead. State that accumulates toward a threshold does not collide
on first repeat, which is the assumption `-count=2` encodes. `pkg/remoteexec`
got the same seam, with one difference forced by what it holds: it EMPTIES
the memo as well as capturing it, because the map holds Runner POINTERS and
handing the same map back returns the same Runner with its counter intact.

**Lesson.** Recorded as `LESSONS_LEARNED.md` #159. The sweep is also its
own evidence for #157: fixing only `internal/launch`, which is what the
prior session's handoff had scoped, would have fixed two of twenty-four
failures. Going one step past the new gate found a tenth package failing for a THIRD
cause, the circuit breaker described above, which is the same lesson
arriving a third time in one session and the reason the gate ships at 3
rather than at the 2 that would have been enough for everything already
found.

## 189. A lost race returned the same sentinel as a real collision, so the operator was told to rename a credential type that does not exist

**Symptom.** None visible, which is the point. Two controllers cold-starting
against one shared empty database both reconcile the managed credential
types this platform ships. One wins. The other logs:

```
WARN a managed credential type could not be installed because a custom type holds its namespace
     namespace=aws action="rename the custom credential type to free the namespace"
```

and `ReconcileManaged` returns a non-nil error, failing a startup that in
fact succeeded. There is no custom type. The row named is the correct
managed one the other controller had just written a millisecond earlier.

**Root cause.** `EnsureManagedType` reads the namespace with `Only()` and
creates on `ent.IsNotFound`. Between those two statements another process
can claim the namespace, and the `Create` then violates the unique index
`internal/ent/schema/credential_type.go` declares on it. That constraint
error went through `wrapConstraint`, which returns `ErrExists`, and
`ErrExists` is **the same sentinel** the method returns when a genuine
CUSTOM type already holds the namespace. `ReconcileManaged` branches on
that sentinel alone, so the two situations were indistinguishable at the
only place that had to tell them apart.

The window is small but the exposure is not theoretical: driving eight
concurrent reconciles against one shared WAL database produced at least one
bogus `ErrExists` in 84 of 100 runs.

Two things made it survive. The branch had **0% coverage**, and it is
unreachable by any sequential test as the method is written: the query is
on the exact namespace about to be written, so a single-threaded caller
reaching the create arm has already proven no row holds it. And the method's
own doc comment claims it is "idempotent by construction", which is true
right up until two callers run at once, i.e. exactly the situation the
sentence exists to describe.

**Fix.** The create arm now distinguishes a constraint error from any other
write failure. On a constraint it re-reads the row that beat it and hands
it to `adoptExistingType`, the same helper the "row was already there"
branch uses, so the two paths agree by construction rather than by two
similar blocks staying in sync: a managed winner is adopted and reconciled
onto, and a custom winner still returns `ErrExists`, which is the one case
an operator does have to act on. Any non-constraint failure is still
reported, because re-reading after a disk error would report a success that
never happened.

The tests manufacture the collision two ways rather than one. An ent
mutation hook writes the rival row inside the store's own insert, which
produces a REAL constraint violation from the real database at a
deterministic moment, and pins the branch; a starting-gate test with eight
goroutines pins the property. Both were run against the unfixed code first,
where the deterministic one fails with the misleading `ErrExists` verbatim.
One trap worth recording: the hook's own write re-enters the hook, so
guarding it with `sync.Once` deadlocks, because `Do` is not reentrant.

**Lesson.** Two sentinels that mean different things to the caller must not
be the same value, and the compiler will never tell you. The tell here was
available years before the incident: a method documented as idempotent, a
branch at 0% coverage, and a caller switching on a sentinel to choose an
operator-facing instruction. Any one of those is a question worth asking;
all three together describe this bug exactly.

## 190. A capability added to a device type left the Grand Integration Test asserting a set that no longer matched, and main stayed red at test-integration across three merged pull requests

**Symptom.** `make ci` fails at `test-integration`, in the one test that
drives the real binaries against real PostgreSQL and real NATS:

```
--- FAIL: TestGrandIntegration (11.33s)
    integration_test.go:114: rtr1 payload capabilities = [SSHTransportCapable CiscoIOSCapable NetworkAddressableCapable],
        want the set map[CiscoIOSCapable:true SSHTransportCapable:true]
```

The device carries one capability MORE than the assertion allows.

**Root cause.** `cc71f55` gave `cisco.Router` an `IPAddress()` accessor,
which is what makes a device satisfy `NetworkAddressableCapable`, so that
`pleiades.builtin.wait.port` could dispatch against a real device at all.
That was correct and deliberate, and `internal/archtest`'s own sweep
records the reasoning: this capability was "fixed for real instead of
allowlisted", because `IPAddress()` is trivial, already-known data on
every network-reachable device type.

`tests/e2e`'s `wantCaps` compares the dispatch payload against an EXACT
set, by length and then by membership. Nobody updated it. The capability
was added on the Phase 73 branch and merged in PR #23; the expectation
was last touched many phases earlier.

**What made it survive.** The assertion is exact on both sides, which is
the right shape for this test and is also what made it break silently
from the other direction: a capability ADDED anywhere in the device tree
breaks a test that names none of the packages involved, and the failure
surfaces only in the slowest, most expensive, most easily-assumed-flaky
suite in the module. `tests/e2e` is the first entry in
`flaky-packages.json` and `FAILURE_PATTERNS.md` #61 names
`TestGrandIntegration` specifically, so a red run here reads as known
noise. It is not: #61's signature is a container port-mapping race or a
lock-contention hang, and this was a deterministic assertion that failed
identically every time.

Verified pre-existing rather than assumed. A `git worktree` at clean
`origin/main` (`bfdd9a2`) reproduced the identical message, byte for
byte, with none of the current branch's changes present.

**Fix.** The expectation gains `capability.NameNetworkAddressable`, with
a comment naming the phase that added it and pointing at the archtest
sweep that records why it is deliberate.

**Lesson.** An exact-set assertion in an end-to-end test is a
cross-repository invariant wearing local clothes: it constrains every
device type, in packages it never names, and it can be broken by a change
that is correct in itself. That is worth keeping rather than loosening,
because the alternative (a subset check) would have let a real capability
regression through silently. What has to change is where the failure is
noticed: this one sat behind a suite everybody already treats as flaky,
which is how a hard, repeatable failure hid for three merged pull
requests. When a package on `flaky-packages.json` fails, read the actual
assertion before reaching for the rerun.

## 191. Registering the four obvious NATS lifecycle handlers left the cold-start path completely silent, because the library routes the initial-connect retry through a different pair

**Symptom.** Phase 96a set `RetryOnFailedConnect(true)` so a Runner started before
its broker exists waits instead of exiting, and wired
`DisconnectErrHandler`/`ReconnectHandler`/`ClosedHandler`/`ErrorHandler` so a
connection could no longer die silently. A process started against an unreachable
broker then logged **nothing at all** for the whole ten-second wait, and nothing
when it eventually connected. The documentation shipped in the same change said
"connection events are logged."

**Root cause.** `nats.go` splits its callbacks by whether the connection has ever
connected, using an internal `initc` flag, and the split is not symmetric with the
names. In `doReconnect`, `DisconnectedErrCB` is called only `if !nc.initc`; during
the initial-connect retry window the library instead consults `ReconnectErrCB`. On
eventual success it calls `ReconnectedCB` only `&& !nc.initc`, and `ConnectedCB`
`&& nc.initc`. So the two handlers a reader naturally reaches for
(disconnect, reconnect) are precisely the two that are suppressed during the exact
window `RetryOnFailedConnect` exists to create, and the two that fire there
(`ConnectHandler`, `ReconnectErrHandler`) look redundant when skimming the option
list.

**Why it was not caught.** The test asserted `opts.DisconnectedErrCB != nil` and
its three siblings. Every one passed. No test captured a byte of output, so
"the handlers are registered" was proven and "the handlers say anything" was not.

**Fix.** Register five handlers rather than four, adding `nats.ConnectHandler` and
`nats.ReconnectErrHandler`, with the asymmetry written into the comment beside
them. Add a container-backed test that captures a real `slog` logger across a real
severance and asserts on the text.

**Lesson.** When a library gates callbacks on connection lifecycle state, read the
gating conditions rather than the callback names: a name that describes an event
does not promise it fires for every occurrence of that event. And an option that
creates a new lifecycle phase (here, "connecting for the first time, in the
background") should prompt the question of which observability covers that phase,
because it is usually a different set from the steady state.

## 192. Every graceful shutdown logged a WARN saying "reconnecting" and an ERROR saying "closed permanently" about a shutdown that was going exactly to plan

**Symptom.** After wiring the NATS lifecycle handlers, an ordinary SIGTERM produced,
per process, three `WARN nats connection lost, reconnecting ... error=<nil>` lines
and three `ERROR nats connection closed permanently ... last_error=<nil>` lines. A
Runner and a Controller hold three NATS connections each, so a routine rolling
update emitted six false failure lines per pod, at the two severity levels an
operator is most likely to alert on.

**Root cause.** `Conn.Close()` calls `nc.close(CLOSED, !nc.Opts.NoCallbacksAfterClientClose, nil)`,
and `NoCallbacksAfterClientClose` was not set, so `doCBs` was true. Inside `close()`,
`DisconnectedErrCB` is invoked whenever `nc.conn != nil` (which it still is on that
path, since the socket is closed by a deferred call and the field is never nilled),
and `ClosedCB` is invoked unconditionally. Both were therefore doing exactly what
they were written for, on an event that was not a failure. The `error=<nil>` in the
output is the tell: `close()` passes a nil error, which no genuine disconnect does.

**Why it was not caught.** Same reason as #191: the only assertions were that the
callback fields were non-nil. The evidence was actually sitting in the phase's own
long-severance test log, where the two lines appear at the same second as the
test's `t.Cleanup` closing the bus, and it was read as expected shutdown output
rather than as a defect.

**Fix.** Add `nats.NoCallbacksAfterClientClose()` to the shared option set, and
assert in a container-backed test that a graceful `Close()` emits neither
"reconnecting" nor "closed permanently".

**Lesson.** A handler wired to a transport's "connection ended" event will fire on
the deliberate ending too, so decide at wiring time which severity a planned
shutdown deserves, and prefer a library switch that suppresses the callback over a
flag the application has to remember to set before every close. A monitoring signal
that fires on every intentional restart trains the operator to ignore it, which
costs more than not having it.

## 193. Two constructors leaked their NATS connection on every error path after the dial succeeded

**Symptom.** Latent, found by inspection while changing the same functions.
`event.NewNatsBus` and `lock.NewNatsLockManager` each dialled NATS, then did two or
three more things that could fail (`jetstream.New`, `EnsureStream`,
`CreateOrUpdateKeyValue`), and returned the error from each without closing the
connection they had just opened. Every failure left a live connection, its reader
and flusher goroutines, and its reconnect machinery running, owned by nobody.

**Root cause.** The error paths were written before the constructors acquired
anything that needed releasing, and each new post-dial step was added by pattern
matching on the one above it, which had the same omission. Nothing in the shape of
the code marks the transition from "nothing acquired yet" to "must clean up".

**Why it was not caught.** These branches are only reachable against a broker that
answers TCP and then refuses a JetStream operation, which no test simulated. A
process that hits them then calls `log.Fatalf` anyway, so in production the leak was
immediately followed by an exit, which is why it never manifested. It would have
manifested the moment a caller retried construction instead of exiting, which is
precisely what the surrounding resilience work was moving toward.

**Fix.** `nc.Close()` on every post-dial error return, and then, better, collapse
dial-plus-wait into one `topology.Connect` that owns the cleanup once so no future
caller has to remember it.

**Lesson.** When a constructor acquires a resource and then does more fallible work,
the acquisition and its release belong in one function that returns either a fully
usable thing or nothing at all. Spreading the steps across the caller means every
new step is a new chance to forget, and "the process exits on this path anyway" is
a property of today's callers, not of the function.

## 194. A guard that matched a function by unqualified name could be defeated by declaring a local function of that name

**Symptom.** Latent, found by adversarial review of a guard added in the same
change. `internal/archtest`'s new rule required every `nats.Connect` call to spread
`topology.DialOptions(...)`, and its matcher accepted any call whose final argument
was a spread of a call to a selector or identifier **named** `DialOptions`, with no
check on what it was a selector *of*. Two lines defeated it completely:
`func DialOptions() []nats.Option { return nil }` beside the call site, then
`nats.Connect(url, DialOptions()...)`, reproducing the exact zero-option defect the
guard existed to prevent while passing it.

**Root cause.** The matcher was a near-mechanical port of an existing rule that
matches a METHOD name on a receiver whose type it cannot see, where ignoring the
qualifier is the deliberate and correct tradeoff. Ported to a package-qualified
function, the same leniency stopped being a tradeoff and became a hole, because a
package qualifier is a thing the AST can actually see.

**Why it was not caught.** The negative control proved the matcher found a bare
call and accepted a correct one. It contained no decoy, so it proved the rule
worked on honest input and said nothing about hostile input, which is the only kind
a guard exists for.

**Fix.** Stop matching on how the call is configured and match on who is allowed to
make it: forbid `nats.Connect` outside `internal/topology` entirely, with the
options and the mandatory post-connect wait paired inside one `topology.Connect`.
There is then no name to spell correctly and nothing to alias around.

**Lesson.** A guard phrased as "the call must be configured correctly" invites an
arms race against every way of spelling the configuration; a guard phrased as "only
this package may make the call" ends it, and is usually available once the thing
being guarded has a single owner. When porting a matcher, re-derive which parts of
its leniency were deliberate: the reason an existing rule ignores something is
frequently that it *could not see* it, not that it should not care. And a negative
control without a decoy tests the happy path of the rule itself.

## 195. A design's load-bearing precedent cited a source file that does not exist, and the real one argued the opposite way

**Symptom.** Phase 96b was specified as "the Controller owns stream shape;
Runners attach and fail loudly if the stream is absent", and both
`FAILURE_PATTERNS.md` #178 and the phase text justified the refusal with the
same sentence: it "is the same fail-closed instinct
`internal/transport/ssh/known_hosts.go` already applies to a missing
`known_hosts`". That file does not exist. `ls internal/transport/ssh/` lists
seven files and none of them is `known_hosts.go`.

**Root cause.** The claim was written once, from memory of a real behaviour in
a package that had since moved, and then copied forward into a second document
without being re-derived. `LESSONS_LEARNED.md` #152 already names this exact
mechanism: a `file:line` citation is unverifiable by any tool here and rots
silently, and copying one forward multiplies the rot into false corroboration
rather than inheriting a verified fact. Two documents agreeing looked like
confirmation and was actually one unverified sentence counted twice.

**Why it mattered more than a broken link.** The real site is
`pkg/remoteexec/knownhosts.go`, and it is not analogous in the way the argument
needed. It fails ONE CONNECTION rather than a process; its alternative is
accepting a man-in-the-middle, which is a security boundary rather than an
availability tradeoff; and it ships both a configured path and a documented
per-task bypass. A missing stream has none of those three properties. The
analogy was the only argument offered for refusing to start, and refusing to
start would have removed self-healing (today a destroyed stream is recreated by
whichever process arrives first, and a running Controller never re-asserts) and
introduced a start ordering that neither shipped deployment expresses.

**Fix.** The design was changed before any code was written: the Controller is
the only process that may CHANGE a shape, every process may create a missing
one, and a mismatch is a warning rather than a refusal. The precedent actually
followed is `internal/tlscert`'s lock-free convergence, which the chart already
documents as "none of them waits on another".

**Lesson.** A citation is a claim, and a claim that is load-bearing for a design
decision deserves the same verification as a measurement. Before quoting a file
as precedent, open it: confirm it exists, and confirm it does the thing the
argument needs rather than something that merely shares a name. When a
precedent is the ONLY argument for a decision, its absence is not a
documentation defect, it is the decision being unsupported.

## 196. The safety-critical half of a multi-writer provisioning defect went uncatalogued for a phase because only the retention half had been noticed

**Symptom.** `FAILURE_PATTERNS.md` #178 recorded that three composition roots
reshape the JetStream stream on every process start from compile-time
constants, so an operator's retention choice is reverted by whichever binary
restarts last. It did not record that the "Pleiades_Locks" KV bucket has the
identical shape from two roots through
`js.CreateOrUpdateKeyValue(ctx, topology.LockBucketConfig())`.

**Root cause.** #178 was found while chasing a retention question, so retention
is what it looked at. The bucket was provisioned by a different call in a
different package and never came up. The tree already contained the sentence
that should have made it obvious: `internal/archtest`'s own comment says a
lowered bucket TTL "expires live lock entries and lets two runners execute
against one device", which is a safety failure where the stream's is a policy
one.

**Why it was not caught.** Nothing measures "how many composition roots can
write this object". The stream and the bucket are provisioned through
completely different call paths (`event.NewNatsBus` and
`lock.NewNatsLockManager`), so neither grep nor review of the stream's fix would
surface the bucket.

**Fix.** Phase 96b applied the same read-before-write treatment to both, adding
`topology.BindLockBucket` beside `topology.BindStream`, with the Controller as
the only role permitted to reshape either and every other process able to create
a missing one but not change an existing one.

**Lesson.** When a defect is found in one instance of a pattern, enumerate every
instance of that pattern in the same pass and rank them by consequence rather
than by which one was noticed first. "Reachable from more than one composition
root" is a greppable property, and the entry that generalizes it should list the
siblings even if it does not fix them. Four more were found this way and
recorded against later phases, including database migrations running from every
Controller replica with no advisory lock.

## 197. A test's own output capture raced its cleanup, and the race report replaced the assertion message that would have explained the failure

**Symptom.** `TestControllerScheduler_FiresExactlyOnce_ReleaseGate` failed under
full parallel `-race` load with two things at once: a real assertion failure
("three controllers ran the overdue schedule 2 times, want exactly 1") and
`testing.go:1712: race detected during execution of test`. The race pointed at
`strings.(*Builder).String()` inside the test's own cleanup.

**Root cause.** The helper that starts each controller subprocess assigns a
`strings.Builder` to both `cmd.Stdout` and `cmd.Stderr`, then in `t.Cleanup`
calls `cmd.Process.Kill()` followed by **`cmd.Process.Wait()`** before reading
`out.String()`. Because Stdout is not an `*os.File`, `os/exec` starts goroutines
that copy the pipes into the Builder, and **only `cmd.Wait()` waits for those
goroutines**. `cmd.Process.Wait()` reaps the operating system process and
returns immediately, so the copiers were still writing into the Builder that the
next line read.

**Why it was invisible until now.** The cleanup only reads the Builder
`if t.Failed()`, and the copiers have normally drained by the time a passing
test tears down. The race therefore required the test to fail first, which meant
it appeared exclusively in runs that already had something else wrong, and it
then obscured that something: a reader sees "race detected" and stops reading,
when the line above it was the real finding. The underlying failure here was in
fact benign, a slow run letting a second backlogged hourly occurrence come due,
each occurrence still firing exactly once.

**Fix.** Use `cmd.Wait()` in the cleanup. It reaps the process and joins the
output copiers, so the Builder has exactly one accessor by the time it is read.
A repo-wide sweep found this was the only `Process.Wait()` in the module.

**Lesson.** `cmd.Process.Wait()` and `cmd.Wait()` are not interchangeable, and
the difference is invisible in the common case: the first is a syscall on a pid,
the second is that plus joining every goroutine `os/exec` created to service a
non-file Stdout, Stderr or Stdin. Any test that captures a subprocess's output
into an in-memory buffer must use `cmd.Wait()`. More generally, when a race
report and an assertion failure arrive together, read the assertion first: a
race inside test scaffolding is frequently a consequence of the failure path
running, not the cause of it.
