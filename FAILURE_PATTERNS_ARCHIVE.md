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
task with no device," without ever calling `Executor.resolver`. That is correct at Walk tier, where a
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
matches nothing" are different conditions, and only the second is a mistake. Walk-tier behavior is
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

**Status: FOUND, NOT FIXED.** Discovered 2026-08-13 while verifying whether a test-harness container flake could reach production. It cannot; this can, and it is a different and worse thing. Recorded here rather than fixed in place because the fix changes the dispatch plane's failure behavior and the session that found it was building credentials, and folding an unrelated behavior change into that commit is how a change nobody reviewed in its own right ships.

**Symptom (predicted, not yet observed in a real deployment).** A Runner loses its NATS connection for longer than the client's reconnect budget. The process stays up. Its container keeps reporting healthy. It fetches nothing, executes nothing, and reports nothing, indefinitely. Capacity disappears from the mesh with no signal anywhere that says so, and the only visible evidence is a backoff-loop error line repeating in a log nobody is watching for that shape.

**Root cause.** Three things compose into it, and each is individually reasonable.

`nats.Connect(url)` is called with default options at all four production sites (`internal/event/nats.go`, `internal/lock/nats.go`, `cmd/runner/main.go`, `cmd/controller/main.go`). The nats.go defaults are `MaxReconnects: 60` and `ReconnectWait: 2s`, so the client gives up permanently after roughly two minutes of unreachability and closes the connection. That is a sensible library default for a request/response client and the wrong one for a long-lived worker whose entire job is to be attached to the bus.

No `ClosedHandler`, `DisconnectErrHandler` or `ReconnectHandler` is registered anywhere in the module, so nothing observes the transition.

`internal/runner/agent_run.go`'s `fetchLoop` backs off and retries on a fetch error rather than returning, which is correct for a transient error and indistinguishable from correct for a permanent one. `Run` therefore never returns, the process never exits, and no supervisor ever restarts it.

The Controller does not have this problem, and the difference is instructive: `cmd/controller/main.go`'s `readinessChecks` probes `nc.IsConnected()`, so an orchestrator sees it unready and restarts it. The Runner has no readiness surface at all, so the identical failure is invisible on one side of the mesh and self-healing on the other.

**Fix (sized, not applied).** Either register a `nats.ClosedHandler` that cancels the Runner's root context, turning a permanently dead connection into a process exit and letting the supervisor do what it is for; or pass `nats.MaxReconnects(-1)` so the client never gives up. The two are not equivalent and the choice is a real one: exiting surfaces the failure to whatever schedules the Runner, while retrying forever keeps a Runner that will recover on its own but leaves it invisible in the meantime. Giving the Runner a readiness surface of its own is the third option and the most work. Whichever is chosen, `internal/event` and `internal/lock` build their own connections with their own defaults and need the same treatment, or the fix covers one of three connections.

**Lesson.** A retry loop that cannot distinguish a transient failure from a permanent one converts an outage into silence, and silence is worse than the outage: an operator can see a crashed worker and cannot see an idle one. When a component's whole purpose is to stay attached to something, the library default for "give up" is almost never the right one, and the give-up path needs an owner that escalates rather than a backoff that absorbs. The tell here was structural and available without any incident: two processes connect to the same bus with the same defaults, one has a health probe that reads the connection and one has no health surface at all, and nobody had asked what the second one does when the first one's probe would have fired.
