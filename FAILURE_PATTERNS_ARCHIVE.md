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

**Fixed (2026-09-24, Phase 35).** By then most of this had been closed another way: top-level keys by
#252's `RunbookKeys`, and task-level keys by the module-as-key rewrite (`task_syntax.go`), which refused
any second unknown key, though with a message about sugar syntax rather than about the key. What was still
dropped: every key under `metadata:` and `secret_mask:`, and a lone undotted key holding a map, which
became a task calling a method of that name (`vars: {...}` became `fqcn: vars`). Seven of the fourteen
shipped example runbooks carried four `metadata.mcp*` keys nothing read. `KnownFields(true)` could not be
applied as this entry proposed, because the engine decodes a rewritten `yaml.Node` and
`(*yaml.Node).Decode` has no strict mode. The fix is a walker over the parsed tree that reads the same
struct tags the decoder reads (`internal/engine/schema_keys.go`, `schema_keys_yaml.go`) and a token-level
JSON reader (`json_strict.go`). Ansible keywords get their own message (`ansible_keywords.go`). Proven by
`TestStrictKeys_RefusesUnknownKeyAtEveryLevel` and by `TestStrictKeys_AgreesWithKnownFields`, which checks
the walker against yaml.v3's own strict decoder on every map of a full runbook. Removing the walker call
fails both.

**Class.** the silently-dropped-fields class (the unnumbered note after C10)
**Portable.** yes: any decoder that drops unknown keys in a document that states what will run
**Detector.** a differential test against the format's own strict decoder, over every map in a valid document; see ~/vuln-corpus/README.md

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

**Measured worse, then fixed (2026-09-24, Phase 35).** The three rules (a third, `lifecycle_rule.go`, had
joined them) later moved onto one shared helper, `engine.TaskTarget` (`internal/engine/action.go`), which
the executor's `resolveDevices` also uses. It kept the `, ok` assertion but fell back to the runbook's
`hosts:` for a malformed target. So a task written `target: [web1, web2]` was no longer skipped: it was
validated and then run against the `hosts:` device or group, which it never named. `TestTaskTarget` pinned
that behavior. The builder now refuses a present `params.target` that is not a non-empty string, naming
the value's kind and never the value (`validateTarget`, `internal/engine/task_target.go`), so no built DAG
can carry one. Proven by `TestBuild_RefusesMalformedTarget` (list, map, number, boolean, null, empty
string, JSON float), and by a property both builder fuzz targets now assert on every input that builds
(`assertTargetsAreNonEmptyStrings`): 3,000,000 executions of `FuzzBuildFromYAML` and 1,000,025 of
`FuzzDAGBuilder`, each ending cleanly at its count bound with `-fuzzminimizetime 2s` (#318).

**Class.** C9 (fail-open: a malformed value rendered as the permissive default; CWE-636)
**Portable.** yes: any "use the caller's value, else the default" helper whose type check sends a malformed value down the default branch
**Detector.** fuzz the builder and assert that every accepted document carries only well-typed values in that field; see ~/vuln-corpus/README.md

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
`internal/ui/static/vendor/` - the ECharts and HTMX bundles the controller embeds and
redistributes, together with their licence and NOTICE files. `git add` prints nothing when it skips
an ignored path and exits zero, so nothing about the commit looked wrong.

The result was a tree that could not compile: `//go:embed app.css app.js chart.js vendor` fails at
build time with `pattern vendor: no matching files found` when the directory is absent. Local builds
stayed green throughout because the files were sitting on disk the whole time, ignored but present.

**Fix.** Anchored the pattern to the module root (`/vendor/`), which is the only place a Go vendor
directory ever exists, so the leading slash costs nothing and is what the line always meant. Then a
gate, because the failure shape - silent, invisible locally, fatal on a fresh clone - is one no
amount of local testing catches: `TestEmbeddedAssetsAreTrackedByGit` walks the embedded filesystem
and runs `git ls-files --error-unmatch` over every asset, so a file the binary embeds but the
repository does not contain fails the build. It caught a second file (`stream.js`) within the hour.

Proven rather than reasoned about: `git archive HEAD` into a clean directory, then
`go build ./internal/ui/static/`, which reproduced the failure exactly.

**Lesson.** A gitignore pattern is matched at every depth unless anchored, and `git add` reports
nothing when it skips what it ignores - so "I added the files and committed" is not evidence the
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

It was not exploitable as written - browsers refuse to combine `*` with credentialed requests - but
it was one `Access-Control-Allow-Credentials` line away from being so, on the endpoint that streams
live output from privileged automation.

**Fix.** Removed the header entirely rather than narrowing it, since same-origin serving means there
is no legitimate cross-origin reader left. An e2e assertion against the real controller keeps it
gone.

**Lesson.** A security header is only correct relative to the authentication model underneath it,
and that model can change without the header being touched. This one went from correct to wrong
without anybody editing the line - the edit happened two packages away, in the middleware that
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
`api.AuthMiddleware(evaluator)` - the Bearer-only wrapper - so the cookie source was never passed to
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
work - which is what RULE 0 is asking for when it says a test only counts if it runs the path the
platform actually runs.

---

## 97. An inventory is a grant surface, so unvalidated membership is a cross-tenant privilege escalation with every individual step passing its own check

**Symptom.** None yet - found by adversarial review before the mechanism it exploits was wired up. Reported by all three attack lenses independently.

**Root cause.** Inventories were introduced as shareable containers, with sharing implemented as a RoleBinding at the new `ScopeInventory` level. That makes an inventory a *grant surface*: the resolver treats every device reachable through a shared inventory as in scope for the team it was shared with.

`SetStore.Create` and `Update` accepted arbitrary group and device ids and wrote them straight through. Nothing checked that a member belonged to the same organization as the inventory holding it.

The escalation needs no step that is individually suspicious. A caller holding `inventory:write` in their own tenant creates an inventory in their own organization (permitted), lists another tenant's device ids as its members (unchecked), shares it with their own team (permitted - it is their inventory), and is then legitimately authorized against hosts nobody granted them. Every permission check along the way passes, because each one is asking a question the attacker can honestly answer yes to.

Groups made it worse: a group has no organization edge of its own, so a group containing one foreign device smuggles that device in even when the direct device list is clean.

**Fix.** Validate membership at the write, which is the only place it can be stopped - by the time the resolver sees the containment it is a fact, and resolving it is exactly the correct behaviour. `assertMembersInOrganization` refuses any device belonging to another organization, and any group containing one. The API maps the refusal to 403 rather than 400: the submission is well formed and the caller is authenticated, they are simply not entitled. The error reports a count, never the ids - naming which devices belong to somebody else would answer, on that very request, the question the attacker was asking.

Devices with no organization at all are admitted deliberately: a single-tenant deployment has never populated that edge, they belong to no tenant, and refusing them would make the feature unusable for exactly the deployments most likely to adopt it first.

**Lesson.** **Ask what a new container grants, not just what it holds.** A collection that is merely descriptive can accept any membership; one that is an input to an authorization decision cannot, because its membership *is* a permission grant written in a different vocabulary. The tell is that sharing was implemented through the RBAC system - the moment a container feeds the resolver, every write to it is a privilege operation and belongs behind the same scrutiny as a role assignment.

Also: this was found by adversarially reviewing a *design* before implementing it, by agents told to break it rather than approve it. The same three lenses rejected the surrounding proposal outright. A review that had been asked "is this good?" would have said yes.

---

## 98. `scopeRule` discards the Role the resolver returned, so every RoleBinding's role is decorative

**Symptom.** None observable: `auth.NewScopeRule` has never been in a running admission chain, so no deployment has executed this path.

**Root cause.** `ScopeResolver.Resolve` returns `(Role, Effect, error)` and performs the full Section 18.4 walk - system, organization, inventory, group, device - with explicit Deny beating a broader Allow. `scopeRule.Check` calls it as `_, effect, err := r.resolver.Resolve(...)` and returns only the effect.

So the resolved role is thrown away. A binding granting `viewer` at a target and a binding granting `admin` at the same target produce an identical answer, and the `role` column on every RoleBinding row is decorative. Composed with `tokenScopeRule`, a caller holding `inventory:write` in their token plus any viewer-level Allow binding is authorized to delete.

**Fix.** Not yet applied, and deliberately so. The correct fix needs a decision this codebase has not made: `AdmissionRequest` carries a `RequiredScope` but no required *role*, so satisfying the role axis needs either a scope-to-minimum-role table or a required role on the request. Choosing one while the rule is unwired, in the same change that introduced an unrelated container, would be inventing policy in the wrong place. Recorded here so the phase that wires `NewScopeRule` addresses it deliberately rather than discovering it.

**Partial fix (2026-08-11).** The silence is fixed; the policy question is still open and still belongs to whoever wires the rule. `scopeRule.Check` now carries a doc comment stating in full that the resolved Role is reported and not enforced, what a correct fix would require, and why choosing it here would settle a policy question in the one place nobody would look for it. The discard itself is unchanged, and that is the point: an enforcement rule invented ahead of its first real caller is the failure recorded at #96 and #100, so the honest move was to make the gap legible rather than to close it speculatively. A first attempt did change the behaviour - returning an error on a clean Deny so the role reached the audit line - and was reverted, because `hateoas.go` documents relying on the distinction between "the chain could not reach a verdict" (error) and "it reached one" (Deny with a nil error), and collapsing that to surface a role nothing enforces would have traded a real signal for a cosmetic one.

**Lesson.** A function returning three values where the caller uses one is worth a second look, especially when the discarded one is the entire subject of the table it came from. This survived review because the call site reads naturally - `_, effect, err :=` looks like idiomatic Go, and nothing about it says "the role column is now meaningless".

---

## 99. A RoleBinding with `scope_id = 0` is a system-wide Allow, and nothing rejects one

**Symptom.** None observed; the rule is unwired.

**Root cause.** `ScopeTarget`'s own doc comment argues that zero-value fields "never match a real RoleBinding: ent primary keys are auto-increment starting at 1". That reasoning is sound for the *target* side and does not hold for the *binding* side, because nothing validates what goes into `role_bindings.scope_id`.

`ScopeResolver.Resolve` unconditionally folds an organization layer at `&target.OrganizationID` and a device layer at `&target.DeviceID`. When a target does not name one - a collection request, a runbook, anything outside the hierarchy - those fields are 0. A stored binding with `scope_id = 0` therefore matches, and because the device layer folds last under `policy.ModeOverride`, it beats every other layer including an explicit Deny at a real scope.

One row with a zero in a column with no positive constraint is a system-wide grant that outranks everything.

**Fix.** Not yet applied; it belongs with the phase that wires the rule, alongside #98. The shape is clear: reject a non-positive `scope_id` at any non-system scope, at the repository boundary and in the ent schema, and skip rather than match such a row when resolving.

**Fix (2026-08-11).** The resolver half is done, and it turned out to be a precondition rather than a follow-up. Organization-scoped *visibility* resolution passes a target naming an organization and nothing else, so `DeviceID` is 0 on every such call: resolving visibility through `ScopeResolver` before fixing this would have let one bad row grant global visibility across every tenant. `Resolve` now folds a containment level only when the target actually names a row there, via `appendScopeLayer`, which skips any level whose id is not positive. A request about an organization says nothing about a device, so a device-scoped binding can no longer answer it. System-scoped bindings are unaffected: they carry a nil `ScopeID` by design and name no row, and a separate test asserts they still apply to every target.

The correction to the entry above is worth recording too, because the original overstated the danger in one direction and understated it in another. A `scope_id = 0` row does **not** beat an explicit Deny: `combineScopeDecision` returns early once the accumulator holds a Deny, so a terminal Deny is genuinely terminal. What it did do is act as a blanket Allow for every check whose target named nothing at that level, which is worse in practice, because that is the shape of every organization-level question the platform asks.

Proven by `TestScopeResolver_ZeroScopeIDNeverMatchesAnUnnamedLevel`, and the test was mutation-checked: with the guard disabled, a `scope_type=device, scope_id=0` binding grants **admin** on an organization question. The repository-boundary rejection of a non-positive `scope_id` still belongs with the management surface that can write one.

**Lesson.** "The zero value cannot occur in practice" is an argument about one side of a comparison. Both sides need it, and the side that comes from a database column needs a constraint rather than a comment - a schema that permits the value will eventually contain it, whether by a migration default, a bad import, or a test fixture that escaped.

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

Nothing caught it because every test asked a question the defect answered consistently. The conformance suite's affordance test compares what the UI renders against what the generator permits over *the same candidates* - both sides read `Ops.Candidates()`, so both sides omitted the action and agreed. A test that derives its expectation from the code under test cannot see a whole category go missing.

**Fix.** `Descriptor.Candidates()` unions the operations' affordances with the actions', and the web handler asks the descriptor rather than its `Ops`. `validateOps` gained the actions, because operations and actions now share one relation namespace: `Affordances` is keyed by relation, so two entries sharing one would make permitting either permit both, which on an action means offering an operation nobody granted.

The regression test asserts the rendered HTML contains the action's href, which is the only assertion that could have failed: it names the outcome a user experiences rather than a value the implementation computes.

**Lesson.** **When a feature is gated by a set, test that the set contains it, not that the gate works.** Every layer here was individually correct. The action carried a real endpoint, the filter applied the right rule, the generator evaluated what it was given. The defect lived in what was never put into the set, and absence is the one thing a consistency check between two derived values cannot detect.

The sharper tell: this was a *new optional part* added to an existing descriptor. Sections, charts and streams were all added the same way and all render, because each has a route that fails visibly when unwired. An action's only failure mode was silence, because a control that does not render looks exactly like a control the caller is not permitted to see - and "not permitted" is the answer this UI is designed to give quietly.

---

## 101. A required select whose only option source has no writer anywhere, so the create form it gates could never be submitted

**Symptom.** On a fresh `make ui-dev`, the Inventories create form renders an organization `<select>` with no options, and every submission is refused with "Choose the organization this inventory belongs to." There is no way to proceed from the UI. The whole Inventories feature is unreachable in the one environment built for reviewing it.

**Root cause.** Three individually reasonable decisions that nobody held together.

The schema makes `Inventory.organization` a required edge, correctly: an inventory belonging to no tenant would resolve against no organization scope, and whether that made it reachable by everyone or by nobody would depend on which way the resolver failed. The store refuses `OrganizationID == 0` for the same reason, and the view's `Bind` refuses `org < 1` to give the refusal a field to attach to.

And nothing in the repository creates an Organization. `Organization.Create` appears in exactly two test files. `tools/uidev/main.go` seeds six devices and no organization. There is no API endpoint, no UI view, no CLI command and no seeder.

So the required control is populated from `ListOrganizations`, which correctly returns an empty list, because the table is correctly empty, because nothing was ever built to fill it.

**Fix.** The management surface that can create an Organization, a Team, a User and a RoleBinding, plus a `uidev` seed that exercises it through real HTTP. Recorded before that landed, because the ordering is the lesson.

**Lesson.** **A required field is a dependency on a writer, and a `Kind: Select` says so out loud.** The validation was right, the schema was right and the store was right; what was missing was anything that could ever produce a valid value. This is the `init()`-that-nothing-imports failure (#52, #96) in a new medium: a complete, correct, well tested component with no path from the running system into it.

The generalizable check is cheap. For every required field whose values come from another table, ask what writes that table. If the answer is "a test", the feature does not work - and it will pass every test, because tests write their own fixtures.

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

**Lesson.** **A dependency that is loaded is not a dependency that is used, and "the plumbing is ready" is indistinguishable from "the plumbing is dead" without a caller.** This is the same shape as an `init()` nothing imports (#52), a credential source nothing wires (#96), and a record action whose relation never entered the candidate set (#100) - infrastructure ahead of any caller, correct in isolation, invisible in aggregate.

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

**Symptom.** A production Ascender job template stores `tripplite_python/tripplite_config.yml` in its Playbook field. This platform, immediately after a change whose stated purpose was fixing the playbook reference grammar, answered: `playbook "tripplite_python/tripplite_config.yml" is named by id, not by filename: write it as "tripplite_python/tripplite_config"` - advice naming a file that does not exist, in a form AWX never produces. No playbook belonging to any real customer could be named at all.

**Root cause.** Two packages stated the playbook reference contract independently and disagreed: the launch kind's `validatePlaybookPath` accepted project-relative `.yml` paths, and `internal/adapters/legacy`'s resolver accepted only flat `^[A-Za-z0-9_-]{1,64}$` ids. That much was correctly diagnosed (#112). The repair then unified them **onto the resolver's flat-id grammar**, because the resolver was the side that touched the filesystem and its grammar was the easier one to defend at a trust boundary.

That reasoning is the defect. Which of two disagreeing statements is *right* is decided by the system being compatible with, never by which is simpler to enforce or which side "owns" the boundary. The flat-id grammar existed because the resolver read one flat directory, and it read one flat directory because there is no Project entity to be relative to (`.SPECIFICATION/AWX_TEMPLATE_GAPS.md` §1) - so the repair propagated a *consequence of a missing feature* outward into the contract, and then wrote tests asserting it. Those tests are the worst part: `playbook_test.go` grew entries named `"filename"` and `"path"` listing `site.yml` and `playbooks/patch` as things that must be refused, which reads as deliberate design to the next person.

**Fix.** The grammar is project-relative paths, stated once in `internal/playbook.ValidateReference` and delegated to by the kind: non-empty, bounded, no NUL, not absolute, no backslashes, `path.Clean`-idempotent, no `..` climb, `.yml`/`.yaml` extension. `DirSource.Get` joins under the root and re-verifies confinement against the resolved absolute path; `DirSource.List` walks the tree returning relative paths, skipping dot-directories (`.git` above all) and the conventional role-layout directories a playbook never sits in. The e2e gate's fixture moved into a subdirectory so a resolver that cannot descend fails it.

**Lesson.** When two statements of one contract disagree, do not ask which is safer or which package owns it; ask which one an outside system already speaks, and make the other one that. Reconciling on the wrong side produces something strictly worse than the disagreement: the disagreement was at least visible as a bug that made nothing work, while the tidy wrong answer works perfectly for every value the codebase's own fixtures use and for none that a customer has. And when a contract looks unreasonably narrow, check whether it is encoding the absence of a feature rather than a real constraint - a flat namespace is what "we have no Project" looks like from inside the resolver.

## 114. A field made absent from the edit form was still demanded by the code that reads the submission, so every edit on three views failed

**Symptom.** Every inventory, team and template edit through the web UI answered 422 with an error naming a control the page had not rendered ("Choose the organization this inventory belongs to"). `make ci` was green.

**Root cause.** `view.Field.Immutable` (#111) removes a field from the edit form and refuses it as undeclared if a submission carries it. The three views' `Bind` functions still parsed those fields unconditionally, because `Bind` is one function serving both create and edit and had no way to know which it was serving. Adding `Immutable` to a field therefore silently converted a working edit path into one that could never succeed, and the writer's `Update` - which reads the immutable value from storage precisely so the submission need not carry it - never got the chance to run.

Nothing caught it because the conformance suite exercised create forms, validation refusals and CSRF, but never a *successful edit*: the one shape that traverses render → submit → narrow → validate → bind → update as a user does. Each half was individually correct and individually tested. The bug lived exactly in the seam, which is where the previous three findings in this file also lived.

**Fix.** `Values.Editing()` exposes the mode the narrower already tracked, and each affected `Bind` skips parsing an immutable field on an edit, leaving the zero value for `Update` to overwrite from storage. The general guard is `TestViewConformance_AnEditFormsOwnFieldsAreAnAcceptableSubmission`: for every registered writable view it fetches the edit form, parses the controls the server actually rendered along with their prefilled values (every selected option of a multi-select, not the first), resubmits exactly that, and requires acceptance. It is mechanical rather than per-view, so it covers views that do not exist yet. The faithful round-trip is also what makes it non-destructive: it writes a record's own values back over themselves, so the shared fixtures stay as seeded. An earlier draft collapsed multi-selects to one value and silently emptied a template's promptable fields, breaking an assertion three files away, which is the same class of bug the test exists to catch and a fair warning about writing round-trip tests carelessly.

**Lesson.** A change that narrows what a form renders is a change to what its handler may demand, and the two are usually in different files written months apart. Any framework flag that removes a control has to be paired, in the same change, with a check that the code reading that control tolerates its absence - and the check that actually holds is an end-to-end round trip of the surface, not a unit test of either half. "Submit exactly what was rendered and expect success" is a cheap, generic assertion that every form-driven UI should carry from its first view onward.

## 115. A checkbox's edit-form prefill used a different truthiness convention than the checkbox template itself checks for

**Symptom.** Found by code reading while building the Templates edit form's new per-field prompt checkboxes (B1), not by a live incident: `internal/ui/resources/templates/templates.go`'s `projector().Form` prefilled `allow_simultaneous` with `yesNo(tmpl.AllowSimultaneous)`, which renders `"yes"` or `"no"`. `internal/ui/render/field.templ`'s `KindBool` control renders the `checked` attribute only when the prefilled value is the literal string `"true"`. A template saved with `AllowSimultaneous: true` would therefore always render its edit-form checkbox unchecked, and an operator who opened it, changed nothing else, and saved would silently flip a true value to false.

**Root cause.** Two independent, un-reconciled statements of what a `KindBool` field's prefill string means. `yesNo` was written for `Cells` (list and detail-view display text, where "yes"/"no" is the right human-readable word) and reused for `Form` (prefill, which the checkbox template compares against a literal) without checking that the two consumers agreed on the convention. Nothing caught it because the seeded fixture's `AllowSimultaneous` happened to be `false` for the templates the conformance suite's `firstRecordID` reaches first, so the round-trip test (#114's own guard) never exercised the `true` case: the assertion is only as good as the fixture data it is run against.

**Fix.** `Form`'s prefill for `allow_simultaneous` uses `strconv.FormatBool` (or an equivalent literal `"true"`/absent-for-false) instead of `yesNo`, matching what the render template actually checks. `Cells` is untouched, since a list column showing "yes"/"no" is the correct, separate convention for that consumer.

**Lesson.** A helper written for one rendering context (a display cell) and reused for a different one (a form control's prefill value) carries an implicit assumption that the two contexts agree on what a value means - and `Cells` and `Form` do not have to, because one is read by a human and the other is compared against by a template's own conditional. When a field's Kind determines how a control is rendered (`view.FieldKind`'s whole reason for existing), the prefill value that control receives has to be produced in that Kind's own vocabulary, checked against what the render template for that Kind actually tests for, not against whatever a neighbouring consumer of the same domain field happens to expect. A fixture whose boolean fields are all `false` (or all the zero value generally) cannot exercise this class of bug at all; a seeded fixture used by a round-trip conformance test should include at least one record with every boolean field set true.

## 116. A resolver's output was correctly computed and never read by anything downstream of the function that computed it

**Symptom.** Found by code reading, tracing what happens to a template's execution fields (forks, limit, verbosity, extra variables) after B1 finally gave the Templates form real controls to set them. `launch.Template.Resolve` correctly folds a template's defaults, a saved configuration, survey answers and a launch's own overrides into `Resolved.Fields` and `Resolved.ExtraVars` - verified correct by this package's own tests. `internal/api/dispatcher.go`'s `LaunchTemplate` calls `Resolve`, receives `resolved`, and builds a `dispatch.Job` from it naming only `Definition`, `Kind`, `InventoryID` and `OrganizationID`. `resolved.Fields`, `resolved.ExtraVars` and `resolved.AllowSimultaneous` are read out of the return value and never referenced again anywhere in the codebase. Every field B1's new UI lets an author set was, until this session, inert: settable, validated, resolved correctly at launch time, and discarded before it reached a job record, the wire, or either execution adapter.

**Root cause.** The launch-configuration system (prompts matrix, per-field overrides, `Resolve`'s precedence folding) was built and thoroughly tested as a pure function of its own inputs and outputs, and every test asserting it asserted against its return value directly. Nothing tested, or could have caught by construction, whether a *caller* of that function used the whole return value. A resolver that computes three things and a caller that reads one of them both pass every test either half owns; the gap is only visible by reading the caller's own body field by field against the struct it received, which no automated check in this codebase does for a plain Go struct literal.

**Fix, partial this session.** `dispatch.Job` gained `Fields` and `ExtraVars` columns, and `LaunchTemplate` now stamps both from `resolved` (`.SPECIFICATION/AWX_PARITY_ROADMAP.md` Section 3b.1 has the full detail and what still has to be built: the wire and both adapters still do not read these values back off the job, so a launch's fields now reach the audit record but not yet a real execution). Tested end to end against a real ent store and a real dispatcher.

**Lesson.** A struct returned by a resolver is a checklist, not a report: before treating a resolve-and-persist path as complete, list every field the resolver's own type declares and grep for a second reference to each one downstream of the call site that received it. A field read exactly once - at the moment it comes out of the function that computed it - is a field on its way to being silently dropped, and no unit test of the resolver itself will ever show that, because the resolver was never wrong.

## 117. A job's completion state and tallies were fan-out publish outcomes, reported as though they were execution outcomes, while the real per-device outcome was already being reliably published to a subject nothing subscribed to

**Symptom.** Found by code reading, prompted by an adversarial review of what a job's `state` actually proves. A job whose every device failed its `ansible-playbook` run midway through still reaches `state = "completed"` with `failed_count = 0`: the Templates list's new Activity badge (B2, this session) would render it green.

**Root cause.** `internal/dispatch/worker.go`'s `Complete` call runs once the Controller's fan-out loop finishes, and its `dispatched`/`skipped`/`failed` counts answer "did the Controller succeed in publishing a dispatch message for this device," not "did the device's execution succeed" - a distinction `internal/ent/schema/job.go`'s own `state` field comment states correctly but which nothing surfacing the state to a reader (a badge, a status word) carries forward. Separately, and confirmed only by grepping every reference to `topology.ResultSubject`: `internal/runner/agent_wal.go` already durably WAL-buffers and reliably publishes a real per-device execution outcome (`internal/runner/wal.go`'s `ResultEntry`: device, outcome, reason) to that subject on every execution, and has done since PLAN.md Section 16's State Desync Mitigation was built. Nothing on the Controller side - not `internal/dispatch`, not `internal/api`, not `cmd/controller` - has ever subscribed to it. `ResultWAL`'s own doc comment says as much outright: the Controller-side half "has no consumer in this codebase yet."

**Fix.** Not built this session; sized and scoped in `.SPECIFICATION/AWX_PARITY_ROADMAP.md` Section 3b.2 as a design-then-build phase, because the state-machine question (what a job's state means once fan-out and per-device execution can each independently be incomplete, and what happens on a permanently lost result) needs an answer before the subscriber can be written, not after.

**Lesson.** A field's own schema comment can already state the honest scope of what it measures ("fan-out finished," not "execution succeeded") while every place that *renders* the field to a person quietly widens that scope back out, because a green badge reads as success to anyone who has not read the column's doc comment. When a system has two distinguishable notions of "done" (dispatched vs. executed, published vs. delivered, requested vs. confirmed), grep for every renderer of the status field whenever a second notion is introduced, not only the schema that defines it - and before building a reporting mechanism from scratch, grep for whether the data it needs is already being produced and simply has no reader, which is cheaper to find than to rebuild and was true here.

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
equally normal termination signal - the far end closing the
connection - that a local serial line has no equivalent of at all. A
real socat PTY pair was used to verify `pkg/serialexec`'s identical
timeout logic before writing any code (see `pkg/serialexec`'s own doc
comment), but `pkg/serialtcp`'s design was written from that verified
precedent by analogy rather than independently checked against a real
TCP connection first - the gap was found only once the real tests, which
happened to write a test double that closes promptly (a realistic
console-server behavior, not a contrived one), were actually run.

**Fix.** `readUntilQuiet` now treats `io.EOF` identically to a read
timeout: both mean "stop reading, return what was accumulated," not an
error. A raw byte pipe has no session semantics to say whether the far
end closing the connection was deliberate, and `transport.Result.ExitStatusUnknown`'s
whole premise is that this package cannot know more than "here is what
came back before it stopped" - an EOF is exactly as valid an answer to
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
was) showed both existing packages at `0.0% of statements` - not "low,"
zero. Neither had a test file at all.

**Root cause.** Workstream D (the phase immediately before this one)
built exhaustive, real, non-mocked RULE 0 evidence for the two `pkg/`
primitives underneath these adapters (`pkg/serialexec` at 96.3%,
`pkg/serialtcp` at 95.0%, both against real fixtures: a socat PTY pair
and a real `net.Listen` server respectively) and treated that as
sufficient, because the adapter's own job - a type assertion on
`transport.Target.Endpoint` plus a field-for-field translation of the
result - looked too thin to need its own test. It was still real,
reachable, untested code: the type assertion's failure branch (a
binding-configuration bug reaching the wrong `Endpoint` kind) and the
error-wrapping branch (a real `pkg/` failure reaching the caller) had
never executed under `go test` at all, only been read and reasoned
about.

**Why it was not caught at the time.** Workstream D's own verification
sweep ran `go build ./...`, `go vet ./...`, `gofmt -l`, and a full `go
test ./...` pass, and all of it stayed green - a missing test file
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
evidence that a thin wrapper above it is tested - a delegation function
still has its own branches (the type assertion, the error wrap), and
each one is reachable code that can be wrong independently of whatever
it delegates to. Before treating a new package as done, run `go test
-cover` on it *and* on every sibling package the same session's own
work sits beside, not just the new one being written - this gap would
have been caught a full workstream earlier by the same one-line check
that found it here.

## 174. A frame's announced size was allocated before it was checked against the output cap

**Symptom.** None yet observed in production - found during Phase 73
Workstream H's own Schema/Injection Hardening audit, reading
`pkg/dockerexec`'s `readDemux` deliberately rather than in response to a
failure. `TestReadDemux_NeverPanicsOnAdversarialInput`'s own existing
adversarial case, `{1, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF}` ("announces
~4GiB payload, delivers none"), was passing - but only because
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
tested from the accumulation side - "does the running total exceed
`maxOutput` after this frame" - which is the natural way to reason about
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

**Symptom.** None yet observed in production - found during the same
Phase 73 Workstream H audit that found #174, by asking the identical
question ("is there a remote-controlled loop here that grows without
bound") of `pkg/rfc2217`'s COM-PORT-OPTION subnegotiation parser
(`iacFilter.feed`) once #174 had already shown the general shape was
worth checking for elsewhere in the same phase's new packages.

**Root cause.** RFC 2217 subnegotiation frames are `IAC SB <option>
<payload...> IAC SE` - delimited by a terminator, not a length prefix,
so at first read this looked like it could not have #174's exact defect
(there is no length field to allocate against). But `iacFilter.feed`
accumulated every non-IAC byte between `SB` and `SE` into `sbPayload`
with no upper bound at all: a subnegotiation that simply never sent its
own `IAC SE` - a compromised or malfunctioning access server, or a
machine-in-the-middle - would grow `sbPayload` for as long as the
caller's own `Options.ReadTimeout` window allowed a byte stream to keep
arriving, which on a fast local network is enough time to accumulate a
meaningful amount of memory before the deadline ends the call.

**Why it was not caught building the feature.** `FuzzIACFilter`'s own
doc comment already (incorrectly) described this framing as
"length-prefixed," apparently written by analogy to the plan's own
language for this boundary rather than checked against what the parser
actually does - RFC 2217 has no length field anywhere in this framing.
That mischaracterization meant the fix this doc comment implied
(validate a length field before trusting it) did not exist and could
not, since there is no such field; the REAL risk - an attacker
withholding the terminator instead of lying about a length - was a
different question nobody had asked yet, because the doc comment's own
wrong premise made it look already covered.

**Fix.** Added `maxSBPayload` (256 bytes - every real COM-PORT-OPTION
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

**Root cause.** `serialline.BaudRate` is `type BaudRate int` - a named
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
fails to *parse* as an integer but enforces no range - so a
misconfigured or malicious property value reaches these conversions
unbounded.

**Why it was not caught building the feature.** `Parity` and `StopBits`
(the same package, same file) both ship a `Valid()` method precisely
because their own wire encodings have a small, fixed set of legal
values - the pattern was already established for the two fields where
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
different, adjacent class of mistake - `make gosec`'s G115 rule is what
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
address nothing is listening on" need arose - the earlier entry's own
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
`internal/catalog/file` twice, `internal/tlscert`) - entry #181's lesson,
demonstrated again in the very next phase. One of the five,
`internal/tlscert`, wrapped the failure in `t.Skipf("this platform cannot
create a unix socket")`, so on macOS it would have gone on silently
skipping real evidence rather than failing: a second copy of #164's
skip-shaped hole, arrived at from a different direction.

**Fix.** A `shortTempDir` helper at each site (`os.MkdirTemp("", "px")`
with a `t.Cleanup` removal), which keeps the whole path near 70 bytes on
either platform because the name no longer carries the test's. The
failure was reproduced on Linux BEFORE fixing it, by pointing `TMPDIR` at
a 52-character path - Linux's 108-byte limit less macOS's 104 is exactly
the 4 bytes that make a 49-character macOS prefix equivalent to a
53-character Linux one - which produced the identical nine failures and
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
build-tagged pair per package - `ownership_posix_test.go` under
`//go:build !windows` doing the assertion, `ownership_windows_test.go`
returning `ok == false` - with every caller keeping its existing skip,
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
evidence. Under a native-Linux daemon - GitHub's `ubuntu-latest`, the
only leg that runs this package at all - the host routes to every bridge
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
first - so that readiness gate can only ever be satisfied by a published
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
the container, which needs no host mapping - the same check
`wait.ForListeningPort` performs internally, minus the mapped-port
precondition. The host-routing-dependent control was replaced by two that
hold wherever the daemon runs: the console server has no host port
binding at all, and it is unreachable from a host attached only to the
outer network BY ADDRESS. That last distinction matters - the
pre-existing one-hop control dialed the DNS alias `consoleserver`, which
only the management network's embedded DNS answers, so on its own it
proved the name does not resolve and not that there is no route.

**Lesson.** Three. A control assertion has to fail for the reason it
claims: "the dial failed" and "there is no route" are different
propositions, and the gap between them only becomes visible on a
differently-installed daemon. A negative claim in a doc comment ("no port
published") is not an assertion - this one was false for the entire life
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

## 198. A publish reported failure and had succeeded, and the only thing that would have re-run the work was a reclaim ten minutes later, far outside the window meant to make retrying safe

**Symptom.** Measured against real NATS behind real Toxiproxy: a JetStream
publish while the link was severed did not fail fast. It blocked for the
caller's whole context, returned `context deadline exceeded`, and **the
message was persisted anyway**. The stream held three messages before the
outage and five after the heal, and a durable consumer drained all five.

**Root cause, at source level.** `nats.go`'s `Conn.publish` does not reject a
write while the connection is RECONNECTING: it appends to the pending
buffer and returns nil. The JetStream request layer then waits for an
acknowledgement on the caller's context, and on expiry deletes its own
response-map token and returns the context error. When the link heals,
`flushReconnectPendingItems` sends the buffered message. So the write
succeeded, the acknowledgement was abandoned, and the caller was told it
failed. There is also a ceiling nobody had written down: past the 8MB
reconnect buffer the publish genuinely fails and is genuinely not
persisted, and the two outcomes are indistinguishable to the caller.

**Why the obvious defence did not work.** Producer-side deduplication was
already correct and already wired: the fan-out stamps a retry-stable
`jobID:deviceID` identity that reaches `jetstream.WithMsgID`. But the
stream's duplicate window was two minutes, and the only thing that
re-issues an unconfirmed dispatch is the stale-job reclaim, which fires
after ten. The dedup memory expired eight minutes before the duplicate it
existed to catch. And the window could not simply be lengthened, because
the reclaim's own correctness depends on the window having closed by the
time it republishes.

**Fix.** Consumer-side suppression, in the Runner's raw pull loop, keyed on
the same `jobID:deviceID` identity the producer and the write-ahead log
already use, backed by the KV dedup store that `FAILURE_PATTERNS.md` #179
recorded as fully built with no production caller. It is marked only after
a successful execution, never on receipt, or a first attempt that failed
becomes indistinguishable from one that succeeded and the dead-letter path
stops being reachable. A store failure runs the work rather than skipping
it, so a storage blip cannot turn into silently skipped automation.

**Lesson.** Producer-side idempotency is bounded by a window, so it only
helps if the retry happens inside that window, and the thing to check is
not whether a key is stable but how long after the original the retry
actually occurs. Measure the gap. Here it was ten to twenty minutes
against a two minute window, and the mechanism had looked complete for
several phases because every piece of it was individually correct.

## 199. Deriving a resilience budget exposed that the deployment's own probe cancelled it, and the default chart would have failed its own new check

**Symptom.** Adding one operator-facing outage budget, with a validation
that the chart must not claim a budget its own probes cancel, made the
DEFAULT installation refuse to render: `mesh.maxOutageSeconds` defaulted to
1800 while `runner.heartbeat.livenessStaleAfterSeconds` defaulted to 60.

**Root cause.** The 60 second staleness limit was correct when it was
chosen. The NATS client gave up permanently after about two minutes and
logged nothing, so an unreachable broker really did mean a dead process and
restarting was the only recovery. A later phase made the client reconnect
for as long as the process lives, which removed the cause without anyone
revisiting the probe that existed for it. The two numbers were then in
direct contradiction, and nothing connected them, so nothing complained.

**Why the failing default was the useful outcome.** The validation was
written to catch an operator setting an incoherent pair. The first thing it
caught was the chart's own shipped defaults, which is the strongest
possible evidence that the contradiction was real rather than theoretical,
and it caught it at template time rather than in production.

**Fix.** The liveness staleness limit now defaults to the budget, and the
chart refuses any configuration where it is shorter. The cost is stated
rather than hidden: a runner wedged for a reason reconnection cannot fix
now takes up to the budget to be noticed instead of about a minute.

**Lesson.** When a change removes the reason a timeout exists, the timeout
does not become harmless, it becomes wrong in the other direction. And when
you add a rule connecting two previously unconnected values, run it against
the shipped defaults first: if the defaults fail, the rule has already paid
for itself, and the failing default is a finding rather than an obstacle to
the rule.

## 200. A bare host and port in NATS_URL was accepted and silently meant unencrypted, and nothing in the module parsed the value at all

**Symptom.** Latent, found while adding TLS support. `NATS_URL` reached
`nats.Connect` completely unvalidated: there was no `url.Parse` anywhere in
the composition roots or in the three packages that dial, and the Helm
chart typed `externalNats.url` as a bare string with no pattern. The first
thing that looked at the value was the driver.

**Root cause, and why it is a security defect rather than a usability one.**
The scheme in a NATS URL selects the TRANSPORT: `nats` is plaintext TCP,
`tls` is TCP with TLS, `ws` and `wss` tunnel over WebSocket. `nats.go`
treats a value with no scheme as plaintext, so `broker.example.com:4222`
connects, works, and is unencrypted. An operator who meant to encrypt and
mistyped `tsl://`, or copied `https://` from a browser, gets the same
outcome. The failure is silent in the worst direction: everything works.

There is a second shape. A comma separated list is legal for a cluster, and
a list mixing `tls://` and `nats://` was accepted. Which member a client
uses is not the caller's choice, so the plaintext member decides what an
observer sees.

**Why no existing check would have caught it.** `gosec` does not model this,
there is no type to constrain, and every value involved is a legal string.
The chart's only NATS validation was a non-emptiness check on
`externalNats.url`.

**Fix.** An allowlist of the four schemes `nats.go` implements, enforced in
`topology.Connect`, which is the single point every dial in the module
passes through. Enforcing it there rather than at each composition root is
what covers `cmd/demo`, which reads no environment and dials a hardcoded
default, so an env-level check would have skipped the one site nobody
watches. A bare host and port gets its own message naming the two
spellings the operator probably meant, because `url.Parse` reports that
input as scheme "localhost" and the generic message would have claimed
"localhost" was an unimplemented transport. Mixed encryption is refused
outright.

**Lesson.** When a configuration string selects a transport, a codec, or a
protocol, an allowlist is the only safe check, because the library's own
default for an unrecognised value is usually the insecure one and it never
errors. Validate at the single chokepoint every caller shares rather than
at each entry point: entry points multiply, and the one that gets missed is
the one that reads no configuration and therefore looked exempt.

## 201. A volume mount was added to a StatefulSet whose volumes block existed only in two branches the default configuration did not take

**Symptom.** Broker TLS was wired into the Helm chart: a ConfigMap for the
new configuration file, a Secret mount for the certificate, and matching
`volumeMounts`. Rendering the chart with TLS on and default persistence
produced the mounts and NO corresponding volumes, which Kubernetes rejects
at apply time.

**Root cause.** The StatefulSet's `volumes:` key appeared twice, in two
mutually exclusive branches: one for `persistence.enabled=false` (an
`emptyDir`) and one for an `existingClaim`. The default is
`persistence.enabled=true` with no `existingClaim`, which uses
`volumeClaimTemplates` and therefore needs no `volumes:` entry at all, so
NEITHER branch renders. Adding the new volumes to the branch that looked
like the main one put them on a path the default configuration never takes.

**Why it was nearly missed.** The first render tested had TLS off, so no
volumes were expected. The second had TLS on with a non-default
persistence setting, and passed. The combination that fails is TLS on with
DEFAULT persistence, which is the one an operator would actually use, and
it renders without error: YAML with a mount and no volume is structurally
valid and fails later, at apply.

**Fix.** One `volumes:` block whose contents are conditional, since YAML
cannot carry the key twice, with the shared entries in a named template so
the two data cases cannot drift. Then every combination of persistence and
TLS was rendered and checked for the mount and the volume together, rather
than for the absence of an error.

**Lesson.** Before adding a volume to a chart, enumerate every branch in
which the `volumes:` key does or does not exist, and note that a
StatefulSet has a third case, `volumeClaimTemplates`, in which it exists in
neither. And test a matrix rather than a sample: a mount without its volume
is valid YAML, so `helm template` succeeding proves nothing about it. Assert
the pair.

## 202. A phase spec's checkmarks, doc comments, and a specific bug story all described code that was never written

**Symptom.** `.SPECIFICATION/IMPLEMENTATION.md`'s Phase 86.5 ("The Interactive
Network CLI Transport, and `net.cli.*`/`net.ios.config`") presented as 9 of 12
items complete: `pkg/remoteexec/shell.go`, a new `pkg/netcli` package, `net.cli.command`/
`net.cli.config`/`net.ios.config` all flipped to `StatusImplemented`, four new test
files, and a specific, plausible-sounding bug story ("an early `netcli.Session.Config`
sent `configure terminal` and then read until the exec-mode prompt... caught immediately,
as a hang"). None of it existed. `grep -rn "RequestPty\|\.Shell()"` across the whole
module returned nothing; the three FQCNs were still `StatusDeclared`, still returning
`fmt.Errorf("...: not implemented")`; the four named test files did not exist. The
phase's own "Measured starting position" paragraph, written in the present tense to
describe the state BEFORE the phase's own work, was still a byte-accurate description
of the tree on the date this was found -- the checkmarks below it were the only thing
that had drifted from reality, because they had never touched it.

**Root cause.** The spec was authored end-to-end in the past tense, as a narrative of
work already done, complete with an invented war story lending it circumstantial
credibility, rather than as a plan for work to do. This is Phase 73's own audit finding
("five real defects sit inside phases already marked [x]") recurring in a more extreme
form: not a phase whose functional claim was overstated, but one whose claimed
artifacts never existed as code at all. The tell, findable without running anything: the
"Measured starting position" section and the `[x]` items below it described mutually
exclusive states of the same files, and nobody had re-run the measurement to notice.

**Why it was nearly missed.** The prose was detailed, internally consistent, and cited
real, correct architectural reasoning (the Adapter/Strategy pattern justification, the
real reason Phase 74's `pkg/netconf` had to duplicate SSH dialing and this phase did
not) that happened to be true regardless of whether the code existed. A reader
evaluating the REASONING would find nothing wrong with it. Only checking the reasoning
against the actual file tree (`grep -rl`, `go test ./pkg/netcli/...`, reading the
target files directly) surfaces that the reasoning was never implemented.

**Fix.** Built the real thing this time, verified against a real device rather than
assumed, and it surfaced two further, GENUINE bugs no fabricated story predicted: (1)
`Shell.WriteLine` sending `"\r\n"` made the device print every prompt twice (a phantom
empty Enter from the trailing `\n`), caught only by running against the real DevNet
Catalyst 8000 Always-On sandbox, never by the fake-server test suite, which passed
throughout because a fake server only ever behaves as its own author assumed; (2)
`net.ios.config`'s `backup: true` stat is a full, unsanitized running-config that came
back holding the device's own real `enable secret`/`enable password`/TACACS+ key,
printed unmasked in `--verbose` output the first time it actually ran. Both are recorded
in `.SPECIFICATION/IMPLEMENTATION.md` Phase 86.5's own corrected entries, alongside the
fabrication note itself.

**Lesson.** A `[x]` in this repository's spec is a claim about the file tree, checkable
in seconds (`grep`, `go test`, reading the named file), and it should be checked before
being trusted for planning purposes -- especially when a phase's own prose already
supplies the means to check it (a "Measured starting position" that would contradict the
checkmarks below it if anyone re-ran the same grep). And once real code replaces a
fabricated claim, running it against a real, unowned peer (not just a scripted fake one
this project also controls) is not optional diligence: it is what actually found both of
this phase's real bugs, and a scripted fake server -- built by the same author who chose
`"\r\n"` and left `backup` unmasked -- proved incapable of finding either, by construction.

---

## 203. A phase section was rewritten to correct a fabrication, and silently dropped two of the mandatory gates in the process

**Symptom.** Phase 86.5 was rewritten from scratch after #202 found every one of its checkmarks
fabricated. The replacement text was accurate about the code, verified item by item against a real
device, and presented as complete: ten items, ten `[x]`. The phase originally carried twelve. The two
missing ones were `Adversarial Pattern Justification` and `Schema/Injection Hardening`, and the
`Release Gate and Coverage Assurance` item had been retitled to a bare `Release Gate`. Nothing failed.
No test covers the shape of a phase section, so the loss was invisible to the entire build.

**Root cause.** The rewrite was driven by what the session had actually built, not by the template the
file's own preamble mandates. Every real item got an honest, verified entry; the two gates that ask a
question ABOUT the work rather than describing a deliverable had nothing in the session's own memory to
attach to, so they were never reinstated. This is the same class of error as #202, arrived at from the
opposite direction: #202 claimed work that did not exist, and this claimed completeness that did not
exist. A count of ticked boxes says nothing when the denominator is chosen by the same pass that ticks
them.

**Fix.** Both gates restored and answered for real. `Adversarial Pattern Justification` found something
worth the trouble: `internal/catalog/net/cli` and `internal/catalog/net/ios` each declare their own
`realOpenSession` and `sessionAndConn`, roughly forty-five near-identical lines. That is a second
implementation of one shape, which Gate 2 says fails outright unless defensible. It is defensible here,
because `TestCatalogPackagesImportOnlyPkg` forbids a Collection package importing anything outside
`pkg/`, so the two cannot share an `internal/` helper; the duplication is now recorded with that reason
in the phase and in the code comment that previously deferred to a reason stated nowhere.
`Schema/Injection Hardening` confirmed the `\r`/`\n` refusal and found `FromPrompt` already routes a
device's `cli_prompt` through `regexp.QuoteMeta`, and named the unmasked `backup` stat as the finding
the gate should have caught before a real run did.

**Lesson.** When rewriting a phase section, take the item list from a neighbouring phase, not from what
the current session remembers doing. The gates that survive a bad rewrite are the ones describing
deliverables, because a deliverable leaves evidence; the gates that ask whether a choice was right leave
none, so they are exactly the ones that vanish. Cheap detection, which is what found this: count
`` `[x]` `` items against a sibling phase's gate names. A phase missing `Adversarial Pattern
Justification` or `Schema/Injection Hardening` is missing them because someone rewrote it, since 106 and
103 phases respectively carry each.

---

## 204. A capability was given only its structural half, so it passed the architecture sweep and no real device could ever satisfy it

**Symptom.** `capability.NetconfCapable` had existed in the vocabulary since Phase 32, requiring a
`NetconfPort() int` accessor. Zero types in the module implemented it. Phase 74's device-type item
proposed the obvious fix, "give `cisco.Router` and `cisco.Switch` a real `NetconfPort() int` reading a
`netconf_port` property", and that fix on its own would have been silently insufficient: the accessor
would exist, `internal/archtest` would report the capability satisfiable, `make ci` would be green, and
`net.netconf.config` would still have been undispatchable against every real inventory item in
existence.

**Root cause.** A capability in this codebase has two halves, and only one of them is code.
`record.Base.HasCapability` is `Declares(name) && capability.Implements(c, name)`
(`internal/inventory/devices/cisco/router.go`): the STRUCTURAL half is the Go method set, and the DATA
half is the capability name appearing in the item's own declared set. `NetconfCapable` is registered
with `Parent: NameNetworkCLI`, making it a SIBLING of `CiscoIOSCapable` rather than an ancestor, so
`capability.Resolves` never resolved a Cisco router's declared `CiscoIOSCapable` up to it. Nothing else
could supply the name either: `pleiades add-host` has no capability flag at all (only `--dir`,
`--type`, `--classify`, `--tags` and `--set`), and a `--type`-created `record.Record` carries
`Capabilities: nil`.

The reason the architecture sweep could not see this is the interesting part.
`satisfiableCapabilities` in `internal/archtest/registry_sweep_test.go` hydrates every probe Record with
EVERY registered capability name, deliberately, so that the sweep tests the structural half in
isolation. That is the right design for what it tests, and it means the sweep is structurally incapable
of noticing that no real path exists to put a name into a real item's declared set. The sweep and the
gap are blind to each other by construction.

**Fix.** `netconfBaseline` in `internal/inventory/devices/cisco/router.go` appends
`capability.NameNetconf` to the vendor baseline when the record's own `netconf_enabled` property is
true, giving the capability its data half through a property that already existed. Keyed on the
property rather than granted unconditionally, because NETCONF is configuration on a Cisco device and
not a property of the model: the same sandbox device answers NETCONF on port 830 while refusing to
serve it on 22. Tests assert BOTH directions, since a device with NETCONF switched off claiming the
capability would make the property meaningless. This also retired a separate complaint the phase spec
had recorded, that `SupportsNETCONF() bool` "asserts a claim the type system cannot check": the
property it reads is now the classification data half of a capability whose structural half
`NetconfPort` proves.

**Lesson.** When adding an accessor to satisfy a capability, ask the second question explicitly: what
puts this capability's NAME into a real item's declared set? Answer it by naming the mechanism
(a vendor constructor's baseline, a classification rule, an operator-supplied property), not by
observing that `make arch` is green. Cheap detection, and the one that found this: construct the device
type the ordinary way, with no classification data, and call `HasCapability` on it. If that returns
false while the structural assertion passes, the capability has one half. An `archtest` sweep whose
probe hydrates every capability name cannot answer this question and must not be read as though it
had.

## 205. A colon in a KV key made the Runner's duplicate suppression client-side invalid, and the error was swallowed as a warning, so the feature has never once run

**Symptom.** None visible. The Runner logs two Warn lines per dispatch and
carries on. No startup failure, no test failure, no wire traffic.

**Root cause.** `internal/runner/agent_dedup.go:52`'s `dispatchDedupKey`
returns `payload.JobID + ":" + payload.DeviceID`. NATS KV keys are
validated client-side against
`validKeyRe = ^[-/_=\.a-zA-Z0-9]+$` (`nats.go@v1.52.0/jetstream/kv.go:502`),
which does not include the colon, and `keyValid` guards `get` and `put`
alike (`kv.go:908`, `:923`, `:1031`, `:1094`, `:1126`). Every `SeenRecently`
and every `MarkSeen` therefore fails with `ErrInvalidKey` before a single
byte reaches the broker.

The second half is what hid it. `agent_dedup.go:71-76` treats a store error
as "not seen" and logs a warning, and `:94-100` logs and moves on. Both
choices are individually correct and documented: refusing to run real work
because a KV read failed would convert an optimisation into an outage. The
two together mean a permanently broken store is indistinguishable from a
healthy one that has seen nothing.

**Fix.** Not applied here, deliberately, and recorded rather than done
quietly: this was found during Phase 101b recon and fixing it is a
behavioural change to duplicate suppression that needs its own test proving
a redelivered dispatch is actually suppressed. The key needs an encoding
that is legal in a KV key. The colon was chosen to match the idempotency
key `internal/dispatch` stamps and the one the write-ahead log derives, and
that agreement is the point of it, so the encoding has to preserve the
agreement rather than just pick a different separator here.

**Lesson.** A fallback that treats "the store is broken" as "the store says
no" removes the only signal that would have reported the breakage. When a
degraded path is deliberately silent, something else has to assert the
happy path actually works, or the feature can ship dead. Note what did NOT
catch this: the key was agreed across three mechanisms, which felt like
evidence, and none of the three ever checked that the agreed string was
legal in the one place it had to be a KV key.

## 206. A hazard closed for the dispatch subject was left open in the lock subject, whose own comment argued it could not happen

**Symptom.** Latent, and narrower than it first looks. A device id
containing a dot produces `$KV.Pleiades_Locks.router1.example.com`, a
five-token subject where three were intended. Dots are LEGAL in a NATS KV
key (`validKeyRe` includes `\.`), and nats.go builds the subject from the
key itself, so the lock works correctly today and will keep working under a
`$KV.Pleiades_Locks.>` grant. What it breaks is a single-token grant, which
is the per-device scoping this phase exists to make possible.

**Root cause.** `internal/lock/nats.go`'s `kvSubject` interpolates the raw
item id into `$KV.<bucket>.<key>`, and `itemIDValid` rejects only `".."`.
Phase 101a closed exactly this class for the dispatch subject with
`topology.SubjectToken` and recorded it as `LESSONS_LEARNED.md` #169.

The interesting part is the doc comment already sitting above
`itemIDValid`, which argues the hazard cannot occur because "a real caller
only ever passes an inventory device ID (a UUID) or the scheduler's own
fixed key here". That is the same reasoning that hid the same class in
`LogSubject` and `ResultSubject`, and it is wrong for the same reason: a
device id is operator-supplied text from a YAML inventory, and
`pkg/inventory` documents it as opaque, so "it is a UUID" is a description
of today's fixtures rather than a property of the type.

**Fix.** RESOLVED 2026-08-25, later the same day the first attempt was
reverted. The first attempt's failure was never a broker mystery, and the
paragraph recording it is preserved below because how it misdiagnosed
itself is the durable part.

The first attempt encoded once at `tryAcquireOnce`, carried the encoded
value on `natsLease` as a `key` field, left `ID()` returning the caller's
itemID (the conformance suite makes that part of the port contract), and
switched all six `kv.Create`/`kv.Get`/`kv.Delete` call sites. Its account
of itself said "all six call sites use the encoded value", and that
sentence was the bug: the key-bearing surface was TEN sites, not six.
`publishWithTTL` builds the raw "$KV.<bucket>.<key>" subject itself, it
has FOUR callers, and the attempt switched only the shared-join one. The
three lease-side callers (exclusive KeepAlive, shared KeepAlive, shared
non-last Release) still passed `l.itemID`, so every TTL-refreshing publish
went to a subject nothing was reading, carrying a CAS expectation taken
from the encoded key's revision history that the raw subject could never
satisfy. Every symptom follows: the refresh loops spun on an impossible
publish, the key's 5s TTL was never refreshed, keys expired mid-churn
("key not found"), other workers re-Created them with fresh holders
("lease is no longer current"), and the last-holder Delete saw the key
vanish between its Get and its Delete.

The recorded isolation evidence was itself the trap, twice over:

  - The identity-function diagnostic "proved the refactor correct" by
    making the wrongly passed `l.itemID` accidentally equal to the right
    argument. Identity does not exercise a split, it erases it. And the
    "-x" probe then indicted the encoding for the same reason reversed:
    ANY non-identity encoder exposes the three missed sites, so the
    failure tracked "key differs from itemID at all" perfectly while
    having nothing to do with the encoder. See LESSONS_LEARNED.md #170.
  - "Exclusive mode untouched" was an artifact of reading a `tail -4` of
    the test output. The conformance suite's KeepAliveOnValidLease runs
    in the zero-value mode, which is ModeExclusive, and under the attempt
    its KeepAlive fails loudly; the failure was simply off-screen. It
    survived into this archive because every lifecycle test of exclusive
    KeepAlive asserts a FAILURE path, so an error where success belonged
    had no test asking the opposite question.

Proven by reconstructing the attempt exactly from the session transcript
and reproducing the collapse against a real broker (the churn overflowed
its own 144-slot error channel and deadlocked to the 5m timeout), then
switching ONLY the three missed sites: the same churn passes in 27s.

The shipped fix makes the mistake unwritable rather than merely fixed:
`kvKey` returns a distinct `storedKey` type, `publishWithTTL` and
`kvSubject` accept only that type, and passing `l.itemID` where a key
belongs is now a compile error (verified by writing exactly that and
watching the build fail). `itemIDValid`'s ".." rejection is retired: the
encoder makes every itemID a single legal token, so the fuzz target now
asserts the total property (every itemID acquires and releases cleanly,
no allowance branches) and `TestNatsLockKeyIsASingleSubjectToken` pins
broker state for a dotted itemID, including that KeepAlive's refresh
publish advances the ENCODED key's revision, the exact observable the
three missed sites broke silently.

**Lesson.** #169 said a hazard closed in one function is not closed in its
siblings. This is that rule finding a sibling in a different package the
same week, which is the argument for treating the rule as a sweep to run
rather than a note to remember. And a comment asserting a hazard cannot
occur is a claim about callers, not about the function; when the input type
is documented as opaque, the comment is the thing to distrust.

## 207. Five defects shipped behind a green Release Gate, because the gate's broker was configured without the subsystem the code under test exists to serve

**Symptom.** Latent, and complete: under a real operator-mode broker with
JetStream on, NOTHING worked. The Controller died at startup provisioning
the stream, the Runner authenticated and then received no job ever, its
ten-second heartbeat probe was withheld so the whole fleet reported
unhealthy, and a five-times-failed `job.requested` vanished silently. None
of it was visible in any test, and Phase 101b's Release Gate passed in 12
seconds.

Five separate defects, in code committed the same session:

1. `internal/meshid/grant.go` granted
   `$JS.API.CONSUMER.MSG.NEXT.PLEIADES.runner-agent.>`, but nats.go's
   template is `apiRequestNextT = "CONSUMER.MSG.NEXT.%s.%s"`
   (`jetstream/api.go:61`), which ends AT the consumer name. `>` matches
   one or more trailing tokens and never zero, so the grant covered every
   subject except the one the driver sends.
2. The same, for `CONSUMER.INFO` (`api.go:58`), which the heartbeat probes.
3. `ControllerGrant` used `streamAPI(">")`, putting `>` in a NON-FINAL
   token (`$JS.API.STREAM.>.PLEIADES`), which is not a wildcard position at
   all.
4. `pleiades.dlq.pleiades.jobs.requested` was granted to nobody, and
   `internal/event/dlq.go` returns before `msg.Term()` when the publish
   fails, so the job is neither dead-lettered nor terminated.
5. `meshid.NewAccount` never set `claims.Limits`, and `jwt.NewAccountClaims`
   initialises `JetStreamLimits` to all zeros, whose own comment reads
   "JetStream is disabled by default by setting MemoryStorage and
   DiskStorage to zero". Every account the platform minted had JetStream
   off.

A sixth thing was not a defect but a missing fact with the same shape: in
operator mode, JetStream REFUSES TO START without a system account
("Can't start JetStream: setting up internal jetstream subscriptions
failed: system account not setup", then exit 1), and that system account
must NOT have JetStream enabled ("Not allowed to enable JetStream on the
system account"). An operator-mode deployment needs two accounts minted.
Phase 101b minted one.

**Root cause.** Two blind spots that lined up perfectly.

The GATE's broker ran with JetStream off. Its command was `-c
/etc/nats/nats.conf` and nothing else, the only NATS start in the
repository without `-js`, and it asserted core publishes only. Every one of
the five defects lives on the JetStream control plane. The gate was
otherwise exemplary, with acts, a control and a negative control, and it
could not have caught any of this, because the fixture was missing the
subsystem the code under test exists to serve.

The UNIT test asserted the grant against a hand-written list of expected
entries, by exact string membership. The list was written from the same
misunderstanding as the grant, so it carried the identical wrong suffixes.
It asserted that the grant equalled itself, and passed on all four subject
defects. There was no `ControllerGrant` test at all.

**Fix.** A gate that calls the REAL functions against a JetStream-enabled
operator-mode broker: `topology.ProvisionStream`, `topology.BindLockBucket`,
`topology.DispatchConsumerConfig`, a real `CreateOrUpdateConsumer`, a real
`FetchNoWait`, a real dispatch published by the Controller and pulled and
acked by the Runner. Falsified against two of the five defects
individually, each failing at the right act with the right message.

The unit test now asserts MATCHING rather than equality, using a NATS token
matcher whose own semantics are pinned by a table (`a.b.>` does not match
`a.b`; `a.>.c` does not match `a.b.c`), and its required list is the
subject the DRIVER sends, taken from nats.go's templates. Falsified by
restoring the old suffix: it fails naming both operations. `ControllerGrant`
gained the test it never had.

`meshid.NewSystemAccount` exists so the two account kinds are distinguished
at the call site rather than by a boolean, and
`TestAccountKindsDifferOnlyInJetStream` pins both directions without Docker.

**Lesson.** See `LESSONS_LEARNED.md` #171. Two rules, and the second is the
one that generalises furthest: configure a gate's fixture like production
or it proves only that the fixture works; and never assert a permission
list against a restatement of itself, because the restatement is written by
the same person, at the same moment, from the same misunderstanding.


---

## 208. A decodable-but-invalid message became a poison pill, because the consumer split "retry" from "give up" on the wrong axis

**Symptom.** Phase 40's Controller-side run journal consumer answered its two
failure kinds deliberately: a batch it could not decode was acknowledged and
logged, because it would never become decodable and retrying it forever would
block the consumer group; a store failure was returned, because a database
being briefly unavailable is exactly the condition redelivery exists for. Both
halves had tests and both passed.

The phase's own Schema and Injection Hardening audit then fed the consumer a
deliberately hostile payload, a deeply nested object about two thousand levels
deep. It decoded cleanly. `encoding/json` was perfectly happy to produce a
`Batch` holding one entry whose every field was its zero value, including an
empty `Outcome`. That entry then reached the store, which refused it, because
the outcome column is an enum with no empty member and the mapping is an
exhaustive switch that fails closed. The consumer saw a store failure, returned
it, and asked for the message to be sent again. Forever.

**Root cause.** The split was drawn between "could not decode" and "could not
store", and that is not the axis that matters. The axis that matters is whether
offering the same bytes again could ever produce a different answer. A
malformed payload and a batch carrying a value no column can hold are the same
kind of failure on that axis and opposite kinds on the axis that was used. An
unreachable database is the only one of the three that is genuinely transient.

The deeper trap is that the two halves were each tested against exactly the
input the author had in mind. The malformed-payload test used bytes that were
not JSON at all; the store-failure test used an entry with a deliberately
invalid outcome and asserted an error came back, which at the time read as
correct. Neither test asked what happens to a payload that is malformed AND
decodes, which is the region between them and the only place the bug lives.

**Fix.** The store now marks what it will never accept, with a sentinel error
(`journal.ErrUnstorable`) wrapped around the validation failure, and the
consumer classifies on that rather than on where the error came from. A batch
marked unstorable is acknowledged and logged at error, exactly like an
undecodable one. Everything else is returned and retried.

Both directions have a test, and the pair is the point: one proves a permanent
failure is acknowledged, the other proves a transient one (produced by closing
the client, an unreachable database rather than a malformed row) still comes
back as an error. A single test could have been satisfied by classifying
everything one way.

**Lesson.** When a consumer decides between retrying and giving up, the
question to write the split on is "could the same bytes ever succeed", not
"which layer said no". Layers are where errors come from; they are not what
errors mean. And when two failure paths are handled oppositely, test the
region BETWEEN the two inputs you had in mind, because a message that is
malformed enough to be wrong and well formed enough to decode is the one that
belongs to neither test.

## 209. A multi-device job silently lost every copy but one of a skipped task's journal row, because the row's identity assumed a field that only some rows carry

**Symptom.** A Walk-tier job fanned out to two devices, each dispatch running the
same three-node runbook with one task skipped on both, stored FIVE journal rows
instead of six. No error, no warning, and no log line above debug said so. Worse,
it was intermittent: four identical launches of the same template produced 5, 6,
6 and 5 rows. Nothing in the runbook, the inventory or the two devices differed
between the launches that lost a row and the ones that did not.

**Root cause.** A journal row is identified by `(job_id, device_id, attempt,
node_id)`, a unique index chosen so that a redelivered publish of one batch
collapses into the row it already wrote. That identity silently assumes every
entry names a device. Three kinds do not: a skipped task (the condition is
evaluated once per node, before any device is resolved), a controller-side task
with no target, and the synthetic parallel fan-out marker. All three reached the
store with an empty `device_id`, so each of a job's dispatches produced the
IDENTICAL key for such a node, and `EntStore.saveOne` discarded every copy after
the first through the branch that reads a constraint error as "already
recorded". Which is correct for a republished batch and wrong for a different
device's run of the same node.

The intermittency is the second half. Two dispatches collide only while they
share an attempt number, so a job where JetStream happened to redeliver one of
the two dispatches stored both rows and looked healthy. The completeness of the
audit record depended on whether a delivery had been retried.

Every automated gate missed it for one shared reason: all of them dispatch a
one-device job, and none of their runbooks contains a skipped task. The defect
needs at least two devices AND a node that resolves none, and no test had both.
It was found by launching a real two-device template through the real Controller
and Runner binaries and counting the rows.

**Fix.** `journalPublisher.Record` now fills in an entry's empty `DeviceID` from
the device the dispatch names, beside the `JobID` and `Attempt` it already
stamps there and for the same reason: a run cannot know which dispatch it is
serving, and on this tier a dispatch is scoped to exactly one device, so a node
that skipped in that run skipped for that device. It fills a gap rather than
overwriting, so a device the run genuinely resolved is still the run's own
answer. `TestJournalRoundTripKeepsEveryDispatchsCopyOfADevicelessNode` drives two
dispatches of one job through the real publisher, bus, subscriber and database
and asserts six rows with one skip row per device; it fails at five without the
fix.

**Lesson.** A unique index is a claim about identity, and a nullable column in
one makes that claim only about the rows that fill it. Before writing such an
index, ask which rows carry every component, and what two rows that share a
blank one actually mean. Here they meant two different devices, and the store
read them as one fact arriving twice. The reason it survived every test is worth
keeping separately: a defect that needs two independent conditions at once (more
than one device, and a node belonging to none) is invisible to a suite whose
fixtures each hold one of them.

## 210. One run recorded "no keys" two different ways, because a normalization was applied at one of the two sinks

**Symptom.** The same journal entry read `"stat_keys": []` in the Crawl tier's
JSON Lines file and the JSON scalar `null` in the Walk tier's `journal_entries`
column. Both mean "this task recorded no keys", and every failed task records
none, so the divergence covered exactly the rows an operator opens the journal
for.

**Root cause.** `internal/journal/record.go`'s `normalize` exists precisely to
stop this: `encoding/json` writes `null` for a nil slice and `[]` for an empty
one, the projection leaves a vector nil whenever it admitted no keys, and a
reader should not have to handle two spellings of one fact. It was called on the
file sink's encode path only. `EntStore.saveOne` passed the nil slice straight
into the column, even though the same function sitting in the same package had
already been written for the same problem, and even though that method already
handled the analogous zero-value case for `started_at` and `finished_at`.

On SQLite the difference hides: `json_array_length('null')` returns 0, the same
as for `'[]'`. The columns are `jsonb` on PostgreSQL, where the same call fails
outright with "cannot get array length of a scalar", measured against a real
server rather than assumed.

**Fix.** `saveOne` calls the same `normalize` before building the row, and
`normalize`'s own doc comment now records that both sinks apply it and what the
PostgreSQL half costs.
`TestEntStoreStoresAnEmptyKeyVectorAsAnEmptyArray` reads the three columns back
raw and fails on `null`.

**Lesson.** When a normalization exists because two readers must see one
spelling, it belongs at every write, not at the one where the problem was first
noticed. And a difference that a development backend forgives is not a
difference that does not exist: SQLite and PostgreSQL disagree about JSON null,
so "it queries fine locally" says nothing about the deployment.

## 211. A stress test raced a fixed sleep, so the gate failed describing an empty buffer instead of an unbounded one

**Symptom.** `TestSubsystem_StderrIsBoundedAndMarkedTruncated` failed inside
`tools/coverage-check`'s own full suite pass with
`Stderr() = 0 bytes ending "", want it marked truncated`, in 0.00s, while the same
package had already passed three times earlier in the same `make ci` run. It arrived
immediately after a `golang.org/x/crypto` bump, which made the bump the obvious
suspect and the wrong one.

**Root cause.** The test's fake server writes a 16 KiB flood to the channel's standard
error and then sleeps 50 milliseconds. The client opens the subsystem, closes it at
once, and reads `Stderr()`. Closing tears the channel down, so the whole assertion
rests on the drain goroutine having been scheduled inside that 50 millisecond window.
It is a fixed delay racing a scheduler, and under a full parallel suite the scheduler
wins.

The implementation was not at fault and is worth noting for the next reader: `Close`
already waits on the drain (`drainCloseGrace`), precisely so a post-`Close` `Stderr` is
complete rather than racy. What it cannot do is invent bytes that never crossed the
wire before the close.

**Fix.** The test now waits for the flood to reach the client, polling the client's own
buffer until it holds `maxSubsystemStderrBytes` or five seconds pass, and only then
closes and asserts. The server's `Write` returning was never the right signal: it says
the bytes left the server, not that this side saw them.

Ruled out rather than assumed, because the timing made it look like a regression: the
identical failure reproduces at `-count=200` on a worktree pinned to the OLD x/crypto
v0.54.0, so the dependency bump did not cause it. After the fix, 500 repetitions pass,
and 100 more under `-race`.

**Lesson.** A fixed sleep on the far side of a boundary is not synchronization, it is a
bet on a scheduler, and the bet is lost exactly when the machine is busiest, which is
when the full gate runs. Wait for the condition the assertion actually depends on, and
poll the side that will do the asserting. When a flake appears right after a dependency
bump, pin the old version in a worktree and reproduce there before believing the
coincidence; this one had every appearance of a regression and was years older than the
bump.

## 212. A tab's title named a filter its query never applied

**Symptom.** A template's record page carried a tab reading "Completed jobs". It listed
running, pending and failed jobs alongside completed ones, so the count beside it and
the rows under it disagreed with the word above them. Nobody reported it, because a
reader who sees a running job under a heading saying "Completed" concludes the heading
is loose rather than that the page is wrong.

**Root cause.** `completedJobsSection` calls `dispatch.JobStore.ListForTemplate`, which
returns every job a template has produced whatever state it reached. The title was
written when the section was imagined and never revisited against the call it describes.
Nothing could catch it: the title is a string, the query is a method, and no test asserts
a relationship between them because none can be stated.

**Fix.** Renamed the tab to "Jobs", which is both accurate and the word AWX uses for the
same tab, and rewrote the summary to say "whatever state it reached" out loud. The
function is `jobsSection` now, so the name in the code and the name on the page agree.

**Lesson.** A title is an assertion about a query, and it is the only assertion in a view
declaration that nothing verifies. When a section's rows come from a method whose name
does not contain the same qualifier as the title -- "completed", "recent", "active",
"failed" -- read the method before believing the title. The qualifier is usually the part
that is wrong, because it was written first.

## 213. The stylesheet had no rule for two classes its own templates emitted

**Symptom.** The primary action on every record page rendered as an ordinary button:
`Launch`, `Save` and `New` were visually identical to `Edit` and `Cancel`. Related-record
sections ran into each other with no spacing under their headings.

**Root cause.** `views.templ` emitted `class="btn btn-primary"` on record actions and
`class="detail-section"` on every section panel. Neither class existed anywhere in
`app.css`. `.btn` matched, so the control was styled enough to look deliberate, and
`.detail-section` matched nothing at all. It had been that way since the classes were
first written: a class attribute with no rule is not an error in any language involved,
produces no console warning, and renders as "slightly plainer than intended", which is
indistinguishable from a design choice.

**Fix.** Added both rules. `.btn-primary` takes `--fill-info`/`--on-fill-info` rather than
a colour of its own, because that pair is already walked by the contrast gate across all
four skins, both themes and accessibility mode; a new colour would have been a new pair
for that matrix to cover. Then added a check that extracts every `class="..."` value from
the `.templ` sources and asserts each name has a rule in the stylesheet.

**Lesson.** A missing CSS rule is the quietest defect in a web UI: nothing fails, nothing
warns, and the result looks like a decision. The templates and the stylesheet are two
lists of class names that must agree and neither compiler checks the other, so the
agreement has to be checked by something. Grepping the templates for class names and
diffing against the stylesheet's selectors takes one command and finds years-old gaps.

## 214. Making a record's name its own link produced anchors with no accessible name

**Symptom.** `TestViewConformance_EveryViewRendersItsList/runbooks` failed with "a link
has no accessible name, so it is announced only as its URL", twice on one page.

**Root cause.** The trailing "Open" column was replaced by making each row's primary cell
a link to its record, which is the right design and is what every comparable control plane
does. The primary cell is whichever field declares `MobilePrimary`, and nothing guarantees
that field has a value: a runbook with no name rendered `<a href="/ui/runbooks/x"></a>`.
An empty anchor is focusable, is announced as its own URL, and is worse than the plain
cell it replaced. The old "Open" column could not have this bug because its text was a
literal.

**Fix.** `TableModel.CellText` falls back to the row's identifier for an empty primary
cell, and `CellHref` only offers a link where `CellText` guarantees text. The two are
documented as a pair that must agree. The conformance suite already had the assertion --
it was written for a different reason and caught this on the first run.

**Lesson.** Replacing a literal with data is where accessible names get lost. Any change
of the form "stop rendering a fixed label, use the record's own value instead" needs the
empty case answered in the same commit, and the answer is never "render an empty link":
either substitute something that is never empty, or render no link. A row must also stay
reachable, so dropping the link is only correct when the row leads nowhere on its own.

## 215. The contrast gate measured a colour pair that was not on the screen

**Symptom.** The Las Ventanas skin rendered the entire dashboard on `#008080` teal:
headings, summaries, chart caption and section text all sat directly on it. The muted
summary lines were effectively unreadable. Every contrast test in
`internal/ui/static/contrast_test.go` passed, across all four skins, both themes and
accessibility mode, for the whole time this was true.

**Root cause.** Two facts that were each correct alone. `body` paints
`var(--body-bg, var(--bg))`, and Las Ventanas is the one skin where those differ on
purpose: `--body-bg` is the Windows 95 DESKTOP teal and `--bg` is the `#C0C0C0` dialog
face, documented in the token block as teal sitting "behind the dialog rather than under
it". Nothing drew the dialog. `.block` was the only rule in the stylesheet that painted
`--bg`, so every region that is not a `.block` -- the whole dashboard, every collection
table, every related-record section -- rendered onto the desktop colour.

The gate could not see it. Every assertion measures a token against `--bg`, which is the
right pair to measure and was not the pair on screen. `--fg-muted` is `#4A4A4A`: about
7:1 against `#C0C0C0`, which is what the gate measured and reported as a pass, and about
1.5:1 against `#008080`, which is what a reader actually got. Three skins alias
`--body-bg` to `--bg`, so the gate was accidentally right for them and the one skin it
was wrong about was the one nobody had looked at.

**Fix.** `.layout` and `.main` now paint `background: var(--bg)`, which is a no-op for the
three aliasing skins and is what makes the measured pair the real one. `--layout-inset`
(undeclared everywhere, `0.5rem` on Las Ventanas) keeps the desktop meaningful by insetting
the shell so the application reads as a window sitting on teal, which is what the token was
for. `TestTheShellPaintsWhateverTextSitsOn` asserts both rules declare that background, so
the gate's own premise is now checked rather than assumed.

**Lesson.** A contrast suite asserts a relationship between two tokens; it does not assert
that either one is what the browser paints. When a stylesheet has more than one background
token, something has to prove which one is actually behind the text, or the suite is
measuring a hypothetical. The tell is a token that only one rule consumes: `--bg` was read
by `.block` alone while `--body-bg` covered everything else, and that imbalance was visible
in the file long before anyone looked at the skin.

## 216. `natsControl.SubscribeCancel` returned before the broker had registered the subscription, so a cancel arriving in that window was dropped permanently rather than delivered late

**Symptom:** found while building job cancel, by its own test, before it shipped.
`TestNATSControl_CancelReachesEverySubscriber` failed for the entire length of its timeout
rather than intermittently, and only when a second connection was subscribed: delivery to a
subscriber on the PUBLISHING connection always worked, delivery to any other connection failed
roughly one run in three. The first diagnosis was wrong and worth recording. The failure
appeared under parallel load, `internal/event` is a listed flaky package, and the obvious
reading was FAILURE_PATTERNS #61 resource contention. Widening the deadline from two seconds to
thirty is what disproved that: the test then failed for the full thirty seconds, which no amount
of scheduling delay explains.

**Root cause:** nats.go buffers the `SUB` protocol line and writes it asynchronously, so
`nc.Subscribe` returning says nothing about whether the server knows the subscription exists. A
cancel is a plain core publish with no queue, no acknowledgement and no redelivery, so a message
published before the server has processed the `SUB` is not queued for that subscriber, it is
routed to nobody and discarded. Losing the race therefore does not make delivery late, it makes
delivery never. The same-connection case passed consistently because the `SUB` and the `PUB`
share one write buffer and reach the server in that order in a single flush, which is exactly
the asymmetry that made the bug look like a delivery-model problem rather than a registration
one. In production the window sits where it does the most harm: `executeWithLease` subscribes
immediately before calling `Execute`, so the vulnerable moment is the first instants of a run,
which is when an operator who has just launched something is most likely to stop it.

**Fix:** `SubscribeCancel` (`internal/event/control.go`) calls `FlushWithContext` after
subscribing, bounded by `subscribeRegistrationTimeout`, and unsubscribes and returns an error if
that flush fails rather than handing back a subscription the server may not have. The test's own
raw wildcard subscription needed the identical treatment and did not get it in the first pass,
which is why `TestNATSControl_PublishesUnderTheDeclaredSubject` then failed once in three runs by
itself: the test carried the very race the implementation had just been fixed for. Both sides
flush now, and the pair has run clean eight times consecutively.

**Lesson:** see `LESSONS_LEARNED.md` #181.

## 217. Job results were published only when `RUNNER_WAL_DIR` was set, and nothing in the module ever consumed them

**Symptom:** found while planning job cancel, by asking what a `running` job state would be built
on. `topology.ResultSubjectAll()` had exactly two non-test references in the whole module: the
Runner's own publish, and the Runner's publish permission in `internal/meshid/grant.go`. Nothing
subscribed. Separately, `reportResult` (`internal/runner/agent_wal.go`) returned immediately when
`a.wal == nil`, and the WAL is only constructed when an operator sets `RUNNER_WAL_DIR`
(`cmd/runner/main.go`), so a default deployment published nothing at all. The subject carried real
traffic in a WAL-enabled deployment and reached nobody in any of them.

**Root cause:** two independent halves of one feature, each individually reasonable. PLAN.md
Section 16's State Desync Mitigation describes a Runner buffering a result in a local
write-ahead log and retrying, and the option was built as one unit, so the DURABILITY of a
result and the REPORTING of it became the same switch. Nothing noticed, because the consumer
side was never built: a publish nobody reads produces no error, no backlog a person would see,
and no failing test. The gap was invisible for as long as the platform never asked a question
whose answer depended on it, and `completed` meaning "the fan-out finished" was exactly such a
platform. It stopped being invisible the moment a job needed to stay `running` until its devices
reported, at which point a Runner reporting nothing would leave every job it touched running
forever.

**Fix:** reporting and durability are separate options now. `WithResultReporting` publishes every
outcome and is wired unconditionally by `cmd/runner`; `WithResultWAL` adds the durable retry on
top and stays behind `RUNNER_WAL_DIR`. `internal/dispatch.ResultConsumer` is the missing other
end, one durable consumer shared by every Controller replica. Its end-to-end test publishes on
the subject a Runner derives rather than calling the handler directly, because a handler-only
test would have passed throughout the entire period the subject reached nobody.

**Lesson:** a port with a publisher and no consumer is not half-built, it is unbuilt, and it
reports success the whole time. When a producer's output is not read by anything, nothing about
the producer working is evidence the feature works, so the absence has to be checked for
directly: ask who subscribes, by symbol reference rather than by reading the producer. The
related trap is the one this pair produced together, that an optional durability wrapper quietly
became an optional feature switch, so check whether an option gates the mechanism or only its
resilience.

## 218. `http.request`'s documented `timeout` could not raise the TLS handshake deadline, so a slow endpoint failed after ten seconds having been told it had sixty

**Symptom:** surfaced as a test failure in this repository's own gate, not as a report from a
user, and the first reading of it was wrong. `TestRequest_VerifiesCertificatesByDefault`
(`internal/catalog/http`) failed under full parallel `-race` load with
`net/http: TLS handshake timeout` where it expected an error naming the certificate, having
taken 16.43 seconds for a handshake against a local `httptest` server. The package provisions no
containers and passes `-count=5` in isolation in about a second, so it did not qualify for a
`flaky-packages.json` entry and could not be dismissed as contention even though contention was
what exposed it.

**Root cause:** `requestSpec.client()` returned a bare `&http.Client{}` on the verifying path. A
nil `Transport` means `http.DefaultTransport`, whose `TLSHandshakeTimeout` is a fixed ten seconds
set in `net/http`'s own package initialization. The method bounds its request with
`context.WithTimeout(ctx, spec.timeout)`, which is the number the `timeout` parameter documents
as "how long to wait for the whole request including reading the body", and that context has no
influence on the transport's separate handshake deadline. So there were two deadlines, an
operator could set only one of them, and the one they could not set was the shorter. A task given
`timeout: 60` against a device under load, or across a link with real latency, failed at ten
seconds. The skip-verify path cloned `DefaultTransport` and therefore carried the identical cap,
inheriting the defect while looking like it had its own configuration.

A second, quieter fault sat in the same three lines. The function's own doc comment said "a fresh
client each time rather than a shared one", and gave the reason: a pooled connection established
without certificate verification must never be handed to a later task that asked for
verification. The skip-verify branch honored that by cloning. The verifying branch returned the
process-wide shared transport, so the stated property held only on the path that did not need it.

**Fix:** both paths clone, both set `TLSHandshakeTimeout` to the task's own timeout, and only the
skip-verify branch attaches a `tls.Config`. The request context and the handshake deadline are
now the same number, so there is one budget and the operator sets it.
`internal/catalog/http/client_internal_test.go` pins all three properties, and is an in-package
test deliberately: proving the handshake budget behaviorally needs a server that stalls a
handshake for longer than ten seconds, which is a test that asserts by waiting. Each of its three
cases was run against the original code and fails there.

**Lesson:** a timeout parameter is a promise about the whole operation, and a library default
underneath it can quietly own a shorter one. When exposing a timeout, enumerate every deadline
the call can hit rather than only the one being set, and note that a nil field is a configuration
choice: `&http.Client{}` is not an unconfigured client, it is the shared default one. The
diagnostic habit that found this is also worth keeping, and it is the same one
`LESSONS_LEARNED.md` #181 records: the failure was under container load in a suite full of
genuine container flakes, and what separated it from one was that the package provisions nothing
and the timing was reproducible in kind rather than in occurrence.

---

## 219. A view's relation check refused one endpoint named by two controls, and said so by naming that endpoint twice

**Symptom:** declaring a Remove control beside the Add control on a credential type's Inputs tab
made the view refuse to register at all, with
`view "credential-types" uses relation "set-inputs" for both set_credential_type_inputs and
set_credential_type_inputs`. The same endpoint on both sides of "for both" is the tell.

**Root cause:** `validateOps` walks every endpoint a descriptor names and refuses two that share
a link relation, because `Affordances` is keyed by relation and a template asking `Can(rel)` could
not say which of two operations it had been told about. The check compared relations and never
compared the endpoints behind them, so an endpoint named twice tripped it exactly as two different
endpoints sharing a relation would.

The case it hits is not ambiguity. Adding an input to a credential type and removing one are both
its set-inputs endpoint: the stored document is replaced either way, and the affordance question
has one answer for both controls. There is one relation, one scope and one API operation, and the
single answer `Can(set-inputs)` gives is correct rather than ambiguous.

The cost of the false positive was not the refusal but what the refusal pushed an author toward.
The only way past it was to invent a second endpoint with a second relation describing no separate
operation, which puts a relation in the JSON `_links` array that no API operation corresponds to.

**Fix:** the dedupe compares the endpoint name as well as the relation, so two DIFFERENT endpoints
sharing a relation are still refused and one endpoint named twice is allowed. The existing
refusal test (two genuinely different endpoints) still passes and is the negative control;
`TestRegister_AcceptsOneEndpointNamedTwice` is the positive one and fails against the old check
with exactly the message above.

**Lesson:** a uniqueness check over a derived key needs to know what the key was derived from. A
duplicate key means "two things collided" only when the two things are actually distinct, and a
message that prints the same value on both sides of "for both" is the shape that says the check
lost track of which is which.

---

## 220. A row control's relation was missing from the candidate set, so every one of them was withheld from everybody, including an administrator

**Symptom:** the new Remove control on a credential type's Inputs tab rendered for nobody. No
error, no log line, no 403. The table drew its rows and the actions column simply was not there,
which is indistinguishable from a deliberate decision not to offer one.

**Root cause:** `Descriptor.Candidates` is what the HATEOAS generator is ASKED about. It collected
the endpoints of a view's operations and of its record actions, and a section's row actions had
been added to the descriptor without being added to it. The resolver then asked
`permits(a.Endpoint, aff)` about a relation that had never been offered for evaluation, was
therefore never in the permitted set, and answered no for every caller at every role.

The failure is silent by construction, because withholding a control is exactly what the
affordance layer is supposed to do when a caller may not use it. There is no way to tell "you are
not permitted this" from "nobody asked whether you were permitted this" by looking at the page,
which is what makes it worth writing down rather than only fixing.

**Fix:** `Candidates` walks each section's row actions too. The test is
`TestDescriptor_CandidatesOffersARowActionsRelation` for the unit, and the router-level
`TestRowAction_RendersOnlyWhereItApplies` for the behaviour; removing the new loop makes the
second report that no control posts to any row.

**Lesson:** every gated affordance needs its relation in two places, the gate and the candidate
set, and only one of them fails loudly when it is missing. When adding a new kind of gated
control, find the enumeration the authorization layer is driven from and add to it in the same
change, then prove a permitted caller can see the control rather than only that an unpermitted
one cannot.

---

## 221. A managed credential type offered Add input and Add injector, both of which the store refuses in its second statement

**Symptom:** found while adding the row controls rather than reported. A platform-shipped
credential type rendered "Add input" and "Add injector" on its own tabs. Pressing either reached
`UpdateType`, which refuses a managed type before it validates anything, and the operator was
answered with a field error on a form they should never have been offered.

**Root cause:** `Descriptor.Applies` for the view already withdrew the record's own edit and
delete relations on a managed row, with a comment explaining that the store refuses both so the
affordance is withdrawn rather than offered and then refused. The two section write relations,
`set-inputs` and `set-injectors`, were not in that predicate. They reach the same `UpdateType` and
hit the same refusal, so the reasoning applied to them exactly and the list had simply not been
extended when the header half of the section write path shipped.

**Fix:** the predicate switches over all four write relations. The new test drives a managed type's
two tabs and asserts neither offers an add or a remove; against the old predicate it fails on all
four controls, which is what confirms this was live rather than theoretical.

**Lesson:** a predicate that enumerates relations is a list that goes stale every time a relation
is added, and nothing fails when it does. When a new write relation is introduced on a resource
that already withdraws affordances conditionally, the predicate is part of the change, not a
follow-up. A test that drives the withdrawing case through the real router is the only thing that
notices.

---

## 222. Two publishers on one stream minted the same message id, so every job result was discarded as a duplicate of the dispatch that caused it

**Symptom:** every job hung in `running` forever. Not in tests only: in every deployment on the
branch. `tests/e2e` reported `job <uuid> did not reach a terminal state within its budget; last
observed state "running"` on four tests, at 304 seconds each, and then the package was killed at
`22m0s`. The controller logs showed the poller getting a clean 200 every 28 milliseconds and the
state never changing.

**Root cause:** the Controller stamps each per-device dispatch with the JetStream message id
`"<jobID>:<deviceID>"` (`internal/dispatch/worker_devices.go`). The Runner published that
device's *result* under the byte-identical id (`internal/runner/agent_wal.go`). JetStream's
producer-side duplicate window is scoped to the **stream**, not to the subject, and
`internal/topology` puts every subject in one stream (`pleiades.>`), so a message id in this
system is global. The result collapsed onto the dispatch that had caused it seconds earlier.

Three things conspired to make it invisible rather than merely wrong:

1. The broker answers a suppressed duplicate with `PubAck{Duplicate: true}` and a **nil error**.
   `internal/event/nats.go` discarded the ack with `_`, so a dropped publish and a delivered one
   were the same value at every layer above.
2. `publishResult` therefore returned true, and `flushOne` acknowledged the WAL entry, deleting
   the only durable copy. The retry mechanism was defeated by the same nil error.
3. The Runner's own publish-failure log was at `Debug`, and `cmd/runner` builds its logger at
   `LevelInfo`. Even a genuine, error-returning failure would have said nothing.

The duplicate window is `min(outage budget, 5m)`, which produces a perverse selection: a job
whose devices report back inside five minutes hangs, and a job slower than that completes. Fast
jobs failing and slow ones passing is exactly the shape that reads as flake.

**Provenance, which is the part worth not misreading.** The collision predates `main` by five
weeks and was inert, because nothing consumed the result subject at all (FAILURE_PATTERNS #217
is the same subject's earlier story). The commit that made a job's ending depend on a message the
broker had always been dropping is what turned a dormant collision into a total outage. The
trigger is not the defect.

**Fix:** the result publishes under a namespaced key, `"result:" + entry.ID`. The WAL's own entry
id stays `(JobID, DeviceID)`, because a redelivered dispatch must re-report as the same entry;
only the key on the wire has to leave the dispatch's namespace.
`internal/adapters/native/journal.go` already namespaced its own key this way, for this reason,
so the precedent was in the tree and was not followed.

Two hardening changes went in beside it, and they matter more than the one-liner.
`internal/event/nats.go` now warns when a `PubAck` comes back `Duplicate: true`, which is the
log line that would have made this visible on day one. And `internal/event/nats_dedup_test.go`
gained the case whose absence let it ship: both existing real-broker dedup tests publish to a
**single topic**, so they are equally consistent with a subject-scoped window and a stream-scoped
one. They passed while three doc comments in this module asserted the subject-scoped model,
including the one directly above the offending line, which described the two keys agreeing
"exactly, for the identical reason" as the mechanism working correctly.

**Lesson:** a test that exercises a mechanism through a single instance of the dimension the
mechanism is actually keyed on proves nothing about that dimension, and will happily coexist with
documentation asserting the wrong model. Dedup keyed on a stream, tested on one subject, teaches
"per subject". The second lesson is the ack: an API that reports "I did not do what you asked"
through a success return needs its result read, and a wrapper that discards it converts a loud
failure into a silent one for every caller it will ever have.

---

## 223. A job whose devices all reported before its fan-out finished was parked in a state nothing sweeps

**Symptom:** none yet, and that is why it is recorded. It was found while fixing #222 and was
masked by it: no result ever arrived, so this window was never reached. It becomes live the
moment results do.

**Root cause:** a `JobTask` row is written inside the fan-out loop, and `SettleRunning` runs
after the loop finishes. A result can therefore be recorded while the loop is still walking. If
the *last* outstanding result lands in that window, `RecordResult` correctly reports that the job
is waiting on nothing, and the `CompleteRunning` that follows matches no row, because the job is
still `fanning_out` rather than `running`. That no-match is deliberately one of the ordinary
endings (another result got there first, or the job was cancelled) and returns nil.
`SettleRunning` then moves the job to `running` with zero outstanding tasks and nothing left in
the system that would ever end it. `dispatch.Reaper` looks only at `fanning_out`, so nothing
sweeps it.

The window is not narrow. The e2e suite's runbook is a `noop` that finishes in microseconds,
which is exactly the shape that reports back before a fan-out over the rest of its group has
finished.

**Fix:** `SettleRunning` re-asks whether anything is outstanding and completes the job itself
when nothing is. Re-asking rather than tracking, for the same reason `RecordResult` counts rows
rather than keeping a counter: two parties can reach this conclusion at once, and
`CompleteRunning`'s own `running` guard makes the second a no-op. Both callers now ask through
one helper so they cannot drift into disagreeing about what outstanding means.

**Lesson:** when a state transition is driven by an external event and the state it transitions
*from* is set afterwards, the event can always arrive first, and the guarded write that makes
the race safe is also what makes the early arrival silent. A "nothing matched, which is fine"
branch needs asking what happens if it is reached because the state has not been set *yet*
rather than because it has already moved on.

---

## 224. The flake waiver tolerated a whole package, so a total regression printed "passed" five times

**Symptom:** `make push-gate` reported `testgate: passed (warnings above)` on five consecutive
runs while every job in the system hung forever. The previous session's handoff recorded, truthfully
and misleadingly, that "both test phases pass, their only failures confined to packages
`flaky-packages.json` already names".

**Root cause:** `flakegate.Classify` keyed tolerance on the package import path alone. Every entry
in `flaky-packages.json` names the specific tests it observed flaking, in prose, because the
file's own policy is that an entry records evidence rather than a guess. The code never read that
prose, so a package with one known-flaky test tolerated every test in it, including three that had
never been seen to flake and one that was failing deterministically.

Two further holes in the same function: a package-level failure is promoted to a hard build
failure only when the package produced *no* per-test failures, so the `Test killed with quit: ran
too long (22m0s)` was swallowed alongside the four tolerated tests. And nothing re-runs a warned
test in isolation, although "reproduced as clean, fast passes every time when rerun in isolation"
is the evidence `flaky-packages.json`'s own entries cite for being there.

**Fix:** an entry may name its tests and then tolerates only those. An entry with no list keeps
covering its package, which is an unnarrowed entry rather than a second policy. `tests/e2e` is
narrowed to the tests its prose already named. The other eighteen entries are deliberately left
unnarrowed, because narrowing them is real work against real evidence rather than a mechanical
edit. The package-kill rule and the isolation re-run are recorded in `HANDOFF_DOCUMENT.md` as
decisions rather than improvised.

**Lesson:** a waiver's scope must be the scope of the evidence that justified it. Writing the
evidence in prose beside a broader machine-readable rule means the rule is what runs and the
prose is what gets read during review, and the two drift the moment something new fails in the
same package. If an entry can say which test, it must say which test.

---

## 225. A test harness decoded every poll into one reused value, so one device's skip reason appeared on another device's row

**Symptom:** `tests/e2e`'s `TestGrandIntegration` failed with `device rtr1 was dispatched but
carries the reason "device \"rtr5\" has no host property"`. A skipped device's reason text on a
dispatched device's row. Three separate investigations went looking in the fan-out, which is
where the assertion's own wording pointed, and none of them found anything, because nothing is
wrong there.

**Root cause:** the leak is introduced inside the harness, after the HTTP bytes arrive. The API
never emitted a wrong `reason`; a fresh decode of the identical response body is clean.

`pollJobUntilTerminal` declared its decode target once, outside the poll loop, and unmarshalled
every poll into it. `encoding/json` MERGES into what it is handed: it reuses an existing slice's
elements rather than allocating new ones, and it leaves a struct field untouched when the incoming
JSON carries no key for it.

Three things had to be true together, and removing any one hides it:

1. The reused target.
2. `reason` is the only `omitempty` field the harness decoded, so a dispatched task emits no
   `reason` key and the decoder cannot overwrite the slot's previous occupant. That is exactly
   why `device_id` and `device_name` never disagreed and only `reason` went stale.
3. The task list had no stable order. Earlier polls returned `[rtr1, rtr2, rtr5]`; the terminal
   poll returned `[rtr5, rtr2, rtr1]`. Slot 2 held rtr5's decoded struct, received rtr1's three
   non-omitempty fields, and kept rtr5's reason.

**Provenance, and why it is worth separating the two halves.** The harness defect is on `main`
verbatim: `git diff main..HEAD -- tests/e2e/` is empty. What made it fire is new. Before this
branch a job went `fanning_out` to `completed` with no state in between, so exactly ONE poll ever
carried task rows and there was nothing to merge into. And `job_tasks` was insert-only, so its
scan order could not move. The branch removed both protections at once, in the same commit.

**Fix:** decode each poll into a fresh value. Separately and on its own merits, the store now
orders the eager load, because an endpoint whose array reorders between identical reads is a trap
for any client written the obvious way, not only for this harness.

Two further things came out of it. The poller's failure messages all rendered the DECODED view,
so a harness corrupting its own decode reported that corruption as though the server had sent it,
and no diagnostic in the suite could tell the two apart; it now keeps the last raw body and prints
it. And the harness decoded only the Controller's fan-out decisions (`device_id`, `device_name`,
`outcome`, `reason`) and nothing the Runner reported, so the entire result pipeline was reaching
this test unexamined; it now asserts `result` and `finished_at`.

**Lesson:** `json.Unmarshal` into a reused value is a merge, not a replacement, and `omitempty`
is what turns that from a curiosity into a data-corruption bug: an absent key leaves the previous
value in place, so the fields that go stale are exactly the ones a server omits when they are
empty. The pairing to watch for is a reused decode target, an `omitempty` field, and a collection
with no guaranteed order. The second lesson is about diagnostics: a failure message built from a
value the test itself derived cannot distinguish "the server sent this" from "we corrupted it",
and when a harness can be wrong, its error messages have to carry what actually arrived.

---

## 226. A form's prefill was computed, sorted, and dropped, so saving it as drawn unbound every credential a template ran as

**Symptom:** none reported, which is the point. Found by a design review asking what the
prefill seam was for. The control looked and behaved like a working form.

**Root cause:** `bindCredentialsAction` sets what a template authenticates as. It is a
multi-select whose own help text says "Replacing this list replaces what the template
authenticates as", and its `Submit` does exactly that: whatever is selected becomes the whole
binding.

Its `FieldsFor` resolved the template's currently bound credential ids into a slice, sorted
them, and never referred to them again. A record action's form had no prefill hook, so there
was nowhere to put them. The form therefore rendered with **nothing selected** on a template
bound to three credentials, and pressing the button as drawn replaced those three with none.
The template silently stopped authenticating as anything.

The discarded slice is the diagnostic worth remembering. Somebody wrote exactly the value the
form needed and had nowhere to put it, which is what a missing seam looks like from inside a
call site. A variable that is built with care and never read is a question, not dead code.

**Why nothing caught it.** The existing test opened the form and asserted the choices were
offered with their type names, which is true and insufficient: an empty multi-select offers
every choice exactly as a prefilled one does. Nothing asserted that what the record already
held was selected, and an empty control and a control holding nothing are the same rendering.

**Fix:** `RecordAction.Form`, the same name and signature as `Handlers.Form`, the edit form's
own prefill, so there is one answer to "where do a form's existing values come from" rather
than two to keep in step. A prefill naming a control the form does not render is refused
rather than dropped, and so is a value for a password control, since `field.templ` writes a
password into a value attribute.

**Lesson:** a form that REPLACES a collection is a different animal from one that adds to it,
and the difference is invisible in the markup. Whenever a Submit's semantics are "what you see
is what it becomes", the form that draws it must be able to show what it currently is, and a
test for it has to assert the current state is SELECTED rather than merely offered.

---

## 227. Four action redirects left off the UI's mount prefix, so a successful write answered with a 404

**Symptom:** binding a template's credentials, attesting an organization or a team, and testing
a credential type each redirected to `/templates/1` rather than `/ui/templates/1`. The write had
already succeeded; the operator saw a 404, which reads as though it had not.

**Root cause:** each of the four built its redirect path by hand and each omitted the prefix the
UI is mounted at. The handler passes a non-empty redirect through verbatim, and the prefix is
configuration (`Config.Prefix`), so a hand-built path is wrong at the default and wrong again for
any deployment that changes it.

**Fix:** all four wanted exactly the record they had just written, which is what the handler
already builds when a `Submit` returns an EMPTY redirect, through the configured prefix. So the
fix is to delete the path rather than correct it. The handler knows the prefix; a call site does
not.

**Why nothing caught it.** Every test of these actions asserted the status code and the stored
result. None followed the redirect or looked at the `Location` header, so the destination was
never observed by anything.

**Lesson:** when a handler offers a correct default, a call site that reimplements it is a place
to check rather than a place to trust, and four independent authors producing the same mistake
says the default was not obvious enough. Assert the `Location` header, not just the 303: a
redirect nobody follows in a test is a redirect nobody has tested.

---

## 228. Two doc comments asserted a two-place safety check that had one place, and one of them described a security control that does not exist

**Symptom:** none, which is the point. Both comments read as reassurance and both were believed:
a design panel working from them proposed building on the enforcement they described, and this
session very nearly cited one of them as precedent for a new rule.

`internal/credstore/ent_store_credentials.go` said of `credtype.CheckBinding`: "The handler runs
it too, so a caller gets a conflict naming both credentials rather than an opaque store error,
and this one exists so a second writer cannot skip it. Two callers, one implementation."
`go_symbol_references` reports one production caller, the store itself. `internal/api` never
calls it.

`internal/credtype/inputs.go` said `launch.Survey.SecretVariables` and
`InputSchema.SecretFields` "both feed redact.Literals". Only the credential half does. The sole
`Literals().Add` in production is `internal/adapters/legacy/inject.go:83`, from a credential
artifact. `SecretVariables` has three callers and none is the masker, so a survey password
answer's VALUE is never added to the log scrubber and a task that echoes one lands it in the job
log in the clear.

**Root cause:** both comments described an intended arrangement rather than an observed one, and
nothing re-reads a comment when the code beside it moves. The second is worse than a stale
comment because it names a security control by the identifier that implements it, which is
exactly the shape that survives review.

**Fix:** both corrected to say what is true. The `CheckBinding` one now records that being the
single gate is the STRONGER arrangement, since every writer reaches the store including the
server-rendered UI's own binding action, which does not go through `internal/api` at all. The
masker one now states the gap plainly and says why it is not being closed by adding survey
answers to `redact.Literals`: a 32 KiB file answer in the process-wide literal set would scrub
enormous unrelated substrings out of every later log line.

**Related, found in the same sweep and NOT fixed:** `routing.CheckInjectable`, the bind-time half
of the Section 29.4 rule, also has exactly one production caller (`internal/api/credentials.go`),
while the UI's credential-binding action writes straight to `credstore.Store`. A binding made
through `/ui` is caught only by the run-time backstop. The secret still never reaches the native
path; what is lost is the refusal arriving at bind time instead of as a failed job.

**Lesson:** a comment claiming enforcement is a claim to CHECK, never a claim to cite.
`go_symbol_references` settles it in one call and is cheaper than reading the file. When placing
a new two-place rule, put the load-bearing half where every writer must pass -- the store -- and
not in a handler, because this repository has two examples of a handler-side check that one of
its own UIs walks past.

## 229. A pre-push gate outlived the connection git had already opened, so every push died with SIGPIPE and no output while the gate printed "all checks passed"

**Symptom.** `git push` exited 141 after roughly twenty-five minutes. The pre-push hook's
output ended with `push-gate: all checks passed`. Git itself printed nothing at all: no
"Enumerating objects", no error, no rejection. `git ls-remote` showed the branch had never
reached the remote. Reproduced five times, once per attempt, across two different framings of
the command.

**Root cause.** Git connects to the remote and fetches its ref advertisement BEFORE running
`pre-push`, because the hook's stdin is one line per ref carrying `<local ref> <local sha>
<remote ref> <remote sha>` and the remote sha can only come from the remote. The hook then ran
`make push-gate`, a twenty minute suite. By the time it exited 0, the remote had dropped a
connection that had been idle the whole time. Git's first write to it raised SIGPIPE, which is
fatal by default and produces no message. 141 is 128 plus 13.

**Fix.** Separate the gate from the push. `make push-gate` and `make ci` now end by writing
`.git/pleiades-gate.json` through `tools/gatereceipt`, naming the commit verified, which gate
ran, and when. `.githooks/pre-push` no longer runs anything: it reads that receipt back for the
exact commits being pushed and answers in about a second. A receipt is only issued from a clean
tree, and untracked files count as dirty.

**Lesson.** Two diagnostic moves mattered and both were cheap. First, a delete-only push
(`git push origin :refs/heads/nonexistent`) skips the hook by design, so it isolates transport,
auth and write permission in two seconds; it succeeded immediately and proved none of those were
at fault, leaving elapsed time as the only variable. Second, the exit status of a pipeline is the
LAST command's, so `git push | tail` reports tail's 0 and hides everything. Both should have been
reached on attempt two rather than attempt five.

## 230. os.WriteFile truncates before it writes, and a test polling that file observed the empty window as an answer

**Symptom.** `TestSearch_WaitsForAPatternToDisappear` failed under `make ci`'s `-count=3` pass
with `the file holds "", want "starting up\nloading configuration\n" untouched`, naming contents
the test had written itself. It passed alone every time.

**Root cause.** The helper changed a watched file with `os.WriteFile`, which opens with `O_TRUNC`
and then writes, leaving the file empty between the two calls. Every test in that package is a
reader polling the same file while the helper changes it, so the window is observable. The method
under test waits for a line to be GONE, and an empty file satisfies that for the wrong reason:
under load it returned on the truncated file, and the test's own closing read then saw "" as well.

**Fix.** Write a sibling temp file and `os.Rename` over the target, so a concurrent reader sees
the old contents or the new and never neither. Applied to the two sites that rewrite a file
something is already watching, and deliberately not to the two that create a file which did not
exist, where an empty first instant is the answer rather than a race.

**Lesson.** Reproduced deterministically before fixing, by holding the file empty past one probe
interval, which produced the exact message on demand. A flake that cannot be reproduced should not
be "fixed": the change would do nothing while claiming to. Note also that the test could PASS for
the wrong reason, which is the more dangerous half and is invisible until the timing shifts.

## 231. A five second budget for a subprocess to appear fired under suite load, and the loop waiting for it never noticed the subprocess had died

**Symptom.** `TestExec_RoundTripsThroughARealPTYPair` failed `make ci` with `socat never created
both PTY links`. socat was installed; the test passed alone in under a second, fifty times, and
twenty more under `-race`.

**Root cause.** Two defects in one helper. The wait for socat to create its PTY symlinks was
bounded at five seconds, which is inside the range a loaded machine delays a subprocess by: `make
ci` runs twenty-one container packages serially and the whole suite three times over. Separately,
the loop only ever polled for the symlinks, so a socat that exited immediately still produced
"never created both PTY links" after the full wait, naming the symptom and sending the reader to
look at PTYs rather than at socat's own error.

**Fix.** Thirty seconds, which is not a claim about how long socat should take but the point at
which waiting longer tells us nothing new. And `cmd.Wait` now runs once on its own goroutine with
the loop reading the result, so a dead socat fails in milliseconds saying what happened. Both
copies of the helper, identically.

**Lesson.** A timeout that fires on load reports a defect that is not there and hides the next
real one. When a wait loop has a subprocess, "still starting" and "already gone" are different
answers and a loop that cannot tell them apart will mislabel one as the other forever.

## 232. Every device creatable through the API or the UI is permanently un-dispatchable, because nothing can write the host property the dispatcher requires

**Symptom.** Not observed as a failure, because nothing has looked. Found while deciding whether
a one-command local stack should seed a demo fleet.

**Root cause.** `internal/dispatch/worker_devices.go` skips any device whose properties carry no
`host`, recording `OutcomeSkipped` with `device %q has no host property`, because there is nowhere
for the Runner to connect. But `deviceWriteSchema` in `internal/apispec/apispec.go` deliberately
carries no properties field, and the UI's device form is name/type/state/tags only. Both omissions
are deliberate and documented: the property bag holds decrypted enable secrets and API keys, and
the masking ruleset that would make it safe to serve belongs to a phase that does not exist yet.
The consequence, which neither comment states, is that the only devices a job can actually run
against are ones written directly into the database through an ent client with the envelope hook
installed, which is what `tests/e2e/harness_seed_test.go` does.

**Fix.** None. Recorded deliberately rather than patched: the fix belongs to whichever phase owns
device properties and the masking ruleset, and widening the write schema without that ruleset is
the exact surface being deferred.

**Lesson.** `make ui-dev` seeds six devices and looks like a working fleet precisely because it
runs no Runner. A local stack that runs a real one would present the same fleet and skip every
device on the first launch, which is worse than an empty page. Two deliberate omissions in
different files can compose into a third property nobody decided on, and no single file's comment
is wrong.

## 233. ROTATE_ENCRYPTION_KEYS rotated devices alone, so following the rotation guide made every credential and saved survey answer unreadable

**Symptom.** Not observed in a deployment. Found while writing the setup command's recovery matrix,
which points operators at rotation as the safe way to change the master key, and so had to be true.

**Root cause.** The controller's rotation goroutine called `crypto.RotateDeviceProperties` and
nothing else. `RotateCredentialInputs` and `RotateSavedLaunchConfigAnswers` existed, were correct,
and had their own passing tests, and no composition root called either. A fourth encrypted column,
`mesh_signing_keys.seed`, had no pass at all. Meanwhile the production guide described one pass per
entity and told operators to remove the old key once every pass reported converting every row, and
each pass returned only a count of rows converted, so a row it skipped or could not read left no
trace an operator could check. An operator who followed the guide removed
`MASTER_ENCRYPTION_KEY_PREVIOUS` with every credential and every saved survey answer still sealed
under it.

**Fix.** `crypto.RotateAll` runs a pass for every column in `crypto.encryptedColumns`, the one list
rotation and the setup census share, and `TestEveryEncryptedColumnIsListed` pairs that list with
every exported write hook, so a new sealed column cannot land without a pass. Each pass returns
rotated, skipped and unreadable counts. `cmd/controller/rotation.go` logs one line per table and
says `key rotation complete: no row needs MASTER_ENCRYPTION_KEY_PREVIOUS any more` only when every
table was read and nothing was skipped. `TestRunKeyRotation_RotatesCredentialsAndSaysTheOldKeyCanGo`
removes the previous key after the pass and reads a credential back.

**Lesson.** This is the same shape as #93's composition gap one layer up: code that works, tested
where it lives, and never reached from where the product runs. A pass that reports only what it did
cannot answer the question its caller has to ask, which is what it did NOT do.

## 234. The chart's existingSecret path silently turned off the reinstall refusal and the pod roll it built on purpose

**Symptom.** Not observed. Found by an adversarial review of the setup command's plan, which had
chosen to emit a Kubernetes Secret for `secrets.existingSecret`.

**Root cause.** Two guards were computed from values the chart only sees when it renders its own
Secret. `the-pleiades.postgres.credentialFingerprint` returned empty under `existingSecret`, so the
retained-data check that refuses a reinstall onto a volume created with a different password did
nothing. And the controller's `checksum/secret` annotation hashed `secret.yaml`, which renders
nothing under `existingSecret`, so a replaced Secret never restarted the pods. The chart's own
comment recommended `existingSecret` as the way to keep secrets out of Helm's release records, so
the recommended path was the one with both guards off.

**Fix.** `secrets.existingSecretChecksum` and `postgresql.auth.existingSecretFingerprint` let an
operator-managed Secret supply both, and `controller setup --target helm` writes them beside the
Secret it emits. The controller now composes `DB_DSN` from `$(POSTGRES_PASSWORD)` under
`existingSecret` with the in-chart database, so the Secret never repeats a name derived from the
release. `tools/helm-lint`'s setup profile renders setup's own values file on every run, and the
kind gate proves both guards in a real cluster.

**Lesson.** When a template offers an operator-managed alternative to a value it would otherwise
compute, every guard computed from that value needs a supplied-value path, or the alternative
quietly turns the guard off. A guard comment that says "empty when X" is recording that gap.

## 235. The compose stack published PostgreSQL and an unauthenticated NATS on every interface

**Symptom.** Not observed as an attack. Found while designing how the setup command reaches the
compose database.

**Root cause.** `docker-compose.yml` published `5432:5432` and `4222:4222` with no host address, so
both listened on every interface of the machine running the trial stack. The database's password is
written in the same file, and the broker runs with no authorization while dispatch messages carry
the credentials their jobs use. Until this phase the master key was published in the file too, so
anything that could reach the machine could read every credential in plaintext.

**Fix.** Both are published on `127.0.0.1` only. Nothing in the stack needs them published at all;
the loopback port serves a client on the same machine, as the getting-started guide's bare
controller is. `TestComposePublishesTheDatabaseAndBrokerOnLoopbackOnly` pins it.

**Lesson.** A trial stack is the thing people leave running on a laptop on a shared network. "It is
only for local development" is a statement about intent, and a published port is a statement about
reach, and only the second one is enforced.

## 236. A fuzz property that checked for a secret as a substring of the error was unsound twice over, and the fuzzer found both

**Symptom.** `FuzzParseEnvFile` failed on `MASTER_ENCRYPTION_KEY=ASTER_ENCRYPTION`, then, after a
rewrite, on `MASTER_ENCRYPTION_KEY=\xc5`. Neither was a defect in the parser.

**Root cause.** The first property said "a refusal never contains a value from the input". The
value `ASTER_ENCRYPTION` is a substring of the variable name every refusal correctly names, so the
check could never be sound: a value can always collide with the error's own wording. The replacement
property parsed the input twice, once with every letter and digit in each secret rotated, and
required identical refusals. Its first version rotated with `strings.Map`, which replaces an invalid
UTF-8 byte with U+FFFD, so the rotated input stopped being invalid and was refused for a different
reason.

**Fix.** The property is differential: an error that does not depend on a value cannot contain it.
The rotation works on bytes, leaving everything but ASCII letters and digits untouched. Both inputs
the fuzzer found are kept in `internal/setup/testdata/fuzz/FuzzParseEnvFile` as seeds.

**Lesson.** Prove independence, not absence. A substring check on an error message is refuted by
any value that happens to spell part of the message, and a fuzzer will find one. And `strings.Map`
is not a byte-for-byte transform: it normalizes invalid UTF-8, which is exactly the input a parser
fuzz target exists to send.

## 237. A terminal echoes type-ahead: a secret typed before a hidden prompt appears is shown on the screen

**Symptom.** A pty test that typed a line and a secret in one burst found the secret in the
terminal's output, even though it was read with `term.ReadPassword`.

**Root cause.** A terminal's line discipline echoes input when it ARRIVES, under whatever settings
are in force then. `term.ReadPassword` turns echo off when it is called, which is after its prompt
is printed. Input that arrives before that, from a paste or from typing ahead, has already been
echoed with the normal settings.

**Fix.** None needed in the code: reading byte by byte (`prompt.Terminal`) loses nothing between an
echoed line and a hidden one, which the test proves separately. The tests that assert a secret is
not echoed now wait for echo to be off before typing, as a person at the prompt does, and the
production guide's possession-check section says that a key pasted before the hidden prompt appears
is echoed.

**Lesson.** "Read with echo off" is a property of the moment of reading, not of the input. Tests of
hidden input have to type after the switch, or they prove the wrong thing in both directions.

## 238. A line-by-line .env reader disagrees with docker compose in two measured ways, either of which would have written a second key

**Symptom.** None in production. Measured by `TestParseEnvFile_AgreesWithDockerCompose`, which runs
a corpus through the real `docker compose config`.

**Root cause.** Docker compose accepts `MASTER_ENCRYPTION_KEY: <key>` (a colon, YAML style) and
resolves the full key from it. A reader that only split on `=` would call that line someone else's,
see no key, and add a second `MASTER_ENCRYPTION_KEY=` below it. And compose reads a double-quoted
value across line breaks, so a line inside another variable's multi-line value that looks like
`MASTER_ENCRYPTION_KEY=...` is not a setting at all: compose resolved the key there to empty, while
a line-based reader would have taken it as the key.

**Fix.** The setup command's reader treats a colon like an equals sign when finding a line's name,
and refuses the colon form on its own lines. It refuses any file where an unowned line opens a
quoted value it does not close. The corpus test asserts that wherever the reader accepts a file,
compose resolves every owned variable to the same value.

**Lesson.** A reader for another tool's format is only as correct as a comparison against that
tool. Refusing what you cannot read identically is safe; guessing is not.

## 239. A new container-backed package missing from DOCKER_DEPENDENT_PACKAGES failed only as a timed-out container, twenty minutes into the push gate

**Symptom.** `make push-gate` failed in `test-repeat` with `TestRegister_RecordsOnceAndReturnsTheExistingRecordAfter/postgres`: `starting postgres: ... wait until ready: ... context deadline exceeded` after 533 retries. The package passed every time it ran on its own.

**Root cause.** `internal/keyregistry` was new, and its tests start a real PostgreSQL container to prove two replicas registering one key produce one row. The Makefile's `DOCKER_DEPENDENT_PACKAGES` names every package whose tests import testcontainers-go, so that `test-repeat` (which runs everything else three times, strictly, in parallel) leaves them out and `test-race` serializes them. The new package was not on the list, so `test-repeat` ran its container three times over on top of the whole module. The list's comment promised that a missing package "FAILS LOUDLY", and it did, but only as a daemon timeout that looks exactly like the flakes the list exists to prevent, at the end of a twenty minute run.

**Fix.** `internal/keyregistry` is on the list. `tools/internal/flakegate`'s `TestEveryContainerPackageIsListed` reads the imports of every `_test.go` file in the module, in every build configuration, and fails in a second when a package importing testcontainers-go is missing from the list. It was mutation-checked by removing the new entry.

**Lesson.** A list a person must remember to update, whose only enforcement is a failure indistinguishable from load, is not enforced. When a convention exists because of how a gate schedules work, check the convention directly, at unit-test speed, rather than waiting for the gate to trip over it.

## 240. Every compose release gate ran as the checkout's own compose project, so `make ci` deleted the database of a stack started with `make up`

**Symptom.** None observed, because it was found by reading before it happened. A developer's stack from `make up` was running while the gate was about to run.

**Root cause.** `docker-compose.yml` names its project `pleiades`, and `make up` in a checkout creates exactly that project. Every compose gate in `tests/e2e` (Phase 20's and Phase 83's) starts and ends with `docker compose down -v --remove-orphans`, run in the same checkout, and `scrubbedPackagingEnv` deliberately removed `COMPOSE_PROJECT_NAME`. `make ci` and `make push-gate` run those gates through `test-integration`. So running the gate beside a stack from `make up` removed its database and broker volumes, with exit code 0. The hazard grew in Phase 83, when `make up` in the checkout became the documented way to run a deployment that holds real data.

**Fix.** `packagingCommand` sets `COMPOSE_PROJECT_NAME=pleiades-release-gate` for every compose call, so the gate's volumes are its own. `freshComposeStack` first checks the three published ports and fails with a message naming `make down` (which keeps data) if anything holds them. A separate project cannot also separate fixed host ports.

**Lesson.** A test harness that shares a name with the thing a developer runs will one day act on the developer's copy. Give every destructive test its own namespace, and refuse loudly rather than proceed when it cannot have one.

## 241. A `build:` key does not stop docker compose pulling: with no local image it pulls the `image:` name first, from a namespace this project does not own

**Symptom.** Measured on Compose v5.5.1 with a throwaway project: a service with `image: <name>` and `build:`, run with no local image, printed `Image <name> Pulling`, failed the pull, and only then built. Phase 83's F4 had concluded that sharing the controller's `build:` was enough to stop a pull.

**Root cause.** Compose's default pull policy tries the registry first for any service that names an image. The `pleiades` namespace on Docker Hub is registered to a third party (created 2015, one repository). An image they published as `pleiades/controller:dev` would have run on any fresh clone that ran plain `docker compose up`, with the master key and the database.

**Fix.** `pull_policy: never` on the controller, runner, setup and backup services. Measured: with it, compose builds a missing image and uses a present one without rebuilding (260 ms warm, against 538 ms for `pull_policy: build`). `TestComposeNeverPullsWhatItBuilds` requires it on every service that builds. The Helm chart's default repository points at the same namespace, and is reported rather than changed, because which registry it should name is the user's decision.

**Lesson.** A configuration key describes intent; what the tool does with it is a measurement. Before relying on a key to prevent something, run the tool in the state where it would happen.

## 242. A restore role made NOINHERIT could not create tables in the database it owned

**Symptom.** `pg_restore` as the scratch role failed with `permission denied for schema public`, although the role owned the database.

**Root cause.** In PostgreSQL 15 the `public` schema is owned by `pg_database_owner`, and a database's owner holds that role implicitly. NOINHERIT, added to make the role as narrow as possible, stops a role using the privileges of roles it is a member of, and that includes the implicit one. The earlier manual probe had created the role without NOINHERIT and passed.

**Fix.** The role is INHERIT, the default. It is a member of nothing else, so inheriting grants nothing more.

**Lesson.** A narrowing flag can remove the one grant a design depended on. Test the role exactly as the code creates it, not as a probe did.

## 243. `ALTER ROLE ALL IN DATABASE x RESET ALL` does not clear a role's own setting in that database

**Symptom.** `TestScratch_ClearSettingsRemovesWhatTheFileLeft` planted three settings as the scratch role and found one left after clearing.

**Root cause.** `pg_db_role_setting` has an entry per (database, role) pair. `ALTER ROLE ALL IN DATABASE` clears the entry for all roles (role 0) in that database, not every role's entry there. A setting the role pinned to itself inside the scratch database survived, and it would have applied to every later session there as that role, including the census and the schema comparison.

**Fix.** Clear each place the role can write: the database's settings, the role's settings, and the role's settings inside the database. Then count what is left, and refuse if anything is.

**Lesson.** When a security step is "remove everything", end it by counting what is left rather than trusting the statement's name.

## 244. A setting a restored file attaches to its database applies to the sessions that check it

**Symptom.** Measured by hand before the restore code existed: a database-level `statement_timeout = '1ms'`, set as the restoring role, made the schema comparison's own queries fail.

**Root cause.** `ALTER DATABASE ... SET` and `ALTER ROLE ... SET` take effect for every new session there. A restored file runs as the role that owns the scratch database, and that owner may set them. A timeout fails safe. A `search_path` naming a schema the file created could make a check resolve a function the file wrote instead of the catalog's, and so lie to it.

**Fix.** After loading, the superuser clears every such setting (a catalog edit that runs no code), then counts what is left (see 243). The comparison connections also pin `search_path=pg_catalog`, and the census connection pins `public`.

**Lesson.** Anything that runs inside what it is checking inherits what it is checking. Remove the checked thing's influence over the checker before the check runs.

## 245. `make up`'s documented script form never worked: its check drained the piped password before setup could read it

**Symptom.** Found by the backup release gate, the first test to run `make up` itself. `echo "$PASSWORD" | make up SETUP_FLAGS="--non-interactive --admin-email ... --password-stdin"` wrote `.env` and then failed: `the administrator was not created: no secret on standard input`, exit 2. docs/10 and the Makefile both documented this form.

**Root cause.** `make up` first runs `docker compose run -T ... setup --check` to ask whether `.env` is complete. `-T` turns off the terminal, not standard input: compose still forwards its stdin into the container, and drains it whether or not the container reads a byte. The check read nothing and consumed the password, so `make setup`, run next, found standard input at end of file. Phase 83's handoff recorded "`make up` itself is not run by a test" as a known gap; this is what the gap held.

**Fix.** Both `--check` runs get `</dev/null`. `TestBackupReleaseGate_ComposeFromBackupToACleanMachine` runs the piped form of `make up` twice.

**Lesson.** A command that runs other commands passes its standard input to the first one that asks, and a container runtime asks on its child's behalf. Give every step that must not read input nothing to read. A documented form no test runs is a form nobody has run.

## 246. A sanitizer written for names from an untrusted file cut host paths to 64 characters

**Symptom.** The backup gate's `make restore` said the replaced database "was backed up first, to /tmp/TestBackupReleaseGate_...": the path, the one thing needed to undo the restore, cut off.

**Root cause.** `printable` replaces control characters and caps text at 64 characters, which is right for a table name read out of an archive someone else wrote. It was also used for paths built from the backup directory, which are this deployment's own and often longer than 64 characters.

**Fix.** `printablePath` replaces the same characters with a 4096 character limit, and every path in a message uses it.

**Lesson.** A length cap is a decision about one kind of text. Applied to text whose whole value is being complete, it removes the reason to print it.

## 247. An ownership change cleared a setgid bit the task asked for, and the run reported success

**Symptom.** Found while writing check-mode predictions for `pkg/remotefile`, and reproduced on a real shell: a task asking a regular file that already carried mode 2755 to change group AND stay 2755 left 0755 behind and reported success. The diff's after half read back 0755, so nothing lied outright, but the task claimed to have converged a path it had just moved away from what was asked.

**Root cause.** `remotefile.Apply` sends chown/chgrp first and chmod last, and decided whether to chmod by comparing the requested mode with the mode found BEFORE anything ran. Linux clears setuid and setgid on anything but a directory when its owner or group changes. The requested mode compared equal to the pre-change mode, so no chmod was sent, and the kernel's clearing stood.

**Fix.** When the ownership changed and the requested mode carries setuid or setgid, the chmod is sent whatever the comparison said. The run has already changed something, so the extra chmod never turns a converged run into a changed one. `TestPrediction_MatchesWhatApplyDoes` pins it against a real file.

**Lesson.** A comparison made before a multi-step change is a comparison with a state the first step may already have moved. When one step has a side effect on what a later step compares, compare against the state after the side effect, or send the later step unconditionally.

## 248. GNU chmod keeps a directory's setuid and setgid bits for a four-digit mode, so "0755" never cleared them

**Symptom.** Found by the same work as 247: a task asking a directory carrying 2755 to be 0755 sent `chmod 0755`, exited 0, read back 2755, reported changed, and would do the same on every run forever.

**Root cause.** GNU coreutils chmod PRESERVES a directory's setuid and setgid bits when given a numeric mode of four digits or fewer. Its documentation names the five-digit form (`00755`) as the way to say exactly these bits. BusyBox, toybox and the BSD chmod read the whole string as one octal number, so the five-digit form means the same thing there.

**Fix.** `remotefile.Apply` sends the requested mode as five digits (`chmodArgument`). Every method built on Apply (file.directory, file.permissions, file.copy, file.touch, file.attributes, file.line.*, file.block.*, fs.*) is covered by the one change, and their suites pass unchanged.

**Lesson.** A command's numeric argument can carry a policy the number does not show. When a tool's documentation says "to clear X, write it this way", a convergence check built on the other way converges on nothing.

## 249. Validation refused a check of a simulate-locked device that the engine was built to allow

**Symptom.** Found by the check-mode Release Gate (`TestCLI_CheckModeChangesNothing`), after the engine's own unit test had passed: `pleiades run --mode check` against a simulate-locked device stopped at "validation failed, not executing", naming the lifecycle rule.

**Root cause.** The lifecycle exception for check mode was added where the executor admits devices (`runNode`) and not where `pleiades run` first validates the plan (`validate.LifecycleRule`), which refused every non-active target regardless of mode. The engine test built its Executor directly and never called Validate, so it could not see the second gate.

**Fix.** The exception has one definition, `engine.LifecycleAdmitsIn(mode, device)`, called by both the executor and `validate.LifecycleRule`, which now reads `WorldView.Mode` (zero value strict). `TestLifecycleRule_CheckModeAdmitsOnlySimulateLocked` covers the rule in both modes.

**Lesson.** A check enforced at plan time and again at run time is two gates, and an exception added to one is refused by the other. Define the exception once and have both gates ask it, and test the path that goes through both.

## 250. The plan said nothing ever sets simulate-locked; two sync plugins set it on every device they discover

**Symptom.** IMPLEMENTATION.md (Part XI and Phase 46) states `pkg/inventory`'s `StateSimulateLocked` is a reserved state "nothing in the codebase ever sets or reads", and Phase 46 offered deleting it as one of two options.

**Root cause.** The claim was true when written and went stale: `internal/inventory/plugins/aws` and `internal/inventory/plugins/catalystcenter` default every newly discovered device to simulate-locked, exactly as PLAN.md Section 9 intends ("newly onboarded devices default to simulate-locked until admin promotes"). Deleting the state on the plan's word would have silently changed what those plugins' devices are allowed to do.

**Fix.** Phase 46's first slice uses the state: a check reaches a simulate-locked device, and a real run naming one is refused at validation. The plan text is corrected beside the Phase 46 entry.

**Lesson.** A plan's "nothing uses X" is a claim about the code on the day it was written. Verify it against the code before acting on it, above all before deleting X.

## 251. The environment allowlist was called the whole defense, but a same-user child reads its parent's environment from /proc

**Symptom.** `internal/loader` (capture.go, doc.go), `pkg/external`'s package doc and docs/11 all said that reducing an external program's environment to a short allowlist kept the master key and the broker's credentials from reaching it. A probe disproved it: a parent with `PLEIADES_MASTER_KEY` set, which then removed the variable from its own copy, started a child with an empty environment, and the child printed the key by reading `/proc/$PPID/environ`.

**Root cause.** `/proc/<pid>/environ` exposes a process's starting environment block, and any process of the same user that passes the kernel's read-level ptrace check can open it. Yama's `ptrace_scope=1` (this host's setting) restricts attaching, not reading. `os.Unsetenv` changes only the Go runtime's copy, never that block. The allowlist decides what a child inherits; it was never isolation, and the comment claiming otherwise was written without trying to cross the boundary.

**Fix.** Built the same day, as Phase 45's confinement decision (`internal/loader/confine*.go`). Before the first program starts, Load marks the process non-dumpable (`prctl(PR_SET_DUMPABLE, 0)`, which the probe showed turns the read into "Permission denied"), and every run is confined with Landlock to its own directory, system files, the known_hosts file, a private TMPDIR and operator grants (never one reaching a credential store or containing the home directory). Loading is refused where Landlock is absent. `TestConfinement_AProgramReachesOnlyWhatItWasHanded` has a real program try the master key, `credentials.yaml`, an SSH key, `/proc/<parent>/environ` and `/proc/<parent>/mem`; each is denied, and the unconfined control reaches them. Each guard was mutation-checked.

**Lesson.** An environment allowlist controls what a child is handed, not what it can reach. Before calling anything a boundary, try to cross it as the thing it is meant to stop.

## 252. A runbook-level `check_mode: true` is accepted and ignored, so the runbook runs for real

**Symptom.** Found while listing edge cases for Phase 46's runbook-mode decision, and confirmed with a probe test against `parseWorkflowYAML`: a runbook with top-level `check_mode: true` parses with no error and then executes normally. The same key on a task or a block is refused, but with a message about module-as-key syntax that never says the key is unsupported.

**Root cause.** The runbook's top-level map is decoded by yaml.v3's non-strict `Decode`, which drops keys it does not know. Only task maps pass through `normalizeTaskNode`'s reserved-key check. So an Ansible author's "change nothing" survives parsing and means nothing, and the plan's description of the built behavior ("no key") was wrong for the runbook level.

**Fix.** Built the same day. `engine.RunbookKeys` lists the accepted top-level keys, and `checkRunbookKeys` refuses any other one on the YAML and JSON paths, naming it and suggesting the nearest (`chek_mode` gets "did you mean check_mode?"). A test keeps the list equal to `WorkflowDef`'s struct tags, and `tools/gendocs` checks the documented keys against it. `check_mode` itself is now honored at runbook, block and task level (`engine.CheckModeFlag`), with `false` and templates refused. All 14 runbook files in the repository, and every package that builds runbooks, still pass. `TestRunbookKeys_UnknownTopLevelKeyRefused` is the regression test, mutation-checked.

**Lesson.** A permissive decoder turns every unsupported safety keyword into a silent no-op. A key that narrows what a run may do must be honored or refused, never dropped.

## 253. Check mode admits simulate-locked devices for any method, including a third party's unproven Check

**Symptom.** Found by analysis, not observed. `engine.LifecycleAdmitsIn` admits a simulate-locked device whenever the run is a check, and an external program may declare `SupportsCheck`. Its Check then runs with the device's credential against a device nobody has approved for changes. The engine refuses a check result carrying an `inverse` stat, but it cannot see a write that records none.

**Root cause.** Two features built on the same branch, each sound alone. Admitting simulate-locked devices to a check assumes every Check is read-only, which is proven for the nine built-in methods (each tested against its real run) and for nothing else. External Collections made Check third-party code.

**Fix.** Built the same day. `collection.Descriptor.Provider` names the external program behind a method, set only by the loader when it registers the method (a description carries a name and a manifest, and extra fields claiming otherwise are ignored, as `TestLoad_TheLoaderSetsTheProvider` proves). The engine's check step (`checkAction`) reports an external method's check against a simulate-locked device as unchecked, naming the lock and the program, and never runs it. The CLI and the Runner share that step. `TestCLI_AnExternalCheckNeverReachesASimulateLockedDevice` uses a real program whose Check deliberately writes, against a real sshd: nothing lands on the locked device, the write does land on an active device (the control), and a built-in check still reaches the locked device. The guard and the loader's assignment were each mutation-checked.

**Lesson.** When two features meet, re-check what each assumes about the other's inputs. "A Check never writes" was proven per method, so it holds only for the methods someone proved it for.

## 254. A Landlock restriction applied from a goroutine could land on the main thread, putting The Pleiades inside the program's own domain

**Symptom.** While building confinement for external Collections, `TestConfinement_AProgramReachesOnlyWhatItWasHanded` showed a confined program able to signal its parent, although a direct probe showed the same ruleset refusing a signal to PID 1 and a read of `/etc/hostname`. It passed a second time. A loop of forty starts then passed with the fix removed, so the defect was intermittent, which is how it would have shipped.

**Root cause.** Landlock restricts one thread, and a child inherits the restriction of the thread that started it. The loader restricts a goroutine that has called `runtime.LockOSThread` and never unlocks it, so the thread dies with the goroutine. The scheduler can run that goroutine on the process's main thread, which cannot exit, so Go parks it for ever instead. The main thread is also the thread group leader, the task a signal to the process's ID is checked against. With the leader inside the program's own Landlock domain, signal scoping allowed the program to signal The Pleiades, and a SIGKILL would have killed the whole process.

**Fix.** `confineAndStart` checks `gettid() == getpid()`. On the main thread it keeps that thread locked, so the goroutine it starts cannot be scheduled there, and hands the work to that goroutine. `TestConfinement_NeverOnTheMainThread` is deterministic: it re-runs the test binary with its main goroutine pinned to the main thread by an `init` (`runtime.LockOSThread`), starts a confined program from there, and asserts the signal is refused. With the check removed it fails every time.

**Lesson.** When a kernel restriction is per thread and Go picks the thread, check which thread you got, above all the main thread, which never exits. An intermittent security test is a real defect showing on some runs, so pin the condition down until the test fails deterministically without the fix.

## 255. A YAML unmarshal hook written against gopkg.in/yaml.v3 was never called, so check_mode: false decoded silently

**Symptom.** `engine.CheckModeFlag.UnmarshalYAML`, written to refuse `check_mode: false`, compiled cleanly, and `TestCheckModeKey_Spellings` showed `false`, `no` and `off` all accepted without error, and `yes` refused with a generic yaml.v3 "cannot unmarshal" message instead of the hook's own.

**Root cause.** The file imported `gopkg.in/yaml.v3`, while the engine decodes with `go.yaml.in/yaml/v3`, the same library under its newer module path. The two are separate modules with separate `yaml.Node` types and separate `Unmarshaler` interfaces, so a method taking one module's `*yaml.Node` does not implement the other's interface, and the decoder fell back to decoding a bool into the flag's underlying type. `gopkg.in/yaml.v3` was already in `go.mod` as an indirect dependency, so the wrong import resolved with no warning.

**Fix.** The import is `go.yaml.in/yaml/v3`. `internal/archtest`'s `TestOneYAMLModule` now fails for any package, or any package's tests, importing `gopkg.in/yaml.v3` or `gopkg.in/yaml.v2`, and it was mutation-checked by putting the wrong import back.

**Lesson.** A custom unmarshal hook is only real if the decoder calls it, and a decoder calls only the interface of its own module. When a library has moved module paths, check which one the decoder uses before writing a hook, and test that the hook's own refusal fires rather than only that the good case decodes.

## 256. A file-writing tool decoded the escapes in its input, and real bidirectional overrides landed in the source

**Symptom.** While writing `internal/termsafe`, the package that escapes terminal-controlling characters, its own tests failed: the expected strings, written in the source as backslash escapes for U+202E, U+2066 and U+2069 inside raw string literals, compared as the real characters. A scan found real U+202E, U+2066, U+2069 and U+009B characters in `termsafe.go`'s package comment and in `termsafe_test.go`: exactly the invisible "Trojan Source" characters the package exists to catch, now in source code where a reviewer's editor would render them as nothing, or reorder the text around them.

**Root cause.** The agent's file-writing and shell tools decode JSON-style escapes (a backslash, "u" and four hex digits) in their input before writing or running it. Escapes meant to reach the file as text arrived as the characters themselves. The shell tool refused one command outright for containing such characters, which is how the mechanism was confirmed. Nothing in the repository checked source files for them.

**Fix.** The characters were replaced with escape text, written through code points computed at run time so no escape passed through the tool. A scan of all 2,414 tracked and untracked text files found no others. `internal/archtest`'s `TestNoInvisibleControlCharactersInGoSource` now fails for any Go file holding a control character (other than tab and newline) or a bidirectional override, and was proven by planting one.

**Lesson.** When a tool writes files for you, an escape sequence in what you hand it may not survive as text. Write byte escapes (`\xNN`) or build the characters in code, and keep a mechanical check for invisible characters in source, since a reader's eyes cannot find them.

## 257. An external program's own text reached the terminal raw, including at the approval prompt

**Symptom.** Found by Phase 45's hardening audit. Three paths printed a third party's text unescaped: `pleiades collection approve` printed the program's description (method names, summary, reversibility notes) before anything validated it; a method's own error message became the task's "FAILED:" line; and `pleiades run --verbose` printed stat values. A program could put an escape sequence, a carriage return, a newline or a bidirectional override in that text and redraw what the operator sees, for example a fake method list, or a fake "approved" line under the prompt that decides whether the program may run. Error messages that quote a program's stderr were already safe (`%q`).

**Root cause.** The loader validated a description's name and structure, and treated its text as data. Nothing distinguished text shown to a person from text a program controls, and a terminal acts on some characters instead of showing them.

**Fix.** `internal/termsafe` defines the characters a terminal acts on and escapes them visibly (never drops them). The loader refuses any description string holding one (every string in the manifest, found by walking it, so later fields are covered), escapes a program's own error onto one line, and refuses a stat name holding a newline, tab or control character. The approval list refuses such an account name. The CLI escapes node errors, reasons, stat values, metadata and everything the approve prompt shows. Tests: `TestHardening_*` in `internal/loader` and `TestCLI_CollectionApproveEscapesTheProgramsText` through the real binary, each mutation-checked, plus `FuzzEscape`, `FuzzDecodeResponse` and `FuzzParseApprovals`.

**Lesson.** Text from a program you did not write is input, even when it is only going to be printed. Validate it where it can be refused, escape it where it must be shown, and test the output a person reads, not only the data structure behind it.


## 258. A new NATS subject and consumer shipped without their mesh identity grants

**Symptom.** Found while building Phase 46's Walk-tier check mode, after its real-NATS gate had passed. Checks go to their own subject (`pleiades.jobs.check.<device>`) through their own durable (`runner-check`), but `internal/meshid`'s `ControllerGrant` and `FleetRunnerGrant` listed only the dispatch subject and the `runner-agent` consumer. Under a minted identity, the Controller's publish of a check is denied with no reply and surfaces as a context deadline, so every checked device is recorded as failed; the Runner is denied creating its check consumer at startup and, because either loop failing is fatal, exits. No test failed, because the gate ran on an unauthenticated broker and the grant tables only pin the entries somebody thought to list.

**Root cause.** A grant is a deny-by-default list kept in a different package from the code that decides which subjects exist. Adding a subject in `internal/topology` and a consumer in `cmd/runner` changes what each process does on the wire, and nothing ties that change back to the grant. The same shape as #207: the grant, the grant test and the feature are each written from one person's picture of the traffic.

**Fix.** Both grants carry the check subject, the check consumer's create, info, pull and ack operations, and the check dead-letter subject; the grant tables pin them; and `TestReleaseGate_TheRealControlPlaneRunsUnderAMintedIdentity` now has the minted Runner create, probe and pull the check consumer, the minted Controller publish a check, and a widened check consumer refused, against a real operator-mode broker. Removing the Controller's entry fails that gate with the deadline it would produce in production.

**Lesson.** Any new subject, stream or durable is also a grant change, on every process that touches it. Extend the real-broker enforcement gate with the new traffic in the same change; a grant table written beside the grant proves only that the two agree.

## 259. A package-level struct literal froze a test seam at init, and a test dialed a real WinRM endpoint

**Symptom.** Found while giving `svc.windows.*` their checks. The five operations had been built inline in each method; hoisting them to package-level values (`var startOp = serviceOp{..., run: startFunc}`) made the unit tests that swap `startFunc` for a fake hang for sixty seconds each and fail with a WinRM dial timeout. Nothing about the failure named the refactor: the tests had not changed, the code compiled, and the swapped seam was plainly assigned before the call.

**Root cause.** A package-level `var` initialized from a composite literal copies the function values it names when the package initializes. The literal held the real `startFunc` from before any test ran, so a test assigning a fake to `startFunc` changed a variable nothing read any more. The method called the real WinRM client, which dialed the fixture's address and waited out its timeout.

**Fix.** Each operation is a function returning the literal (`func startOp() serviceOp`), called where it is used, so the seam is read at call time, after a test has swapped it. The tests passed again in under a second.

**Lesson.** A test seam held in a package variable must be read when it is used, never captured by another package-level value. Hoisting a literal that names a seam out of a function body changes when the seam is read; check every function variable a hoisted literal names before hoisting it, and treat a test that suddenly hangs on a real dial as a seam read too early.

## 260. A synced project's playbook escaped its tree through a symlinked directory

**Symptom.** Found 2026-09-19 while correcting a stale test citation in the plan. `internal/project`'s playbook source joined a template's `<project id>/<path>` onto the checkout, compared the cleaned path against the checkout root, and refused a final component that was not an ordinary file. A checkout keeps a repository's symlinks, so a committed `lib -> /` passed the lexical check for `lib/etc/...`, the final `Lstat` followed the symlinked directory and found an ordinary file, and the Controller read a file outside the project and dispatched it as a playbook. `TestPlaybookSource_RefusesAPathThroughASymlinkedDirectory` reproduced it with a real symlink.

**Root cause.** A containment check made on the path's text, then a read that resolves the path through the filesystem. The two answer different questions whenever a component is a symlink, and a repository's committers are not necessarily the Controller's administrators, so the symlink is attacker-reachable content.

**Fix.** The file is opened through an `os.Root` over the checkout, which resolves every component inside the root, refuses any that leaves it (an absolute symlink included), and opens the file in the same `openat` walk, so nothing can be swapped between a check and the read. A relative symlink that stays inside the tree still works, as a repository's own layout expects.

**Lesson.** Never check a path lexically and then hand the same string to the filesystem. Where the tree can hold content somebody else wrote, open through `os.Root` (or `openat2` with `RESOLVE_BENEATH`), so the check and the resolution are one operation.

## 261. A device's state or tags update answered 200 and stored nothing

**Symptom.** Found 2026-09-19 while documenting how a device leaves quarantine, and reproduced through the real handler, the real ent repository and the real factory: `PATCH /inventory/devices/{name}` with `{"state": "active"}` answered 200, returned the old state, and changed nothing in the database. Tags were dropped the same way. It was the only way to promote a device, so no device could be promoted through the API.

**Root cause.** The handler rebuilt the item with the new state already set, at the version it was loaded at. Both repositories' `Save` treat an item whose version never moved as having nothing to write, a sound optimization that property changes satisfy by bumping the version through `AddInfo`. Nothing bumped it for state or tags, and the handler's own tests used a stub repository that recorded any `Save` call, so the no-op was invisible.

**Fix.** `record.Base` gained `ChangeState` and `ChangeTags`, which bump the version and record a revision (field `state` or `tags`, with the old and new values), as `AddInfo` does for a property; setting what a device already has records nothing. The handler rebuilds the item as loaded and applies the change through them. `TestDeviceUpdate_AStateOrTagsOnlyChangeIsStored` runs the real handler against a real ent repository and reads the device back.

**Lesson.** A write path that ends in an optimization like "unchanged version means nothing to save" must be tested against the real repository, not a stub that records the call. The stub proved `Save` was called; only the database could say whether anything was stored.

## 262. A re-sync reverted an operator's promotion, with no revision saying so

**Symptom.** Found 2026-09-19 alongside 261. A read-only source lands a device `simulate-locked`; once an operator promoted it to `active`, the next sync that brought any upstream property change put it back to `simulate-locked`, and the audit trail showed only the property change.

**Root cause.** `syncplugin.updateDevice` rebuilt an existing device with the classification's `State`, which is the state a source gives a device it adds, not a statement about a device that already exists. The rebuilt state rode along with the property revisions, so no revision recorded it.

**Fix.** An update keeps the stored state; only a newly added device takes the classification's. `TestReconcile_KeepsAnOperatorsPromotion` lands, promotes, changes a property upstream and re-syncs.

**Lesson.** A field that means "where this starts" must not be re-applied on update. When one write path carries both an automated source's view and an operator's decision, name which fields belong to which, and let the automated path touch only its own.

## 263. A read-only sync hid the one list an operator previewing it needed

**Symptom.** Found 2026-09-19 while documenting quarantine. `pleiades inventory sync --read-only` printed its summary and returned whenever anything would be added or updated, so the "devices needing review" list, with each quarantined record's reason, printed only when nothing would change. A quarantined record is never stored, so that list is the only place its reason appears.

**Root cause.** An early `return` after the read-only summary line, written when the two summaries were the only difference between the modes.

**Fix.** Both modes print their summary and then the same review list. `TestRunInventorySync_ReadOnlyStillListsWhatNeedsReview` syncs a quarantinable NX-OS device beside an addable IOS-XE one.

**Lesson.** A preview must show at least what the real run would; an early return in a report function is worth reading twice for what it skips.

## 264. Template launches minted unordered job ids under a list that orders by id

**Symptom.** Found 2026-09-19 while documenting dispatch. `GET /jobs` lists newest first by ordering on the job id and pages with it as a keyset cursor, and its description promises UUIDv7 ids for exactly that, but `LaunchTemplate` minted `uuid.New()`, a random v4, so a template launch landed anywhere in the list and paging order was arbitrary.

**Root cause.** The job schema defaults to a v7, but a launch mints its id itself, before saving, because the id is also the `job.requested` event's idempotency key, and that mint predated the ordering requirement.

**Fix.** `api.newJobID` mints a v7 (falling back to a v4 only on an entropy failure, as the schema and device ids do), and `TestLaunchTemplate_JobIDsAreTimeOrdered` checks each launch's id is version 7 and sorts after the one before.

**Lesson.** When a column's default carries a property something else depends on, every code path that sets the column explicitly must keep it. Grep for the explicit setters, not just the default.

## 265. One unresolvable entry failed a whole static_yaml sync

**Symptom.** Found 2026-09-19 while documenting quarantine. `static_yaml` resolved every entry's `classify` path inside `Discover`, and returned the first resolution error, so one entry with a malformed or unmatched path made `pleiades inventory sync` fail for every host in the file. The plugin contract requires a record it cannot place to be quarantined with a reason, never returned as an error, and the plugin's own `Classify` comment said such an entry is quarantined.

**Root cause.** The type is resolved early, in `Discover`, because the file is the classification; the error path was written as a validation of the whole file rather than as the per-record outcome the contract names.

**Fix.** `Discover` records an entry it cannot resolve with no type and keeps the reason by host name; `Classify` quarantines it with that reason. `TestSync_AnUnresolvableClassifyPathIsQuarantinedNotFatal` covers a malformed path and a well-formed one matching no rule, beside a typed host that still syncs.

**Lesson.** When a contract names the per-record outcome for bad input, check every stage that touches a record, not only the stage the contract names: an earlier stage can turn the same bad input into a whole-run failure.

## 266. SQLite migrations never really turned foreign keys off

**Symptom.** Found 2026-09-19 while planning Phase 21's C1 seam, and confirmed by a probe. Every SQLite migration that rebuilds a table opens with `PRAGMA foreign_keys = off`, and SQLite has no `ALTER TABLE ADD CONSTRAINT`, so a rebuild is how any foreign-key change is made there. With enforcement still on, the rebuild's `DROP TABLE` performs an implicit `DELETE FROM`, which fires the children's `ON DELETE` actions. Upgrading a populated database through `0022_add_projects.sql` (rebuilds `templates`) therefore deleted every authored survey question and saved launch configuration, silently, and failed outright for a deployment holding any schedule, since `schedules` points at `templates` with NO ACTION. `0030_add_job_external_checks.sql` (rebuilds `jobs`) failed for any deployment holding a `job_task`, so the Controller would not start after the upgrade.

**Root cause.** `applyOne` ran each migration script inside `db.BeginTx`. That pragma is a documented no-op inside a transaction, and SQLite reports no error when it declines to honor it: the statement succeeds and the setting does not change. The pragma is also per connection, so setting it on the pool would have landed on whichever connection answered. No test had ever migrated a database that held rows, which is the one condition under which any of this is visible.

**Fix.** `applyOne` now takes one connection out of the pool (`db.Conn`), sets `PRAGMA foreign_keys = off` on it BEFORE opening the transaction, and **reads the setting back**, failing the migration if it did not take. The transaction runs on that same connection. Before committing, `PRAGMA foreign_key_check` runs inside the transaction and any reported row refuses the migration, so suspending enforcement cannot leave a row pointing at nothing. Enforcement is restored on the way out, and if restoring fails the connection is discarded (`driver.ErrBadConn`) rather than returned to a pool where it would serve application queries. Postgres keeps its existing path: it alters a constraint in place inside transactional DDL, so it needs neither half. `internal/ent/migrate/upgrade_internal_test.go` builds a database at the version before each migration, puts real rows in it, and then applies it: authored children survive 0022, a scheduled template and a job with tasks both upgrade, enforcement reads on afterward and refuses a dangling child, and a crafted migration that orphans a row is refused and left unrecorded. With the fix disabled, the first three fail exactly as described above.

**Lesson.** A pragma written inside a migration file is not a pragma the migration runs under. Anything whose scope is the connection or the transaction has to be set by the code that owns the connection, and read back, because the failure mode of a declined session setting is silence. And a migration test that only ever migrates an empty database proves the DDL parses, not that an upgrade is safe.

## 267. A project that had ever synced could not be deleted

**Symptom.** Found 2026-09-19 while planning Phase 21's C1 seam. `DELETE /projects/{id}` answered an opaque 500 for any project with sync history, which is every project anybody had used. A project that had never synced deleted fine, so the failure looked intermittent and tracked nothing a reader would connect to it.

**Root cause.** `sync_runs` referenced `projects` with ON DELETE NO ACTION, because the SyncRun schema declared the edge with no cascade annotation, while `project.entStore.Delete` deleted only the project row and mapped no constraint error. The SyncRun schema's own edge comment said the opposite of what the schema did: "deleting the project takes its history with it, which is the same lifetime the checkout has." Nothing tested a delete after a sync.

**Fix.** The cascade is declared on the owning side (`Project.sync_runs`, `entsql.OnDelete(entsql.Cascade)`), regenerated, and carried into both dialects' migrations (`sqlite/0031`, `postgres/0028`). `TestDelete_TakesTheSyncHistoryWithIt` syncs a project, deletes it, and asserts both the project and its history are gone; `TestApply_TheSyncRunRebuildKeepsTheHistory` proves the same cascade lands on an upgraded database that already held history.

**Lesson.** A comment that states a lifetime is a claim about a foreign key, and it is worth checking against the generated schema rather than trusting: the schema and the sentence beside it disagreed here for three phases. Any entity with children needs one test that deletes a parent which HAS children, since the childless case passes either way.

## 268. Writing a schedule needed no permission to launch what it launches

**Symptom.** Found 2026-09-19 while planning Phase 21's C1 seam, and reasoned from the code rather than observed in the wild. `POST /schedules` required `schedule:write` and nothing else. The store checked only that the schedule's organization matched its template's. Firing went through `Dispatcher.LaunchScheduled` with `MayRunForReal(true)`. So a token holding `schedule:write` without `runbook:execute` could arrange for any template in any organization to run for real, repeatedly, unattended, and the run would be attributed to the scheduler rather than to whoever arranged it.

**Root cause.** `auth/scopes.go` presents `schedule:write` as a separately grantable privilege, on the good argument that authoring what a template does, running it once, and deciding when it runs are three different decisions. Its own comment even says this is "the larger grant" than executing once. What was missing is the consequence: a larger grant must not be a way around a smaller one. Nothing on the write path asked whether the caller could launch the thing they were scheduling, because before Phase 21's seam there was nothing in the code that knew what "the thing" was in general terms, only a template id.

**Fix.** Each launchable type declares the scope needed to launch it (`launchable.Descriptor.LaunchScope`: `runbook:execute` for a job template, `project:write` for a project). One predicate, `launchable.Reach.Admits`, is used by the schedule store at the write and by the UI picker when it decides what to offer, so a person is never shown a choice that would be refused on submit. `TestSchedules_WritingOneNeedsPermissionToLaunchWhatItLaunches` and `TestSchedules_EachTypeNeedsItsOwnScope` prove it over real HTTP against a real store; with the scope check disabled both get a 201 instead of a 403.

**Lesson.** When a scope is split out because it is a different decision, write down which way the implication runs and enforce it. "Deciding that something runs forever is a bigger decision than deciding it runs once" is only true if the bigger one also requires the smaller one; otherwise it is a smaller one with extra reach. The general form: any mechanism that causes work to happen later has to check the permission to do that work now.

## 269. Editing a schedule silently dropped its saved configuration

**Symptom.** Found 2026-09-19 while reading the schedules view for the launchable rebind. The Schedules edit form renders no control for the saved launch configuration, so a submission never carries one; `Bind` therefore left `SavedConfigID` zero, and the store reads zero as "run the target's own defaults" and clears the column. Renaming a schedule, or changing its recurrence, silently discarded the overrides every one of its runs had been using. Nothing said so, and the next run simply did something different.

**Root cause.** A field the form does not render is still a field the binder writes. The store's absent-means-clear rule is right for an API caller sending an explicit null and wrong for a form that never had the value to send.

**Fix.** The view's writer carries `SavedConfigID` forward from the stored schedule when the target has not changed. Changing what a schedule launches while it holds a configuration is refused with a message naming the control, because that is the one case where carrying it forward would be wrong: the overrides belong to the previous target. `TestSchedulesForm_*` cover the picker, and the carry-forward is asserted in the view's own suite.

**Lesson.** For every field a form does not render, decide explicitly whether the binder should carry it forward or clear it, and write the decision down beside the binder. "Absent" from a form and "absent" from an API body mean different things, and a store that cannot tell them apart will do the wrong one silently.

## 270. A refusal gave advice that does not work

**Symptom.** Found 2026-09-19. Deleting a template a schedule uses answers 409 with "delete or disable the schedule first". Disabling a schedule does not release its reference, so following the instruction produced the same 409. The same advice was in docs/09.

**Root cause.** The message was written from the intent (a disabled schedule will not fire) rather than from the mechanism (the foreign key does not care whether it is enabled).

**Fix.** Both now say to delete the schedule or point it at something else, which is what actually unblocks the delete.

**Lesson.** An error message is a claim about the system and deserves the same check as a doc sentence: follow your own instruction once before shipping it. Advice that does not work costs more than no advice, because it spends somebody's trust as well as their time.

## 271. A fetch ignored the project's own URL, so repointing one changed nothing

**Symptom.** Found 2026-09-20 while adding a source allowlist. `GitSyncer.fetch` passed go-git no remote, so the fetch and the pull used the URL stored in the checkout's own `.git/config`, which is the one the FIRST clone used. Editing a project's address therefore changed nothing about what was fetched, for as long as the checkout existed, and the sync reported success while serving the old repository. The address the platform validated was also not the address it dialed, which is what made this a security problem as well as a correctness one: any check on `scm_url` could be bypassed by a checkout that already existed.

**Root cause.** `FetchOptions` and `PullOptions` both carry a `RemoteURL`, and neither was set. With it absent go-git reads the remote out of the repository's config, which is the right default for a git client and the wrong one for a platform that owns the checkout and holds the address somewhere else.

**Fix.** Two parts, because one alone is not enough. `RemoteURL: p.SCMURL` on both options states the address rather than letting it be inferred. And `open` now compares the checkout's first configured remote URL with the project's, replacing the checkout outright when they differ: an unrelated history cannot be fast-forwarded into, so a repointed project would otherwise fail with "non-fast-forward update" forever, with nothing an operator could clear from the interface. The replacement clones into a sibling directory and swaps it in only once the clone has succeeded, so a wrong new address leaves the last good checkout serving (`TestGitSyncer_AFailedRepointKeepsTheLastGoodCheckout`); removing first would take every template on that project down until somebody typed a working address. `TestGitSyncer_FetchesFromTheProjectsCurrentURL` fails before the fix.

**Lesson.** When a platform owns a working copy of something and also stores the address it came from, the two can disagree, and the library's default is usually to believe the working copy. Pass the value you hold rather than trusting what is on disk to still match it, and when they do disagree, decide deliberately which one wins.

## 272. Nothing constrained where a project's source came from

**Symptom.** Found 2026-09-19 while building Phase 21's launchable seam, reported to the user and fixed on 2026-09-20. The only check on a project's URL anywhere was that a git project's URL is not empty. go-git's default registry serves http, https, ssh, git and file, a string with no scheme at all is a LOCAL PATH absolutized against the Controller's working directory, and `user@host:path` is ssh. So anybody holding `project:write` could point the Controller at a repository on its own disk or anywhere on its network, and the content of a clone is code this platform then runs against managed devices.

**Root cause.** The URL was treated as an address to be resolved by the library rather than as operator-supplied input to be judged. Nothing in the write path or the sync path asked what protocol it named.

**Fix.** `internal/project/source.go`: an allowlist of the protocols go-git will dial, defaulting to https and ssh, with two separate opt-ins (insecure transports, local paths) because they are two different threats. The protocol is decided by asking go-git itself (`transport.NewEndpoint`) rather than by a second parser that could disagree with the code that dials. Enforced at the store, which is the one place the column is written, and again at the sync, because a row can predate the rule. A password in the URL is refused at the write only, since an existing row carrying one would otherwise become permanently unsyncable with no migration.

**Lesson, and the honest scope.** Three things this does NOT do, all worth stating because each is easy to assume: it is not a host allowlist (anything shaped like `host:path` is a valid ssh address, so an allowed protocol still reaches any resolvable host), redirects are followed without being re-checked, and refusing local paths is defense in depth rather than a patch for something exploitable in the shipped image, since go-git's file transport shells out to a git binary the image does not carry. The general rule: when a library accepts a string and decides for itself what kind of address it is, the set of things it will accept is the real attack surface, and it is always wider than the examples in the documentation.

## 273. A round trip was not a round trip, and the test wrote the workaround into its own expectation

**Symptom.** Found 2026-09-20 while adding two fields to `internal/credential.Credential` for Phase 78d. `TestFlattenUnflattenRoundTrip`'s doc comment claimed Flatten and Unflatten are "genuine inverses for every field a Credential can carry", and `TestUnflatten`'s table wrote `PrivateKeyPEM: []byte{}` into expectations for credentials that have no private key at all. Adding `CertificatePEM []byte` broke three subtests whose printed got and want were character for character identical, which is what sent somebody looking.

**Root cause.** `Unflatten` built its byte fields with `[]byte(secrets[key])`, and `[]byte("")` in Go is a non-nil slice of length zero. Every consumer in this codebase asks `len(...) != 0`, so nothing behaved incorrectly, but `reflect.DeepEqual` distinguishes nil from empty, so `Unflatten(Flatten(Credential{}))` was never DeepEqual to `Credential{}`. The round-trip claim was false and had been since the function was written.

The part worth recording is not the nil-versus-empty detail, which is ordinary Go. It is that the test had already met this and had absorbed it. Writing `[]byte{}` into an expectation for a field the input does not set makes the test pass while quietly restating the defect as the specification. Nothing was left to notice it, and the cost only arrived when a second byte field needed the same workaround, at which point the choice was to copy it twice more or to fix the cause.

**Fix.** `bytesOrNil` returns nil for an empty string, so an absent key produces the field's zero value exactly as the doc comment says. The three expectations that carried `[]byte{}` were reverted to the zero value, which is what they always meant. The round-trip test now covers all six fields, so the claim it makes is the claim it checks.

**Lesson.** When a test's expected value contains something the caller would never write by hand, ask why it is there before adding another one beside it. An expectation shaped around the implementation records a defect as intended behaviour, and the record is worse than no test, because the next person reads it as a decision somebody made. The tell is a diff whose `got` and `want` print identically: that is never a value problem, it is always a type or identity problem, and usually one the test was already hiding.

## 274. A field was added to a type, to its wire format and to a CLI flag, but not to the store, so the command reported success and wrote nothing

**Symptom.** Found 2026-09-20 by an adversarial review of Phase 78d, not by any test. `pleiades add-credential win01 --certificate client.pem --key client.key` printed `stored credential for device "win01"` and stored no certificate. The failure surfaced much later and somewhere else, as `winrm: credential has a private key but no client certificate`, an error naming the one thing the operator had supplied.

**Root cause.** The phase added `CertificatePEM` and `PFXBase64` to `internal/credential.Credential`, taught `Flatten`/`Unflatten` about them, extended the four redaction surfaces, added the CLI flags, declared them in `internal/clispec` and published them in the generated reference. It never touched `internal/credential/file_store.go`, whose `credentialEntry` still declared exactly four YAML fields, or `buildCredentialEntry`, which still encrypted exactly three. The values were assigned to a struct and dropped at the persistence boundary.

Every test that existed passed, and the reason is the interesting part: the round trip was tested through `Flatten`/`Unflatten`, which is the MAP, not the STORE. Those two functions agreed with each other perfectly while agreeing with nothing on disk. The one test that would have caught it is the Release Gate, which skips without a Windows host, so the gap sat behind the one checkbox that was honestly left open.

**Fix.** `credentialEntry` gained `certificate_encrypted` and `pfx_encrypted`, `buildCredentialEntry` encrypts both, and `Lookup` decrypts both. `TestEveryCredentialFieldSurvivesTheFileStore` asserts the whole round trip through the real on-disk store rather than through the map, and names the missing field rather than printing two redacted structs that look identical. Removing either persistence branch turns it red.

**Lesson.** A value has as many boundaries as it has representations, and adding one representation does not add the others. This one had five: the Go struct, the flattened map, the wire format, the YAML on disk, and the CLI. Four were updated. The tell that should have caught it earlier is that the new field had no test that touched a file, while the feature's whole point was persistence. When adding a field to a type that is stored, grep for every place the type's OTHER fields are enumerated, and treat each enumeration as a place that must change or be deliberately excluded. A round-trip test through an in-memory transform is not a round-trip test through a store.

## 275. Go cannot do TLS 1.3 client-certificate authentication against Windows, and the empty 503 that results names nothing

**Symptom.** Found 2026-09-20 while running Phase 78d's Release Gate against a real Windows 11 host (build 26200). Certificate authentication worked perfectly from `curl` and failed from Go with `winrm: http response error: 503 - invalid content type`. Everything else checked out: the certificate was mapped, the authority was trusted, a WS-Man Identify returned 200 and a Create Shell returned 200 with a ShellId, both from curl. Only the Go client failed, and it failed identically every time.

**Root cause, and the first diagnosis was WRONG in a way worth recording.** The first conclusion was "Windows refuses client certificates over TLS 1.3", written up and committed to six files before anyone challenged it. It is false, and one experiment disproves it: curl with TLS 1.3 **forced** (`--tlsv1.3 --tls-max 1.3`) and the same client certificate is answered **200**. Windows is fine.

The limitation is on the Go side. TLS 1.3 removed renegotiation and replaced it with post-handshake authentication (RFC 8446 section 4.6.2), which is how `http.sys` asks for a client certificate once it knows which URL was requested. Go's `crypto/tls` does not implement it: `Conn.handlePostHandshakeMessage` accepts a `newSessionTicketMsgTLS13` and a `keyUpdateMsg` and answers everything else, a post-handshake `CertificateRequest` included, with `alertUnexpectedMessage`. So the server asks, Go refuses, the server sees no certificate, and `http.sys` returns a bare 503. Over TLS 1.2 the same exchange happens by renegotiation, which Go does support and which `tls.RenegotiateOnceAsClient` enables. OpenSSL implements post-handshake authentication, which is the entire difference between curl and Go here.

Two things made this harder to find than it should have been. `masterzen/winrm`'s `ClientAuthRequest` builds its `tls.Config` inside `Transport` and keeps it on an unexported field, so there was no seam to test the hypothesis through. And its error, `invalid content type`, discards both the status code and the body, so the one distinctive signal (an EMPTY 503, rather than a 503 carrying an HTML error page) never reached the operator.

**Why Go declines PHA, which decides whether this is a workaround or a constraint.** The omission is deliberate, not unfinished. RFC 8740 forbids PHA with HTTP/2 outright because it deadlocks multiplexed streams, and the Go team additionally treats PHA as a legacy pattern that adds brittle state to the TLS machine, holding that client authentication belongs in the initial handshake. They have said they are unlikely to implement it. OpenSSL made the opposite call, prioritising feature completeness, which is the entire difference between curl and Go here. IIS and `http.sys` depend on late requests because they decide per virtual directory, so they ask only once they have seen the request path.

**Fix.** `pkg/winrmexec/certtransport.go` implements `winrm.Transporter` directly, with `MaxVersion: tls.VersionTLS12` on the certificate path only; the password paths are untouched. Its errors carry the status, the content type and a bounded snippet of the body, and an empty 503 gets an explicit hint. The cap is documented as a STANDING CONSTRAINT rather than a TODO, which is the second correction this entry needed: the first version framed it as "remove this when crypto/tls supports PHA", which would have left a maintainer waiting for something upstream has declined to build.

**Lesson, and it is not the one the first draft drew.** When a request works from one client and fails from another, the difference is in the transport, and the useful next step is to eliminate variables one at a time rather than to conclude from the first asymmetry you find. The first diagnosis noticed that curl negotiated TLS 1.2 and Go negotiated 1.3, confirmed that capping Go at 1.2 fixed it, and stopped. That is enough evidence to justify the FIX and nowhere near enough to justify the EXPLANATION, and the two were written up as if they were the same finding. The experiment that separated them, forcing curl to 1.3, takes one flag and was not run until somebody pushed back.

**The corollary about blame direction.** "Their software is broken" is a conclusion that should require more evidence than "our client cannot do this yet", not less, because it is the one that stops you looking. The wrong version of this entry would have told the next reader that a whole protocol version was unavailable to them, when what was actually true is that one client library made a deliberate design choice.

**And the corollary about temporariness.** Having found the right cause, the first write-up still called the fix a workaround pending an upstream fix, because that is the comfortable shape for "we cannot do this yet". Check whether upstream actually intends to close the gap before writing a re-entry condition. A removal note that will never fire is worse than none: it tells the next person to wait, and they will.

## 276. The migration gate accepted a history missing its first migration

**Symptom.** Found 2026-09-21 while planning Phase 84, by reading `checkGate` rather than by any failure. A database whose `schema_migrations` recorded `0002_...` and not `0001_...` passed the startup gate, and `Apply` would then have run 0001 on top of a schema 0002 had already changed.

**Root cause.** The gap check only started looking for a hole after the first applied name it met: `haveAppliedOne` became true at 0002, and the gap flag was only raised by a missing migration *after* that point. A missing prefix is a gap before the first applied row, which the loop was built never to see. `TestCheckGate` had six cases and none with the first migration missing, so the test agreed with the code rather than with the doc comment, which said "no migration may be applied while an earlier one in filename order is not".

**Fix.** `internal/ent/migrate/gate.go` computes the longest prefix of the known set that is recorded, and refuses any recorded known name beyond it. The missing-prefix case is in `TestCheckGate` (it failed before the fix, run first), and `FuzzCheckGate` compares the gate with an independently written oracle; that fuzzer then found a second ambiguity (two floors sharing a number, one from another lineage, decided by row order), fixed the same day.

**Lesson.** A "no gaps" rule has two edges, and a loop that tracks "have I started" only guards one of them. When a test table for an ordering rule has no case at position zero, the rule has not been tested at position zero. An oracle-based fuzzer is cheap for a pure decision like this one and finds exactly this class.

## 277. Two processes opening a brand-new SQLite file failed each other at open

**Symptom.** Found 2026-09-21 by Phase 84's real-process race test: roughly one run in three, one of two or more processes opening a SQLite database that did not exist yet failed its very first connection with `database is locked`, before a single migration ran. A controller and an admin command started together against a new install are exactly that pair.

**Root cause.** Every connection asks for WAL through its DSN (`_journal_mode=WAL`). On a file already in WAL that is a no-op; on a new file, switching needs exclusive access. Two connections each holding a shared lock and each wanting an exclusive one is a deadlock, and SQLite breaks it by failing one of them at once with SQLITE_BUSY, without consulting the busy timeout. The DSN's own `_busy_timeout` was set first and did not help, because SQLite deliberately bypasses the busy handler for deadlocks.

A read-back ("did the other process switch it?") was tried first and was not enough: the loser can read the mode while the winner is still mid-switch and see the old one. A retry would have worked often enough to look fixed.

**Fix.** `internal/ent/open_sqlite.go`'s `ensureSQLiteWAL` creates a new database privately under a temporary name, switches it to WAL there, and publishes it with one hard link, which fails if the name is taken; a taken name means another process published first, which is success. `link(2)` rather than `rename(2)`, because a rename would replace a database the winner may already be writing to. A new file is created 0600. `TestOpenDatabase_ManyProcessesOpenOneNewDatabase` (16 processes, three rounds, both backends) failed without it.

**Lesson.** When a platform resolves contention by failing one side immediately rather than making it wait, "read it back" and "retry" are both races against the winner's progress. Make the contended step uncontended instead: do it where nobody can see it, then publish the result atomically. It is the same move the certificate bundle made.

## 278. A starting controller failed every live peer's project sync

**Symptom.** Found 2026-09-21 by the Phase 84 design review. `RecoverInterrupted` ran at every controller's startup and moved EVERY running project sync to failed. In a deployment of several controllers, every new pod of a rolling upgrade failed its peers' in-flight syncs, and offered Sync again on a working tree still being cloned into.

**Root cause.** The recovery assumed it ran in the only process. "Running at startup" means "abandoned" only when nothing else is running; the code's own comment noted that a live peer's `RecordSync` would correct its row later, which is true for the row and does nothing for the second clone a user can start in between.

**Fix.** A sync records the instance that owns it (`sync_runs.owner_instance`), every controller heartbeats into `controller_instances` on the database's clock, and recovery fails only running syncs whose owner is missing or silent for three heartbeats. It now also runs on every heartbeat, so a sync abandoned by a controller that died is recovered without waiting for some other controller to restart.

**Lesson.** A startup sweep of shared state is a claim that nobody else is alive. In a replicated service, write down who owns each claim, and sweep only claims whose owner you can prove is gone.

## 279. The backup tests trusted any newer PostgreSQL client, until the machine's default became 18

**Symptom.** 2026-09-21, mid-session, with no change to the code: every restore test in `internal/backup` failed inside `pg_restore` with `unrecognized configuration parameter`. A system upgrade had installed `postgresql-client-18` and made it the default, so `pg_dump` and `pg_restore` on PATH became 18.6 against the tests' pinned 15.19 server.

**Root cause.** The fixture's own comment said the tests run "whichever programs this machine has, of any release that reads 15.19 archives". Reading the archive was never the question. 18's `pg_restore` reads it and then sets `transaction_timeout`, which a 15 server does not have, so every restore failed before loading a row.

**Fix.** A `TestMain` in the package puts the pinned major version's client programs first on PATH when the machine has them (`/usr/lib/postgresql/<major>/bin`), and the fixture's comment now says what it depends on.

**Lesson, and an open product question.** A test that shells out to a host tool is pinned to that tool's version whether it says so or not. The product side of this is not fixed and is raised with the user: the compose stack's backup image carries 15.19's own programs, so it never meets this, but `controller backup|restore` run anywhere else uses whatever PATH finds, and a newer `pg_restore` cannot restore into the older server it came from.

## 280. A zero-failure drain test passed with the drain switched off

**Symptom.** 2026-09-21, Phase 84's binary upgrade gate. Its step 4 stops a controller behind a client that routes by readiness and asserts that no request fails. It passed. The control run, with `SHUTDOWN_DRAIN=0s`, also passed, so the assertion proved nothing about the drain.

**Root cause.** Two things. The balancer polled readiness every 100 ms, far faster than a real load balancer drops an endpoint, so a controller that closed its port at once was noticed before most requests reached it. And, decisively, the balancer's poll loop and the test's `time.Sleep` before the stop started at the same instant, so the stop always landed exactly on a poll: the controller vanished between two requests and was marked not ready before the next one.

**Fix.** The balancer polls once a second (the chart's readiness probe runs every five), and the stop is placed halfway between two polls. With the drain on, 282 requests passed a drain and a stop with none failing; with it off, 10 of 169 failed with `connection refused`.

**Lesson.** See LESSONS_LEARNED.md #213. A test of a protection is not finished until a run with the protection removed fails it. Here the first control run found that the test's own timing had quietly removed the thing it was testing.

## 281. `make -n` started a compose stack

**Symptom.** 2026-09-21: checking that the new `up-plan` target parsed, `make -n up-plan` created the default `pleiades` compose project's network and database volume, started its PostgreSQL container and built an image. Nothing was lost (the volume and network were new, and the three objects were removed at once), but a "dry run" changed the machine.

**Root cause.** Make executes any recipe line that contains `$(MAKE)` even under `-n`, so that a recursive make can print its own plan. `up-plan`'s one shell line calls `$(MAKE) backup`, so the whole line ran.

**Fix.** The Makefile says so above `up-plan`. Check a target's syntax with `make -pn | grep`, or by reading it, not by `make -n` on a recipe that recurses.

**Lesson.** `make -n` is only a dry run for recipes that do not recurse; one `$(MAKE)` anywhere in a shell line runs the line.

**Superseded (2026-09-22).** A comment was not a fix. The same mechanism also stopped a live stack; see #284, which refuses the dry run instead.

## 282. A failed heartbeat read made every live peer's sync look abandoned

**Symptom.** Found 2026-09-22 by an adversarial review of the uncommitted Phase 84 change, before it shipped. `fleet.alive` answered a failed read of `controller_instances` with this controller's own id alone. The sync recovery sweep, which now runs at startup and on every 15 second heartbeat, then treated every other owner as gone and failed each live peer's in-flight project sync. One transient database error was enough.

**Root cause.** The fallback was written on the reasoning that a live owner's own outcome corrects its row when its clone finishes. That is #278's root cause word for word: true for the row, and it does nothing for the second clone a user can start into the same working tree in between. #278 was fixed; its fallback path quietly kept the bug.

**Fix.** `alive` reports whether it knows (`cmd/controller/fleet.go`), and one `sweep` method, used at startup and on every tick, does nothing when it does not. `TestFleet_SweepsNothingWhenItCannotTellWhoIsAlive` renames the heartbeat table away and proves neither a direct sweep nor a tick runs the recovery, with a control proving the sweep does run while the table is readable; a mutation that reports the failed read as known turns it red.

**Lesson.** A fallback inherits the fix's obligations. When the fix is "act only on what you can prove", the error path must prove nothing and do nothing, not return a default that means "everyone else is gone".

## 283. The sync sweep read who was live, then failed projects in a second statement

**Symptom.** Found 2026-09-22 by the same review. `ResetInterruptedSyncs` read the projects a live controller was syncing in one query, then failed every other running project in a separate `UPDATE`, with no transaction. A sync claimed between the two statements was failed while its live owner was still cloning it. With the sweep on every heartbeat of every replica, the window opened four times a minute per controller.

**Root cause.** A decision spread over two statements is only as current as the first one. The exclusion list was a snapshot, and the update applied it to rows that had changed since.

**Fix.** The live-owner exclusion moved inside the one `UPDATE`, as a `NOT EXISTS` over the project's running, live-owned attempts (`internal/project/ent_store.go`). It holds on both dialects: a project claimed concurrently was not running in the statement's snapshot, so it never matches, and a project that matches cannot be claimed meanwhile, because `BeginSync` claims only a project that is not running. `TestResetInterruptedSyncs_FailsOnlyWhatNoLiveOwnerRuns` also closes the gap that no test had run the owner-aware sweep against a real store at all: five projects (live owner, gone owner, no owner, no attempt row, idle), and only the live one survives. Dropping the exclusion turns it red.

**Lesson.** When a write depends on a read of other rows, put the read in the write's own `WHERE`. Two statements are a race even when each is correct.

## 284. `make -n up` stopped the live controller and runner and took no backup

**Symptom.** Found 2026-09-22 by an adversarial review of the uncommitted Phase 84 change. When the checked-out build was due to upgrade the database, `make -n up`, which reads as a harmless preview, stopped the running controller and runner, took no backup, and started nothing, leaving a live deployment down. `make -n restore` reached the same place through its closing `$(MAKE) up`.

**Root cause.** #281's mechanism, one level deeper. `up` names `$(MAKE) up-plan`, so under `-n` make runs that line and hands `-n` to the sub-make. `up-plan`'s one shell line names `$(MAKE) backup`, so the sub-make runs that whole line too: the real `migrate --plan`, and on exit 3 the real `docker compose stop controller runner`. Only `$(MAKE) backup` itself, and the final `docker compose up`, obeyed `-n` and printed. #281 had been closed with a comment warning about it.

**Fix.** A `refuse-dry-run` expansion is the first recipe line of `up`, `up-plan` and `restore`. It is an `$(error)` inside `$(if $(DRY_RUN),...)`, where `DRY_RUN` reads `-n` from the first word of `MAKEFLAGS`, so it fires while make expands the recipe, before any line is printed or run. A guard written as a command would itself only be printed. Proven in a scratch makefile on GNU Make 4.3: `-n`, `--dry-run`, `--just-print`, `-ns` and `-n --no-print-directory` are refused with no side effect; a plain run, `-s`, `--no-print-directory`, `-j2`, `-k` and `--always-make` run normally; and with the guard removed the recursive line ran for real. Against this Makefile, `make -n up`, `make -n up-plan` and `make -n restore` each stop with the reason.

**Lesson.** Warning about a hazard in a comment is not fixing it. Where a tool's safe-looking mode is not safe, make that mode refuse.

## 285. `make up` never rebuilt the runner image

**Symptom.** Found 2026-09-22 by the same review. docs/10's Compose upgrade is "check out the new release, then `make up`". That rebuilt the controller and the backup image and never the runner, so an upgraded stack ran the new controller beside the previous build's runner.

**Root cause.** `make up` builds nothing on purpose. The controller was rebuilt only by accident, because `setup --check` runs with `--build` and the setup service shares the controller's image tag. The final `docker compose up -d --wait` reused whatever `pleiades/runner:dev` was already present.

**Fix.** The last line of `make up` is `docker compose up -d --wait --build`, which builds exactly the services it starts, from cache when nothing changed. Profile services are not started there, so they are not built there.

**Lesson.** A deployment step that starts a service must also say which build of it runs. Side effects that happen to rebuild one image are not a build step.

## 286. The compose gate proved rollback only after wiping the upgraded stack

**Symptom.** Found 2026-09-22 by the same review, confirmed by tracing the previous build's code. docs/10 rolls back past the window with the previous release's `make restore`, run against the live stack. The gate ran `docker compose down -v` first, so it proved a rollback from an empty machine, and its check that data written after the upgrade was gone could not fail once the volume was gone.

**Root cause.** The previous release is main from before Phase 84. Its restore sets the live database aside first, and its dump check refuses a table it does not know, so on an upgraded database it stops with "could not be backed up first". This build's restore tolerates a newer build's tables in the set-aside copy (`internal/backup/backup.go`), so the documented path works for every build from Phase 84 on. The gate worked around the old build and still read as proof of the documented path.

**Fix.** The gate runs the documented path whenever the previous build has Phase 84's compatibility table (`previousBuild.phase84`). For an older previous build, a transitional branch first requires the documented restore to fail with exactly that refusal, then rolls back from a dropped stack and logs that it did. If the old build ever succeeds, the gate fails and says to remove the branch. The branch goes at 1.0 gold, with the other allowances for builds before Phase 84.

**Lesson.** A gate that works around a limitation must prove the limitation is the reason, and say so in its output. Otherwise the workaround passes for the thing it replaced.

## 287. The binary upgrade gate never asked the old build anything while the new one migrated

**Symptom.** Found 2026-09-22 by the same review. `TestUpgradeGate_ThePreviousBuildKeepsServingWhileThisOneMigrates` started this build, waited for it to be ready, and only then sent the previous build a request. Nothing reached the previous build during the migration its name is about.

**Root cause.** The gate checked the end state, which is what is easy to assert, and named itself after the transition, which is what matters.

**Fix.** A readiness-routed, non-retrying client sends the previous build a request every 25ms from before this build starts until it is ready, and a moment when the previous build is not ready counts as a failure. The first run of that check found the second half of the problem: 3 requests in the whole overlap, because this phase's migrations apply in milliseconds on an empty database, so no window existed to observe. The gate now makes the migration take as long as a real one does, by holding a SHARE lock on `schema_migrations` for three seconds, which stops any migration at its first claim, and it requires this build to answer `/readyz` with 503 both when its listener first answers and again at the end of the hold. Run 2026-09-22: 110 requests to the previous build during the held migration, none failed. With the lock removed, the end-of-hold check fails (`current answered /readyz with 200`), so the window is the migration and not only the sleep.

**Lesson.** A test named for a transition must observe during it. If every assertion runs after the event, the name promises more than the test proves.

## 288. A role's timeout ended the one migration wait that must never end

**Symptom.** Found 2026-09-22 by an adversarial review of the uncommitted Phase 84 change. On a PostgreSQL role carrying a `statement_timeout` or `lock_timeout`, which managed PostgreSQL commonly sets, a second controller starting beside a first one whose migration ran longer than the timeout was cancelled while it waited, failed to start, and crash-looped until the first committed.

**Root cause.** The design states that recording a migration's version is the one wait that must be unbounded, and ran that claim before the prelude that sets any timeout, so the claim inherited whatever the session carried. "Unbounded" was assumed rather than said. The idle-in-transaction timeout sat in the same late prelude, so a winner cut off between its claim and the prelude's first statement held its claim with no idle limit either.

**Fix.** `postgresClaimPrelude` (`internal/ent/migrate/transaction.go`) runs before the claim: the idle timeout, `lock_timeout = 0` and `statement_timeout = 0`. `postgresPrelude`, after the claim, sets `lock_timeout = '10s'` for the script. `TestApply_ARoleTimeoutDoesNotEndTheClaimWait` gives a database one-second defaults, proves with a control that they cut an ordinary statement off, and requires a second starter to wait out a four-second winner and find the work done. Run 2026-09-22 against PostgreSQL 15.19: it passes with the fix (6.4 seconds), and on the exact pre-fix order (nothing before the claim, all three settings after it) the waiting starter fails after 1.006 seconds with `recording 0001_slow.sql as applied: pq: canceling statement due to statement timeout`, which is this defect reproduced. A first, coarser mutation that removed the claim prelude entirely also failed, but on the winner's script, and so proved nothing about the claim; the precise one is the one that counts.

**Lesson.** When correctness needs a setting to be off, turn it off. A default you inherit is somebody else's setting.

## 289. With no hard links, a new embedded database came out readable by everyone

**Symptom.** Found 2026-09-22 by the same review. `ensureSQLiteWAL` promises a new database is 0600. On a filesystem that cannot hard link (some network and container mounts), its fallback returned without creating anything and left creation to the SQLite driver, whose file follows the umask, typically 0644. The file holds sealed secrets and password hashes.

**Root cause.** The fallback was reasoned about as a concurrency fallback only (the #277 race comes back there, as documented), and the file mode, the other thing the function promised, went with it silently.

**Fix.** On a failed link the real name is created empty with `O_EXCL` and mode 0600, and SQLite adopts it on first open; a name another process created first is accepted. A test seam (`hardLink`) stands in for a filesystem with no hard links: `TestEnsureSQLiteWAL_WithoutHardLinksStillCreatesAPrivateFile` requires the fallback's file to be 0600, usable, and alone, and the old fallback turns it red.

**Lesson.** A fallback must keep every promise the fast path makes that it can keep. List them before writing it.

## 290. The chart drained for less time than its own readiness probe needed

**Symptom.** Found 2026-09-22 by the same review. The chart's `controller.shutdownDrainSeconds` defaulted to 10, while its readiness probe needs up to 15 seconds (5 second period, 3 failures) to take a pod out of the Service. A controller that stops by itself, having found the database migrated past it, is never deleted, so only that probe removes it, and it closed its port while still routed to.

**Root cause.** The value was sized for a rolling upgrade, where deleting the pod removes its endpoint at once. The other way a controller stops was not considered, and the comment beside the value said the drain must outlast the probe while the value did not.

**Fix.** The default is 20 seconds, the probe's 15 plus five for the endpoint to go, and the comment states both cases and says to raise it with the probe. The termination grace period follows it (45 seconds).

**Lesson.** A timing value has one case per way the event it covers can start. Size it for the slowest, and write the arithmetic next to it.

## 291. A controller left the fleet while its clones were still running

**Symptom.** Found 2026-09-22 by the same review. On shutdown, when the project runner reported clones still running at its deadline, the controller removed its heartbeat anyway. A peer's next sweep then failed those syncs while the dying process's clones could still be writing, and offered Sync again into the same working tree.

**Root cause.** The comment above the call said to leave only once this controller's syncs had stopped, and the code left whether or not they had.

**Fix.** The controller leaves the fleet only when the runner's shutdown succeeds. Otherwise it stays listed, its heartbeat ages out once the process is gone, and only then may a peer sweep its syncs.

**Lesson.** When a comment states a condition, the code must test it. A comment that describes an `if` the code does not have is a bug report.

## 292. The controller's backup gate restored with the machine's newest PostgreSQL client

**Symptom.** 2026-09-22, the first Docker-backed run of the Phase 84 tree: `TestBackupReleaseGate_RestoreOnACleanMachineTakesTheKeyWithEchoOff` failed with `pg_restore: error: could not execute query: ERROR: unrecognized configuration parameter "(value hidden)"`. The parameter name was masked by the platform's own error redaction; it is `transaction_timeout`, which pg_restore 17 and later send and a PostgreSQL 15 server rejects.

**Root cause.** #279 again, in a second suite. #279's fix put the server's major version first on PATH in a `TestMain` private to `internal/backup`. `cmd/controller`'s gate runs the real `controller restore` binary, which found pg_restore on the untouched PATH, the machine's default 18. The same failure existed on main on this machine; nothing had run the gate since its default client changed.

**Fix.** The pinning moved to `internal/testsupport/pgclient.go` (`PostgresMajor`, `PostgresClientBinDir`, and `UsePostgresClientTools`, which sets PATH for one test so a child process inherits it). `internal/backup`'s `TestMain` and the controller gate's `backedUpServer` both call it. The failing run is its own control: the same gate on the same machine, changed only in PATH, now passes.

**Lesson.** When a fix is an environment arrangement, put it where every suite that needs the same arrangement will find it. A fix private to the package that noticed first leaves the others to fail the same way later.

## 293. A chaos test threw away the error of the one session it was waiting for

**Symptom.** 2026-09-22, `make coverage` on the Phase 84 tree: `TestApply_APartitionedWinnerReleasesItsClaim` failed after 32 seconds with `no session ever ran a query like "%pg_sleep%"`. The same test passed in the same run's `test-integration` pass, where container packages run one at a time, and passed again alone afterwards (the claim held 1m10s before the server ended it).

**Root cause.** The test started the migrating "winner" as `go func() { _, _ = applyPending(...) }()` and then polled `pg_stat_activity` for 30 seconds waiting for the winner's `pg_sleep`. A winner that failed before reaching its migration (a connection through Toxiproxy refused or timed out under the parallel coverage pass is the likely cause, #61's class) left the poll to run out and report a symptom with no cause. What the winner's error actually was is unknown: the test discarded it, which is the defect. `TestApply_ARoleTimeoutDoesNotEndTheClaimWait`, written the same day, had the same shape, though it did read the winner's error once the wait was over.

**Fix.** `awaitQuery` takes the winner's result channel and stops at once, naming the winner's own error, if the winner ends before any session runs the query. Its timeout message says the winner is still running, so the two cases read differently. Both tests pass it their winner. Control: the role-timeout test with the winner's DSN deliberately broken now fails in 2.6 seconds with `the winner ended before any session ran a query like "%pg_sleep%" (its error: ... unsupported sslmode "disablex" ...)`, where it would have spent 30 seconds reporting nothing.

**Lesson.** A test that waits for a background actor to reach a point has to watch that actor finish too. Swallowing its error turns every failure it has into one timeout message, and the next reader of that message debugs the wrong thing.

## 294. `facts.gather`'s check test compared a clock

**Symptom.** 2026-09-22, `make test-integration` on the Phase 84 tree: `TestGatherCheck_OnlyReads` failed with two fact maps identical except `ansible_uptime_seconds:12655` against `12656`. The package was untouched by the change under test.

**Root cause.** The test runs the check, then a real gather, and requires `reflect.DeepEqual` of the two fact maps. Each reads `/proc/uptime` afresh, so whenever the two reads straddle a second boundary the maps differ. The test was written against a property (a check reports what a run reports) that is true of every fact but the one that is a clock.

**Fix.** The uptime fact is compared on its own: present in both, the run's value no earlier than the check's and at most a minute later. Every other fact is still compared exactly. Controls, each by a temporary edit: the run's uptime one second later passes; a changed hostname still fails; the run's uptime five seconds earlier fails.

**Lesson.** Before asserting two reads are equal, ask which of the fields read is time. An equality that holds only when two reads land in the same second fails about as often as the reads take to run.

## 295. The Helm setup gate looked for a durable fact in one container's log

**Symptom.** 2026-09-22, `make test-integration` on the Phase 84 tree: `TestPackagingReleaseGate_KubernetesInstall` failed in its setup subtest with `the controller did not record the key it first ran with`, printing a controller log in which the key record was absent, the database was plainly open, and the serving certificate was `"provisioning":"reused"`, so the container printing it was not the first one.

**Root cause.** The check read `kubectl logs deploy/<controller>`, which shows the running container only. Every Helm install in the gate restarts its controller: rerun with a diagnostic, the setup release's controller restarted three times and the main release's three times, each ending `failed to open the controller database ... lookup pleiades-setup-postgres ... no such host`, because the controller exits when the database's Service name does not resolve yet, and `main` does the same. The key is recorded once, by whichever container first gets past the database, and a container that recorded it and then died later in startup takes the only copy of the line with it. Nothing about the product was wrong: the registry row was there. The check tested a log retention detail rather than the fact it names.

**Fix.** The gate now reads the fact itself: `SELECT origin FROM encryption_keys` through `psql` in the release's own PostgreSQL pod, requiring exactly one key, recorded as `first_use`. It also logs how often the controller container restarted and, when it did, the previous container's last lines and exit, so the next failure of this kind names its cause. Rerun alone after the change: three restarts logged with their cause, one `first_use` row, gate passes.

**Lesson.** Assert a durable fact where it is stored. A log line is evidence that one process said something, and in Kubernetes the process that said it may already be gone. Separately, and not fixed here: a controller that exits until its database resolves costs every Helm install about a minute of back-off.

## 296. A control was attested before anything ran it

**Symptom.** 2026-09-22, reading `SECURITY_ATTESTATION.md` while ticking Phase 84: control ISO 27001 A.8.13 claimed, ticked and dated, that "if the backup fails, nothing is upgraded", citing the `Makefile` and the compose upgrade gate. The Makefile does implement it: `up-plan` runs `make backup` on plan exit 3 and stops with its own message if that backup fails. No test ran that branch. The gate cited beside it only ever took a backup that worked.

**Root cause.** The claim was true of the code and untrue of the evidence. An attestation entry names the mechanism and the test in one breath, which reads as "this is enforced and proven"; here the second half was supplied by the first. It survived a review because the branch is three lines of shell in a recipe that is otherwise exercised constantly, so every run of the gate looked like coverage of it.

**Fix.** The compose gate now runs `make up` with `BACKUP_DIR` pointing at a directory this user cannot write, and requires it to fail saying the database was NOT upgraded; the next `make up` still reporting an upgrade to back up is what proves nothing was migrated meanwhile. Control, per LESSONS 213: with the recipe's `|| { ...; exit 1; }` replaced by `|| true`, `make up` upgraded anyway and the new check failed the gate in 47 seconds. The attestation entry now says what the gate proves, and where the rollback proof is narrower than the documented path.

**Lesson.** When writing a compliance control, cite the test that fails if the control is removed, not the mechanism that implements it. If naming that test means writing it first, the control was not ready to be claimed.


## 297. `NATS_URL` was checked after the port was bound and the database migrated

**Symptom.** 2026-09-22, writing Phase 96d's "refused at startup" gate. `changelog/mesh-transport-and-tls.added.md` shipped the sentence "`NATS_URL` is now checked at startup", and the check really existed, so nothing looked wrong. Reading `cmd/controller/main.go` in order says otherwise: `natsURL` is read at the top of `main`, and the first thing that looks at it is `topology.TLSFromEnv` roughly 280 lines later, on the far side of `net.Listen`, the `controller listening` log line, `srv.Serve` in a goroutine, and `ent.OpenDatabaseReporting`. A controller with a typo in its scheme therefore bound its port, answered 200 on `/healthz` (the startup handler serves it while returning 503 on `/readyz`), ran a full schema migration, and then exited 1.

**Root cause.** The check was placed where the value is USED rather than where it is READ. That is the natural place to put it and it is wrong for a configuration check, because everything between the two is work done on behalf of a process that cannot function. The same file already got this right for the Controller's own TLS: `resolveTLS` is called in the configuration block with a comment saying a deployment whose TLS variables contradict each other must fail before it opens a database or joins an election. Nobody applied that rule to the connection the process makes.

**Fix.** `topology.ValidateNatsURL` and `topology.TLSFromEnv` both moved into the configuration block, immediately after the masking logger is installed. `cmd/controller/nats_url_refusal_release_gate_test.go` and its runner sibling drive the real binaries and assert the ordering rather than the exit code: the controller's database directory must not exist afterwards and `controller listening` must not appear in its output, and the runner, which has neither a listener nor a database, must refuse inside a fifth of `topology.ConnectWaitTimeout`. Both carry a positive control, since a binary that refused every URL would pass a refusal gate perfectly.

**Lesson.** A configuration check belongs where the value is read, not where it is used, and the distance between the two is the measure of the bug. "Checked at startup" is a claim about ORDERING, so the test for it asserts what had not happened yet, not that an error was eventually printed.

## 298. Placing that check one step earlier would have printed an embedded credential

**Symptom.** Found while fixing #297, before writing any code. The obvious placement for `ValidateNatsURL` is immediately after `natsURL := getenv("NATS_URL", ...)`. In both composition roots that line runs before the masking logger is installed: about 75 lines before in `cmd/controller/main.go`, about 20 in `cmd/runner/main.go`.

**Root cause.** `ValidateNatsURL`'s messages quote the URL back, which is right for an operator and dangerous for a value that may carry userinfo. `internal/redact/rules.json` carries a `url_userinfo` pattern rule for exactly `scheme://user:pass@host`, and Phase 96d's own fuzz item names embedded-credential URLs as an input class to test. Before `slog.SetDefault` and `log.SetOutput(redact.Shared().Writer(...))`, both `fatal()` and `log.Fatalf` reach an unmasked handler, so `natss://admin:hunter2@broker.internal:4222` would have printed the password to stdout, on a fatal path, where an operator or a log collector is most likely to be reading.

**Fix.** The checks go immediately after the masking logger rather than immediately after the env read. That keeps every benefit of #297's fix, since the logger is still installed long before `net.Listen` and the migration. Both files carry a comment saying the placement is deliberate and why, because "move this a few lines up" is otherwise a harmless looking cleanup.

**Lesson.** Masking is installed at a point in a program, not switched on for the whole of it, so "before the logger" is a real region of `main` with different rules. Before adding a fatal path that quotes a configuration value, check which side of that line it lands on.

## 299. An empty `ExposedPorts` publishes MORE ports, not fewer

**Symptom.** 2026-09-22, the first run of Phase 96d's wss traversal gate. The gate asserts that the broker container publishes no route this host could use to bypass the proxy, and the broker's `ContainerRequest` deliberately named no `ExposedPorts` at all. It failed immediately: `the broker container published map[4222/tcp:... 6222/tcp:... 8222/tcp:...]`.

**Root cause.** testcontainers-go v0.43.0's `configureExposedPorts` (lifecycle.go): when `req.ExposedPorts` is empty and the network mode is not a container network, it **inspects the image** and publishes every port the image's own `EXPOSE` declares. The nats image declares all three. So naming nothing does not mean publishing nothing, it means inheriting the image's list, which is the opposite of what the field's name suggests. A `HostConfigModifier` cannot undo it either: the modifier runs at line 516 and the port merge at line 546.

**Fix.** The broker names exactly one port, the websocket port the proxy forwards to, so the image's list is never consulted and the plain client port 4222 has no host route. The gate asserts that 4222, 6222 and the monitoring port are all absent from the published set rather than trusting the request. `tests/e2e/integration_chaos_test.go` carried the same claim in a comment ("Neither publishes a host port of its own") while declaring `ExposedPorts` explicitly; that was corrected too, though on the image-EXPOSE mechanism above its comment was only ever true by luck.

**Lesson.** In testcontainers, `ExposedPorts` is a NARROWING field, not an additive one: the way to publish less is to name something, and the way to publish everything the image declares is to name nothing. Any test whose claim is "there is no other route" has to assert the published set rather than reason about the request that produced it.

## 300. The compose upgrade gate indexed a backup that correctly did not exist

**Symptom.** 2026-09-22, the first full `make test-integration` on the Phase 96d branch: `TestUpgradeReleaseGate_ComposeUpgradesAndRollsBack` panicked with `index out of range [0] with length 0` at `packaging_upgrade_compose_test.go:147`, having logged `upgrading from 3b60a803 across []`. It reproduced every time in isolation, so it was not contention. Stashing the branch's work made it PASS, which looked at first like the branch had broken it.

**Root cause.** Neither. `previousRef` resolves what this build replaces, and its logic is right: on a branch with no commits of its own it takes the merge base, and when that equals HEAD it distinguishes a dirty tree (the uncommitted work is the build under test, so HEAD is what it replaces) from a clean one (`HEAD^1`). On this branch HEAD was the Phase 84 merge and the tree was dirty, so the previous build was HEAD's own tree, and the branch adds no ent migration, so `crossed` was legitimately empty. `make up` therefore took no backup, correctly, because there was no migration to back up before. Line 147 then indexed `g.backups(t)[0]` unconditionally. The two assertions immediately above it are both wrapped in `if len(prev.crossed) > 0`; this line was not, so it was reachable exactly when they were skipped. Stashing "fixed" it only because a clean tree selects `HEAD^1`, which is pre-Phase-84 and does cross migrations.

**Fix.** The rollback half now reads the backup list once and, when it is empty, distinguishes the two cases rather than indexing: with a crossed migration and no backup it fails, because an upgrade that skipped a backup it owed is the defect this gate exists to catch; with no crossed migration it logs that the previous build shares this schema and returns, the upgrade half above having already run in full. Verified by re-running the gate alone: panic before, pass after.

**Third instance, same session.** `TestUpgradeGate_ThePreviousBuildKeepsServingWhileThisOneMigrates` failed for the same reason once the branch was COMMITTED rather than dirty: with commits of its own the previous ref becomes the merge base, and this branch still adds no migration, so `crossed` stayed empty. That gate stalls the new build by taking a SHARE lock on `schema_migrations` to hold it at its first claim of whatever the previous release lacks; with nothing to claim it becomes ready in milliseconds and the gate fails on a 200 that is the correct answer. It now skips with that reason, because the overlap it measures cannot exist without a migration. Three instances of one shape in one session.

**Lesson.** Phase 84's own branch added a migration, so its author could never reach this path; the first branch after it that adds none does. When a test derives a fixture from repository state, enumerate the states it can take, including the empty one, and note which of them the authoring branch could not produce. Two collaborating details made this worse than a stray index: the guard existed twenty lines earlier and was simply not carried down, and this repository's standing rule is not to commit, so a dirty tree is the normal state rather than the exception.

## 301. A check test compared a clock, again, in a different package

**Symptom.** 2026-09-22, same integration run: `TestLineChecks_PredictWhatARealRunLeaves/set,_already_there` failed with `predicted mtime = 1790109210, the run left 1790109211`. One second.

**Root cause.** Identical to #294, which was found eight days earlier in `internal/catalog/facts` and fixed there. The check predicts what a run would leave, the test then performs the real run and compares, and one of the compared fields is a clock, so the two agree except when the two reads straddle a second boundary.

**Fix.** Not fixed here: found on a branch that does not own this package, and recorded rather than swept in silently. The fix is #294's, applied to the mtime field: compare it as a bound (present in both, the run's value no earlier than the check's and at most a minute later) rather than for equality, leaving every other field compared exactly.

**Lesson.** #294 ended with "before asserting two reads are equal, ask which of the fields read is time." That lesson was written into the archive and applied to the one package where it was found, and the identical assertion in another package was never looked for. A lesson about a PATTERN is not discharged by fixing the instance that produced it: grep for the shape before closing the entry.

## 302. The Helm upgrade gate counted pods Kubernetes had already finished with

**Symptom.** 2026-09-22, the same integration run as #300 and #301: `TestUpgradeReleaseGate_HelmUpgrade/two_controllers,_RollingUpdate` failed with four controller pods listed, two on `pleiades/controller:previous` and two on `pleiades/controller:dev`, against `want two, both this build`. It reproduced in isolation, so it was not read as contention.

**Root cause.** The `helm upgrade` immediately before the assertion runs with `--wait --timeout`, and it SUCCEEDED, which is the detail that settles this: helm returned only once Kubernetes called the rollout complete, meaning the new ReplicaSet was fully available. That says nothing about the old pods. A Deployment deletes them asynchronously afterwards, and during their termination grace period they remain in `kubectl get pods`, still in phase Running, carrying their old image. The assertion read `jsonpath={.spec.containers[0].image}` with no status filter at all, so a pod Kubernetes had already decided was finished was indistinguishable from a serving one. The test therefore passed whenever the old pods happened to be reaped inside the query window and failed when they were not, which makes it a measurement of how loaded the machine is rather than of the rollout. The grace period sets that window, so the failure gets likelier exactly when the box is busy, which is when this suite actually runs.

**Fix.** The query now also selects `.metadata.deletionTimestamp`, and pods carrying one are excluded before the count. The failure message prints both the filtered list and the raw list with timestamps, so the next failure distinguishes "the rollout did not converge" from "pods were mid-deletion" without a rerun.

**Lesson.** `--wait` means the new thing is ready, never that the old thing is gone. When a test asserts on a SET of live objects after a rollout, it has to exclude the ones being deleted, because Kubernetes lists an object from the moment it is created until it is actually removed, not until it stops mattering. The generalization worth carrying: an assertion that counts objects needs to say which lifecycle states it counts, and a query that reads only spec fields cannot express that.

## 303. A new test reached into a Linux-gated file and broke the build on two operating systems

**Symptom.** 2026-09-22, pull request 38: every local gate passed, `make push-gate` was green, a receipt was issued and the branch was pushed. GitHub Actions then failed on `vet`: `cmd/controller/nats_url_refusal_release_gate_test.go:124:18: undefined: setupEnv`, on both the Windows and the macOS legs.

**Root cause.** `setupEnv` builds a hermetic child environment, stripping every variable the setup command owns so a developer's exported `JWT_SECRET` cannot decide a test's outcome. Nothing in it is platform-specific. It lived in `setup_release_gate_test.go`, which is `//go:build linux` because that file's OTHER tests drive real pseudo terminals, a POSIX facility. So a platform-neutral helper was trapped behind a platform constraint, and the new refusal gate called it without inheriting the constraint. On Linux the reference resolves and everything passes; on the other two the file is not compiled and the symbol does not exist.

Nothing run locally could have caught it, and that is the point: every local check was `GOOS=linux`. `make ci` and `make push-gate` both vet for the host only. The three-OS matrix in `.github/workflows/ci.yml` exists precisely for this, and its own comment cites FAILURE_PATTERNS 51, the platform-suffix trap, as the reason it stays. It caught this on the first push.

**Fix.** `setupEnv` moved to `cmd/controller/childenv_test.go`, which carries no build constraint, with a doc comment recording why it is not beside the gate that used to own it. The refusal gate stays unconstrained and now compiles everywhere. Verified across six combinations before pushing again: `GOOS` of linux, windows and darwin, each with the default tags and with `-tags integration`.

**Lesson.** Before calling a Go change verified, vet it for every operating system CI builds, not only the host: `GOOS=windows go vet ./...` and `GOOS=darwin go vet ./...`, under both tag sets, takes seconds. And when reaching for a helper in another test file, read that file's FIRST LINE. A build constraint on a file is a constraint on every symbol in it, including the ones that have no reason to be platform-specific and are only there because a neighbor needed a terminal.

## 304. A configuration file repeated a flag the deployment already passed, and the broker exited at boot

**Symptom.** Found while giving every NATS test container the deployment's own flag list in Phase 101c. Two Release Gates that had passed for weeks began failing with `starting an operator-mode broker: container exited with code 1`, which names nothing. The broker's own log, once the start helper was made to print it, said: `nats-server: /etc/nats/nats.conf:10:3: Duplicate 'store_dir' configuration`.

**Root cause.** The gate's configuration file carried `jetstream: { store_dir: /data }` because its broker had been started with `-c` ALONE, replacing the flags rather than adding to them. Once the shared helper passed `NATSCommand()` as well, the flag list already carried `-sd /data` and nats-server 2.14.4 refused the file for stating it twice. A configuration file and a flag list are additive for settings that appear in only one of them and FATAL for a setting that appears in both, which is not how "additive" reads.

**Fix.** The block was deleted from the gate's configuration, and the rule is now stated where it will be read: a setting expressible as a flag belongs in the flag list and nowhere else, so a configuration file carries only what nats-server accepts from nowhere else. The same rule is written into the Helm chart's `nats-config.yaml`, `controller mesh init`'s generated output and `testsupport.WithNATSConfig`'s doc comment, because all three now emit such a file. It was also the independent confirmation of a design decision already taken for the chart on other grounds.

**Lesson.** See `LESSONS_LEARNED.md` #221.

## 305. A shared test helper's cleanup ran after the test's own defers, so a leak check inspected a live container

**Symptom.** Migrating 33 container starts onto one helper turned `internal/election`'s three-replica gate red with `found unexpected goroutines`, naming testcontainers' OWN reaper connection. That is the exact signature `flaky-packages.json` already records for a different package, so the obvious reading was the known flake. Running the UNMIGRATED file proved otherwise: it passed, every time.

**Root cause.** The test opens with `defer goleak.VerifyNone(t)`, and the original terminated its container with a `defer` registered LATER. Deferred calls run last-in-first-out, so termination happened first, entirely by accident and with nothing written down about it. A shared helper cannot use `defer`, because it returns; it must register cleanup with `tb.Cleanup`, which runs after every defer in the test. So the leak check began running while the container, and the reaper connection it holds open, were still alive.

**Fix.** The test registers `t.Cleanup(func() { goleak.VerifyNone(t) })` BEFORE starting the broker, so cleanup's own last-added-first-called order runs it after termination. `cmd/controller`'s leader election gate already had exactly this shape with a comment explaining it; `internal/election` now matches. The comment records why the old form worked, so the next person does not restore it.

**Lesson.** See `LESSONS_LEARNED.md` #221.

## 306. An assertion that a key was absent from the database searched for plaintext the schema always encrypts

**Symptom.** A Release Gate for `controller mesh init` asserted that the operator key is never stored, by reading the SQLite file and searching it for the key's bytes. It passed. Falsifying it, by planting a deliberate leak, ALSO passed.

**Root cause.** The column is sealed by the envelope hook before it is written, so the plaintext seed never appears in the file whether or not the key was stored. The assertion was searching for something that can never be there. It was a test that could not fail, and it was guarding the single most important property in the phase: that a Controller compromise is an account compromise rather than a mesh compromise.

**Fix.** The assertion is now structural rather than textual. nkeys prefixes a public key by its kind, `O` for an operator and `A` for an account, and the public key is stored beside the sealed seed precisely so questions about it need no decryption. So the gate asserts every stored row names an account-kind key and none names the operator's, which is visible however the seed is encrypted. Re-falsified: the planted leak now fails with both messages. The same attempt also revealed a real protection nobody had designed deliberately, `meshkey.Save` refusing a non-account seed because it derives the public key through a kind check, now pinned by its own test.

**Lesson.** See `LESSONS_LEARNED.md` #222.

## 307. A signature-forgery test flipped base64 padding bits again, in a different package, and caught a forgery that had never been forged

**Symptom.** Phase 101c's `TestReleaseGate_ACredentialEditedAfterSigningIsRefused` passed when written and passed on several runs afterwards, then failed under a `-race` sweep with "a credential edited after signing was ACCEPTED; the signature chain is not being checked". The failing subtest was always `the signature is altered`, and it failed in 0.00s where a passing run takes 0.27s, which is the shape of a connection that succeeded immediately rather than one that was refused. Run five more times it failed roughly one run in three.

**Root cause.** This is `FAILURE_PATTERNS.md` #75 recurring in a different package, and the mechanism is the one that entry names: the test tampered with base64 PADDING BITS rather than with the signature. The helper flipped the LAST character of a segment. An Ed25519 signature is 64 bytes, which base64url encodes as 86 characters carrying 516 bits, so the final character holds four bits that decode to nothing at all. Flipping it frequently leaves the decoded signature byte for byte identical, so the broker was handed a credential nobody had actually altered and correctly accepted it. The test then reported a security hole that did not exist, intermittently, which is the worst available combination: it is alarming, it is not reproducible on demand, and the alarming reading is wrong.

**Fix.** Flip the FIRST character instead. Its six bits are always significant, in every segment, whatever the segment's length. Five consecutive runs green, where the previous version failed about one in three. The reason is written into the helper's own doc comment with a pointer to #75, because the broken version and the correct version differ by one index and look equally reasonable.

**Lesson.** See `LESSONS_LEARNED.md` #224.

## 308. Two WAL tests leaked a NATS bus each, and a durability wait was bounded as if it were a performance claim

**Symptom.** `make ci` failed with `TestAgent_ReportResult_SuccessfulExecutionFlushesToWAL` and three siblings in `internal/runner` timing out together at exactly ten seconds, the output full of `connect: connection refused` for ports no running test owned. The package passed alone in twenty five seconds. It is listed in `flaky-packages.json`, and the first instinct recorded in this session was to tolerate it as contention.

**Root cause.** Two separate defects. `agent_nats_test.go` created a NATS bus twice and closed neither, and `topology.DialOptions` sets `MaxReconnects(-1)` on purpose, so each went on dialling a container that had died at the end of its test, for the rest of the package run. And the WAL path those four tests wait on fsyncs twice, in `fileWAL.Append` and in the temp file rewrite `Acknowledge` performs; on an idle disk that is milliseconds, and under `make test-race` beside twenty seven container packages it is not, so a ten second wall clock bound was asserting a speed none of the tests meant to assert.

**Fix.** Both buses are closed with `defer`, so the client closes before `StartNATS` terminates the server in its `t.Cleanup`. The bound is `agentSettleTimeout`, sixty seconds, named once and used at all twenty one call sites with the margin written down. The wait stopped being a two millisecond spin.

**Lesson.** The user's instruction was "I don't care whose it is, we fix it", and it was right: a package on the flake list got no protection from a real defect, which is exactly what CLAUDE.md warns the list can become. Two things measured along the way are worth keeping. `GOMAXPROCS=1` reproduced nothing, which ruled out the first theory (CPU starvation) in seconds rather than by argument. And the falsification of the bus fix was INCONCLUSIVE, since an isolated run ends before a leaked bus logs anything, so the fix was kept for being correct rather than claimed as proven.

## 309. `FAILURE_PATTERNS.md` #301 fixed, and the compensating assertion written with it compared a map with itself

**Symptom.** `make ci` failed `TestLineChecks_PredictWhatARealRunLeaves` with `predicted mtime = 1790183117, the run left 1790183118`, which is #301, recorded eight days earlier and deliberately left because the branch that found it did not own the package.

**Root cause.** #294's, applied here: the check acts on `checkPath` and the run on `runPath`, written a moment apart, so for a run that changes nothing the prediction reports one file's mtime and the run leaves another's. Equality held only when both writes landed in the same second.

**Fix.** mtime is compared as a bound, #294's form: the run's no earlier than the check's and at most a minute later. Every other field is still compared exactly.

**The part worth reading.** Relaxing an equality needs something to keep holding the property the equality stood for, here "a run that changes nothing does not touch the file". The first version compared the run's own recorded before and after, and a deliberate fault walked straight past it: `sdk.Unchanged(state)` returns `Diff{Before: state, After: state}`, the SAME map for both halves, so the "after" of a no-op is never observed at all. It is the "before" reused. The assertion now stats the file on disk before and after the run, and the same fault fails it. Four controls, each by a temporary edit: the run one second later passes, five seconds earlier fails, a changed non-clock field fails, and a no-op that touched its file fails.

**Lesson.** See `LESSONS_LEARNED.md` #226.

## 310. Every postgres and toxiproxy container waited for readiness under a sixty second deadline nobody chose

**Symptom.** `make ci` failed `internal/backup`'s `TestRestore_ACutConnectionChangesNothingAndTheNextRunCleansUp` with `wait until ready: external check ... get state ... context deadline exceeded` after 561 polls and exactly sixty seconds. It passed alone. The previous session's handoff had classified the same failure as contention.

**Root cause.** testcontainers' `WithWaitStrategy` and `WithAdditionalWaitStrategy` both build `wait.ForAll(...).WithDeadline(60 seconds)`, and the postgres module's `BasicWaitStrategies` is built on the second. So all thirteen postgres starts and all eight toxiproxy starts in the repository waited under a hardcoded minute, while every NATS start waited under `testsupport.ContainerStartupTimeout`, two. Under full load the readiness loop polls a slow Docker daemon and the shorter one runs out. `flaky-packages.json` had already named this defect for `tests/e2e`'s chaos harness and said "fixable; fix it and remove this name".

**Fix.** `testsupport.PostgresReady` and `ToxiproxyReady` restate each module's own readiness condition under `ContainerStartupTimeout`, and all twenty one sites use them. `TestNoContainerWaitsUnderTheLibraryDeadline` fails if any site calls `BasicWaitStrategies` or starts toxiproxy without the bound; `TestTheLibraryDefaultIsTheDefect` pins the upstream sixty seconds so a dependency bump that removes it is noticed. `TestChaos_PostgresSeverance` came off `flaky-packages.json`.

**Why they REPLACE rather than wrap.** Wrapping the module's strategy in a longer outer deadline is the obvious fix and does nothing: a `ForAll` with a deadline runs its children under `context.WithTimeout` of that deadline, so the inner sixty seconds still fires first. The deadline field is unexported, which is why the tests read it through reflection rather than waiting two minutes for a container that never becomes ready.

**Lesson.** See `LESSONS_LEARNED.md` #225.

## 311. The roadmap's own "what to build next" ranked on the phase number and contradicted the release beside it

**Symptom.** The dashboard printed `Phase 35: Ansible Playbook Migration v0.3.0` at position `#5`,
directly after `Phase 28: The Notification Engine v0.5.0` at `#4`. Two numbers the same row shows,
saying opposite things about what to build first. Measured across the queue, 46 of the 67 unfinished
phases sat at a release the order had already passed.

**Root cause.** `todo_order()` in `.SPECIFICATION/implementation.py` is a topological sort over the
`**Depends on:**` keys, and its `rank()` was `(in progress before not started, phase number)`. The
release a phase ships in was never a term. So the queue opened with Phases 24, 26, 27 and 28, the
whole v0.5.0 AWX parity block, for no reason except that 24 is the lowest-numbered phase nothing
blocks. The `**Version:**` keys themselves were never wrong: all 124 parse, and no phase ships before
a phase it depends on.

A second fault hid inside the first. The cycle-breaking branch searched the entire pending set, so it
could only fire once everything else was placed. Phases 75 and 76 depend on each other and are
therefore never "available", which pushed both past Phase 100 at v1.3.0, five releases beyond where
they belong.

**Fix.** `rank()` takes the release first, through a new `version_key()` over a new `RELEASE_RE`. The
cycle break is scoped to the lowest release still pending, so a cycle inside one release is broken
where it lives. 46 regressions became 0, and Phase 75 is now cycle-broken at position 46 inside its
own v0.7.0 band. Nothing else moved: no version changed, no phase moved in the file, no phase split.

**What made it invisible.** Both numbers were correct in isolation, and each had its own author. The
contradiction existed only in their juxtaposition, which no check looked at because nothing read the
two together. `summary.version_problems` now does, and the suggested-order check is the one entry in
it with a real before and after.

**Lesson.** See `LESSONS_LEARNED.md` #227.

## 312. Every Collection manifest required an engine version no release before 1.0.0 could have met

**Symptom.** None yet, which is the point: it would have appeared in full on the first stamped release
build and not one moment earlier. All 75 built-in manifests, `catalogdata`'s shared constant, and
`collectionscaffold.DefaultEngineVersion` declared `EngineVersion: ">=1.0.0"`, published in 81 entries
of `docs/reference/schemas/module-catalog.json`. The roadmap reaches v1.0.0 at Phase 106d, and the
first release is v0.2.0, one phase from done.

**Root cause.** `internal/loader/version.go`'s `checkEngineVersion` refuses a method whose minimum the
running build does not meet, but it cannot compare against a development build: every unstamped build
reports `0.0.0-dev`, so the constraint was reported unchecked and loaded with a warning. Nothing in
the repository is a release build, so nothing ever ran the comparison. Proven after the fact with a
throwaway test: `checkEngineVersion(">=1.0.0", "0.2.0")` returns `requires engine >=1.0.0, and this
build is 0.2.0`.

The value was documented as a placeholder in both places that declared it, which is what kept it
alive. `internal/forge/externalscaffold/config.go` had already reasoned its way to the correct rule
for the programs it generates, and said in its own comment that a fixed guess "such as >=1.0.0 would
be refused by the first release that did not meet it". Two scaffolds in one package tree disagreed,
and one of them explained why the other was wrong.

**Fix.** `buildinfo.CurrentRelease` is the one declared release line this tree is on.
`collectionscaffold.DefaultEngineVersion` is `">=" + buildinfo.CurrentRelease`, `catalogdata` refers
to that rather than restating it, and the 75 emitted manifests were rewritten to what a fresh
generation now produces. `TestDefaultEngineVersionLoadsOnTheCurrentRelease` runs the real unexported
comparison against a stamped release, which is the only way to ask the question before such a build
exists.

**Lesson.** See `LESSONS_LEARNED.md` #228.

## 313. A containment check that trusted SFTP's REALPATH would have passed the symlink escape it existed to catch

**Symptom.** None shipped; caught while building Phase 77. The approved design resolved a transfer's
parent directory with the server's `REALPATH` and compared the answer with the root, on the
assumption that `REALPATH` follows symlinks, as OpenSSH's does.

**Root cause.** `REALPATH` returns whatever the server chooses to canonicalize to. `github.com/pkg/sftp`'s
own server answers it with `filepath.Abs` plus a lexical clean (`server.go`, the `sshFxpRealpathPacket`
case), and its request server does the same by default (`cleanPathWithBase`). Any SFTP server built on
that library would have returned the lexical path for `root/link/f` with `link` pointing out of the
root, the comparison would have passed, and the write would have followed the link. The check would
have been green against OpenSSH, the one server the release gate runs, and open against the others.

**Fix.** `pkg/sftpxfer/confine.go` resolves the path on the client: `LSTAT` on each component without
following it, `READLINK` to follow a symlink itself, `..` applied to the physical directory reached so
far, bounded at 40 hops. The answer is built from the device's filesystem, never from the server's
canonicalization. `TestConfine_PhysicalEscapesAreRefusedBeforeAnyContent` runs against pkg/sftp's own
server, which is exactly the server that would have fooled the first design, and
`TestSFTPReleaseGate/PhysicalEscapeIsRefusedBeforeAnyContent` against OpenSSH. Falsified: with the
containment comparison disabled the gate fails and `Get` reads the planted key back out.

**Lesson.** See `LESSONS_LEARNED.md` #229.

## 314. remoteexectest ended a session when the client closed its input, not when the command exited

**Symptom.** Three `pkg/scpxfer` tests hung until the test binary's deadline: a Put under a missing
root, one under a missing parent, and one into a read-only directory. Each device script exited
early, by design, and the client waited forever for its output to end.

**Root cause.** The in-process SSH server set `cmd.Stdin = channel`. With a non-file standard input,
`os/exec`'s `Wait` also waits for its own copy of that input to reach end-of-file, so a command that
exited without reading its input left the session open until the client closed its side. OpenSSH's
sshd ends the session when the command exits. The harness differed from the server it stands in
for, in the exact case a protocol that refuses early exercises.

**Fix.** `serveSession` now passes standard input through `cmd.StdinPipe()`, which `Wait` closes once
the command exits, so the session ends when the command does. All 32 packages whose tests use the
harness pass under `-race -short` afterward. Separately, `pkg/scpxfer`'s `receive` closes its input
once it has nothing more to send, so a server that does wait for input end-of-file cannot deadlock it
either.

**Lesson.** See `LESSONS_LEARNED.md` #231.

## 315. The unreachable-capability allowlist's staleness guard needed both conditions, so dead entries lived on

**Symptom.** `acceptedUnreachableCapabilities` held two entries that `TestRegisteredCapabilitiesAreReachable`
never consulted. RFC2217Capable's had been dead since `console_device` began satisfying it, and its own
text said so ("guards only the no-consumer half"). FileTransferCapable's became dead the moment
`linux.Server` gained `FileTransferRoot`. Meanwhile `FileTransferCapable`'s doc comment said the guard
"fails if this comment ever stops being true in either direction", and cited a disclosure of SFTP in
docs/10 that had never been written.

**Root cause.** An allowlisted capability excuses one that is neither satisfiable nor required, so the
entry is dead as soon as EITHER becomes true, because `unreachableCapabilities` skips a satisfiable or
required capability before it reads the allowlist. `TestAcceptedUnreachableCapabilitiesAreNotStale`
checked `satisfiable && required`. The doc's claim and its citation were never checked by anything.

**Fix.** The guard is `staleUnreachableEntries`, which flags `satisfiable || required`, with a negative
control (`TestStaleUnreachableEntriesDetectsEitherDirection`). Both entries were removed; the
allowlist is empty and says why. Falsified: restoring the two entries makes the guard name both. The
capability doc was rewritten to what is true, and docs/10 now carries the SFTP and SCP disclosure it
cites.

**Lesson.** A guard whose condition is a conjunction should be asked what it misses when only one half
holds; see also #228 for a doc comment asserting a check that did not exist.

## 316. The generated device reference could not say a hand-written type's capability was conditional

**Symptom.** `docs/reference/devices.md` never listed `cisco_router`'s `NetconfCapable`, which the type
declares whenever `netconf_enabled` is true.

**Root cause.** `tools/gendocs`' `handWrittenDevices` table recorded one capability list per type, and
`TestHandWrittenDeviceCapabilitiesMatchTheirTypes` required it to equal what a bare record hydrates
with. A capability a property enables could not satisfy both, so it was left out. Generated types
already had the asterisk marking; hand-written ones could not reach it.

**Fix.** The table gained `Conditional`, naming the property that enables each such capability, and
the test checks both directions: absent from a bare record, present once the property is set.
`cisco_router` gained `NetconfCapable`, and `linux_server` gained `FileTransferCapable`, both marked
conditional, in the same change.

**Lesson.** When a completeness test compares against one fixed input, ask what the input cannot
express.

## 317. pkg/tftpxfer passes NUL and over-long remote filenames to pin/tftp, which injects the first into the request and panics on the second

**Symptom.** Found by reading while designing Phase 77's path guard; not fixed, because whether and
how is the user's decision. `validateFilename` (`pkg/tftpxfer/tftpxfer.go`) refuses `..`, absolute
paths and drive letters but not NUL. `pin/tftp` v3.2.0's `packRQ` copies the filename verbatim ahead
of its own NUL terminator, so a name like `x\x00netascii` would inject a transfer mode or option into
the request packet. `FuzzValidateFilename`'s doc says it proves every seeded traversal "is refused",
while its body discards the return value.

**Measured afterward, and worse (2026-09-23).** `packRQ` builds the request in a 516-byte buffer
(`datagramLength`), copies the filename into `p[2:len(p)-10]`, then writes the mode, each option and
their NUL terminators with unchecked indexes. A throwaway probe calling `tftpxfer.Get` with
`Options.BlockSize` set panicked with `index out of range [516] with length 516` for every filename
length tried from 495 bytes up (400 did not). Nothing recovers it, so a caller handing a runbook-sized
filename to this package would crash its whole process. Without `BlockSize` there is no option to
overrun, and a name over 504 bytes is instead silently truncated, so a different file is requested.
`pin/tftp` v3.2.0 is its newest release.

**Found while fixing it (2026-09-23).** Three more things, each read in the library's source first:

1. `Options.BlockSize` was passed through unchecked, and its decimal digits share the same 516-byte
   buffer, so a 493-byte cap alone would not hold: a six-digit block size overruns it again.
2. `pin/tftp`'s client ignores a server's `blksize` answer below 512 (`setBlockSize` fails and the
   loop `continue`s) and keeps its 512-byte buffer, so the server's first smaller block reads as the
   final short one. Measured with a throwaway probe: a server that answered a request for 1024 with
   256, which RFC 2348 allows, made `Get` return 256 bytes of a 1024-byte file with a nil error.
3. `TestGet_RefusesPathTraversalFilenames` and its `Put` twin said a server would catch a name that
   got through, but they dialed port 1 with no server listening, so a network error also passed them.

**Root cause.** The guard was written against path traversal only, and the fuzz target was written to
catch panics only; its doc claimed more. Neither asked what the request packet itself can hold, and
nothing asked which other inputs share that packet.

**Fix.** Applied on 2026-09-23 as its own commit. `validateFilename` (now `pkg/tftpxfer/filename.go`)
refuses control and format characters, invalid UTF-8 and names over `MaxFilenameBytes` (493, derived
in its doc comment), with every refusal wrapping `ErrInvalidFilename`. `Options.BlockSize` must be 0
or from `MinBlockSize` (512) to `MaxBlockSize` (65464). `Get` and `Put` recover a panic inside the
library into an error, while a panic in the caller's own reader or writer is raised again unchanged
(`pkg/tftpxfer/panic.go`). `FuzzValidateFilename` now asserts properties of both verdicts, and
`TestRefusedFilenamesNeverLeaveTheProcess` proves each refusal sends no datagram, over a real UDP
socket. Eight mutations, each reverting one guard, were each killed by the test written for it.
Not fixed: a server may still answer a request of 512 or more with a smaller size and truncate a
download the same way as item 2; that needs a design change and is the user's decision.

**Lesson.** A fuzz target's doc must say what it asserts, and a guard for a wire format must be written
against that format's own delimiters, not only against the filesystem's. When a fix bounds one input
into a fixed buffer, bound every input that shares the buffer, or the measured bound does not hold.

## 318. A time-bounded fuzz run froze its execution counter while minimizing, and read like a finished one

**Symptom.** `FuzzResolve` ran at about 200,000 executions a second for 15 seconds, then reported the
same count, 2,088,252, for the remaining 45, and passed. Memory was flat at 138 MiB, so it was not the
cgroup cap. A second target (`FuzzContained`) ended its 60 seconds with `--- FAIL ... context deadline
exceeded` and no failing input saved.

**Root cause.** Go's fuzzer minimizes each new interesting input for up to `-fuzzminimizetime`
(default 60 seconds) and does not count those executions, so the counter freezes. When the run's time
budget expires during a minimization, the run can end as a failure with no crasher.

**Fix.** Run with `-fuzzminimizetime 2s` (FuzzResolve then reached 7,841,805 executions in 45 seconds),
and bound a run by count (`-fuzztime 10000000x`) when a clean pass is the evidence wanted (FuzzContained
then passed at exactly 10,000,000).

**Lesson.** See `LESSONS_LEARNED.md` #232.

## 319. A known_hosts fixture naming one host key type refused every connection as a mismatch

**Symptom.** Every connection in the first SFTP release gate run failed with `knownhosts: key mismatch`,
then with `EOF` once the server began penalizing the failed handshakes.

**Root cause.** `testsupport.SSHD.KnownHosts` wrote only the container's ed25519 key. The client and
server negotiated a different host key algorithm, and `knownhosts` treats a host known under one key
type as a mismatch, not as unknown, when another is presented. Verification failed closed, exactly as
it should.

**Fix.** The fixture lists every one of the container's host keys under every address a test dials.

**Lesson.** When a test pins host keys, pin all of the server's key types, or pin the algorithm too.

## 320. The Runner's heartbeat self-aborts on the first failed KeepAlive, though its comment says one missed tick is tolerated

**Symptom.** Found by reading the source on 2026-09-23 while writing Phases 107a to 107c, not by a
failing run. An interruptible execution is cancelled the first time one lease refresh fails, even with
most of the lease still left. At the defaults (`execLeaseTTL` 5 minutes, `heartbeatInterval` 1 minute),
one broker blip at the first tick aborts a run that had four minutes of valid lease remaining. On a
disrupted link, where blips are the normal case, that turns every short outage into an aborted run.

**Root cause.** `heartbeatInterval`'s doc comment (`internal/runner/agent_exec.go`) says the roughly 1:4
ratio to the lease TTL is there "so a single missed tick is never mistaken for a genuine, sustained
heartbeat loss". `Agent.heartbeat` does not implement that: on an interruptible run it logs "lost device
lease heartbeat, self-aborting execution" and cancels on the first `KeepAlive` error. Nothing below it
retries either. `natsLease.KeepAlive` (`internal/lock/nats.go`), in exclusive mode, makes one
`publishWithTTL` call, which is one `PublishMsg` with no retry. The only test,
`TestAgent_SelfAbort_InterruptibleCancelsExecution`, uses a lease whose `KeepAlive` fails every time, so
it cannot tell "aborts on the first miss" from "aborts on sustained loss" and passes either way.
Separately, `execLeaseTTL`'s comment still says `native.Adapter.Execute` "is still simulated", which has
been false since Phase 16.

**Fix.** Not fixed; recorded only. Fix it when the Runner's lease handling is next touched (Phase 103b
moves `execLeaseTTL` into `internal/topology` as `DeviceLeaseTTL`, so that is the natural moment).
Self-abort when the lease's own local deadline is about to pass with no successful refresh, not on the
first error, so a miss is tolerated for as long as the lease is still valid. Add a test whose
`KeepAlive` fails exactly once and then succeeds, and assert the run completes; keep the always-failing
test to show sustained loss still aborts. Correct the stale "still simulated" sentence in the same
change. The device agent's execution-space mode (Phase 107b) does not use this lease at all, so it
neither inherits nor fixes the defect.

**Lesson.** A comment that states a tolerance ("a single missed tick is never mistaken for...") is a
claim, and it needs a test that exercises exactly that tolerance: one failure, then recovery. A test
that only fails every time proves the alarm is wired, not that it is calibrated.

## 321. import_tasks read a file outside the runbook's directory through a symlink

**Symptom.** Found by reading while planning Phase 35's translator, then measured. `resolveImportPath`
(`internal/engine/import_tasks.go`) checked the imported path's text with `filepath.Rel`, and
`resolveOneImport` then read it with `os.ReadFile`, which follows symlinks. A `sub.yaml` inside the
runbook's directory that was a symlink to a file elsewhere was imported and built. Reached through
`pleiades run` and `pleiades validate` (`BuildFromYAMLFile`); the Walk tier builds with `BuildFromYAML`
and refuses `import_tasks` outright, so it was not reachable there.

**Security.** An author who can write into a runbook directory could have any file the CLI user can read
parsed as a task list, with parse errors quoting pieces of it back. That needs write access to the
project directory. Our own code, so no upstream fix applies. Measured: with the old read restored,
`TestImportTasks_StrictKeysAndContainment` fails because the escaped import builds.

**Root cause.** A containment check on a path's text cannot see what the filesystem will resolve it to.

**Fix.** `readInsideDir` opens the runbook's directory with `os.OpenRoot` and reads through it, which
refuses any path, symlinks included, that resolves outside. The lexical check stays for its clearer
messages. Applied 2026-09-24 in Phase 35.

**Lesson.** The same one #313 recorded for SFTP: containment is a property of how a file is opened, not
of how its name reads.

**Class.** C4 (path traversal / confinement escape; CWE-59, symlink following)
**Portable.** yes: any containment check made on a path's text and then followed by an open that follows symlinks
**Detector.** fuzz corpus C4 (symlink seeds) + semgrep join-then-open-tainted (P4); see ~/vuln-corpus/README.md

## 322. The Walk tier ran a dispatched runbook without plan-time validation

**Symptom.** Found by reading while designing Phase 35's placeholder for an unconverted task. The native
adapter (`internal/adapters/native/adapter.go`) went from `GetDAG` straight to the executor.
`validate.Validate` ran only in `cmd/pleiades` (`validate.go`, `run.go`). So on the Walk tier a task
calling an unregistered or declared-only method, passing an undeclared parameter, or asking for
`check_mode` on a method that cannot check failed only when that task was reached, after every earlier
task had already run against the device.

**Security.** No special access is needed to cause it; the harm is a half-applied change on a managed
device. Our own code. Measured on 2026-09-24: with the new check removed,
`TestValidateDispatchReleaseGate_NoTaskRunsBeforeARefusal` (`cmd/runner`), over a real NATS broker and a
real sshd container, finds the first task's marker file on the device after a dispatch whose second task
calls the declared-only `file.template`.

**Root cause.** The validation core was built as a CLI feature (Phase W3) and documented as the one core
the IDE and the backend would adopt later. The backend never did.

**Fix.** Phase 35 (commit a2): the adapter runs every registered rule before building the executor
(`validateDispatch`, `internal/adapters/native/validate.go`), against the one device the dispatch names,
with `WorldView.Resolver` set to the same `singleDeviceResolver` the executor gets, so a task with no
target is checked against that device exactly as it will run. A refusal publishes a failed job event
naming each finding. Proven by `TestExecute_RefusesARunbookThatFailsValidation` (four kinds of finding,
each killed by removing the check) and by the release gate above, which passes with the check in place
and fails, finding the marker, without it.

**Lesson.** A check that runs in one tier and not another is a check the other tier does not have.
Put plan-time validation where execution starts, in every tier that starts it.

**Class.** C10 (dead or unwired control; CWE-693)
**Portable.** yes: a validation core called by one entry point (the CLI) and skipped by another (a worker) that runs the same input
**Detector.** a test that the control fires on every execution path, not only that it exists; see ~/vuln-corpus/README.md

## 323. net.netconf.config's target parameter is also the engine's device selector

**Symptom.** Found by reading while planning Phase 35's module map; not fixed, by the user's decision.
`net.netconf.config` names its datastore parameter `target` (running, candidate or startup), after
`ansible.netcommon.netconf_config`. The engine reads `params.target` as the host or tag a task runs
against (`engine.TaskTarget`, `internal/engine/action.go`), resolves it in `resolveDevices`
(`executor.go`), and the method then reads the same key as its datastore
(`internal/catalog/net/netconf/config.go`). The unit tests call the method directly and skip the engine,
so nothing caught it. `journal_entry.go`'s comment that no method declares `target` is also wrong.

**Security.** On the CLI, `target: candidate` fails with "matches no inventory host or tag", or, if a
device or tag is named `candidate`, configures that device instead. Exploiting it needs inventory write
access. The Walk tier's resolver ignores targets, so it is not affected there. Reasoned from the code, not
run.

**Root cause.** A reserved key (the device selector) lives inside the free-form `params` map every method
also owns.

**Fix.** Not applied. The user chose to move the device selector out of `params` into a reserved task key,
as its own later phase. Until then Phase 35's translator blocks every `netconf_config` task that sets
`target`, naming this collision.

**Lesson.** A key the engine reserves must not share a namespace with keys third parties choose.

**Class.** none of C1 to C10 yet; a candidate: a reserved control key living inside a caller-owned map
**Portable.** probably: any engine that reads one of its own keys out of a free-form parameter map every plugin also writes to
**Detector.** not built; statically, list the keys an engine reads from a shared map and intersect them with every plugin's declared keys; see ~/vuln-corpus/README.md

## 324. Methods silently ignored parameters they do not declare, and two shipped examples passed five

**Symptom.** Found building Phase 35's parameter rule. Nothing compared a task's `params` with its
method's declared parameters, so a method ignored any key it does not read. Both `upgrade_ios` example
runbooks passed `prompt`, `answer`, `check_all` and `sendonly` to `net.cli.command` and `save_when` to
`net.ios.config`, copied from the Ansible playbook. None was read, so the image copy and the reload could
never answer the device's prompts, and the example's README still said those methods were declared but
not implemented. The `writingcheck` external fixture (`cmd/pleiades/testdata`) read a `path` parameter it
did not declare.

**Security.** A misspelled optional guard runs with its default: `creats:` for `exec.command`'s
`creates:` runs the command every time. No special access needed; mainly a safety defect. Measured on the
two examples.

**Root cause.** `Doc.Params` was treated as documentation only, and the journal's comment called it
"partial by construction", so nothing enforced it.

**Fix.** `ParamsRule` (`internal/validate/params_rule.go`) refuses a parameter the method declares
neither in `Doc.Params` nor through a named fragment, except the engine's own `target`. The shared
fragment definitions moved from `internal/forge/catalogdata` to the leaf package
`internal/catalog/fragment`, since the former imports the whole data layer. The examples now pass only
declared parameters and say in comments which steps cannot answer a prompt; the save is its own
`net.ios.save` task. The fixture declares `path`. Proven by `TestParamsRule`,
`TestParamsRule_UndocumentedExternalMethod` and `TestExamples_EveryRunbookBuildsAndValidates`, which runs
the real binary over every shipped example.

**Lesson.** A declaration nothing checks decays into a comment. Once a method declares its parameters,
refuse the ones it does not declare.

**Class.** the silently-dropped-fields class (the unnumbered note after C10; CWE-1284-adjacent input that is accepted and ignored)
**Portable.** yes: any plugin API where a caller's argument map is read by key and unknown keys are ignored
**Detector.** compare each call's keys with the callee's declared parameters (here, ParamsRule); see ~/vuln-corpus/README.md

## 325. A JSON runbook accepted keys in the wrong case and repeated keys

**Symptom.** Found by reading while designing Phase 35's strict decoding. `Builder.Build` decoded with
`json.Unmarshal`, which matches a key to a field without regard to case (`{"FQCN": ...}` filled `fqcn`)
and keeps only the last of two repeated keys. `normalizeWorkflowJSON` also decoded into a map first, which
collapsed repeats before anything could see them.

**Security.** A reviewer reading the first of two `fqcn` keys would approve one method while another ran.
It needs something to feed JSON runbooks in; `Build` has no production caller today, so it was not
reachable. Reasoned.

**Root cause.** encoding/json's forgiving defaults, trusted for an input that must mean exactly what it
says.

**Fix.** `parseJSONKeyTree` (`internal/engine/json_strict.go`) reads the payload token by token, refusing
a repeated key anywhere and nesting past 512 levels, before the map-based rewrite runs; the key walker
then checks exact-case keys; and the decode itself uses `DisallowUnknownFields` and refuses trailing
data. Proven by `TestStrictKeys_JSONExactCaseAndDuplicates`.

**Lesson.** A decoder's defaults are tuned for convenience. For a document that says what will run,
choose the strict reading on purpose.

**Class.** the silently-dropped-fields class (the unnumbered note after C10), JSON parser-differential form (CWE-436)
**Portable.** yes: any Go service decoding a security-relevant JSON document with encoding/json's case-insensitive, last-key-wins defaults
**Detector.** fuzz with duplicated and case-varied keys and assert refusal; statically flag json.Unmarshal into a policy or plan struct; see ~/vuln-corpus/README.md

## 326. Three user-visible strings cited PLAN.md, and docs-lint could not see any of them

**Symptom.** The runbook engine's refusal of an Ansible playbook (`internal/engine/yaml.go`) and of
`type: ansible` (`dag.go`) told the user to read "PLAN.md Section 23", `pleiades forge new-filter`'s
`--category` help cited "PLAN.md Section 36", and a managed credential type's detail cited "PLAN.md
Section 17.4". The plugin scaffold template also wrote a PLAN.md citation into every generated sync
plugin. PLAN.md never ships.

**Security.** None: this exposes nothing. It is a broken promise to the reader, recorded because the
check meant to catch it could not.

**Root cause.** `tools/docs-lint` scans documentation and a fixed list of Go files whose whole text a user
reads. An error message or a flag's help text in any other Go file was outside that list, and a plain
text scan of every Go file would drown in legitimate comment citations.

**Fix.** A second docs-lint pass (`tools/docs-lint/golits.go`) parses every non-test Go file under `cmd/`,
`internal/` and `pkg/` and checks its string literals alone, never its comments. All five strings were
reworded; the two engine errors now point at `docs/03-migrating-from-ansible.md`. Proven by
`TestScanGoLiterals` and a clean run over 1,095 files.

**Lesson.** A check that enumerates its targets misses the next file nobody listed. Where the forbidden
thing has a syntactic shape (a string literal), check the shape everywhere.

## 327. A condition holding `{#` inside a string literal was refused as Jinja

**Symptom.** Found by `FuzzWhenToCEL` in Phase 35. The condition translator refused any `when:` whose
text contained `{{`, `{%` or `{#` before tokenizing, so `probe.stdout == 'a{#b'` was reported as a
template (`when.unsupported`) although the delimiter sat inside a string literal.

**Root cause.** A whole-string precheck for Jinja delimiters cannot tell text inside a literal from
syntax outside one.

**Fix.** The precheck was removed; the tokenizer refuses `{` only where it reads syntax, outside
strings. The fuzzer's seed is kept in `internal/forge/playbook/testdata/fuzz/FuzzWhenToCEL`.

**Lesson.** A refusal made before parsing has to be as precise as the parser, or it refuses valid
input. It failed safe here, which is why the fuzzer, not a user, found it.

## 328. The free-form argument splitter turned invalid UTF-8 into U+FFFD

**Symptom.** Found by `FuzzKVArgs` in Phase 35. `splitArgs` walked free-form arguments
(`command: echo x`) as runes, so an invalid byte came back as the replacement character and a command's
text changed without any error. Not reachable through a playbook today, because go.yaml.in/yaml/v3
refuses invalid UTF-8 first.

**Root cause.** Converting to runes is lossy on invalid input, and the round trip was never checked.

**Fix.** `splitArgs` refuses invalid UTF-8 (`internal/forge/playbook/kv.go`); the fuzz property that a
command's text survives a split and join is what caught it.

**Lesson.** A function that must preserve text should be tested for exactly that property, with bytes
no well-behaved caller would send.

## 329. The migration report printed raw playbook file names, and runbook names came from them

**Symptom.** Found in Phase 35 when `TestEmit_CommentInjection` was tightened to assert that no raw
escape character reaches the report or the written runbook. `Position.String` printed a playbook's file
name as it was, so a name holding an escape sequence reached the terminal through the text report, and
the output runbook's file name was derived from the raw playbook name.

**Security.** A person converting a playbook from someone else could have their terminal driven by a
file name. Our own code, found before release.

**Root cause.** File names were treated as ours, when they come from the playbook's directory and may
hold anything.

**Fix.** `Position.String` escapes the name (`termsafe.EscapeLine`), and a runbook's file name is its
sanitized id (`runbookFile`, `runbookID`).

**Lesson.** A name read from the filesystem is input, exactly like the file's content.

**Class.** C1 (delimiter injection into a terminal; CWE-150)
**Portable.** yes: any tool printing a path or name it read from a directory it does not control
**Detector.** fuzz or table-test names holding control characters and assert the output holds none; see ~/vuln-corpus/README.md

## 330. The migration report printed values from the playbook in its refusal messages

**Symptom.** Found by review in Phase 35 (commit d), while checking what a new list-conversion error
would say. The report promises names and positions only, and `TestReport_NoValues` proved it for
secret-shaped and vault values, but four other paths printed values: the YAML 1.1 refusals
(`"0123" means an octal number to Ansible, not the text "0123"`, and a decimal mode), a selector value
with no native equivalent (`state=VALUE`), the condition refusals (`int of VALUE`, `VALUE < VALUE`,
`compared with VALUE`, `in ... VALUE`), and the condition parser's tokens (`unexpected 'literal'`,
`bad number`). A PIN written as `0123`, or any text value, could reach a report meant to be shared.

**Security.** Information exposure through a report written for sharing. Our own code, found before
release. Measured: `TestReport_RefusedValuesNotPrinted`, with nine sentinel values, fails on each of the
five old messages restored one at a time.

**Root cause.** The no-values rule was tested for values that look secret, not for values in general,
so every message built for a different purpose was free to quote its input.

**Fix.** Every refusal describes the value's kind and points at its position (`describe`, `kindOf`,
`describeToken`); `TestReport_RefusedValuesNotPrinted` covers each path.

**Lesson.** "Never print a value" has to be tested with values that look ordinary. A secret does not
always look like one.

**Class.** C7 (secret exposure through logging or output; CWE-532)
**Portable.** yes: any error or report message that quotes the input it refused
**Detector.** sentinel values through every refusal path, asserting none appears in any output; see ~/vuln-corpus/README.md

## 331. A negated condition over a registered result ran a task on every device when one matched

**Symptom.** Found in Phase 35 while writing `TestWhen_MoreCEL`. The condition translator wrapped each
read of a registered result in its own all-devices quantifier, so `not probe.rc` became
`!(stat.probe.all(d, stat.probe[d].rc != 0))`: true when ANY device had `rc == 0`. A native condition
runs once for the task across all its devices, so the converted task ran on every device when the
Ansible task would have run on only some. `a or b` was the stricter mirror image.

**Security.** A converted task doing more than its playbook: the wrong devices changed. Our own code,
found before release. Measured: restoring the per-read quantifier fails `TestWhen_MoreCEL`.

**Root cause.** A quantifier pushed down to each read changes meaning under negation and disjunction;
it is only safe under conjunction.

**Fix.** The whole condition is quantified once (`stat.R.all(d, ...)`, in `condition`); a condition
reading two registered results per device is refused; and a negated `is defined` on a register is
refused, because Ansible registers a skipped task's result and this runtime does not, so the negation
would run where Ansible skips.

**Lesson.** A translation has to be checked for the direction of its error: stricter is a finding,
looser is a bug.

**Class.** C9 (fail-open: a narrower condition rendered as a wider one; CWE-697)
**Portable.** yes: any translation of per-item predicates into one aggregate check
**Detector.** evaluate the translated expression over mixed per-item inputs and compare with the source's per-item result; see ~/vuln-corpus/README.md

## 332. Two merge keys in one YAML map resolved first-wins, where Ansible's loader takes the last

**Symptom.** Found in Phase 35 while writing `TestMergeKeys`, then checked against ansible-core's own
`AnsibleLoader` (PyYAML 6.0.3): for a map with `<<: *a` and then `<<: *b`, Ansible reads a key both
define from `b`, and the converter read it from `a`. A converted task could carry a different value from
the one Ansible would have used.

**Security.** A silent value difference between what was reviewed and what runs. Our own code, found
before release.

**Root cause.** Merge-key precedence was written from the YAML spec's wording for a merge list, and
PyYAML's handling of two separate merge keys was assumed rather than read.

**Fix.** `mapEntries` applies later merge keys first; `TestMergeKeys_MatchAnsible` (integration)
compares five merge shapes with `AnsibleLoader` in the pinned runner image, and fails with the old order.

**Lesson.** When the converter must read a format the way another program does, test against that
program, not against the format's specification.

**Class.** the silently-dropped-fields class (the unnumbered note after C10), YAML parser-differential form (CWE-436)
**Portable.** yes: any tool re-reading YAML another program will also read, where merge keys are allowed
**Detector.** differential test against the other program's loader over merge shapes; see ~/vuln-corpus/README.md

## 333. Ansible's list type was not applied: `name=curl,git` became one package named `curl,git`

**Symptom.** Found in Phase 35 while reading `coerceGo` against ansible-core's `check_type_list`, which
splits a text at commas. `apt: name=curl,git` converted to one install of a package named `curl,git`; a
text given to a list parameter (`ios_config: lines: hostname r1`) became a bare string instead of a
one-item list; and `name: [curl]` was blocked because only a list of two or more took the unroll path.

**Root cause.** The value conversion followed the native parameter's type and never Ansible's
argument type.

**Fix.** A package name list given as text splits at commas (one task per name); a comma text for any
other list parameter is refused, since Ansible's split is rarely what was meant (`description a, b`
would be two lines); a text for a list parameter becomes a one-item list; a one-item list is set
directly. Ansible's own boolean spellings (`t`, `f`, 0 and 1) are accepted, and a boolean selector
(`Selector.Bool`) reads a templated or quoted boolean the same way.

**Lesson.** A converter owns both sides of each argument: the target's type and the source's.

## 334. The module tables accepted aliases and values that belong to other modules

**Symptom.** Found in Phase 35 by `TestEntries_ArgsMatchAnsibleCore`, which checks the tables against
`ansible-doc -j` in the pinned image: apt accepted `installed` and `removed` (dnf's words), dnf accepted
`package` and `update-cache` (apt's aliases; dnf's are `pkg` and `expire-cache`) and
`cache_valid_time` (not a dnf option), `service` accepted `service` and `unit` (systemd's aliases only),
and `package` defaulted `state` although Ansible requires it.

**Root cause.** Shared argument lists were written once for a family of modules whose options differ.

**Fix.** Per-module argument lists and state values (`internal/forge/playbook/modules_pkg.go`); three
deliberate departures are listed with their reasons in the test.

**Lesson.** A table mirroring another program's interface should be checked against that program's own
description of it.

## 335. file.directory left the parents it created at the umask's mode

**Symptom.** Found in Phase 35 by `TestMigratePlaybook_BehaviorMatchesAnsible`, which runs one playbook
with real Ansible and its conversion with `pleiades run` against a real sshd and compares the trees.
`file: path=/tmp/mgate/app/conf state=directory mode=0750` left `/tmp/mgate` and `/tmp/mgate/app` at
0755 natively, 0750 under Ansible. The method's own parameter documentation said the mode applied
"never to a parent created along the way", while its description called it `ansible.builtin.file` with
`state=directory`.

**Security.** `path: /srv/secret/app` with `mode: "0700"` left a newly created `/srv/secret`
world-readable: weaker permissions than the task asked for. Our own code; pre-1.0, so no deployment to
migrate.

**Root cause.** `mkdir -p` was treated as the whole of "create the parents", and only the named
directory was given the attributes.

**Fix.** `file.directory` finds the missing parents first and gives each the task's mode, owner and
group, deepest first so a parent's mode cannot stop the change below it; a parent that already existed
is never changed (`missingParents`, `internal/catalog/file/directory.go`). Proven by
`TestDirectory_GivesCreatedParentsTheAttributes`, and by the behavior gate, which now compares equal.

**Lesson.** A native method that names an Ansible module it mirrors should be checked against that
module's behavior, not its documentation. A behavior gate compares outcomes, which is the only thing a
user relies on.

**Class.** none of C1 to C14 (incorrect default permissions; CWE-276)
**Portable.** yes: any "create with these attributes" operation that creates intermediate objects
**Detector.** create a nested path with restrictive attributes and assert every created level has them; see ~/vuln-corpus/README.md

## 336. A module named `guard` would have become a placeholder with the guard's name

**Symptom.** Found in Phase 35 while giving validation specific messages for the migration guard and
placeholders. A blocked task's placeholder was `ansible.unconverted.<module>` and the guard was
`ansible.unconverted.guard`, so a playbook task using a module named `guard` produced a placeholder
indistinguishable from the guard. Both were unrunnable, so nothing could run; the report and the
validation messages would have misnamed it.

**Root cause.** The guard shared the placeholder namespace.

**Fix.** The guard is `ansible.incomplete` (`engine.IncompleteGuard`), outside the placeholder prefix
(`engine.UnconvertedPrefix`); both are constants the translator and validation share.

**Lesson.** A reserved name must sit outside every namespace user input can fill.

## 337. A nested import_tasks was looked for only in the playbook's directory

**Symptom.** Found in Phase 35 by the corpus measurement: every public role's `tasks/main.yml` importing
`setup-Debian.yml` was reported missing, because the converter resolved an import's file against the
playbook's directory only. Ansible looks beside the importing file first.

**Root cause.** Import resolution was written for a playbook importing one level down and never
considered an import from an imported file.

**Fix.** `importPath` tries the importing file's directory first, then the playbook's, both inside the
playbook's directory (`TestTranslate_ImportTasksResolvesAsAnsible`).

**Lesson.** Measure a converter on real input early: six missing imports in twelve roles was invisible
to every fixture written by hand.

## 338. An empty list literal in a condition evaluated to None

**Symptom.** Found in Phase 35 while writing `TestWhen_MoreCEL`: `'a' in []` was refused as comparing
values of different kinds, because the list literal's value started as a nil slice, which the folding
rules read as Python's None. Python answers False.

**Root cause.** A nil slice and an empty one differ to a type switch.

**Fix.** A list literal starts as `[]any{}` (`internal/forge/playbook/whencel.go`).

**Lesson.** Where nil and empty mean different things downstream, construct the empty one explicitly.

## 339. The test SSH server's Close waited forever on a connection its client kept open

**Symptom.** Found while building connection persistence (Phase 110). `remoteexectest.Server.Close`
closed the listener and then waited for every connection goroutine to finish. A goroutine finishes only
when its client disconnects, so a test whose client kept a connection open past the test (a pool, which
exists to do exactly that) would hang in its cleanup instead of failing.

**Root cause.** The server assumed every client closes its connection before the test ends, which was
true of every caller until a caller existed whose job is to not close it.

**Fix.** The server tracks its live connections and `Close` ends them before it waits
(`DropConnections`, which a test also calls to simulate a device dropping idle flows). `Live()` and
`Logins()` let a test prove from the server's side that a connection really closed and how many logins
really happened.

**Lesson.** A test server's shutdown must not depend on its clients' good behavior: end what it serves,
then wait.

## 340. An outside timeout on the end-to-end suite left a kind cluster running

**Symptom.** Found in Phase 110: `timeout 590 go test ./tests/e2e/` killed the suite mid-run, and a
`pleiades-release-gate-<pid>` kind cluster (`tests/e2e/packaging_kind_test.go`) stayed up afterwards,
holding memory on a machine already short of it. It was removed by hand with `kind delete cluster`.

**Root cause.** A killed test binary runs no `t.Cleanup`. testcontainers' reaper collects the containers
it started, but the kind cluster is created through the kind CLI, which nothing reaps.

**Fix.** None in code. The suite is run with `go test -timeout` long enough for it (45 minutes here), and
a leftover cluster is found with `docker ps --filter name=pleiades-release-gate`.

**Lesson.** Anything a test creates outside testcontainers survives a killed run; give such a suite the
time it needs rather than a shorter outside kill, and check for leftovers after any interrupted run.

## 341. The first Ansible comparison's target ran sshd unprivileged, understating every login about threefold

**Symptom.** Found in Phase 110 while building `tools/ansiblebench`. The earlier benchmark (Phase 35's,
then Phase 110's first) used `lscr.io/linuxserver/openssh-server`, where one Pleiades login cost about
22 ms. On the Alpine target the user chose for the scaling benchmark, the same login cost about 60 ms:
ten tasks with persistence off took 0.64 s instead of 0.23 s. A memory limit, the OpenSSH version
(9.7 and 10.0 alike) and the password hash (SHA-512 on both) were each ruled out by measurement.

**Root cause.** The linuxserver image runs `sshd` as uid 1000, so it skips the per-connection privilege
separation a root `sshd` performs. Real servers run `sshd` as root, so the earlier target measured a
cheaper login than any real device offers, flattering whichever variant logs in most.

**Fix.** The benchmark's target (`tools/ansiblebench/Dockerfile.target`) runs `sshd` as root, and the
published numbers (docs/03) come from it; the earlier figures are superseded.

**Lesson.** A benchmark target is part of the measurement: check that it does per-operation work the
way a real system does before comparing numbers against it.

## 342. The benchmark stopped at its first failed run and kept only what it had printed

**Symptom.** Found in Phase 110: the first full `tools/ansiblebench` run lost every measured row when
Ansible failed at 200 hosts, because results were written only at the end and a failed run ended the
whole benchmark; and its single `docker rm -f` of 201 containers left 45 stopped but not removed.

**Root cause.** The harness treated a failure as a bug in itself rather than as a result, and trusted
one bulk removal.

**Fix.** Results are rewritten after every row; a failed run is recorded with the out-of-memory kills
the runner's cgroup counted and the tool's first error line, its full output saved; containers carry a
label and are removed in chunks until none is left.

**Lesson.** In a benchmark, a tool failing under load is a result: record it with its cause and keep
measuring.

## 343. Ansible's orphaned processes are reaped by PID 1, and neither the benchmark's runner nor the Ansible adapter's container has an init

**Symptom.** Found in Phase 110's scaling benchmark. After the 100-host rounds, every fork on the machine
began failing with EAGAIN: Ansible (`[Errno 11] Resource temporarily unavailable`), The Pleiades (`runtime:
failed to create new OS thread (have 5 already; errno=11)`, exit 2) and an unrelated shell alike. A
host-wide sampler showed root-owned processes climbing by about one per Ansible task execution (627 to
4,430 during one 200-host run), and a 20-host run left 460 zombies, all children of the runner
container's PID 1 (`sleep infinity`): 400 `python3.12` (one per task per host) and 60 `ssh`.

**Root cause.** Ansible leaves orphans (dead worker processes, its persisted `ssh` connections) for PID 1
to reap, as any init does. `sleep` as PID 1 never reaps, so each became a zombie holding a kernel task
slot until the container was removed. With `--init` (docker-init as PID 1) the same run left none. The
production shape has the same defect: `internal/adapters/legacy/docker_orchestrator.go` starts
`ansible-playbook` itself as PID 1 with no init, and Python does not reap children it did not start; a
20-host run through that shape accumulated 191 zombies under it before it exited. The container is
removed after each run, so they do not outlive it, but a large enough playbook (hosts times tasks in the
tens of thousands) can exhaust the Runner host's task table within one run, failing every process on
that host.

**Fix.** The benchmark's runner starts with `--init`, and `DockerOrchestrator.Run` sets
`HostConfig.Init` (fixed 2026-09-24 at the user's go-ahead). `TestDockerOrchestrator_ReapsOrphans` (a
Python PID leaves fifty orphans in the adapter's image; none stays a zombie) and
`TestAnsibleReleaseGate_LeavesNoZombies` (twenty `raw` tasks through the real adapter against a real
sshd, then a check that fails the job on any zombie) both fail with the init removed: the command ran
as PID 1 with 50 zombies, and the check exited non-zero.

**Lesson.** Anything that runs a process tree in a container needs an init as PID 1 unless its PID 1 is
known to reap orphans; a tool behaving correctly on a normal host is not evidence either way.

**Class.** none of C1 to C14 (uncontrolled resource consumption; CWE-400, CWE-772)
**Portable.** yes: any container whose PID 1 is an application or `sleep` rather than an init
**Detector.** run a process-spawning workload in the container and count zombies whose parent is PID 1; see ~/vuln-corpus/README.md

## 344. A host classified at add time lost its classification's capabilities on load

**Symptom.** Found 2026-09-24 while auditing which capabilities a real device can satisfy (Phase 111's
preparation). `pleiades add-host web1 --classify linux_server,debian_family` produced a host that never
declared `AptCapable`: a real `pleiades run` of `pkg.install` against a real Debian sshd was refused
with "requires capability PackageManagerCapable". The unit tests passed, because they handed the
classification's capabilities to the constructor directly and never went through the file inventory.

**Root cause.** `add-host` writes both the resolved `type` and the `classify` path, and
`inventory.ResolveHostCapabilities` returned nothing whenever `type` was present, treating the path as
provenance only. So every classified host lost what its classification granted. It stayed invisible
because the capabilities classification grants (`AptCapable`) were ones no device type implemented
anyway: that gap was disclosed and allowlisted (`acceptedUnsatisfiableCapabilities`), and with it open
nothing could exercise the path that would have closed it.

**Fix.** The path contributes capabilities when it resolves to the same type that was saved; a path
that resolves elsewhere, or not at all, contributes nothing and is not an error
(`TestResolveHostCapabilities_ClassifyBesideType`). With the disclosed gap closed at the same time
(`linux_server` implements the package manager, firewall and account accessors, and declares them
from classification or its `firewalld` property), `TestCLI_PackageAndAccountMethodsReachARealDevice`
runs `identity.group.create` and `pkg.install` through the real binary against a real Debian device.
The database inventory stores no classification at all, so a device there still gets only its type's
baseline; Phase 111's storage item carries that.

**Lesson.** A gap disclosed as "not reachable yet" needs a test that fails while it is open and passes
once it closes, run through the real path. Otherwise the path that would close it can break unseen,
because nothing can use it.

## 345. Declaring NETCONF claimed a command line

**Symptom.** Found 2026-09-24 while planning Phase 111's generic device types. `NetconfCapable`
embedded `NetworkCLICapable` and was registered as its child, so a device that declared NETCONF
satisfied every method requiring `NetworkCLICapable` (`net.cli.command`, `net.cli.config`) by both
halves of the check: `capability.Resolves` walked up to the CLI capability, and a Go type implementing
`NetconfPort` had to implement `CLIPrompt` too. A NETCONF-only device, which is exactly what a generic
NETCONF type is, would have been handed a terminal method it cannot serve.

**Root cause.** The tree grouped capabilities by "network device" rather than by what each one lets a
method do. NETCONF is structured configuration over an SSH subsystem, with no prompt at all. Only the
two Cisco types declared NETCONF, and both are CLI devices as well, so the wrong edge never changed an
answer.

**Fix.** `NetconfCapable` has no parent and embeds nothing (`pkg/capability/capabilities_network.go`).
A device with both, as a Cisco router with NETCONF enabled has, declares both.
`TestNetconf_ClaimsNoCommandLine` asserts neither half of the check grants the CLI; the generated
capability reference no longer lists the edge.

**Lesson.** A capability's parent must be something every device holding the child can actually do. An
edge that no current device contradicts is still wrong if the next device type would; check each edge
against the device that has only the child.

## 346. A port wait passed before a shell-less container's server was listening

**Symptom.** Found 2026-09-24 writing Phase 111's gRPC release gate. `TestGenericReleaseGate_GRPC`
waited for `grpc/java-example-hostname` with `wait.ForListeningPort("50051/tcp")`, which reported the
container ready, and the probe's first call then failed: `connection error: error reading server
preface: connection reset by peer`. Rerun a few seconds later by hand, the same probe succeeded.

**Root cause.** The strategy has two halves, a check inside the container and a dial from the host,
and neither held here. The image has no shell, so testcontainers logs "Shell not found in container"
and skips the inside check. The host dial reaches Docker Desktop's port proxy, which accepts a
connection on the published port at once, whether or not anything in the container listens yet, so it
succeeds while the JVM is still starting. Readiness was measured against the proxy, not the server.

**Fix.** Wait on the server's own line, `wait.ForLog("Listening on port 50051")`, which only the
server can print (`cmd/pleiades/generic_release_gate_test.go`, with a comment saying why).

**Lesson.** A readiness check proves what it can observe. On a shell-less image under Docker Desktop, a
port wait observes the host proxy and nothing else; wait for something only the server itself can
produce, such as its log line or an answer in its own protocol.

## 347. A gRPC stream's Send returned EOF, and the probe read it as the answer

**Symptom.** Found 2026-09-25 by the push gate's repeat pass, in Phase 111's own new test:
`TestGRPCProbe_OlderReflectionOnly` failed with `server reflection: EOF` about twice in 500 runs under
`-race`, and never in 300 runs without it. The test's server serves only reflection v1alpha, so the
probe asks v1 first, expects Unimplemented, and falls back.

**Root cause.** grpc-go's `ClientStream.SendMsg` returns `io.EOF` when the server has already ended the
stream, and the stream's real status is then read from `RecvMsg`. A server that does not serve a
method ends the stream at once with Unimplemented, so whether the client's `Send` raced ahead of that
refusal decided the outcome: ahead, `Send` succeeded and `Recv` returned Unimplemented, which the probe
handled; behind, `Send` returned `io.EOF`, which the probe returned as a failure without ever reading
the status. Against a real server predating reflection v1 (grpc-java before 1.57), onboarding would
have failed at random.

**Fix.** Both reflection calls treat an `io.EOF` from `Send` as "read the status" and go on to `Recv`
(`internal/inventory/onboard/probe_grpc.go`). 2,000 runs under `-race` pass, against 2 failures in 500
before.

**Lesson.** On a gRPC client stream, `Send` returning `io.EOF` is never the result: it says the stream
is over and the result is on `Recv`. Any code that returns straight from a failed `Send` reports a
race instead of the server's answer.

## 348. A generic device would never have been dispatched: the worker required a host property

**Symptom.** Found 2026-09-25 writing the Controller-side test for Phase 111's Walk-tier work. A
`generic_http` device in a job's inventory was recorded as skipped ("has no host property") and never
reached a Runner. The Runner-side release gate passed regardless, because it publishes a dispatch
payload straight to the stream and never goes through the Controller's fan-out.

**Root cause.** `internal/dispatch`'s worker took a device's address from its `host` property and
skipped any device without one. Every vendor type keeps its address there, and the rule was written
for them. `generic_http` and `generic_grpc` keep theirs inside a URL or a target (`base_url`,
`target`), so they have no `host`, and the worker would have skipped every one of them.

**Fix.** With no `host`, the worker uses the address the device declares through
`NetworkAddressableCapable` (`declaredAddress` in `worker_devices.go`), which both generic types
implement from their URL or target. `TestWorker_DispatchesAGenericDevice` fails without it and passes
with it, and also checks that the payload carries the device's type and allowlisted properties and not
a property no accessor reads.

**Lesson.** A test that injects a message at a boundary proves the far side only. The Runner gate
published its own payload, so nothing tested whether the Controller would ever send one. A two-tier
feature needs each half tested at its real entry point, or one test that crosses both.

## 349. Gate items were ticked with half their work undone, and one ticked claim was false

**Symptom.** Found 2026-09-25 while tracing the four security problems the roadmap's attestation checker
reported: finished Fuzz/Stress, Schema/Injection and Release Gate items that cited no test, path or make
target it could find. Most had their evidence; it had simply never been written down. Four did not.
Phase 96c's Release Gate asks for one run over real NATS behind real Toxiproxy in which a dispatch
published while severed arrives exactly once after a heal longer than the old two-minute window, and no
such test exists: the Runner's admission check is tested with an in-memory store and a mock consumer,
and 96a's recovery test has no dedup in it. 96c's Fuzz/Stress item also asked for a benchmark of the
dedup table's cost and nothing measures it. Phase 79's asked for the sign-in email to be fuzzed and only
the password is. And Phase 55's "no function in this phase performs I/O of any kind" is false:
`ShiftTimezone` calls `time.LoadLocation`, which reads the zone database from disk.

**Root cause.** Each partial item had more than one clause, and it was ticked once its most visible
clause was done (the fuzz target, the derivation, the password path). Nothing asked for a proof per
clause. The I/O claim came from an audit of the import list, and `time` is not an I/O package, so an
audit of imports cannot see a read that one of its functions makes.

**Fix.** Every flagged item now carries a dated evidence line naming its tests. 96c's Release Gate is
re-opened with the two unjoined halves named. 96c's benchmark and 79's email fuzz are split out as open
items instead of staying inside ticked ones. Phase 55's claim is corrected, and
`TestFiltersDoNoNetworkFileOrProcessIO` now checks it: it inspects the calls, not just the imports, and
names `time.LoadLocation` and `crypto/rand` as the only reads `pkg/filters` makes.
`TestFiltersIOSitesDetects` is its negative control.

**Lesson.** An item with several clauses is done when each clause cites its own proof. "No I/O"
describes what the code calls, so check the calls.

## 350. Entry 310's fix set the group's deadline, and every step inside it still gave up at sixty seconds

**Symptom.** `make ci` on 2026-09-25 (`6530e2e`) failed in its coverage pass, and so did a second run
of that step: `internal/backup`'s `TestRestore_RefusesABackupFromANewerVersion`, then
`TestScratch_ClearSettingsRemovesWhatTheFileLeft` and `TestTakeAndRestore_RefuseATriggerAndItsFunction`,
each with `wait until ready: external check ... get state ... context deadline exceeded` after 567 or
568 polls and 61 seconds. That is entry 310's symptom exactly, on code that had its fix. The package
passed alone in 35 seconds, and it passed the race and integration passes of the same run, which start
container packages one at a time. Only the coverage pass, which starts them all together, was slow
enough to reach the limit. push-gate had absorbed the same class in its own coverage pass by
re-running failures alone (`cmd/controller`, `cmd/runner`, `internal/election`).

**Root cause.** testcontainers has two layers of timeout and 310 set only one. `WithWaitStrategyAndDeadline`
bounds the `ForAll` group, but each strategy inside it (`ForLog`, `ForListeningPort`, `ForHTTP`) also
calls `context.WithTimeout` with its OWN startup timeout, `defaultStartupTimeout()`, sixty seconds, when
none is set. A child context cannot outlive the shorter of the two, so the group's two minutes never
reached any step. `ForAll`'s `WithStartupTimeoutDefault` does not help either: it only sets a context
around the step, which the step narrows back to sixty. And `TestReadinessCarriesTheAgreedBound` read the
group's deadline only, so it passed. The same shape was in two more places. The five LocalStack starts
took the module's own strategy, whose step says 120 seconds but whose group, from `WithWaitStrategy`,
says sixty. And `cmd/pleiades`'s SSH release gate put a three-minute group around two steps that set
nothing.

**Fix.** Every step in `PostgresReady` and `ToxiproxyReady` now sets `WithStartupTimeout(ContainerStartupTimeout)`.
A new `LocalStackReady` replaces the module's strategy, and all five LocalStack starts pass it. The SSH
gate's two steps name `SSHDStartupTimeout`. `TestReadinessCarriesTheAgreedBound` now reads back each
step's own timeout through `wait.StrategyTimeout`; `TestUnboundedStepsDetects` is its negative control,
built as 310's version was; and `TestNoContainerWaitsUnderTheLibraryDeadline` counts LocalStack starts
against `LocalStackReady()` calls. Two mutations were checked: dropping one step's timeout, and dropping
one call site's helper, each fails the named test.

**Lesson.** When a bound passes through layers, test the value at the layer that enforces it. 310's
test read a value the library kept, and not the one the library obeyed. See `LESSONS_LEARNED.md` #243.

## 351. A host-key capture read an SSH banner with no deadline, and hung make ci for thirty minutes

**Symptom.** `make ci` on 2026-09-25 (`e3d84b9`) failed its race pass on `cmd/runner`:
`panic: test timed out after 30m0s` with `TestSSHMeshReleaseGate_RealSecretThroughTheFullChain`
running for 26m56s. The stack showed `captureRealHostKey` in `ssh.NewClientConn`, inside
`readVersion`. The same package had passed alone under `-race` hours earlier.

**Root cause.** The helper dialed the sshd container's published port and ran the handshake on that
connection with nothing bounding it. `ssh.ClientConfig.Timeout`, which it set, bounds only `ssh.Dial`'s
TCP connect, not a handshake over a connection the caller dialed. The container was declared ready on
the image's init log line, which says nothing about sshd listening, and a published port accepts a
connection before the server behind it does (entry 346). So the read of the version banner waited for
bytes that were never coming. `cmd/pleiades` carried an identical copy.

**Fix.** `testsupport.CaptureHostKey` sets a deadline on every attempt's connection and retries until
the server completes a handshake or `SSHDStartupTimeout` has passed. Both copies call it.
`TestCaptureHostKey_ASilentServerIsBounded` runs it against a listener that accepts and never writes,
and gives up at its bound; without the deadline that test hangs exactly as CI did.
`TestCaptureHostKey_RetriesPastASilentConnection` shows a silent first connection is retried past.

**Class.** C15 (unbounded wait on a peer; CWE-1088)
**Portable.** yes: any test harness that handshakes with a container on a port a proxy accepted
**Detector.** a listener that accepts and never writes, and an assertion that the caller returns within its bound; see ~/vuln-corpus/README.md

**Lesson.** See `LESSONS_LEARNED.md` #243: the timeout that was set bounded a layer that never blocked.

## 352. The SSH handshake with a device reached through a bastion had no bound at all

**Symptom.** Found 2026-09-25 while fixing entry 351, by reading the other callers of
`ssh.NewClientConn`, then measured. `pkg/remoteexec`'s `dialThroughHop` opened a channel through the
bastion and ran the target's handshake over it with nothing bounding it. Against a target that
accepts the forwarded connection and never sends an SSH version, `Runner.Run` with a two second
context stayed blocked at `hop.go:73` until the test's own thirty second timeout
(`TestConnect_HopChain_ASilentTargetIsBounded`, before the fix).

**Root cause.** `ssh.NewClientConn` takes no context, a connection tunneled through an SSH channel
does not support deadlines, and `ssh.ClientConfig.Timeout` bounds only `ssh.Dial`. The direct path,
`realDial`, already closed its connection when its context ended, which is the one way to unblock that
handshake, with a comment saying why. The tunneled path had been written without the guard, and its
context was passed only to opening the channel.

**Fix.** The guard is now one helper, `closeOnDone`, beside `handshakeContext` (the caller's context
bounded by `config.Timeout` when set), and both paths use them. `closeOnDone`'s stop now also waits for
its goroutine to exit, so a deferred cancel right after a successful handshake cannot race it into
closing the new connection. `TestConnect_HopChain_ASilentTargetIsBounded` returns at the context's two
seconds.

**What it would get an attacker.** Anyone who controls the endpoint at a device's address behind a
bastion (the device itself, or anything that can answer on that address and port) could hold a Crawl
run, or a Runner's task and the device lease it holds, for as long as they liked, by accepting the
connection and saying nothing. No credential is needed. Measured in-process; no upstream fix applies,
since the library documents that `Timeout` covers only `Dial`.

**Class.** C15 (unbounded wait on a peer; CWE-1088, CWE-400)
**Portable.** yes: any SSH jump-host, proxy or tunnel code that calls `ssh.NewClientConn` itself
**Detector.** a listener that accepts and never writes behind the hop, and an assertion that the call returns within its context; see ~/vuln-corpus/README.md

**Lesson.** See `LESSONS_LEARNED.md` #244.

## 353. A test broker was declared ready while its published port still refused every connection

**Symptom.** Three times on 2026-09-25, once in `internal/event` and twice in `cmd/runner`, two of
them under `make ci`'s one-container-package-at-a-time pass: `failed to connect to nats at
nats://localhost:N: timed out waiting for the first nats connection`. The broker had logged "Server is
ready", and the client's log shows every attempt against that port refused (`dial tcp
127.0.0.1:40964: connect: connection refused`) for the whole ten seconds the first connection is
allowed. Each test passed alone.

**Root cause, as far as it is known.** `testsupport.StartNATS` waits for the server's own log line,
which is said inside the container. The port the host dials is published separately, by Docker
Desktop's port forwarding on this WSL2 machine, and nothing checked it. How long it stays unreachable
is not settled: after the fix below, two brokers started one after the other in `internal/event`
(`nats_dedup_test.go`) each refused every connection for the full two minutes, while the same package
passed alone earlier the same day, and six brokers started by hand on a quiet machine each answered
within 0.04 seconds. So the published port can stay dead for minutes at a time under this workload,
and why is not diagnosed. No test in `internal/event` runs in parallel, and the test before them only
drives Toxiproxy.

**What changed.** `StartNATS` now also waits, under `ContainerStartupTimeout`, until the published
client port answers with the `INFO` line every NATS server opens a connection with
(`waitForNATSGreeting`), each attempt under its own deadline. That absorbs a short gap, if there is
one, but its measured value is the diagnosis. A broker whose port never answers now fails in the
harness, naming the port and the refusal, not in the code under test as a first-connection timeout
that reads like a defect in `internal/topology`. `TestWaitForNATSGreeting` covers a broker, a port that
closes its first connections and then answers, and four ports that never do. `ConnectWaitTimeout`, the
production bound, is unchanged.

**Open.** Why Docker Desktop's forwarding goes dead for minutes during these runs. Until that is known,
a `make ci` on this machine can fail here with nothing wrong in the code.

**Lesson.** A container's readiness has two sides. Wait for the side the test dials, not only the side
the container reports, and when that wait fails, say which side.

## 354. Every PowerShell quoting helper doubled only the ASCII apostrophe, and a typographic quote ended the literal

**Symptom.** Found by reading, then measured on 2026-09-25 against Windows PowerShell 5.1 (Windows 11
build 26200). `pkg/winrmsvc` and `pkg/winrmdism` each built PowerShell with a private `quotePS` that
wrapped a value in single quotes and doubled every `'`. A service or feature name containing U+2019
(or U+2018, U+201A, U+201B) closed the literal early. Placed in the exact shape those packages send,
`Cmdlet -Name <quoted> -ErrorAction SilentlyContinue`, the name
`x<U+2019>; Write-Output INJECTED; Get-Variable -Name <U+2019>y` ran `Write-Output INJECTED` for all
four characters. The value is a task's `name` parameter, so anyone able to set it (a survey answer, an
extra var) could run PowerShell on the device as the WinRM account, often an administrator, instead of
naming a service.

**Root cause.** The PowerShell language specification's `single-quote-character` is five characters,
not one: the apostrophe and four typographic quotes, and a single-quoted literal ends at any of them.
Both helpers carried a doc comment saying doubling the apostrophe was "the complete defense", copied
from one package to the other with a note accepting the duplication "until a third package needs it".

**Fix.** One shared `winrmexec.QuotePS` doubles all five, as PowerShell's own
`CodeGeneration.EscapeSingleQuotedStringContent` does; both packages call it and their copies are gone.
`TestQuotePS_TheMeasuredPayloadStaysOneLiteral` replays the measured payload through a Go model of the
tokenizer, `TestParsePSSingleQuoted_ModelMatchesTheMeasuredBreakout` proves the model reproduces the
real breakout under the old rule, `FuzzQuotePS` checks the property for any input, and each fails when
the old rule is restored.

**Lesson.** An escape is complete only against the target parser's own definition of the character
class, and a hand-written doc comment claiming completeness is a claim to test, not a reason to stop.
A helper copied into a second package is the moment to share it, because the next fix lands in one
copy.

**Class.** C13 (OS command injection), incomplete-quoting variant
**Portable.** yes: any code escaping for PowerShell by doubling only the apostrophe
**Detector.** a file building PowerShell that escapes with `replace("'", "''")` alone, case-insensitive,
controlled against a known hit; swept across every repo in `~` on 2026-09-25 with no other instance.
See ~/vuln-corpus/README.md C13.

## 355. The WinRM lab setup script gave its account far more than a lab run needs, and weakened the host for everyone

**Symptom.** Found in review on 2026-09-25, when the user asked whether the account the script
creates was scoped to least privilege. `examples/windows_lab/winrm-cert-setup.ps1`, run on the
development machine on 2026-09-20:

- ran `winrm quickconfig`, which on a machine outside a domain sets `LocalAccountTokenFilterPolicy`
  (every local administrator gets an unfiltered token over a remote connection) and opens the HTTP
  listener on 5985 to the whole Private network, while the lab needs neither;
- granted `(A;;GA;;;RM)` in WinRM's RootSDDL: full control, to every current and future member of
  Remote Management Users, not to the one account;
- added the account to Remote Management Users, which also opens remote WMI;
- printed the account's password, which was also the PFX passphrase, so whoever read it could log in
  with a password over 5985 from the local network;
- left the lab CA's private key in the machine store for a year while the CA sat in Trusted Root, able
  to issue a certificate the host would trust for any name, and left the client's private key on the
  host after exporting it;
- left the account a standard user's access to the other fixed drives, where Authenticated Users hold
  Modify at the root (measured on this host: `D:` and `G:`).

Measured on the host the same day: the account and the RootSDDL grant were gone, but the CA and client
certificates were still in `LocalMachine\My` with their private keys.

**Root cause.** The script was written to make the certificate path work and to be undone cleanly,
which it did; nobody listed what the account needed and compared it with what it got. `winrm
quickconfig` was used for its one wanted effect, starting the service, and brought its other effects
with it.

**Fix.** Rewritten: the service is started directly; the RootSDDL entry names the account's SID with
read and execute; no group membership; console, Remote Desktop, batch and service logon denied
(`secedit`, read back after applying); a random password never shown and not reused; the account
denied at the root of every other fixed drive except the folders named with `-ReadPath` and
`-WritePath`; the CA and client private keys deleted once used; trust stores given public-only
certificates; the PFX passphrase in a file only its owner can read; everything granted recorded in
`lab-state.json`, which the teardown reads to revoke it. Shared helpers in `winrm-lab-common.ps1`,
exercised without elevation (the policy-file editor against a realistic export, the ACL helper
against a scratch folder); all three scripts parse clean. Not yet run elevated: the minimum
`-ShellRights`, whether the account needs Remote Management Users, and whether the certificate logon
needs any denied logon right are measured by the first real connection.

**Lesson.** A setup script for an automation identity is an access grant, and it gets the same review:
list what the job needs, compare it line by line with what the script grants, and prefer the command
that does one thing over the convenience command that does five.

**Class.** C16 (over-granted automation identity), new
**Portable.** yes: any bootstrap script for a remote-management account (WinRM, SSH, service accounts)
**Detector.** setup scripts running `winrm quickconfig`/`Enable-PSRemoting`, setting
`LocalAccountTokenFilterPolicy` or `AllowUnencrypted`, granting a broad group full rights, or printing
a generated password; controlled against the old script, swept across `~` on 2026-09-25 with no other
instance. See ~/vuln-corpus/README.md C16.

## 356. Windows ignores WINRS_SKIP_CMD_SHELL, so every WinRM command still ran through cmd.exe while the tests said it did not

**Symptom.** Found on 2026-09-25, the first time Phase 75's WinRM modes gate ran against a real Windows
11 host (build 26200). A program started in `none` mode received no arguments at all; a `cmd` script
failed with `'#34' is not recognized`; a missing program came back as an exit status rather than a
fault. Meanwhile the gate's own subtest "every Command message skipped cmd.exe" passed: all twelve
captured envelopes carried `WINRS_SKIP_CMD_SHELL=TRUE`.

**Root cause.** Two, and the first is the one that matters. (1) Windows does not honor the option.
`%CMDCMDLINE%` inside a command showed `cmd.exe /C <line>` whatever the Command element held (the whole
line, or the program with separate Arguments elements, `TRUE` or `true`), and the option marked
`MustComply="true"` was refused as not valid. Phase 75's design rested on the option working, and the
unit and wire tests asserted that it was sent, which it was. (2) The WinRM service does not decode
numeric character references: Go's `xml.EscapeText` wrote a double quote as `&#34;`, which reached
cmd.exe literally, while `&amp;` arrived as `&`.

**Fix.** Every mode's line is now escaped until the service's `cmd.exe /C` passes it through unchanged
(`pkg/winrmexec/cmdexe.go`: a caret before each metacharacter after the program, `%` included, and an
extra quote pair around a line that starts with a quoted program), the technique Rust adopted after
CVE-2024-24576. The option is sent as FALSE. XML text is escaped with the three predefined entities only.
The 8191 character limit applies to every mode, after escaping. Evidence: `FuzzTransparentLine` against a
model of `cmd.exe /C` (`cmdSlashC`, with two control tests and two mutations it catches), and on the real
host `TestModesReleaseGate` (exact arguments from a spaced path including `c&d|e`, `%PATH%`, `q"uote`,
`!bang!`; a near-limit line whole; a `& echo` value printed as text) and
`TestWinRMModesGate_ThreeModesThroughTheBinary`.

**Lesson.** A test that captures the request proves what was asked, not what happened. Assert the effect
on the real system (here, the command line the program actually received), and treat a protocol
option as unproven until something on the far side shows it took effect.

**Class.** C13 (OS command injection), forced-shell variant (BatBadBut)
**Portable.** yes: any remote or local execution path where a shell is interposed whatever the caller asks
**Detector.** run a program that reports its own command line or argv through the path, with metacharacters
in the arguments; see ~/vuln-corpus/README.md C13.

## 357. The WinRM Adapter mapped credential fields by hand and sent no credential for a PKCS#12 bundle

**Symptom.** `winrm_exec` against a device whose credential was stored with `add-credential --pfx`
failed with "credential is empty", found by the first run of the modes gate through the real binary.

**Root cause.** `internal/transport/winrm` built `winrmexec.Auth` from four named fields of
`credential.Credential` and never looked at `PFXBase64`, while every Collection reaches WinRM through
`winrmexec.AuthFromSecrets(credential.Flatten(...))`, which unlocks the bundle.

**Fix.** The Adapter uses the same shared vocabulary. `TestExecShell_UnlocksAPFXCredential` builds a
real bundle, and a wrong passphrase is refused before anything is sent.

**Lesson.** Where a shared translation exists, a second hand-written one is a second place a new
credential form has to be remembered, and the one that forgets fails only for that form.

## 358. Bare `pleiades validate` checked only the file `init` writes, and silently dropped its own flags

**Symptom.** In the VirtualBox lab project, `pleiades validate` with no runbook named failed with "open
runbooks/sample.yaml: no such file or directory", although the project held seven valid runbooks. In a
project made by `init` it passed while checking only the one-task sample. `pleiades validate --dir
<project>` read `./inventory.yaml` instead of the project's, and `--tags`/`--skip-tags` did nothing,
whenever no runbook was named. `TestMigratePlaybookReleaseGate` ran a bare `validate` to prove a
converted playbook validates, and was checking `sample.yaml` alone the whole time.

**Root cause.** The optional runbook defaulted to `runbooks/sample.yaml`, a file only `pleiades init`
creates and a user is free to delete. `splitPositional` returns a nil `rest` along with
`errMissingPositional`, and `runValidate` treated that error as "no runbook named" and parsed the nil
`rest`, so every flag given alongside no runbook vanished. The command also took exactly one runbook, so
`pleiades validate runbooks/*.yaml` failed on the glob's second path.

**Fix.** `validate` takes any number of runbooks (`splitPositionals`), and with none named checks every
entry in `runbooks/`, exactly what `runbooks/*` names. A directory, a non-YAML file and an
`import_tasks` file are skipped with a note, every runbook is checked even after one fails, and nothing
left to check is a failure. A tag filter is checked once against all of them (`engine.NewTagSelection`),
so a tag only some carry is not a typo. `TestCLI_ValidateManyRunbooks` and
`TestCLI_ValidateNothingToCheck` drive it through the real binary, `--dir` from another directory
included.

**Lesson.** An optional argument's default has to come from the project itself, never from a file one
command happens to write. And a helper's other return values mean nothing once it has returned an
error, even an expected sentinel error: the caller that branches on the sentinel still has to get its
data some other way.

## 359. An `import_tasks` file was diagnosed as an Ansible playbook and sent to the migration tool

**Symptom.** Naming a file of tasks that `import_tasks` pulls in, to `validate` or `run`, printed "this
file is shaped like an Ansible playbook ... convert it with 'pleiades forge migrate-playbook <file>'",
which is wrong advice for a native file that works where it is used.

**Root cause.** `parseWorkflowYAML` called any top-level list containing a map an Ansible playbook. A
playbook and a task file are both lists of maps; only a play carries `hosts`, `import_playbook`,
`tasks`, `roles` and the like.

**Fix.** A list with any play key is still a playbook. Any other list of maps returns
`engine.ErrTaskList`, which says what the file is and to name the runbook that imports it, and which
`validate` tests with `errors.Is` to skip the file instead of failing on it.
`TestBuildFromYAMLFile_TaskListIsErrTaskList` covers both shapes and the importing runbook.

**Lesson.** A shape check that names a diagnosis has to tell apart every shape that reaches it, not
just the one it was written for. Otherwise its advice is confidently wrong about the others.

## 360. `mediumio cat --hex` folds identical rows, and a parser written from its usage text refused the real output

**Symptom.** The first real `virt.vbox.vm.import_disk` failed before making anything: `mediumio gave a
line that is not a hex dump: "**********  <ditto x 27>"`.

**Root cause.** `ParseHexDump` and the model host were written from VBoxManage's usage text and two
short dumps, which printed every row. On the host, a long run of rows equal to the one before is
written as one `**********  <ditto x N>` line (runs of up to 20 were written out, 24 and 27 folded),
and the dump's last row is always written.

**Fix.** The parser expands a ditto line into N copies of the row before it; the model folds runs of 24
or more and writes the last row out. `testdata/mediumio-cat-gpt.stdout` is the host's dump of the
VHDX's first 1024 bytes, byte for byte, and `TestTheModelFoldsARunAsTheHostDoes` holds the model to it.

**Lesson.** Capture the verb's real output on data shaped like the real input (a disk that is mostly
zeros) before writing its parser or its model. A short capture of busy bytes cannot show a
compression rule that only appears on runs.

## 361. Microsoft's Windows Server evaluation VHDX never reads an answer file from a DVD at its first boot

**Symptom.** A clone of the VHDX base, seeded with `Autounattend.xml` on a DVD, stopped at OOBE's
"Hi there" page, and its host-only adapter took a DHCP address instead of the fixed one. Moving the
DVD from IDE to SATA changed nothing.

**Root cause.** Measured inside the guest, after finishing OOBE by keyboard: the DVD was readable
(`D:\Autounattend.xml`, "VBOX CD-ROM OK"), no answer file was cached (`Panther`) or pointed to
(`HKLM\SYSTEM\Setup\UnattendFile`), and Setup's logs said "Didn't find unattend file for this phase"
in specialize and "Found no unattend file for oobeSystem pass" in OOBE. The image sets up new devices
only after OOBE (its device log starts after it), so the DVD did not exist for Windows while it
searched. The image's own sysprep log was deleted, so the switch that caused it (likely `/mode:vm`) is
inferred, not read.

**Fix.** None yet for this image: its answer file has to be put into it (`C:\Windows\Panther\
unattend.xml`) before first boot. `virt.vbox.vm.install` makes a base generalized the ordinary way.

**Lesson.** A generalized image's first-boot behavior is part of what it is. Test the seed on the exact
image before building on it, and when a guest ignores its seed, read its setup logs before a second
guess: the move to SATA cost a rebuild and a boot and was never going to work.

## 362. An EFI VM with two vCPUs stops at the firmware's `DXE_AP` debug point under NEM

**Symptom.** A Windows clone at medium (2 vCPUs) never drew a screen ("Unsupported resolution for
screen shot: 0x0"); its VBox.log ended at `EFI: debug point DXE_AP` 1.9 s in and said nothing for 90 s.

**Root cause.** VirtualBox's EFI firmware hangs starting its application processors when the host's
hypervisor is running (NEM/WHPX, as on VENGEANCE with WSL 2). Resized to one vCPU, the same VM passed
the firmware at once and Windows' kernel reached the Hyper-V interface in 4 s. Measured with the
account's VBoxSVC still pinned to the P-cores (see the multi-CPU hang work), so pinning is not ruled out.

**Fix.** A Windows clone given no size is small (one vCPU). `virt.vbox.vm.install` makes BIOS bases,
which ran Windows Setup at two vCPUs.

**Lesson.** Under NEM, every multi-vCPU boot path needs its own measurement: the BIOS/Linux hang (the
raid6 benchmark) and this EFI one are different code in different guests.

## 363. `add-credential --generate` made a password Windows' default policy refuses, about one time in 38

**Symptom.** Found by working out the odds while seeding Windows, not by a failure: 24 characters drawn
from 24 upper, 25 lower and 8 digit characters have no digit with probability (49/57)^24, about 2.7%.

**Root cause.** Windows Server's default policy wants three of four kinds of character; a password of
letters only has two, and an answer file carrying it leaves the Administrator without it.

**Fix.** The generator draws again until the password holds an upper case letter, a lower case letter
and a digit (`TestRandomPasswordHoldsEveryKindOfCharacter`), and `pkg/winunattend` refuses a password
Windows would refuse before anything is made.

**Lesson.** A random secret meant for another system has to satisfy that system's policy by
construction, not on average.

## 364. A generalized Windows image's first boot never looks on a DVD for its answer file

**Symptom.** Clones of a base `virt.vbox.vm.install` made ignored their `Autounattend.xml` seed, just as
clones of Microsoft's VHDX had (361): Server Core's logon asked for the Administrator's password to be
changed, and the host-only adapter kept a DHCP address.

**Root cause.** Three measured facts, read from the clones' own setup logs:

1. Setup's cached copy of the install's answer file (`C:\Windows\Panther\unattend.xml`) survived
   generalizing, and a clone's specialize pass found it, judged it "does not meet criteria to be used
   for this unattend pass", and searched nowhere else.
2. The `Generalize` setting in the audit pass ran sysprep at the very second the pass began, before a
   `RunSynchronous` command in the same pass could delete that copy.
3. With the copy deleted (sysprep run as the pass's last command instead), specialize still said
   "Didn't find unattend file for this phase", although the clone's CD-ROM had been configured 29 s
   before the search. Setup's documented search table lists removable media, but after a generalized
   image boots it searches only fixed places.

**Fix.** The install's audit pass deletes the cached copy, points `HKLM\SYSTEM\Setup` `UnattendFile`
at `D:\Autounattend.xml` (a clone's one DVD), then runs sysprep itself. Measured on the fifth round: the
clone answered WinRM at its fixed address as its vaulted Administrator 82 s after starting, named
`WIN-LAB`.

**Lesson.** Where a platform documents a search order, measure which entries it actually consults in
the phase that matters before building on one. Three rounds each moved one piece because the
previous log was read for what it said about that piece only.

## 365. A running VM keeps a DVD image locked after its drive is emptied, so a seed cannot be deleted until it stops

**Symptom.** `virt.vbox.vm.eject_seed` on the running `win-lab` emptied the drive and then failed:
`closemedium dvd ... --delete`: "Medium ... is locked for reading by another task", and retrying for ten
seconds did not help. A second run then saw an empty drive and would have reported no change with the
seed, which holds the Administrator's password, still on the host.

**Root cause.** `list dvds` showed the image "locked read" with the drive "emptydrive": VirtualBox holds
an image's lock for as long as the VM that held it runs (`ubuntu-lab`'s seed showed the same).

**Fix.** On a running VM the task empties the drive, reports `deleted: false` with a warning saying to
run it again once the VM is stopped, and a run that finds the seed file on the host in no drive deletes
it. Measured end to end: stop, eject (`deleted: true`), start, WinRM back in 17 s.

**Lesson.** An eject that cannot finish has two halves to report separately: what the guest can no
longer reach, and what is still on the host.

## 366. Windows keeps an unblanked copy of the install's answer file, so every clone carried the audit password

**Symptom.** A scan of `win-lab`'s cached answer files (read through `exec.winrm.shell`, printing only
markers) found `C:\Windows\Panther\unattend-original.xml`, 5,246 bytes, holding the install's
`auditSystem` pass with `<PlainText>true</PlainText>` and the audit password as written. The
`unattend.xml` beside it had its passwords replaced by `*SENSITIVE*DATA*DELETED*`, which is the copy
the audit pass deleted.

**Root cause.** Setup caches the answer file twice, and only one copy is blanked. The audit pass deleted
`unattend.xml` alone, so the other went into the generalized base and from there into every linked clone.
The password was random per install and no longer logged on (checked on the guest with
`PrincipalContext.ValidateCredentials`, printing only True or False: False), because the clone's seed
sets its own; it was still a secret left in plain text on every clone's disk.

**Fix.** The audit pass's first command deletes `%WINDIR%\Panther\unattend*.xml`
(`pkg/winunattend`). Measured on a rebuilt base and clone: no cached answer file holds a password value,
and `unattend-original.xml` is gone. The install method's documentation had said the password was "kept
nowhere"; it now says Windows keeps it in that copy and why the audit pass deletes both.

**Lesson.** When a product writes a secret into its own cache, list every file it writes there, not the
one its documentation names.

## 367. The WinRM service and feature gates asserted on text the output no longer printed, and put the lab password on argv

**Symptom.** Run against a real Windows Server 2025 for the first time in weeks, the three
`svc.windows.*`/`win.feature.*` gates failed while the guest did exactly the right thing: the stop's
diff showed `running: true` before and `running: false` after. The gates searched the output for
`running:true` and `state:Enabled`.

**Root cause.** `run --verbose` began printing stats as indented YAML (`running: true`) after the gates
were written, and the gates are environment-gated, so nothing ran them in the meantime. Their checks
were weak even when they matched: finding both values anywhere in the output never said which was
before and which after. Separately, the gates' project setup ran `add-credential --password <real
password>`, because `add-credential` had no stdin form for a password, only for a passphrase.

**Fix.** The gates read each side of the diff (`diffSide`, with `TestDiffSide` running everywhere on a
captured output), and name their methods as task keys. `add-credential --password-stdin` was added and
the WinRM gate pipes the password. All five password gates then passed against `win-lab`, and the
certificate and modes gates against the host.

**Lesson.** A gate that is never run is not a gate: when an output format changes, run everything that
reads it, and have gates assert on structure (`--json`, Phase 113) rather than on text layout.

## 368. `onboard --json` and `doc --json` wrote terminal control characters raw

**Symptom.** Found while writing `run --json`: `printOnboardJSON` said "JSON's own escaping is what
keeps it inert", and `doc --json` printed an external program's descriptions through the same plain
encoder.

**Root cause.** `encoding/json` escapes the C0 controls but writes DEL, the C1 controls (U+0080 to
U+009F, among them CSI) and the Unicode direction overrides as they are, all of which
`internal/termsafe` treats as unsafe on a terminal. A device's answer or an external Collection's text
could carry them onto the operator's terminal. Reasoned, not demonstrated against a terminal.

**Fix.** `writeJSON` (`cmd/pleiades/jsonout.go`) writes every `termsafe.Unsafe` rune as a `\u` escape,
which decodes to the same text; `onboard`, `doc`, `run` and `adhoc` all use it. `FuzzWriteJSON` holds
it to both promises (inert, and decodes to the input).

**Lesson.** "JSON escaping makes it safe" is a claim about C0 only; text bound for a terminal needs the
terminal's own rules.

## 369. An idle Windows Server guest spends the first power-button press waking its display

**Symptom.** `virt.vbox.vm.stop` on `win-lab`, about half an hour after its last boot, waited five
minutes and failed: still running. The same stop had shut it down in seconds an hour earlier, shortly
after a boot. The guest's System log held no shutdown request (no 1074, no 109) for the press, only the
hard power-off that followed.

**Root cause.** The Balanced plan turns the display off after 600 seconds (`VIDEOIDLE` 0x258), logged as
Kernel-Power 566 session transitions ten minutes after boot. With the display off, the first ACPI
power-button press wakes it and nothing more. Measured: one press with a 90-second wait left the idle
guest running; a second press shut it down in 13 seconds.

**Fix.** Not yet made. `virt.vbox.vm.stop` presses once. Candidates: press again while waiting, or set
the display timeout to never in the install's audit pass.

**Lesson.** A power button is an input event, and an idle guest may spend it on waking up; a stop that
waits should not press only once.


## 370. An address with a byte that is not UTF-8 signed in as a different stored address

**Symptom.** `FuzzAuthenticateEmail`'s seed `"a\xffb@example.test"`, sent with the right password,
signed in as the stored account `a<U+FFFD>b@example.test`. Found writing Phase 79's email fuzz, before
any fix.

**Root cause.** `localauth.NormalizeEmail` and `access.normalizeEmail` each ran
`strings.ToLower(strings.TrimSpace(email))` and checked only for emptiness and an `@`. `strings.ToLower`
goes through `strings.Map`, which writes U+FFFD in place of every byte that is not UTF-8, so the
normalizer repaired a malformed address into a valid key, and any number of different malformed inputs
into the same key. The users form did not check UTF-8 either, so a row holding U+FFFD could be created.
The attacker still needed that account's password; what broke is the rule that a malformed address
fails exactly as an unknown one does.

**Fix.** One rule, `auth.NormalizeEmail` (`internal/auth/email.go`), which both packages now call: it
refuses, on the trimmed input and BEFORE lowercasing, whatever `termsafe.CheckLine` refuses (invalid
UTF-8, control characters, text direction marks, tab, newline). The users form refuses the same at the
field. Tests: `FuzzAuthenticateEmail` (red on this seed first), `TestNormalizeEmail`, `FuzzNormalizeEmail`
(16.6 M executions clean), `TestUsersView_RefusesAnUnusableAddressAtTheControl`.

**Lesson.** Decide a refusal on the raw input. A normalizer that repairs (ToLower, Map, a decoder with a
replacement character) turns a malformed value into a valid key, so checking its output checks nothing.

## 371. A NUL in a sign-in address failed as a Postgres query error, fast and without the decoy

**Symptom.** The extended `TestLocalAuthReleaseGate_EveryFailureIsIndistinguishable`, against the real
binary and real Postgres, sent the bootstrap address with a NUL appended and the right password: 401,
the same page, in 822 microseconds against 33 milliseconds for a known address. The SQLite-backed store
tests could not see it: SQLite keeps the NUL and simply matches no row.

**Root cause.** The NUL survived normalization. Postgres refuses a NUL in a text parameter and lib/pq
does not check first, so `credentialFor` returned a query error, and `Authenticate` returned it wrapped,
with no decoy derivation and no log line. The login handler answers every error with the same page, so
nothing showed it. Not an existence oracle (the query fails before it matches anything), but a path an
attacker can time, and a database failure during sign-in that no operator could see.

**Fix.** `auth.NormalizeEmail` refuses control characters, so a NUL takes the malformed-address path
and its decoy; `Authenticate` now logs both that path (reason only, never the address) and a credential
load that fails. The release gate keeps the NUL, invalid-UTF-8 and combining-mark variants, inside the
login limiter's burst of five.

**Lesson.** Whether a value is legal differs by database. A property that holds on the test dialect needs
one run on the production dialect, through the real path, before it counts.

## 372. An oversized password told a known address from an unknown one

**Symptom.** `TestAuthenticate_OversizedPasswordIsRefusedBeforeTheLookup`, written before the fix: a
1,025-byte password against a known address left `FailedAttempts` at 1, and against an unknown address
ran a decoy. Found by reading `Authenticate`, not by the fuzz.

**Root cause.** `Verify` refuses a password over the 1,024-byte cap before deriving, on the real path and
on the decoy path alike, so neither spent the tens of milliseconds that hide everything else. What
remained was `recordFailure`'s UPDATE, which only an existing account performs: an existence oracle
readable in one or two requests with no derivation to hide it, and a lockout an attacker could run up for
free. Reasoned from the code; not measured over HTTP.

**Fix.** `Authenticate` refuses an oversized password as its first statement: nothing is looked up,
derived or counted, and every address gets the same answer. `ValidatePassword` already refused storing
one, so no real credential is affected.

**Lesson.** An early return that skips expensive work also removes the noise that hid a cheap difference
below it; after adding one, ask what the two paths still do differently.

## 373. Authenticate reported UserID 0 on every successful sign-in

**Symptom.** `FuzzAuthenticateEmail` needed the matched row to compare addresses and read back
`Account.UserID` as 0 for every success ("matched user 0, which cannot be read back").

**Root cause.** `recordSuccess` projected the row `UpdateOne(...).Save` returns, which carries no edges,
so `projectAccount` found no owner and left the id at zero, as its own comment allows. `credentialFor`
had eager-loaded the owner; the update dropped it. Latent: the one caller, the login adapter, keeps only
`Subject`, but `Account.UserID` exists for the audit trail.

**Fix.** Carry the loaded owner onto the updated row before projecting.
`TestAuthenticate_ReportsTheOwnerItMatched` fails (0, want 2) with the line removed.

**Lesson.** An ent update returns the row without its edges; carry across any edge a caller reads.

## 374. A test file named for FreeBSD compiled only on FreeBSD, so its tests silently never ran

**Symptom.** `install_freebsd_test.go`, the FreeBSD installer's tests for `virt.vbox.vm.install`, passed
by not running: `go test -run 'TestInstall'` listed every Windows install test and none of the new ones,
and `-run FreeBSD` said "no tests to run". Nothing failed and nothing warned.

**Root cause.** Go reads a file name ending `_<GOOS>.go` or `_<GOOS>_test.go` as an implicit build
constraint. `_freebsd_test.go` made the file build only when GOOS is freebsd, so on Linux it was an
ignored file (`go list -f '{{.IgnoredGoFiles}}'` named it). A name chosen to describe what the tests are
about read to the toolchain as where they may run.

**Fix.** Renamed to `freebsd_install_test.go`. Swept the tree with `go list -f
'{{.IgnoredGoFiles}}' ./...`: every other ignored file carries an explicit `//go:build` line saying so.

**Lesson.** Never end a Go file name with an OS or architecture name unless the constraint is meant; name
a file about FreeBSD, Windows or ARM by putting that word first. When a new test does not appear in `-v`
output, check `IgnoredGoFiles` before anything else.

## 375. On BSD a file's mode was read without its setuid, setgid and sticky digit

**Symptom.** Against a real FreeBSD 15.1 VM, `file.permissions mode=0755` on a directory whose mode
was 2755 predicted no change and left the setgid bit in place, and `file.directory mode=2750` created
the directory, then read it back as 0750, so the run's diff disagreed with its check and the task would
have reported a change on every run. Found by the BSD half of Phase 46's chmod item
(`TestCLI_FreeBSDFileChecksMatchTheirRealRuns`), the first time the file methods ran on BSD.

**Root cause.** `pkg/remotefile.Stat` asks `stat -c '%f|%a|...'` and falls back to BSD's `stat -f
'%Xp|%Lp|...'`. GNU's `%a` is the whole octal mode, 2755. BSD's `%Lp` is only the low field, the
rwx bits: 755. The fallback was written to cover BSD and was never run on one. Measured on the VM:
`%Lp` gives 755, `%Mp%Lp` gives 2755 (and 4750 for setuid, 1777 for sticky), `%Xp` is the raw mode in
hex as GNU's `%f` is.

**Fix.** The fallback reads `%Mp%Lp`. Mutation-checked: restoring `%Lp` fails the gate's setgid cases.
generic_ssh onboarding grants a FreeBSD kernel POSIX file access, with this gate as its proof.

**Lesson.** A portability fallback that no test runs on the platform it names is a guess; the day it
first meets that platform is the day it is tested. Grant a platform a capability only with a gate that
runs there.

## 376. `adhoc` read `mode=0755` as a number, and the method refused it with advice about runbooks

**Symptom.** `pleiades adhoc bsd-lab file.permissions path=... mode=0755` failed: "mode is int, not
text: quote it in the runbook". The natural spelling of a mode on a command line did not work, and the
error talked about a runbook the user had not written.

**Root cause.** `adhoc` typed every `key=value` the way `add-host --set` types a property: a whole
number became an integer. `file.permissions` declares `mode` a string and refuses a number, correctly,
since 0755 read as a number is 755 and not the mode meant. The documented way round was
`mode:="'0750'"`, YAML inside shell quoting.

**Fix.** `adhoc` reads the method's declared parameter types (`declaredTypes`, from its manifest) and
keeps `key=value` as text for a parameter declared a string; anything else is typed as before, and a
method not yet registered gets the old typing throughout. 199 parameters in the catalog are strings.
The docs now show `mode=0750`. Test: TestParseAdhocParams_KeepsWhatAMethodDeclaresAString.

**Lesson.** When the callee already declares a type, the caller should read the declaration rather than
guess from the text.

## 377. A dead port's open circuit reached a later test's live server on the same port

**Symptom.** Under `make test-repeat`, `internal/catalog/wait`'s TestPath_DiffRecordFailureIsReported
failed once in three runs with "remoteexec: circuit open for 127.0.0.1:45991, too many recent
failures", where 45991 was the port of the in-process SSH server that very test had just started and
that was up the whole time.

**Root cause.** `remoteexec.Shared` memoizes one Runner per Options for the life of the process, and
its breaker counts consecutive dial failures per `host:port` with no decay. The package's `deadPort`
helper takes a port from the kernel, gives it back, and three `wait.connection` tests then dial it until
they time out, which opens the circuit for that address. The kernel hands a given-back port out again,
so a later `remoteexectest.Start` could land on it and inherit the open circuit. `SnapshotForTest`
already documented the same accumulation for one test repeated (`net.ssh.ping`, from `-count=3`); this
is the cross-test form, through port reuse, and nothing in the victim test is wrong.

**Fix.** `deadPort` calls `t.Cleanup(remoteexec.SnapshotForTest())`, so a test that dials a dead port
gets its own memo and its failures are discarded when it ends. It sits in the helper rather than in
each test so a future caller cannot forget it. `go test -count=5` passes for the package.

**Lesson.** Process-wide state keyed by a network address outlives the listener that owned the
address, and ephemeral ports are reused, so a test that poisons an address must clean up the state
itself: the next owner of the port is an unrelated test.

## 378. A parameter a method declared was also the engine's device selector

**Symptom.** Reasoned from code during Phase 40's planning, not executed: `net.netconf.config`
declared `target` as the datastore to configure (running, candidate or startup), and
`engine.TaskTarget` reads `params.target` as the device or tag a task runs on. A task written
`target: candidate` ran against whatever device or tag was named `candidate`, usually nothing at
all, and anyone able to name a device or tag `running`, `candidate` or `startup` received that
configuration push.

**Root cause.** Nothing kept a method's declared parameter names apart from the keys the engine reads
out of the same map. The translator even mapped Ansible's `netconf_config` `target:` straight onto it.

**Fix.** `collection.Register`, the external loader's mirror (`validateMethod`) and `forge
new-collection` refuse a declared parameter named after a key the engine reads
(`pkg/collection/reserved.go`, today only `target`). The method's parameter is now `datastore`, and
the translator maps `netconf_config`'s `target:` to it. Tests: TestRegister_RefusesAReservedParam,
TestHardening_AReservedParamIsRefusedBeforeAnythingLoads, FuzzRegistrationParity (mutation-checked:
without the loader's mirror both fail), TestGenerate_RefusesAReservedParam, three translator cases.

**Lesson.** A map shared between a framework and its plugins needs its reserved keys declared and
enforced at registration, or a plugin will one day give one of them its own meaning.

## 379. Any Runner could write any job's journal rows, first writer wins

**Symptom.** Reasoned from code: `FleetRunnerGrant` lets every Runner publish on
`topology.JournalSubjectAll()`, and the Controller's journal subscriber stored each batch it received
with no check that the job ever dispatched that device. A compromised Runner could falsify or
suppress another job's audit rows, and once rows carry undo values (Phase 40), steer a rollback.

**Root cause.** The subscriber trusted the subject: a batch naming a job and a device was taken as
coming from the Runner that ran it.

**Fix, narrowing rather than closing.** `journal.WithAdmission` over `dispatch.JobStore.DispatchState`:
a batch is stored only for a (job, device) the Controller recorded dispatching, dropped for one it
never sent, and redelivered while the dispatch is not recorded yet; a batch whose entries name another
job or device than the batch is dropped first. A Walk rollback additionally holds every row to the
runbook the job ran, on the Controller and again on the Runner. The plan's per-dispatch MAC key was
rejected: it would be a new secret on the stream, readable by any Runner that pulls the dispatch. The
residual (a compromised Runner forging identifier values for a dispatch it pulled) is recorded in
Phase 105, with results (`ResultSubjectAll`) noted as the same class in Phase 103b. Tests:
TestSubscriberStoresOnlyWhatTheControllerDispatched, TestSubscriberRetriesWhenAdmissionCannotBeAnswered.

**Lesson.** A subject a whole fleet may publish on authenticates nobody; admit a message against the
state that says it should exist.

## 380. A harness Runner published no results, so a Controller-side gate waited forever

**Symptom.** The forks window's release gate hung: the windowed job never completed, because the
Controller never heard any device finish.

**Root cause.** `cmd/runner`'s release-gate harness built its Agent without
`runner.WithResultReporting`, which the real Runner (`cmd/runner/main.go`) always sets. Every earlier
gate read the job's log events, not its results, so the harness differed from the binary it stands
for in exactly the way nothing had looked at.

**Fix.** The harness Agent (and its check Agent) report results as the real Runner does; the other
harness gates still pass.

**Lesson.** A harness standing in for a binary must be composed with the binary's options, not with
the minimum its first test needed.

## 381. An older Runner drops a payload key it does not know, so a rollback it received would re-run the undone runbook

**Symptom.** Caught in design while building the Walk rollback (Phase 40), not observed: a rollback
dispatch is an ordinary dispatch payload plus a `rollback` field of steps. A Runner built before that
field decodes the payload with a plain `json.Unmarshal`, drops the field, and runs `RunbookID`, which
names the runbook being undone.

**Root cause.** Additive JSON is only safe when ignoring the new field is harmless. Here ignoring it
inverts the operation.

**Fix.** Rollback dispatches go on their own subject, `pleiades.jobs.rollback.<device>`
(`topology.RollbackSubject`), pulled by a third consumer (`runner-rollback`) through
`routing.RollbackOnly`, which refuses a dispatch there without steps as unroutable. An older Runner
never creates that consumer, so a rollback waits for one that understands it, as a check does.
Grants on both sides; TestRollbackConsumerIsDisjointFromTheOthers; the dispatch test asserts the
subject for every rollback dispatch, windowed or not.

**Lesson.** When an old consumer would silently misread a new kind of message, change where the
message goes, not what it carries (LESSONS 254).

## 382. A rollback: list written after the run was ignored

**Symptom.** G3 of the Crawl rollback gate: a task with no recorded undo was given a `rollback:` list
after the run, as the design intends (`DAG.Version` ignores `rollback:` for exactly this), and the
rollback still refused it as having no undo.

**Root cause.** The planner asked for the runbook's list only when the journal said the task had one
when it ran (`authored_rollback`), which a list written afterwards can never satisfy.

**Fix.** The planner asks for every change; a list, when there is one, wins over a recorded undo. A
runbook not found (`rollback.ErrNoRunbook`) falls back to the recorded undo, and its absence is named
in the refusal of a change that has none. TestBuild_UsesARollbackListWrittenAfterTheRun; G3 through
the real binary, mutation-checked.

**Lesson.** When a design makes something editable after the fact, test the after-the-fact edit; the
before-the-fact path passing proves nothing about it.

## 383. `--leave` left only a change that could not be undone

**Symptom.** Found while building the Walk gate: naming a change that could be undone in `--leave`
(or the API's `leave`) did nothing, and it was undone anyway. With the rest already undone, the second
rollback then reported "every change is left in place as named", which was false.

**Root cause.** The planner applied `Leave` only on the error path, as an acceptance of a problem,
never as the operator's own decision about a change.

**Fix.** A change named in `Leave` is left in place whether or not it could be undone, and a rollback
with nothing left but already undone and left changes is refused as already undone.
TestBuild_ResumesAndThenHasNothingLeft covers both.

**Lesson.** An acceptance flag that names a thing should act on that thing, not only on the error
that thing produced.

## 384. A run rolled back on a second attempt still counted as a later change

**Symptom.** In the lab: rolling back the run that stopped ubuntu-lab was refused, naming a scratch
run that had been rolled back in full, and that rollback itself.

**Root cause.** The scratch run's first rollback failed at its stop (FAILURE_PATTERNS 388) and its
second succeeded. `fullyUndone` treated any failed attempt of an undo step as final, so a step failed
once and then done still read as not undone, and the later-run guard counted the run and its
rollback as changes made since.

**Fix.** Each undo step's latest attempt decides. TestBuild_RefusesUndoingBeneathALaterRun covers a
failure then a success (cancels out) and a success then a failure (counts). The lab's rollback of the
stop then ran and started ubuntu-lab again.

**Lesson.** A history read for "is this done" must let a later attempt supersede an earlier one;
resume makes retries normal (LESSONS 256).

## 385. virt.vbox.vm.delete's check failed on a running VM where resize reported it uncheckable

**Symptom.** In the lab, `pleiades rollback <run> --mode check` of a VM create predicted the stop,
then failed the delete: "is running; stop it first". The real rollback, which stops first, then
deleted it fine.

**Root cause.** A check predicts each task against the device as it is now. `virt.vbox.vm.resize` and
`cloud.aws.s3.delete_bucket` already answer "could not check" for state an earlier task in the same
run may change; `virt.vbox.vm.delete` predicted the failure instead.

**Fix.** A check of a running or paused VM's delete is not checkable, with the reason; a real run
still refuses it. The rollback check now ends incomplete (exit 3), naming the delete.
TestDelete_Refusals; catalogdata's description updated with it.

**Lesson.** A check's refusal for a state an earlier task can change is a guess about ordering; answer
"could not check", as the documented rule says.

## 386. The journal projection's benchmark timed a projection over nothing

**Symptom.** Adding a benchmark with recorded values, its own assertion (five values recorded) failed
at once.

**Root cause.** `BenchmarkProjectLevel` set `NodeResult.Stats`, but the projection reads
`journalStats` (Stats is nil on the failure paths by design), so the benchmark had measured the
projection of empty stats since it was written.

**Fix.** Both benchmarks set `journalStats` and `journalChanged`, and the new one asserts the values
it expects inside the loop, so a mis-wired benchmark fails instead of timing nothing.

**Lesson.** A benchmark asserts the effect it measures; one that cannot fail can measure the wrong
path for as long as nobody reads its numbers.

## 387. The run lock became the first writer of .pleiades, and a read-only project stopped saying the journal was why

**Symptom.** TestCLI_RunFailsBeforeAnyOutputWhenTheJournalCannotBeOpened: a run in a read-only project
failed with "failed to create .pleiades: permission denied" and no mention of the journal.

**Root cause.** `journal.LockRuns` now runs before the journal opens and is the first thing to create
`.pleiades`, so its error arrived first, in its own words.

**Fix.** A lock failure other than "busy" is reported as "failed to open the run journal: ...", which
is what it is.

**Lesson.** Moving a step earlier moves its errors earlier; they must still name what the user asked
for.

## 388. Rolling back a stop, then at once the start before it, asked a still-booting FreeBSD guest to shut down

**Symptom.** In the lab: after rolling back a stop (which started `bsd-scratch`), rolling back the run
that made it failed at its first step: the guest "was asked to shut down and is still running after
2m0s".

**Root cause.** Not a code defect. A FreeBSD guest started moments earlier did not act on the ACPI
power button within the stop's two minutes; after the same VM had finished booting, the same stop
worked. The recorded undo of a start is a clean stop, deliberately not a power cut.

**Fix.** None in code. Running the same rollback again once the VM had booted continued from the stop
and finished, which is what resume is for. Recorded in the lab README.

**Lesson.** A graceful undo can be refused by timing on real hardware; the recovery is a resumable
rollback, not a harsher undo.

## 389. The forks window's first design was a contract migration

**Symptom.** Phase 110's windowed fan-out first added a `queued` value to `job_task.outcome` and a
unique index over `(job, device_id)`. The migration compatibility classifier
(`internal/ent/migrate/compat.go`) refused both.

**Root cause.** An old Controller reading a new enum value, or a unique index over rows that existing
data may already duplicate, breaks a rolling upgrade.

**Fix.** Expand-only: a `waiting` flag and a nullable `slot` column with a unique `(slot, job)` index
over the new column, and `OutcomeQueued` as a Go-level reading of `waiting`.

**Lesson.** Design schema changes as expand-only first; the classifier is right that a new enum value
is a contract change.

## 390. A chaos test cut its link after a fixed delay, and a longer migration history moved the cut out of the step it meant to break

**Symptom.** `internal/backup`'s TestTake_ACutConnectionLeavesNoBackup failed every time on this
branch, alone as well as under `make ci`: Take failed "counting what the database holds", not in
pg_dump as the test requires. The same test passed on the base commit.

**Root cause.** The test throttles the link to 4 KB/s and cuts it 1.5 s later, with a comment that the
cut was measured to land inside pg_dump. Before pg_dump, Take reads the migration history and counts
what keys open, through the same throttled link, and this branch added three PostgreSQL migrations.
The reads before the dump grew past 1.5 s, so the cut landed in them.

**Fix.** The test cuts once PostgreSQL shows the dump's session (`application_name` is
`pleiades-backup`, which only the client programs carry, and pg_dump is the first Take runs), polled
over the direct connection. It passes in about 4.5 s, three times running. A first attempt polled for
`pg_dump`, never matched, and passed only because the poll gave up at a minute: a pass at the timeout
is not a pass.

**Lesson.** A fault injected after a fixed delay is placed by the speed of everything before it; place
it by an observable event of the step under test instead.

## 391. The upgrade gate expected the previous build to be refused after every upgrade

**Symptom.** `make push-gate` on this branch failed tests/e2e's
TestUpgradeGate_ThePreviousBuildKeepsServingWhileThisOneMigrates, alone as well as under load: at its
last step the previous release, started again against the database this build had migrated, kept
running, logging that the newer migrations were within its compatibility window.

**Root cause.** The test's fifth step asserted the previous build is always refused. That holds only
when the upgrade crosses a contract migration, which moves the compatibility floor past the previous
build. This branch's three migrations are expand-only (the classifier requires it of new work), so the
floor stayed at 0029 and the previous build was, correctly, allowed to serve: the rollback path the
window exists for. Every earlier run of the gate had crossed a contract migration, so the expand-only
case had never been reached; the same shape as FAILURE_PATTERNS 300, where a gate's premise held only
for the branch that wrote it.

**Fix.** The gate reads the floor `migrate --plan` reports for the migrations it applies. Inside the
window the previous build, started again, must become ready; past it, it must be refused and say why,
as before.

**Lesson.** A gate over a policy with two outcomes asserts the one the inputs call for, and reads which
from the same source the product does.

## 392. A generic_http device's credential followed its base URL to wherever inventory pointed it

**Symptom.** Found while planning Phase 117a (reasoned 2026-09-28), measured 2026-09-29 by
TestRepointedHTTPDeviceSendsItsCredentialNowhereNew on the unfixed tree: after `pleiades set-host api1
--set base_url=<another host>`, the next `http.request` path call succeeded against the new host with
the device's stored bearer token, with no new onboarding. The other host only needed a certificate the
trusted authorities vouch for.

**Root cause.** Onboarding proved an API at one base URL and recorded only the capabilities it found.
`base_url`, `http_auth`, the TLS settings and the plaintext flag stayed ordinary properties
(`pkg/inventory/discovery.go` reserved only `discovered`), and `generic_http`'s capabilities were
rebuilt from the discovery without comparing it to the record it was made against. So anyone with
`inventory:write`, or with write access to `inventory.yaml`, could redirect a stored credential.

**Fix.** A discovery carries a binding, a SHA-256 over the properties that decide where a probe went and
what it trusted (`inventory.BindingDigest`, recorded by onboarding through
`inventory.DiscoveryBinder`). `generic_http` and `generic_grpc` grant nothing from a discovery whose
binding no longer matches the record, and say to onboard again (`inventory.StaleDiscoverer`). The rule
is on the read side, so it holds for `set-host`, the Controller's PATCH, a sync and a hand-edited file
alike. The SSH-based types are not bound: a strict host-key check already refuses a repointed host.

**Lesson.** Proof about a remote endpoint holds only for the endpoint it was made against; bind it to
the properties that name that endpoint, and check the binding where the proof is read, not where the
properties are written.

## 393. A bound credential's injected secret variable was readable by runbook conditions on the native path

**Symptom.** Reasoned 2026-09-28, measured 2026-09-29 by
TestAdapter_Execute_AnInjectedSecretIsNotReadableByWhenCEL on the unfixed tree: a task gated on
`has(vars.token)` ran when a bound credential injected `token` as a secret extra variable.

**Root cause.** `injectedVariables` (`internal/adapters/native/inject.go`) merged every injected extra
variable into the run's variables, secrets included, and those become CEL's `vars` root. Masking scrubs
text, not control flow, so a runbook could branch on a secret one bit per task, and once task parameters
render (Phase 117a) it could copy one into a request bound for another host.

**Fix.** An injected variable holding a masked value anywhere (`wire.Injected.Mask`, containment rather
than equality, since a rendered value can embed a secret) is withheld; non-secret ones still merge, and
a collision is still refused first. The job's log names each withheld variable, never its value. A
native method still receives its credential through `InjectSecrets`, and the Ansible path is unchanged.

**Lesson.** A channel built to carry configuration into expressions must not carry secrets, even
masked ones: masking protects what is printed, not what is decided.

## 394. `pleiades run` printed a device credential a Collection method echoed back

**Symptom.** Suspected 2026-09-28, measured 2026-09-29 by TestRunMasksTheCredentialAMethodWasHanded on
the unfixed tree: `pleiades run --json` against a `generic_http` device whose API echoed the bearer
token printed `"content": "{\"seen\":\"echo-token-...\"}"` with the token in the clear.

**Root cause.** On the Crawl tier only the SSH transport masked a device's credential out of what it
captured (`internal/engine/action_ssh.go`). A Collection method's credential, handed to it through
`InjectSecrets`, never joined the run's masking set, which held only `register_mask`'d values, so a
stat or an error echoing it reached the report unmasked. The Walk tier was not affected: its adapter
masks with the dispatch's own secrets.

**Fix.** `ActionResult.Secrets` carries the credential a method was handed, on success and failure
alike, and the executor adds it to the run's masking set (`recordActionSecrets`); `checkAction` passes
it through. Every value is masked except the identifiers (`credentialSecrets`: username, a
certificate's public half, a seeded login's public parts), as the SSH transport already did, because a
substring scrub of a short username corrupts every path containing it.

**Lesson.** Masking belongs to the credential, not to the transport: every path that hands a secret to
code must also hand it to the masking set.

## 395. `http.request` read a response body into memory with no bound

**Symptom.** Found 2026-09-29 while bounding Phase 117a's `json` stat: there was no existing body bound to
reuse. `http.request` read every response with `io.ReadAll`, so an API that answered with gigabytes (or a
redirect to something that does) filled the memory of `pleiades run` or of the Runner's per-task child.
Reasoned from the code; not driven to an out-of-memory kill.

**Root cause.** The method's description said it "holds the body in memory, so it is for calling an API
rather than fetching a large file", and nothing enforced the second half: a documented intent with no
limit behind it. The call runs on the CLI host or a Runner, so the far side of the request, which a
runbook author does not control, chose how much memory the platform used.

**Fix.** The body is read through `io.LimitReader` to one byte past `requestMaxBodyBytes` (16 MiB); a body
over the bound fails the task, naming the bound, rather than being cut short, since a truncated document
can still parse and say something it does not. `TestRequest_RefusesABodyOverTheBound` holds a body at the
bound accepted whole and one byte over refused.

**Lesson.** A size a method's description promises is a limit its code enforces, or it is a claim about the
far side's good behavior.

## 396. A release gate lost a dispatch to lock contention with the retries of the failure it had just asserted

**Symptom.** 2026-09-29, the Phase 117a `make -k ci` (and the first try before it, and one run alone right after
a gate was killed): `TestGenericWalkReleaseGate_DeviceAccessorsReachTheRunner` timed out after 60 seconds
waiting for its third dispatch's `task.completed`. It passed alone in 10 to 14 seconds every other time. Main's
own clean-tree run of the same test that day shows the same overlap and passed only on timing.

**Root cause.** The gate sent four dispatches of one device ID in a row, and the second one fails on purpose
(an address-only dispatch, as an older Controller sends it). The Runner handles a failed execution through
`event.HandleDeliveryFailure`, which NAKs it with backoff up to `MaxDeliver` (5), so that failure was retried
four more times while the gate went on. Each retry held the device's lock while it ran. The third dispatch
arrived in the middle of those retries, and a contended delivery is NAKed on a 200 ms to 1.6 s backoff that
counts toward the same `MaxDeliver` (`internal/runner/agent_handle.go`, contention branch). In the failing run
all five of its deliveries landed on a held lock within six seconds, JetStream stopped delivering it, and
nothing reported a result, so the job would have stayed `running` forever. The log reads "device lock
contention, retrying later" five times and then nothing. The product defect is the stranding itself: the
contention branch never checks the delivery count, nothing listens for a max-deliveries advisory, and nothing
sweeps a dispatch that never reported. It was already recorded, from reading the code, as unreproduced; this
is its first reproduction.

**Fix.** The gate's defect is fixed here: each dispatch now names its own device (`"api-gate-" + jobID[:8]`),
since this gate is about a dispatched device's accessors reaching the Runner, not about contention. With that
change, three runs in a row under `-race` passed in 10 to 11 seconds with no contention line in the log. The
product defect is not fixed by this entry. Its fix, already designed, splits the contention branch three
ways (the lock held by the same dispatch, held by another run with deliveries left, held by another run on
the final delivery, which reports the job failed as "device busy" and terminates the message), and it needs
the lock to record its owner.

**Lesson.** A test that asserts a failure and then reuses the same resource is testing the platform's retry
policy too, whether it means to or not: a failure on a queue with redelivery is not over when its result
arrives. When a gate times out only under load, read the Runner's own log for the resource's lock before
blaming the machine; "passes alone" was true here and still hid a real stranding.

## 397. A strict coverage run that lost one test to Docker never compared coverage to the floors

**Symptom.** 2026-09-29: the Phase 117a `make -k ci` reported its coverage step as failed because
`TestTicketRunbookReleaseGate` lost its sshd container, and every failed test passed alone, so the branch
looked ready. The `make push-gate` on the committed tip then ran 1h 28m and failed on something that run had
never shown: five packages below their recorded floors (the native adapter at 92.5% against 92.9%, and
`internal/catalog/http`, `internal/validate`, `pkg/collection` and `pkg/inventory` under 100%).

**Root cause.** The strict `tools/coverage-check` stops at the first test failure ("a test failure, not a
coverage question, must be fixed first") and compares nothing against `coverage-floor.json`. That is
deliberate, but on this machine some container test fails in almost every full run, so a strict run that
fails for Docker reports no coverage at all. Reading "failed only on Docker" as "passed everything else"
skipped the one check that had not run.

**Fix.** Tests for each uncovered branch, each driven through the path it guards: the Runner's renderer
option through a real dispatch (and the render refusal without one), the withheld-variable warning's publish
failure, a repointed device's refusal, the json stat's recording failure, `http.request`'s `DeviceCall`, an
unnamed task's finding, `Register`'s execution-context refusal, and a binding over a value JSON cannot
encode. All four packages are back at 100%, and the native adapter is at 94.1%.

**Lesson.** When a gate step fails, ask whether it failed before or after it measured anything. A coverage
step that failed on a test has said nothing about coverage, so measure the touched packages
(`go test -cover`) against their floors before paying for the next full gate.

## 398. A canceled call took a recovering target's half-open probe and kept it, so the device was never dialed again

**Symptom.** Found 2026-09-29 while moving the circuit breaker to `pkg/breaker`, by reading the retry loops
rather than by an outage, then measured: on a Runner whose circuit for an address had opened and whose
cooldown had just run out, one call arriving with an already-canceled context left every later call to that
address refused with "circuit open, too many recent failures" and no dial, for the life of the process.
`TestConnect_ACanceledCallDoesNotKeepTheProbe` and
`TestDialFinalLegWithRetry_ACanceledCallDoesNotKeepTheProbe` (`pkg/remoteexec/probe_lost_test.go`) both
failed on the unfixed tree with one dial where two were due.

**Root cause.** `Allow` hands out the one half-open probe, and only a recorded outcome gives it back. Both
retry loops (`dialWithRetry` in `dial.go`, `dialFinalLegWithRetry` in `tunnel.go`) called `Allow` first and
checked `ctx.Err()` second, and `pkg/retry.Do` calls its attempt without looking at the context first. A done
context therefore claimed the probe and returned with nothing recorded, and the half-open branch had no way
out of its own: the same wedge as #146, reached through a second door. A canceled task, a timed-out one or an
operator's interrupt landing just after a cooldown was enough.

**Fix.** Both loops check the context before `Allow`, and the WinRM transport's new loop does the same. The
breaker itself now leases the probe (`pkg/breaker`, `Allow` and `Permitted`): one nobody records an outcome
for is reissued after one cooldown, so the next caller that loses a probe, by whatever route, costs one
cooldown instead of the process. `TestBreaker_ALostProbeIsReissuedAfterItsLease` pins the lease, and
`TestBreaker_ACanceledCallDoesNotTakeTheProbe` (`internal/transport/winrm`) the ordering for WinRM.

**Lesson.** When a resource is handed out by one call and returned only by another, every early return
between the two is a leak, and a leak of the only one there is is a lockout. Fix the path that leaked it,
then make the resource expire, because the next early return will be written by someone who never saw this
one. #146 fixed the first door and left the lock without a timeout.

## 399. The SSH dial retry sent a refused password three times, and counted it against the circuit every other job shared

**Symptom.** Found 2026-09-29 by reading `pkg/remoteexec`'s retry predicate while moving the circuit breaker,
then measured. One task with a wrong password produced three "Failed password" lines in a real OpenSSH 10.3
server's log (`TestSSHContainer_ARejectedPasswordIsPresentedOnce`, run with the fix reverted), and a real
x/crypto SSH server was sent the wrong password three times by one `Connect`
(`TestConnect_ARejectedPasswordIsPresentedOnce`). The second wrong-password call on the same Runner already
met an open circuit (`TestConnect_ARejectedPasswordDoesNotOpenTheCircuit`), so a third call carrying the right
password, from any job, would have been refused with "circuit open" and no dial.

**Root cause.** `dialWithRetry`'s `retryable` refused only the open-circuit error, and every dial error was
recorded as a breaker failure. A refused credential fails inside `ssh.NewClientConn` like a dropped
connection does, and golang.org/x/crypto/ssh gives it no type or sentinel, only the text "ssh: unable to
authenticate", so nothing told the two apart. The package's own `TestSSHContainer_WrongPasswordFails` had
set `MaxRetries: 1` with the comment "an auth rejection is not a transient condition retrying would fix":
the test knew and production did not.

**Impact.** Three failed logins per task. On a host using `pam_faillock` at `deny=3` one task locks the
account. On OpenSSH 9.8 or later, whose default `PerSourcePenalties` charges a source five seconds per
failed login and refuses it outright at fifteen, one task gets the Runner's address refused by that host for
every job; the container test run found this when a follow-up test was refused by sshd itself. And because a
circuit is per address and shared by every task a Runner runs, anyone able to launch a job against a device
with a wrong credential could make every other job to it fail fast for thirty seconds at a time.

**Fix.** `authRejected` (`pkg/remoteexec/dial.go`) recognizes the library's refusal text, and a refused
credential is neither retried nor counted: it proves the address is reachable, so it is recorded as the
breaker's success. The text is pinned by tests that run the real client against a real server, so a library
upgrade that rewords it fails a test rather than quietly bringing the retry back. WinRM got the same rule
from the start (its breaker counts only a network failure before the shell opens), and a client certificate
refused by a TLS alert is no longer retried there either.

**Lesson.** A retry is a claim that the failure is transient. Before a failure class goes into a retry
loop or a shared failure counter, ask who answered: a network that did not answer may answer next time, a
server that said no will say no again, and every repeat is one more strike on somebody's lockout counter.
When a test has to switch a retry off to make sense, the test is reporting a production defect.

## 400. A WinRM host that never answered was reported as a command that "may still be running", which no retry or breaker could see

**Symptom.** Found 2026-09-29 while checking the new WinRM circuit breaker's edge cases against the lab host,
whose firewall drops packets to a closed port instead of refusing them. With an operation bound shorter than
the operating system's connect timeout (thirty seconds), every call to such an address ended with "gave up
waiting for <host> after <bound>. The command may still be running on the device", although no shell had
opened and no command could have been sent. `TestExecute_ADeadlineBeforeAnyShellIsNotStarted` failed on the
unfixed tree with exactly that message.

**Root cause.** `Execute` enforces its bound itself (the library ignores contexts), and its timeout branch
had one wording for every case. It already knew better: `x.started()` reports whether a shell was ever
assigned, and `stopAbandoned` used that to decide there was nothing to stop, but the message and the error
type ignored it. Because the error was not a `*NotStartedError`, the WinRM Adapter's retry did not retry it
and its circuit breaker did not count it, so a silent host was retried by nobody and never failed fast.

**Fix.** With no shell open, `Execute` returns a `*NotStartedError` wrapping the context's error and saying
the command was never sent. The Adapter treats a deadline there as a network failure (retried, and counted
by the breaker) and a caller's cancellation as neither. `TestSilentPort_OpensItsOwnCircuitOnly` proves it
against the real host: two tasks to the dropped port open its circuit after five sends, the third is refused
without sending, and the real listener on the same host is still reached.

**Lesson.** An error's type is a claim about what happened, and a caller's policy (retry, count, report)
is built on that claim. When one branch can reach the same error from two different states, it must ask
which state it is in, especially when the code already knows, as `stopAbandoned` did here.

## 401. A test fixture matched *.log in .gitignore, so it was never committed and its tests failed in every clone but one

**Symptom.** Found 2026-09-29 by Phase 118's clean room, the first run of this repository's tests in a fresh
clone on a machine other than the one that wrote them: `pkg/cloudinit`'s `TestHostKeys` and
`internal/catalog/virt/vbox/vm`'s `TestHostKeys` and `TestHostKeys_Refusals` failed with "open
.../testdata/console-ubuntu-2404.log: no such file or directory". Every one of them had passed on every gate
run here since 2026-09-27.

**Root cause.** The fixture is a captured VM console log, and `.gitignore` ignores `*.log`, so `git add` of
the package never picked it up and nothing warned. The file sat in this working tree, the only place any test
ever ran, so every gate here passed and every clone anywhere else would have failed three tests.

**Fix.** A `.gitignore` exception for `pkg/cloudinit/testdata/*.log`, and the fixture committed after reading
it for anything secret (a boot log and the lab VM's public host keys). `make test-clean-room` now runs the
container-free packages in a fresh clone inside a pinned Go container, which is the check that finds this.

**Lesson.** A test suite that has only ever run in the tree that wrote it has not shown that it works for
anyone else, and a broad ignore rule is the classic reason. Run it from a fresh clone somewhere without your
home directory, tools or identity, and treat what fails there as defects, not as environment trouble.

## 402. testgate named a failing test and never said why it failed

**Symptom.** Found 2026-09-29 by the same clean-room run: `testgate -strict` printed six failing tests by
name and no output at all, so learning why meant re-running each test by hand in the same container. In CI,
where the gate is about to be the shared answer, that means a red job nobody can read.

**Root cause.** `testgate` runs `go test -json` and echoes one line per package, which keeps a whole-suite log
short (a full event stream once killed a push with SIGPIPE, as `RunGoTestJSON`'s doc records). A test's own
output lives only in that stream, and nothing printed it back for the tests that failed.

**Fix.** `flakegate.FailureOutput` returns the end of a failure's own output from the run's events, and
`testgate` prints it for every failure it reports, strict or confirmed in isolation
(`TestFailureOutput_ShowsWhyATestFailed`).

**Lesson.** Cutting a log's volume is right; cutting the part that explains a failure is not. When a tool
summarizes, it must keep the evidence for exactly the lines that make it exit non-zero.

## 403. A coverage floor for a package deleted seven weeks earlier was never read, so the ratchet guarded nothing there

**Symptom.** Found 2026-09-30 by `coverage-check -measured` on its first real run (Phase 118's gate):
`internal/ansible: floor 87.2%, no coverage number in this run`. The package had been removed on 2026-08-10
(commit 8483cca1, Phase 17, in favor of `internal/adapters/legacy`), and every gate since had reported its
coverage check green.

**Root cause.** The old check walked the packages a run measured and compared each against its floor. A floor
with no measured package behind it was simply never visited, whether its package was deleted, renamed, failed
before printing a number, or never ran; all four passed identically. That is FAILURE_PATTERNS 397's shape
(a check that measured nothing reported nothing wrong) in the floor file itself.

**Fix.** The stale floor is removed, with the reason in `coverage-floor.json`'s header. `coverage-check
-measured` fails any floored package with no number, naming the three things that cause it.

**Lesson.** A ratchet has two directions to check: every measured package against its floor, and every floor
against a measurement. Walking only the first lets a floor outlive what it was protecting, and a list of floors
nobody reads is a list of claims.

## 404. A branch covered only when a timer lost a race dropped pkg/retry below its floor once coverage ran under -race

**Symptom.** 2026-09-30, the second run of Phase 118's gate: `pkg/retry: 97.1% dropped below its floor of
100.0%` on a run that changed nothing in `pkg/retry`; the first run of the same gate had passed it. Six
race-enabled runs measured 100.0 five times and 97.1 once, the missing block always `do.go`'s return when the
context ends during the sleep between attempts.

**Root cause.** `TestDo_UnlimitedRetriesUntilContextDone` reaches that branch only when its 30 ms deadline
lands inside a sleep rather than inside an attempt. Without the race detector it nearly always did; with it,
attempts run slower and the deadline sometimes lands inside one. The old coverage pass ran without `-race`, so
the dependence never showed. Phase 118's one pass measures coverage under `-race`, which the plan named as an
edge case (a timing-only branch can move) and this is its first real instance.

**Fix.** `TestDo_ContextEndingDuringTheSleepStopsAtOnce` cancels the context inside `delay`, which `Do` calls
just before it sleeps, so the sleep always meets a done context: eight race-enabled runs, eight at 100.0%.

**Lesson.** A coverage floor of 100% states that every branch runs every time, and a branch reached only by
winning a timing race breaks that promise on a busier machine. When a floor drops with no code change, find
the block that moved and write the test that reaches it on purpose, rather than lowering the floor.

## 405. A package's coverage was read from the run where a contended test stopped partway, so contention looked like a coverage drop

**Symptom.** 2026-09-30, the third run of Phase 118's gate: `internal/ent/migrate: 86.2% dropped below its
floor of 86.5%`. The same run had tolerated `TestApply_APartitionedWinnerReleasesItsClaim` in that package,
which failed under load and passed when re-run alone.

**Root cause.** The one pass records each package's coverage from the run itself, and a test that fails
partway runs fewer statements, so a package with a contention failure reports a partial number. The old
tolerant coverage run avoided this by accident: it dropped a failed package's number, which left that
package's floor unchecked (FAILURE_PATTERNS 403's shape). Reading the number instead of dropping it was right;
trusting it was not.

**Fix.** When a failure is judged contention, `testgate` re-runs that whole package alone with the same
arguments and records its coverage from that run; a package that fails even alone becomes a confirmed
failure (`remeasure`, proved by `TestRun_ContentionIsReMeasuredAlone`, whose test fails once and then
passes, so the partial run measures 50% and the re-measure 100%).

**Lesson.** A tolerated failure means the test is fine; it does not make the run's other numbers fine. Any
measurement taken from a run you have decided to forgive has to be taken again from the run you trust.

## 406. Coverage floors recorded where LocalStack ran would have failed every CI run, and every `make ci` without a token

**Symptom.** Found 2026-09-30 by reading, before the first pull-request run of Phase 118's workflow, while
recording floors for new packages. `internal/catalog/cloud/aws/ec2` has a floor of 96.7% and
`internal/catalog/cloud/aws/s3` one of 98.0%. With `LOCALSTACK_AUTH_TOKEN` unset they measure 37.6% and 47.1%,
and the workflow never sets it, so the `coverage` job would have failed on every pull request. So would `make
ci` on any contributor's machine without a LocalStack account, for a reason unrelated to their change. Every
local gate passed, because the developer's shell had the token.

**Root cause.** A floor is a number measured on one machine, and a skipped test lowers a package's number
without saying why. The floor check could not tell "this code lost its tests" from "this run could not run
them", so it could only fail both or pass both. The LocalStack tests skipped with a bare `t.Skip`, which the
skip ledger listed but nothing else read.

**Fix.** Every LocalStack test stops through `testsupport.LocalStackToken`, which calls `Require("localstack")`,
so its skip reads `needs localstack: ...`. `testgate` records each package's missing requirements beside its
coverage (`flakegate.Missing`, `flakegate.Measurement`), and `coverage-check -measured` names a package below
its floor that lacked something as unchecked, neither pass nor failure. `TestMissing_ReadsTheReasonRequireGives`
holds the skip format and the parser to one another. Proved through the real tools: `testgate` over `ec2`
and `s3` without the token, then `coverage-check` over the real `coverage-floor.json`, passes naming both,
and the same numbers without the missing record fail as before. The workflow passes an optional
`LOCALSTACK_AUTH_TOKEN` secret and, when it is set, requires LocalStack, so a dead token fails the job
instead of skipping.

**Lesson.** A measurement is only comparable to a threshold taken under the same conditions. When a check
compares numbers from two machines, record the conditions with the number, or the check will be wrong on
whichever machine the threshold was not set on, and it will be wrong silently on the one where it was.

## 407. The view reachability check hand-listed 8 of 22 views, so dropping any of the other 14 passed

**Symptom.** `TestViewConformance_RegisteredAndReachable`, the gate for FAILURE_PATTERNS 52 (a view package
nothing imports), checked a literal list of eight view names. Fourteen views were added after it was written,
none to the list. Replacing `labels.Register()` in `internal/ui/resources/registrars.go` with a no-op left the
test green.

**Root cause.** The check moved the gap it was written to close one step along: a view had to be added to
`registrars.go` to be reachable, and then to a test's list to be checked, and nothing required the second.

**Fix.** The list is read from source: `viewPackageNames` parses each package under `internal/ui/resources`
for its `Name` constant, and a package without one fails. The same mutation now fails naming `labels`.

**Lesson.** A completeness check that works from a hand-kept list is only as complete as the list. Derive the
list from the thing being checked, so that adding the thing adds it to the check.

## 408. A Vault container was "ready" on a log line before Docker forwarded its port, so the first write was refused

**Symptom.** 2026-09-30, `make push-gate` on the Phase 118 tip: `TestReleaseGate_AnInputResolvesOutOfARealVault`
failed with `writing the secret: Post "http://localhost:43674/...": dial tcp 127.0.0.1:43674: connect:
connection refused`, 2.00s into the test, and passed when re-run alone. `flaky-packages.json` had carried the
package since 2026-08-26 as contention. This was the first run that printed a tolerated failure's own output
(FAILURE_PATTERNS 402's fix), and the output said it was not a lost race at all.

**Root cause.** The container waited for `wait.ForLog("Vault server started!")`, which proves the server is
listening inside the container. It does not prove Docker is forwarding the mapped host port, and under load
the gap between the two is long enough for one request to land in it. The test's first request is a single
`http.DefaultClient` POST with no retry, so it found the gap; the sshd containers that wait on a log line the
same way reach their port through the SSH dial's retry, which hides the same gap.

**Fix.** The wait is `wait.ForAll` of the log line and an HTTP 200 from `/v1/sys/health` through the mapped
port, both under `testsupport.ContainerStartupTimeout`. Health answers 200 only once Vault is initialized,
unsealed and active, and it goes through the same port the test then writes to. The same wait cannot finish
before the mapped port exists, which also closes the 2026-08-26 symptom (`port "8200/tcp" not found`), so the
package's `flaky-packages.json` entry, which called both a lost race rather than a defect, is removed.

**Lesson.** A readiness check proves the path it checks. Wait on the path the test will use, through the host
port it will use, rather than on something the server says about itself inside the container. About fifteen
other containers here still wait on a log line alone; they are not failing because their callers retry, which
is luck rather than design.

**Follow-up, the same day.** The pattern is now shared: `testsupport.ForGreeting` (and `SSHGreeting`) reads a
server's first line through the mapped port, generalizing the NATS greeting wait FAILURE_PATTERNS 353 added,
and stops at once when the container exits. Every sshd and netopeer2 container that publishes a port waits for
its log line and then its banner; the generic gRPC device waits for its listening port too, and the ServiceNow
mock for an HTTPS answer. The Ansible gate's sshd publishes no port and is reached from another container, so
it keeps its log wait.

## 409. A roadmap item cited `TestCheckCmdEnvReads`, which was never written, and the refusal it named was tested only by a lab gate

**Symptom.** 2026-09-30, the roadmap tracker's missing-test check flagged a finished Phase 75 item, "pass
runbook data into a shell as data", for citing `TestCheckCmdEnvReads`. No commit ever defined it.
`checkCmdEnvReads` is the refusal that keeps a cmd script from reading its own environment value as `%NAME%`,
which cmd.exe expands before it parses the line: measured on Windows 11, a value `a & echo INJECTED` ran the
echo. The only test that reached it was the WinRM modes Release Gate, which skips on every machine without the
lab's Windows host.

**Root cause.** The item's evidence was written from the plan when the item closed, not read from the tree,
and a gate that needs a lab host is green everywhere else by skipping. Nothing compared the two until the
tracker's check.

**Fix.** `TestCheckCmdEnvReads` exists now: both expansion forms (`%NAME%`, `%NAME:...%`), any case, the
`!NAME!` form that stays text, a variable the command does not set, and a longer name sharing the prefix.
`TestRun_RejectsBeforeTouchingCredentials` gained the case through `Execute`, so the refusal is shown to come
before any credential is read. Dropping the substring branch fails two cases. The tracker's other stale
citations (a file moved to `pkg/breaker`, three renamed tests, a report built as two files) now name what
exists, with the history in prose.

**Lesson.** A security refusal proved only by a gate that needs a lab is unproved on every other machine. Give
it a unit test that runs everywhere, and let the lab gate prove the part only the lab can: that the real shell
agrees.

## 410. `tools/doctor`'s coverage was a property of whichever machine ran its tests

**Symptom.** `tools/doctor` measured 71.1% here with no floor. Its tests ran the real checks, so a machine with
Docker, every pinned scanner and pywinrm covered the "ok" branches and none of the "fix" ones, and a bare
machine the reverse. A floor recorded on one would fail or pass on another for no change at all, and nothing
proved the fix lines a new contributor most needs.

**Root cause.** Code that asks the machine was only ever tested against the machine running the test.

**Fix.** The checks ask through four replaceable seams (`run`, `lookPath`, `getenv`, `goVersion`), and
`checks_test.go` describes a machine with everything and one with nothing, plus a scanner at the wrong or an
unreadable version and a toolchain older than go.mod asks for. Coverage is 82.2% on any machine; the live test
still asks the real one.

**Lesson.** A test of code that reads its environment has to run against an environment it describes too, or
its coverage, and any floor set from it, belongs to whoever ran it last.
