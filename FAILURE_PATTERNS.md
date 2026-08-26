# Failure Patterns

A catalogue of bugs found while working this codebase. Append an entry (Symptom, Root cause, Fix,
Lesson) before debugging anything new, per `.AGENTS/AGENTS.md`.

1. Capability name collision between two packages
2. Unchecked property map assertion panicking the dispatcher
3. go vet copylocks warning from embedding a mutex by value
4. stdlib `flag` rejects the exact CLI usage the spec documents
5. A validation test that could never actually fail
6. Pre-existing data race in the log-streaming test package
7. A parallel terminology rename missed a same-meaning string outside the renamed files
8. Parallel agents' own build/test runs can observe a mid-flight, inconsistent repo state
9. Ansible-shape sniff over-fired on a bare, non-Ansible top-level list
10. Unknown runbook keys are silently dropped, which would defeat any migration guarantee
11. A non-string `target` silently disables two validation rules
12. An instruction that cannot be followed will be violated
13. An empty query result is indistinguishable from a misaimed query
14. `internal/runner`'s test package did not compile, and the one visible symptom was not the only bug
15. `lock.Manager.Acquire`'s `ttl` parameter is honored by one implementation and silently ignored by
16. `yaml.Marshal` panics, rather than returns an error, on a value it cannot encode
17. `adapters/native.Adapter` publishes to a NATS subject its own stream is not configured to capture
18. An unvalidated runbook `id:` can widen or misroute a NATS subject
19. A single `when_cel` string can hang a task's execution for minutes with no special privilege
20. JWT validation has no issuer/audience pinning, deferred pending a real token-issuing path
21. `add-host --set port=<n>` silently produced an unusable port, defaulting `SSHPort()` to 22
22. `credential.Credential` leaked every secret through `encoding/json` and `log/slog`'s JSON handler
23. A masked command's `Stdout`/`Stderr` did not cover the transport-error path, only the success path
24. `MkdirAll`'s mode argument only applies to a directory it creates, not one that already exists
25. A bulk-insert batch size tuned against one schema width silently stopped being safe after the schema grew
26. Stray git worktrees under `.claude/` were not gitignored and polluted whole-tree tooling run from repo root
27. An architecture test written to check import layering also caught a real, dormant production bug
28. A second Rule added to an already-documented O(n) call site silently paid that cost twice
29. An idempotency key derived from message content, not message identity, silently vanished real events
30. A consumer that bypasses the event envelope silently zeroed every field after an unrelated interface change
31. A single shared consumer meant every SSE log viewer saw whatever job was requested first, not the job it asked for
32. An SSE handler committed its response to 200 before it could still fail
33. Fixing #32 introduced a genuine data race between the handler and its own delivery callback
34. A shared lock's own join lost most of a concurrent burst under the default contention policy
35. A fuzz test that never called the function under test
36. A sub-second positive lock TTL reached the NATS server as an opaque API error instead of a clear one
37. A key containing consecutive dots passed this package's own key validation but silently hung the server
39. A canceled context raced a ticker-driven renewal call from two different directions at once
40. A benchmark's own container teardown was measured as part of the call it was timing
41. A new enum field had a Go-to-string direction but no string-to-Go direction, so a runbook could not actually author it
42. A composition root's new fail-closed startup requirement broke an existing test that spawns the real binary as a subprocess
43. A property named the same as the encryption marker key silently bypassed encryption of the entire map
44. A batch query's decrypt-error handling aborted the entire result set on the first bad row
45. A key-rotation write had no compare-and-swap, silently losing a concurrent legitimate write
46. An exclusive file create still left a window where a concurrent reader could observe an empty file
47. A key-rotation pass ran synchronously before the HTTP server started listening
48. A classification path resolver rejoined its whole prefix on every level, making it quadratic in path length with no bound on that length
50. A shared CLI positional-argument parser silently swallowed a real flag as a bare boolean flag's value
51. `pkg/capability`'s own Windows capability file never compiled on any non-Windows platform
52. A generated Collection catalog compiled and unit-tested cleanly while being completely unreachable from the real binary
53. A real end-to-end test writing into the live module tree can race an architecture test's `go list` scan of the same tree
54. A real end-to-end test's fixture used a device property key the code under test never read, so a passing test proved nothing about the feature it appeared to exercise
55. Two end-to-end tests in different packages each removed the *shared parent* directory their per-process fixtures lived under, so a parallel run deleted one test's package while it was still building
56. A repository port had no create operation at all, so the first sync plugin had nothing to onboard a device with
57. A file-backed repository named its own storage backend as the authoritative sync plugin, so the first real sync plugin reported every host as a conflict
58. A read-only guard that failed loudly turned a dry run into an abort on the first device
59. A migration-generation tool's zero-option schema diff silently never emits `DROP COLUMN`
60. A `coverage-floor.json` regression check flagged four packages this phase never touched
61. A full concurrent `go test ./...` run reliably flakes on containerized-dependency packages in this sandboxed environment, and reducing package-level parallelism reliably fixes it
62. Two independent unbounded-recursion sites in the DAG builder scaled stack usage directly with externally-supplied input size
63. A caller-supplied job ID was concatenated straight into a NATS subject, so `>` streamed every job's logs
64. A live SSE handler mutated its response header map while its own consumer goroutine was already writing the body
65. `GET /api/v1/jobs/{id}/logs` authenticated every caller and authorized none of them
66. An unauthorized `runbook:execute` dispatch returned HTTP 200 with a per-device failure tally
67. `cmd/demo` mounted a production SSE handler on a router with no auth, no tracing, and no rate limiting, and its own advertised URL had 400'd for a full phase without anyone noticing
68. Phase 39's "five real forged tokens... correctly rejected by the real `ValidateToken`" audit left no test file behind
69. `go test ./...`'s default parallelism raced a real scaffolded package's temp directory against `go list`'s own tree walk
70. Wrapping `http.ResponseWriter` silently dropped `http.Flusher`, so mounting the HATEOAS middleware would have killed the SSE log stream; the only reason it never did is that nothing ever mounted it
71. A response-rewriting middleware round-tripped every body through `map[string]interface{}`, silently corrupting integers, dropping links from collections, and clobbering handler-set keys
72. A caller-controlled request path was reflected straight back into the response body as a hypermedia `href`
73. The authorization generator's error was discarded, so a policy-backend outage would have been indistinguishable from a caller who is legitimately allowed to do nothing
74. A roadmap checklist item asserted a status-code bug the code did not have, and the phase that inherited it nearly fixed a symptom that does not reproduce
75. A JWT signature-forgery test tampered with base64 padding bits instead of the signature, so one run in sixteen "caught" a forgery that had never been forged
76. `DispatchRunbook`'s per-device loop read the management address from a property key ("ip") no device type in the codebase ever populates, so every real dispatch silently skipped every device while the endpoint still answered 200
77. The dispatch payload's `DeviceName` field was populated from `device.ID()`, not `device.Name()`, so a dispatched device's own display name never actually reached the wire
78. A runbook id read straight from an HTTP query parameter had no allow-list before it reached `filepath.Join`, so `internal/runbook`'s new filesystem-backed `Source` needed its own injection boundary built from nothing
79. `dispatch.JobStore.BeginFanOut`'s idempotency guard had no way back, so a Controller crash between claiming a job and completing it stranded that job in `"fanning_out"` forever
80. `BeginFanOut`'s `staleAfter` reclaim (#79's own fix) was a lease with no fencing token, so a `Worker` that was merely slow, not dead, could keep writing after being reclaimed and silently corrupt the job's terminal record
81. `wire.DispatchPayload.JobID` reached two NATS subjects by bare string concatenation on the Runner side, with no format validation, the identical shape #63 had already fixed once at the HTTP-facing boundary
82. `executeWithLease` had no panic recovery, so a panicking `ExecutionAdapter.Execute` crashed the whole Runner process and could race its own deferred lease release against a still-running heartbeat goroutine
83. `executeWithLease`'s per-execution context was an unconditional child of `Agent.Run`'s own shutdown-cancelable context, so `interruptible: false` only survived a lease-heartbeat failure, never a graceful Runner shutdown -- the far more routine trigger the PLAN.md exception actually exists for
84. `reportResult` appended to the Runner's own write-ahead log using the same context `Agent.Run`'s shutdown cancels, so a job whose execution was still in flight at shutdown had its outcome silently and permanently dropped instead of durably recorded
85. The Runner's write-ahead log minted a fresh random idempotency key on every `Append` call instead of a key stable across a JetStream redelivery of the identical job
86. The DAG executor treated a task naming no target as controller-side and returned before ever consulting the resolver, so a mesh-dispatched runbook's already-chosen device never reached the Collection method, so a crash-then-redeliver-then-reexecute sequence could publish the same logical outcome twice with no dedup catching it
87. `internal/election`'s coverage was nondeterministic across identical runs, swinging from 85.0% to 100% against a fixed 90.0% floor, because five race arms were only ever covered incidentally by a real-NATS timing race, so `make ci` failed at the coverage ratchet on runs where no code had changed
88. `internal/archtest` shelled out to a whole-module `go list` while sibling tests created and deleted scaffolded packages inside that same tree, so a directory caught mid-delete aborted the whole listing and failed an architecture test for a reason unrelated to architecture; `collectionscaffold`'s cleanup separately removed the shared parent directory rather than its own
89. `internal/api`'s dispatcher Release Gate sized its 60-second fan-out budget against a plain `go test` run, but `make ci` judges the `-race` build, which is about ten times slower and runs alongside a dozen other packages, so the margin was 20% at best and negative under real CI load
90. A legacy Ansible adapter designed against PLAN.md's own prose, without running a real ansible-playbook first, would have targeted a callback plugin that does not exist and an inventory format that fails to parse
91. A test container the production code path never used, hiding a database configuration that existed nowhere
92. A dialect map that made a migration runner look portable while one statement inside it was not
93. A compose file setting a configuration key no code read, in front of a service nothing used
94. An unanchored `vendor/` gitignore pattern silently excluded embedded third-party assets, so the committed tree did not build
95. A wildcard CORS header on an endpoint that had just become cookie-authenticated
96. The credential-source generalization was built, tested, and never wired into the composition root
97. An inventory is a grant surface, so unvalidated membership is a cross-tenant privilege escalation with every individual step passing its own check
98. `scopeRule` discards the Role the resolver returned, so every RoleBinding's role is decorative
99. A RoleBinding with `scope_id = 0` is a system-wide Allow, and nothing rejects one
100. A record action's affordance never entered the candidate set, so its control rendered for nobody, on every page, with no error anywhere
101. A required select whose only option source has no writer anywhere, so the create form it gates could never be submitted
102. HTMX was downloaded on every page and invoked by nothing, so half the request pipeline's fragment handling was unreachable (fixed: live refresh now uses it)
103. A PATCH decoded an absent list and an explicit empty list identically, so renaming a record silently emptied its membership
104. A review agent wrote a file into the working tree, and CI failed on somebody else's scratch
105. A guard ran after the deletions it was guarding, so a refusal destroyed the data it refused to destroy
106. Two opposite constraint violations arrived as one ent error type, so "still referenced" was reported as "already exists"
107. A list rendered a foreign key, so the reader had to do the join
108. A replaced doc comment survived above its replacement, so one function documented two opposite policies
109. A testing library reached a production binary, because the package that imported it had no production caller until now
110. A composition root omitted an optional constructor option, and the feature it enabled was refused at run time with nothing failing at build time
111. An edit form rendered a control whose value the update then discarded, and the help text beside it had said "set once" since the day it shipped
112. A feature shipped as four green layers that never composed, and every layer's own tests are why nobody noticed
113. Two statements of one contract disagreed, and the reconciliation kept the wrong one
114. A field made absent from the edit form was still demanded by the code that reads the submission, so every edit on three views failed
115. A checkbox's edit-form prefill used a different truthiness convention than the checkbox template itself checks for
116. A resolver's output was correctly computed and never read by anything downstream of the function that computed it
117. A job's completion state and tallies were fan-out publish outcomes, reported as though they were execution outcomes, while the real per-device outcome was already being reliably published to a subject nothing subscribed to
118. A security control's first working version cost 26x the thing it protected, which is how a control gets turned off
119. A Runner whose NATS connection closes for good stays alive, stays healthy-looking, and silently stops doing any work (FIXED in two halves: 2026-08-16, Phase 20, a heartbeat driven by the consumer answering plus a `runner healthcheck` subcommand; and 2026-08-23, Phase 96a, the reconnect defaults in `internal/event` and `internal/lock` that were the remaining open half)
120. A process registered every value it was handed as a secret, and masked the ordinary ones out of its own output
121. Strict-undefined turned a blank optional credential input into a total injection failure, and only real vendor data revealed it
122. A build that compiles green produces a binary that cannot open its own default database, because the driver became a stub rather than a compile error
123. A test built its "nothing is listening here" address by releasing a port, and so picked the one address on the machine that would answer
124. A `//go:build ignore` file held a second copy of a pinned image, and no guard in the repository could see it
125. A guard rejected only the literal tag `latest`, so `postgres:15-alpine` passed it for months under a doc claiming every image was pinned exactly
126. A private key bind-mounted into a container was readable by nobody, because 0600 on the host is 0600 for a UID that does not exist in the container
127. A certificate meant to be reused was replaced on every restart, because one of its subject alternative names was the container ID
128. Every controller in a scaled deployment refused to boot at once, because the code that provisions a certificate counted its own writes instead of ending on a read
129. A build context was measured from BuildKit's own progress line, which reports a cache delta rather than a size, so the measurement said 82 kB about 11.5 MB
130. A Kubernetes cluster created moments after a container image build lost etcd during CNI install, and the failure surfaced inside the test that installs the chart
131. Four workloads collapsed into one object name at every legal release-name length between 49 and 53
132. A PodDisruptionBudget that silently did not exist, because the template chose its field by truthiness
133. A reinstall with a different database password installed cleanly and crash-looped forever
134. A lock over a cheap, idempotent write turned one slow or dead controller into every other controller refusing to start
135. A provenance record kept as one read-modify-write file lost a concurrent writer's entry, and the process serving that certificate failed its own healthcheck
136. An ownership check keyed on the certificate destroyed the private key beside it whenever the certificate was absent
137. Every kind got one name budget, so the fix for a name collision created two StatefulSets that install cleanly and produce no pods
138. A rule that never replaced material it could not prove it wrote made a directory no controller could ever start in again
139. The trust anchors could not admit what a running replica was still presenting, so one sibling's renewal killed a healthy process
140. A log field named for a certificate carried the path of the private key
141. The Kubernetes gate's cluster name is a constant it deletes on sight, so any second actor's cleanup is a live run's outage
142. An unquoted chart value let an operator-supplied string add fields to objects the chart never wrote
143. A Collection may import only pkg/, so the one SSH module hand-rolled the security-critical dial the transport layer already owned
144. The Crawl tier handed every Collection method an empty secret set, so no method needing a credential could run from the CLI
145. Two collection methods' documentation had already drifted from the data the catalog is generated from
146. A circuit breaker latched half-open forever, so a device that was briefly down was never dialed again
147. The idempotence guard looked in a different directory from the command it guarded
148. A quoted directory beginning with a dash was parsed as a cd option, so the command ran in the home directory
149. A large standard input a remote command never read turned a successful command into an opaque failure
150. The shipped runner container sets no HOME, so every SSH Collection method fails host key verification unless the task opts out (FIXED: PLEIADES_KNOWN_HOSTS)
151. Every Collection manifest declares a required capability that nothing enforces at run time
152. The chart linter could not see a volumeMount naming a volume that does not exist
153. break-glass refuses forever, because stale self-matching pgrep wait loops from earlier sessions never exit and look like a live run
154. A zero-value sentinel collided with a meaningful zero, turning "reject every session" into "accept them all"
155. chmod before chown threw away the setuid bit the task had just asked for, and the module could never converge
156. The manifest declared an inverse that could not be right, because the true inverse is a property of the run and not of the method
157. A repo-wide uniqueness test fails while worktree-isolated agents' checkouts are on disk
158. A new module was added as a bare transport-action name instead of an FQCN, by copying the oldest thing in the codebase
159. netsh converting an adapter to the address it already holds by DHCP leaves it with no address at all
160. A "skip what already exists" flag applied per file resurrected fifteen stub tests underneath real implementations
161. A scaffolder's placeholder for an unsupported arity emitted a function signature that could not satisfy the type it was meant to produce
162. A doc comment assumed the stdlib PEM encoder validated its own block type, and it does not
163. A first-draft `GzipDecompress` had an input cap but no output cap, so a 1 KiB compressed value could allocate 64 MiB
164. A recurrence walk that ran in the target time zone let DST normalisation feed back into its own iteration state
165. Go's `time.Date` and PEP 495 `fold=0` resolve a nonexistent wall clock to different instants
166. A concurrency test passed because most of its workers crashed before reaching the code under test
167. A view's create form rendered no controls at all because every field omitted `InForm`, and the whole conformance suite still passed
168. A hop chain's `Connect` kept closing only its last leg, leaking every earlier hop's connection
169. A goleak check's baseline depended on which other tests happened to run first in the same binary
170. Three `StatusImplemented` Collection methods required a capability zero device types could structurally satisfy
171. A guard built for one capability gap found two more the same way, and three others already disclosed
172. A byte-stream "read until quiet" loop treated a closed connection as a hard failure instead of a valid end of output
173. Two shipped Adapter packages carried real logic at 0.0% test coverage, hidden one layer below a fully-tested primitive
174. A frame's announced size was allocated before it was checked against the output cap
175. A protocol with no length field to lie about still had an unbounded accumulator, because the missing terminator is the same risk in a different shape
176. A named integer type built specifically to prevent one silent-misread class still allowed a different silent-truncation class
177. A "nothing is listening here" test address was built by releasing a port again, the exact recurrence entry #123 already named
178. Stream shape is re-asserted by three composition roots from compile-time constants, so the least-qualified process silently wins
179. Two fully-built shared primitives had zero production callers, and both had been "finished" for phases
180. A sync plugin's two dependencies arrived through constructor options only its tests ever passed, so the CLI built it broken every time
181. Four capabilities, three transports and three fqcns shipped with no device type able to satisfy any of them, in the same commit that added the guard against exactly that
182. The one JetStream KV bucket whose shape was declared outside internal/topology was the lock bucket, and two documents claimed otherwise
183. Entry #177's fix reached one of ten sites carrying the identical construction, and the sweep that went looking for the rest missed half of them too
184. t.TempDir plus a Unix socket overruns macOS's sun_path, and bind reports "invalid argument" rather than anything about length
185. syscall.Stat_t in a test file is a compile error on Windows, and the ok guard beside it reads exactly like it already handles that
186. The bastion proof's console server was published to the host, and publishing it is precisely what let a different Docker network reach it
187. One test asserted a fact was present where its twelve siblings asserted a value, so it alone had no environmental guard and was the only one to fail off Linux
188. Nine packages could not run their own tests twice in one process, and no gate in this repository could ever have noticed
189. A lost race returned the same sentinel as a real collision, so the operator was told to rename a credential type that does not exist
190. A capability added to a device type left the Grand Integration Test asserting a set that no longer matched, and main stayed red at test-integration across three merged pull requests
191. Registering the four obvious NATS lifecycle handlers left the cold-start path completely silent, because the library routes the initial-connect retry through a different pair
192. Every graceful shutdown logged a WARN saying "reconnecting" and an ERROR saying "closed permanently" about a shutdown that was going exactly to plan
193. Two constructors leaked their NATS connection on every error path after the dial succeeded
194. A guard that matched a function by unqualified name could be defeated by declaring a local function of that name
195. A design's load-bearing precedent cited a source file that does not exist, and the real one argued the opposite way
196. The safety-critical half of a multi-writer provisioning defect went uncatalogued for a phase because only the retention half had been noticed
197. A test's own output capture raced its cleanup, and the race report replaced the assertion message that would have explained the failure
198. A publish reported failure and had succeeded, and the only thing that would have re-run the work was a reclaim ten minutes later, far outside the window meant to make retrying safe
199. Deriving a resilience budget exposed that the deployment's own probe cancelled it, and the default chart would have failed its own new check
200. A bare host and port in NATS_URL was accepted and silently meant unencrypted, and nothing in the module parsed the value at all
201. A volume mount was added to a StatefulSet whose volumes block existed only in two branches the default configuration did not take
202. A phase spec's checkmarks, doc comments, and a specific bug story all described code that was never written
203. A phase section was rewritten to correct a fabrication, and silently dropped two of the mandatory gates in the process
204. A capability was given only its structural half, so it passed the architecture sweep and no real device could ever satisfy it
205. A colon in a KV key made the Runner's duplicate suppression client-side invalid, and the error was swallowed as a warning, so the feature has never once run
206. A hazard closed for the dispatch subject was left open in the lock subject, whose own comment argued it could not happen

---

Each line above is a failure pattern's title only. Full Symptom / Root cause / Fix / Lesson detail lives in [`FAILURE_PATTERNS_ARCHIVE.md`](FAILURE_PATTERNS_ARCHIVE.md), same entry numbers. Scan the index above before debugging anything new; open the matching archive entry only if a title looks relevant.

Append a new entry to the archive first, in full, then add its one-line, same-numbered title here. Never renumber an existing entry.
