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
207. Five defects shipped behind a green Release Gate, because the gate's broker was configured without the subsystem the code under test exists to serve
208. A decodable-but-invalid message became a poison pill, because the consumer split "retry" from "give up" on the wrong axis
209. A multi-device job silently lost every copy but one of a skipped task's journal row, because the row's identity assumed a field that only some rows carry
210. One run recorded "no keys" two different ways, because a normalization was applied at one of the two sinks
211. A stress test raced a fixed sleep, so the gate failed describing an empty buffer instead of an unbounded one
212. A tab's title named a filter its query never applied, so "Completed jobs" listed every job whatever its state
213. The stylesheet had no rule for two classes its own templates emitted, so every page's primary action rendered as an ordinary button
214. Making a record's name its own row link produced anchors with no accessible name on any record whose name was empty
215. The contrast gate measured a colour pair that was not on the screen, so a whole skin rendered its text on the wrong background at 1.5:1 while every test passed

---
216. `SubscribeCancel` returned before the broker registered the subscription, so a cancel racing the start of a run was dropped forever rather than delivered late
217. Job results were published only when `RUNNER_WAL_DIR` was set, and nothing in the module ever consumed them
218. `http.request`'s documented `timeout` could not raise the TLS handshake deadline, and the verifying path shared the process-wide transport its own comment said it must not
219. A view's relation check refused one endpoint named by two controls, and said so by naming that endpoint twice
220. A row control's relation was missing from the candidate set, so every one of them was withheld from everybody, including an administrator
221. A managed credential type offered Add input and Add injector, both of which the store refuses in its second statement
222. Two publishers on one stream minted the same message id, so every job result was discarded as a duplicate of the dispatch that caused it
223. A job whose devices all reported before its fan-out finished was parked in a state nothing sweeps
224. The flake waiver tolerated a whole package, so a total regression printed "passed" five times
225. A test harness decoded every poll into one reused value, so one device's skip reason appeared on another device's row
226. A form's prefill was computed, sorted, and dropped, so saving it as drawn unbound every credential a template ran as
227. Four action redirects left off the UI's mount prefix, so a successful write answered with a 404
228. Two doc comments asserted a two-place safety check that had one place, one of them naming a log-masking control that does not exist
229. A pre-push gate outlived the connection git had already opened, so every push died with SIGPIPE and no output while the gate printed "all checks passed"
230. os.WriteFile truncates before it writes, and a test polling that file observed the empty window as an answer
231. A five second budget for a subprocess to appear fired under suite load, and the loop waiting for it never noticed the subprocess had died
232. Every device creatable through the API or the UI is permanently un-dispatchable, because nothing can write the host property the dispatcher requires
233. ROTATE_ENCRYPTION_KEYS rotated devices alone, so following the rotation guide made every credential and saved survey answer unreadable
234. The chart's existingSecret path silently turned off the reinstall refusal and the pod roll it built on purpose
235. The compose stack published PostgreSQL and an unauthenticated NATS on every interface
236. A fuzz property that checked for a secret as a substring of the error was unsound twice over, and the fuzzer found both
237. A terminal echoes type-ahead: a secret typed before a hidden prompt appears is shown on the screen
238. A line-by-line .env reader disagrees with docker compose in two measured ways, either of which would have written a second key
239. A new container-backed package missing from DOCKER_DEPENDENT_PACKAGES failed only as a timed-out container, twenty minutes into the push gate
240. Every compose release gate ran as the checkout's own compose project, so `make ci` deleted the database of a stack started with `make up`
241. A `build:` key does not stop docker compose pulling: with no local image it pulls the `image:` name first, from a namespace this project does not own
242. A restore role made NOINHERIT could not create tables in the database it owned
243. `ALTER ROLE ALL IN DATABASE x RESET ALL` does not clear a role's own setting in that database
244. A setting a restored file attaches to its database applies to the sessions that check it
245. `make up`'s documented script form never worked: its check drained the piped password before setup could read it
246. A sanitizer written for names from an untrusted file cut host paths to 64 characters
247. An ownership change cleared a setgid bit the task asked for, and the run reported success
248. GNU chmod keeps a directory's setuid and setgid bits for a four-digit mode, so "0755" never cleared them
249. Validation refused a check of a simulate-locked device that the engine was built to allow
250. The plan said nothing ever sets simulate-locked; two sync plugins set it on every device they discover
251. The environment allowlist was called the whole defense, but a same-user child reads its parent's environment from /proc
252. A runbook-level `check_mode: true` is accepted and ignored, so the runbook runs for real
253. Check mode admits simulate-locked devices for any method, including a third party's unproven Check
254. A Landlock restriction applied from a goroutine could land on the main thread, putting The Pleiades inside the program's own domain
255. A YAML unmarshal hook written against gopkg.in/yaml.v3 was never called, so check_mode: false decoded silently
256. A file-writing tool decoded the escapes in its input, and real bidirectional overrides landed in the source
257. An external program's own text reached the terminal raw, including at the approval prompt
258. A new NATS subject and consumer shipped without their mesh identity grants
259. A package-level struct literal froze a test seam at init, and a test dialed a real WinRM endpoint
260. A synced project's playbook escaped its tree through a symlinked directory
261. A device's state or tags update answered 200 and stored nothing
262. A re-sync reverted an operator's promotion, with no revision saying so
263. A read-only sync hid the one list an operator previewing it needed
264. Template launches minted unordered job ids under a list that orders by id
265. One unresolvable entry failed a whole static_yaml sync
266. SQLite migrations never really turned foreign keys off
267. A project that had ever synced could not be deleted
268. Writing a schedule needed no permission to launch what it launches
269. Editing a schedule silently dropped its saved configuration
270. A refusal gave advice that does not work
271. A fetch ignored the project's own URL, so repointing one changed nothing
272. Nothing constrained where a project's source came from
273. A round trip was not a round trip, and the test wrote the workaround into its own expectation
274. A field was added to a type, to its wire format and to a CLI flag, but not to the store, so the command reported success and wrote nothing
275. Go cannot do TLS 1.3 client-certificate authentication against Windows, and the empty 503 that results names nothing
276. The migration gate accepted a history missing its first migration
277. Two processes opening a brand-new SQLite file failed each other at open
278. A starting controller failed every live peer's project sync
279. The backup tests trusted any newer PostgreSQL client, until the machine's default became 18
280. A zero-failure drain test passed with the drain switched off
281. `make -n` started a compose stack
282. A failed heartbeat read made every live peer's sync look abandoned
283. The sync sweep read who was live, then failed projects in a second statement
284. `make -n up` stopped the live controller and runner and took no backup
285. `make up` never rebuilt the runner image
286. The compose gate proved rollback only after wiping the upgraded stack
287. The binary upgrade gate never asked the old build anything while the new one migrated
288. A role's timeout ended the one migration wait that must never end
289. With no hard links, a new embedded database came out readable by everyone
290. The chart drained for less time than its own readiness probe needed
291. A controller left the fleet while its clones were still running
292. The controller's backup gate restored with the machine's newest PostgreSQL client
293. A chaos test threw away the error of the one session it was waiting for
294. `facts.gather`'s check test compared a clock
295. The Helm setup gate looked for a durable fact in one container's log
296. A control was attested before anything ran it
297. `NATS_URL` was checked after the port was bound and the database migrated
298. Placing that check one step earlier would have printed an embedded credential
299. An empty `ExposedPorts` publishes MORE ports, not fewer
300. The compose upgrade gate indexed a backup that correctly did not exist
301. A check test compared a clock, again, in a different package
302. The Helm upgrade gate counted pods Kubernetes had already finished with
303. A new test reached into a Linux-gated file and broke the build on two operating systems
304. A configuration file repeated a flag the deployment already passed, and the broker exited at boot
305. A shared test helper's cleanup ran after the test's own defers, so a leak check inspected a live container
306. An assertion that a key was absent from the database searched for plaintext the schema always encrypts
307. A signature-forgery test flipped base64 padding bits again, in a different package, and caught a forgery that had never been forged
308. Two WAL tests leaked a NATS bus each, and a durability wait was bounded as if it were a performance claim
309. `FAILURE_PATTERNS.md` #301 fixed, and the compensating assertion written with it compared a map with itself
310. Every postgres and toxiproxy container waited for readiness under a sixty second deadline nobody chose
311. The roadmap's own "what to build next" ranked on the phase number and contradicted the release beside it
312. Every Collection manifest required an engine version no release before 1.0.0 could have met
313. A containment check that trusted SFTP's REALPATH would have passed the symlink escape it existed to catch
314. remoteexectest ended a session when the client closed its input, not when the command exited
315. The unreachable-capability allowlist's staleness guard needed both conditions, so dead entries lived on
316. The generated device reference could not say a hand-written type's capability was conditional
317. pkg/tftpxfer passes NUL and over-long remote filenames to pin/tftp, which injects the first into the request and panics on the second
318. A time-bounded fuzz run froze its execution counter while minimizing, and read like a finished one
319. A known_hosts fixture naming one host key type refused every connection as a mismatch
320. The Runner's heartbeat self-aborts on the first failed KeepAlive, though its comment says one missed tick is tolerated
321. import_tasks read a file outside the runbook's directory through a symlink
322. The Walk tier ran a dispatched runbook without plan-time validation
323. net.netconf.config's target parameter is also the engine's device selector
324. Methods silently ignored parameters they do not declare, and two shipped examples passed five
325. A JSON runbook accepted keys in the wrong case and repeated keys
326. Three user-visible strings cited PLAN.md, and docs-lint could not see any of them
327. A condition holding `{#` inside a string literal was refused as Jinja
328. The free-form argument splitter turned invalid UTF-8 into U+FFFD
329. The migration report printed raw playbook file names, and runbook names came from them
330. The migration report printed values from the playbook in its refusal messages
331. A negated condition over a registered result ran a task on every device when one matched
332. Two merge keys in one YAML map resolved first-wins, where Ansible's loader takes the last
333. Ansible's list type was not applied: `name=curl,git` became one package named `curl,git`
334. The module tables accepted aliases and values that belong to other modules
335. file.directory left the parents it created at the umask's mode
336. A module named `guard` would have become a placeholder with the guard's name
337. A nested import_tasks was looked for only in the playbook's directory
338. An empty list literal in a condition evaluated to None
339. The test SSH server's Close waited forever on a connection its client kept open
340. An outside timeout on the end-to-end suite left a kind cluster running
341. The first Ansible comparison's target ran sshd unprivileged, understating every login about threefold
342. The benchmark stopped at its first failed run and kept only what it had printed
343. Ansible's orphaned processes are reaped by PID 1, and neither the benchmark's runner nor the Ansible adapter's container has an init
344. A host classified at add time lost its classification's capabilities on load
345. Declaring NETCONF claimed a command line
346. A port wait passed before a shell-less container's server was listening
347. A gRPC stream's Send returned EOF, and the probe read it as the answer
348. A generic device would never have been dispatched: the worker required a host property
349. Gate items were ticked with half their work undone, and one ticked claim was false
350. Entry 310's fix set the group's deadline, and every step inside it still gave up at sixty seconds
351. A host-key capture read an SSH banner with no deadline, and hung make ci for thirty minutes
352. The SSH handshake with a device reached through a bastion had no bound at all
353. A test broker was declared ready while its published port still refused every connection
354. Every PowerShell quoting helper doubled only the ASCII apostrophe, and a typographic quote ended the literal
355. The WinRM lab setup script gave its account far more than a lab run needs, and weakened the host for everyone
356. Windows ignores WINRS_SKIP_CMD_SHELL, so every WinRM command still ran through cmd.exe while the tests said it did not
357. The WinRM Adapter mapped credential fields by hand and sent no credential for a PKCS#12 bundle
358. Bare `pleiades validate` checked only the file `init` writes, and silently dropped its own flags
359. An `import_tasks` file was diagnosed as an Ansible playbook and sent to the migration tool
360. `mediumio cat --hex` folds identical rows, and a parser written from its usage text refused the real output
361. Microsoft's Windows Server evaluation VHDX never reads an answer file from a DVD at its first boot
362. An EFI VM with two vCPUs stops at the firmware's `DXE_AP` debug point under NEM
363. `add-credential --generate` made a password Windows' default policy refuses, about one time in 38
364. A generalized Windows image's first boot never looks on a DVD for its answer file
365. A running VM keeps a DVD image locked after its drive is emptied, so a seed cannot be deleted until it stops
366. Windows keeps an unblanked copy of the install's answer file, so every clone carried the audit password
367. The WinRM service and feature gates asserted on text the output no longer printed, and put the lab password on argv
368. `onboard --json` and `doc --json` wrote terminal control characters raw
369. An idle Windows Server guest spends the first power-button press waking its display (fixed 2026-10-19)
370. An address with a byte that is not UTF-8 signed in as a different stored address
371. A NUL in a sign-in address failed as a Postgres query error, fast and without the decoy
372. An oversized password told a known address from an unknown one
373. Authenticate reported UserID 0 on every successful sign-in
374. A test file named for FreeBSD compiled only on FreeBSD, so its tests silently never ran
375. On BSD a file's mode was read without its setuid, setgid and sticky digit
376. `adhoc` read `mode=0755` as a number, and the method refused it with advice about runbooks
377. A dead port's open circuit reached a later test's live server on the same port
378. A parameter a method declared was also the engine's device selector
379. Any Runner could write any job's journal rows, first writer wins
380. A harness Runner published no results, so a Controller-side gate waited forever
381. An older Runner drops a payload key it does not know, so a rollback it received would re-run the undone runbook
382. A rollback: list written after the run was ignored
383. `--leave` left only a change that could not be undone
384. A run rolled back on a second attempt still counted as a later change
385. virt.vbox.vm.delete's check failed on a running VM where resize reported it uncheckable
386. The journal projection's benchmark timed a projection over nothing
387. The run lock became the first writer of .pleiades, and a read-only project stopped saying the journal was why
388. Rolling back a stop, then at once the start before it, asked a still-booting FreeBSD guest to shut down
389. The forks window's first design was a contract migration
390. A chaos test cut its link after a fixed delay, and a longer migration history moved the cut out of the step it meant to break
391. The upgrade gate expected the previous build to be refused after every upgrade
392. A generic_http device's credential followed its base URL to wherever inventory pointed it
393. A bound credential's injected secret variable was readable by runbook conditions on the native path
394. `pleiades run` printed a device credential a Collection method echoed back
395. `http.request` read a response body into memory with no bound
396. A release gate lost a dispatch to lock contention with the retries of the failure it had just asserted
397. A strict coverage run that lost one test to Docker never compared coverage to the floors
398. A canceled call took a recovering target's half-open probe and kept it, so the device was never dialed again
399. The SSH dial retry sent a refused password three times, and counted it against the circuit every other job shared
400. A WinRM host that never answered was reported as a command that "may still be running", which no retry or breaker could see
401. A test fixture matched *.log in .gitignore, so it was never committed and its tests failed in every clone but one
402. testgate named a failing test and never said why it failed
403. A coverage floor for a package deleted seven weeks earlier was never read, so the ratchet guarded nothing there
404. A branch covered only when a timer lost a race dropped pkg/retry below its floor once coverage ran under -race
405. A package's coverage was read from the run where a contended test stopped partway, so contention looked like a coverage drop
406. Coverage floors recorded where LocalStack ran would have failed every CI run, and every `make ci` without a token
407. The view reachability check hand-listed 8 of 22 views, so dropping any of the other 14 passed
408. A Vault container was "ready" on a log line before Docker forwarded its port, so the first write was refused
409. A roadmap item cited `TestCheckCmdEnvReads`, which was never written, and the refusal it named was tested only by a lab gate
410. `tools/doctor`'s coverage was a property of whichever machine ran its tests
411. Schedule pages were ordered by name and resumed by id, so following Next skipped schedules
412. The Access list linked every row to its team and none to its grant, and the drill-down test skipped it
413. A dependency update moved grpc from a patched release to one that reintroduced a vulnerability, and the gate passed
414. A tolerated failure's printed output was fifteen lines of teardown and not one word of why
415. Phase 118's Documentation Gate was ticked while most of what it named still described the old gate
416. The first pull-request run failed at steps no local gate runs, and reading them against the pinned actions found two more
417. A failed run wrote no job summary, and the summary would have carried test output unmasked
418. A pull request's checkout had no `main`, so the upgrade gates could not find the previous release, and test-repeat failed where nothing could read it
419. The dispatcher gate's own watcher starved the fan-out it was waiting on
420. The fast tier assumed Linux, and the first macOS run failed 43 tests that had nothing wrong
421. Docker's port map lists an exposed port with no binding, and the mesh gate read every key as "published"
422. Two tests measured once and failed a pull request whose change was one handoff document
423. A failed container start was never terminated, in 47 places, and in production it left Ansible running past its job's timeout
424. The nightly job never installed socat, and a bare `t.Skip` made six missing tests read as a coverage regression
425. GitHub's Windows runner ships its own HTTPS WinRM listener, and the lab script refused to replace it
426. The Podman fixture's passwords were refused on the CI runners and accepted under Docker Desktop
427. Code moved between packages left its tests behind, and eight coverage floors failed at once
428. probe_linux.go's file name built the Linux server prober on Linux only (a repeat of 303)
429. A refused SSH host key was retried three times and opened the address's circuit
430. Kubernetes discovery recorded only preferred versions, which would have refused valid manifests
431. A hostile API server could make discovery unbounded, and its whole answer reached an error
432. A replacing kubeconfig import over the API reported success and wrote nothing
433. A branch checkout in an auxiliary worktree moved the main worktree's branch
434. The moby client's hijacked connections bypassed the engine guard's dialer
435. No stdio login was ever pooled
436. Two checks passed vacuously against the Docker fixture
437. OpenSSH applies AllowTcpForwarding to Unix-socket channels and answers every refusal "connect failed"
438. The engine guard judged a decoded path when the target held an invalid character
439. Closing an SSH exec channel did not stop the remote process
440. A test asserted on `ps` in an image with no `ps` and passed vacuously
441. container_host parsed its TLS settings and never exposed them, so a pinned authority was ignored
442. trust-host --first-connect wrote a fixture's key into the real ~/.ssh/known_hosts
443. A fixture's SSHDConfig option replaced its default AllowTcpForwarding instead of adding to it
444. The SSH test fixture's forwarder never ended a connection its destination kept open
445. Pull request #55 failed CI twice on checks that run locally in minutes (a repeat in kind of 427)
446. k3s died at startup in CI because the fixture's readiness exec raced its cgroup move
447. The container-list guard read a proxy, so pkg/kubeapi reached CI unlisted (a repeat of 239)
448. pkg/tftpxfer timed out on macOS three times (two Gets on PR #55, a Put's request on PR #112), and the cause was unknown until 2026-11-06 (fixed: XNU gave the client's dual-stack socket a port an IPv4 loopback socket held, which took the replies)
449. A touched package CI could not floor-check was left out of the pre-push measurement
450. The docker CLI methods passed names and images unchecked, so an image of `--privileged` ran a privileged container (a repeat of 148)
451. docker inspect read any non-zero exit as an absent container, so an unreachable daemon let run create one elsewhere
452. The engine drops a failed task's undo, so a docker run that created a container and failed to start it left it unrecoverable
453. The docker CLI methods buffered whatever the device wrote and copied all of stderr into their errors (a repeat of 431)
454. The docker CLI methods skipped the engine identity pin container.docker.exec checks
455. A registry account a method obtained was never masked on the command line, and its encoded header nowhere (a repeat of 394)
456. Controller onboarding lent any organization's Kubernetes credential to a record naming its address, under the record's authority and route
457. A refused server address was quoted, password included, into the import API's response
458. HasEmbeddedSecret checked only a URL's password, so a token in the user position passed
459. The README and start-here counts drifted 22 methods behind the registry with nothing to notice
460. Session-budgeted tests kept passing after a new first command, now testing the step before the one they name
461. The local engine probe's unit tests read the developer's real Docker socket first
462. A failed Podman start's cleanup ran on an unbounded context while close() bounded the same script
463. Generated packages written into the module tree failed a new reader of it (a repeat of 53 and 55)
464. A second switch for one fact let onboarding grant a reach the engine port then refused (the sibling-guard class, LESSONS 244)
465. CI's pull step asked Docker Hub for a fake image ID a test had named (a repeat of 416)
466. A result that beat its dispatch's row was acknowledged as a message about nothing, and the job never finished
467. The rollback gate's check step asserted what the check left alone, never what it reported (the class of 54)
468. The twin of 211's test kept 211's race, and failed CI's fast job (a repeat of 211)
469. The reaper test read the job's state on the first delivery to return, which was the one that did nothing
470. The openssh fixture's mapped port refused every connection for two minutes while sshd said it was listening (fixed; cause found 2026-11-06: Docker Desktop's forwarder could not bind a host port a socket of the tests' own held, and never retried)
471. The Postgres fixture's mapped port never answered while the server had logged ready (a repeat of 470) (fixed; 470's cause, found 2026-11-06)
472. file.yaml read every key behind a line comment as changed, because the comparison kept the comment
473. yaml.v3 accepts a duplicate key when decoding into nodes, which pkg/docedit had to refuse itself
474. A fuzz target that started a server per input exhausted the ephemeral ports, and the "crash" was the harness
475. gosec's taint analysis followed a CLI argument into a read and a write beside an arbitrary file (G703)
476. The gate tests ran gate.run's own timeout command on the host, and macOS has none that takes -k (a repeat of 420) (fixed)
477. A release gate's fake server read its data while the test wrote it, with no lock (a repeat of 6 and 33) (fixed)
478. The gate's TCP precondition test made its closed endpoint by releasing a port (a repeat of 123, 177 and 183) (fixed)
479. The gate's parallelism test asserted an upper bound on elapsed time (a repeat of 211 and 422) (fixed)
480. The test SSH server's forward dialed with no bound, and Close waited for it (a repeat of 339 and 444) (fixed)
481. A Debian sshd fixture's mapped port never answered while sshd said it was listening (a repeat of 470 and 471) (fixed; 470's cause, found 2026-11-06)
482. A test of a package pkg/external reaches imported internal/testsupport, putting testcontainers-go into every external author's go.sum (fixed)
483. testcontainers' reaper container never became ready within its 60 s wait on a CI containers shard (open; its local sightings are 470's cause, found 2026-11-06; the CI one is not)
484. A Cargo.toml nested a megabyte deep ended the process: go-toml recurses with no limit (a repeat of 62) (fixed)
485. Lockfile readers matched keys regardless of case, so a "Resolved" replaced the source npm fetches from (a repeat of 325) (fixed)
486. The build-file reader saw less than a build does: heredocs at any "<<", one ARG scope, merge keys and aliases unfollowed, duplicate keys kept (a repeat of 473 and 332) (fixed)
487. A failed refresh dropped the undo record that the files it could not put back needed (a repeat of 452) (fixed)
488. containerengine's "set engine:" advice named a parameter no method declared, so validate refused it (a repeat of 270 and 324) (fixed)
489. Fuzz targets fuzzed their selector string, so almost every input stopped at "unknown" and a panic went unfound (a repeat of 35) (fixed)
490. A line count from the file's start for every reference made a 1 MiB Compose file take 53 s (a repeat of 48) (fixed)
491. The release gate's checked refresh asserted only what it left alone (a repeat of 467) (fixed)
492. ReadText's output bound also held standard error, so an unreadable file was reported as too large (fixed)
493. gofmt turned the '' inside ''' in a doc comment into a typographic quote (fixed before push)
494. chmod MODE -- FILE works on GNU and fails on BSD, twice: build's undo directory and scpxfer's put (a repeat of 420 and 476) (fixed)
495. The reaper's end-to-end test failed on a shared-cache test database the Controller does not use (a repeat of 469) (fixed)
496. The Grand Integration runbook job stayed "running" for its whole budget twice in one CI job (a repeat of 396) (fixed)
497. pg_restore read the database name as a connection string, so a name could send the restore and its password to another host (a repeat of 450 and 148) (fixed)
498. The additive-only lint read string literals one way and the server another, so a DROP ran inside what the lint saw as one INSERT (fixed)
499. The device tools carried the database password in their environment, which PostgreSQL's own documentation advises against (fixed, by the owner's decision)
500. remoteexec's stdin-closing test helper assumed a harness that waited for input, written in the same commit that made it stop waiting (a repeat of 460) (fixed)
501. The hop stress test opened more connections at once than one macOS listener holds, and XNU resets the rest (a repeat of 476's class) (fixed)
502. testgate's live tests asked for -race unconditionally, which cannot build on a Windows machine with no C compiler (fixed)
503. durations.json's times were the gaps between log lines, so each shard's first package carried the shard's whole build (fixed)
504. The OS checks' stop test put its stand-in go last on the PATH, so the real go failed at once and the test read that as a stop (a repeat of 54) (fixed before push)
505. Three tests assumed a Linux or macOS host: printf's leftover arguments, /bin/bash, and FreeBSD as the "other" system (a repeat of 420) (fixed)
506. The Runner's per-task child died on the SIGTERM meant to let it stop, so a timed-out command kept running on the device (fixed)
507. A failed node's undo was stripped in the collection bridge only, so a refused prediction reported one (fixed before push)
508. A test started the Windows PowerShell of the workstation behind WSL to parse a script (fixed)
509. win.schtask's script was never sent through the real command line, and failed on every Windows host (fixed)
510. A Windows run of the branch's packages failed 77 tests that assumed a POSIX host (a repeat of 505) (fixed)
511. The hop stress test's listen-queue bound covered macOS only; FreeBSD and Windows reset and refuse past their backlog (a repeat of 501) (fixed)
512. bsd-check was sized at 1 GB by assumption, and could not compile the branch's packages (fixed)
513. A commit that meant to append four entries replaced the failure-pattern archive with them (fixed before push)
514. Twenty methods' documented examples could never run as written (fixed)
515. The examples test held documented examples to the builder alone, not to `pleiades validate` (a repeat of 324 and 514) (fixed)
516. A box resumed from its saved state answered one login, then none for over 20 seconds, and the check failed (fixed)
517. Windows failed 36 tests in three packages #73 never ran there, each starting the /bin/sh test server unguarded (a repeat of 510) (fixed)
518. testgate printed a failed table test's "--- FAIL" line and not which case failed or why (fixed)
519. A WinRM fault in an accepted reply lost its reason, and at a Receive looped until the deadline (fixed)
520. Windows read the real login's home in tests that set HOME alone (a repeat of 510) (fixed)
521. bsd-check found a write's prediction claiming a group the write does not keep, behind two test flaws (fixed)
522. testgate's new run record kept a skip's reason as the test wrote it, beside a ledger that masked it (a repeat of 417) (fixed before push)
523. prreport dropped a stray argument, a second view and an exit code beside -not-run without a word (a repeat of 358) (fixed before push)
524. wait.connection's tries borrowed the run's pooled session, so a wait for a login passed without one (fixed)
525. pr-check's interrupt trap ran twice, and the second merge overwrote the first (fixed before push)
526. A lab check drove its box with whatever pleiades an earlier run had built (fixed)
527. virt.vbox.vm.delete took a saved VM for one it could change, and failed at its first step (fixed)
528. The file inventory's reads took no lock, so a read could straddle a Save (fixed)
529. win.firewall.rule's undo carried lists the journal cannot keep, and only a real rollback said so (fixed before push)
530. A PowerShell script's helper named R ran Invoke-History instead, since an alias wins over a function (fixed before push)
531. The repository taught the task form its owner had refused, so every copy brought it back (fixed)
532. WinRM over HTTP sent a message unsealed whenever its NTLM handshake failed (fixed)
533. The sealed transport's header timeout raced Execute's deadline, and a silent host lost its name again (a repeat of 400) (fixed before push)
534. A value of the wrong type reached a method as absent, literal or rendered, and nothing compared it with the declared type (a sibling of 324) (fixed)
535. A resumed Windows box missed connections for up to two minutes, after the streak entry 516 added had passed (a repeat of 516) (fixed)
536. A run report could not say a task never reached its device, so nothing could retry it safely (fixed)
537. A package that failed to build was reported without the compiler's words, so a full disk read as 169 unexplained build failures (fixed)
538. A new CLI test read the developer's own known_hosts, passed here and failed on CI's runner (a repeat of 520) (fixed)
539. A block's always: was parsed and never run, so a failed lab run left a domain account enabled (fixed)
540. A tag selection dropped the runbook's reversible: flag, so --tags turned the reversible rule off (fixed)
541. A rescue that answered a chain of failures cleared only the first, so the run still read failed (fixed before push)
542. One device's failure ended the run before another device had run its always: (fixed before push)
543. A domain controller resumed from its saved state was 90 minutes slow, and a member's Kerberos join failed (a repeat of 535's clock) (fixed)
544. The journal reader's symbolic-link refusal read as Linux's errno, and two owner-only mode tests assumed Unix (a repeat of 505) (fixed)
545. Every CI job's Go cache restore failed, because setup-go installed go.mod's go line and Go then fetched the toolchain line's release into the module cache (fixed)
546. tests/e2e's per-test times came from a local run, where the compose gates' image builds hit a warm cache, so one CI shard got both builds (a repeat of 503) (fixed before merge)
547. After rescue and always ran, the generated task-key reference still said they never run, and that rollback was refused in them (a repeat of 539's documentation) (fixed)
548. The upgrade_ios example validated clean and could not run past its first task (a repeat of 324) (fixed)
549. netcli.IOS read IOS's "%Error ..." as output, so a failed save could report success (fixed)
550. pleiades validate did not refuse a task missing a parameter its method marks required, and three runbooks left one out (fixed)
551. The rollback gate's "beneath a later run" check let a rollback run, intermittently, on the commit before this work too (fixed: the planner dropped a later run's tasks that failed during their action; a later run killed mid-task with no entry is left to the owner)
552. govulncheck was killed for memory at the gate three times, and the memory was a new advisory making it trace a vulnerable call (fixed)
553. The Controller's result consumer shipped without its dead-letter grant (a repeat of 258) (fixed)
554. Dead letters copied the whole message, so every dead-lettered dispatch put its secrets on the stream twice (fixed)
555. A run longer than the ack wait was delivered again while it ran, and the copies spent its delivery budget (fixed)
556. A template's machine credential reached every device in the fan-out, API-only ones included, because the exemption was a list of types (fixed)
557. validate and a Controller's admission checked no Collection method's required capabilities, only the engine did (a repeat of 151) (fixed)
558. The reaper's leadership test gave the leader a fixed 60 ms, and a slow machine spent it on the first query (fixed)
559. The capability check reached validate with no census of what already validates, and two cmd/pleiades gates failed in CI (a repeat of 550) (fixed)
560. A check of container.prune listed no stopped container while inspect said it had exited (fixed: a Docker Engine bug fixed in 28.3.0; CI runs 28.0.4, this machine 29.8.0)
561. pleiades validate checks the registers a template reads but not the ones a when: condition reads, so a misspelled register fails at run time (a sibling of 515) (fixed)
562. A subcommand's --help exits 1, and one that takes a name prints no flags until a name comes first (fixed in 129d)
563. Four methods recorded their undo only after reads that can fail, and a failed task's undo never reaches the journal (a repeat of 452 and 487) (fixed)
564. A check of an account naming a group the device lacks predicted the change with a warning, so the check passed in full where the run fails (a repeat of 385) (fixed before push)
565. pkg.apt.install's undo could name more dependencies than the journal keeps, which would leave the run's whole undo incomplete (a repeat of 529) (fixed before push)
566. seal.key was created empty and then written, so a concurrent first use could read an empty key and a crash left one that refuses forever (a repeat of 46) (fixed before push)
567. New internal/credential tests assumed POSIX file modes, directory mode bits and unprivileged symbolic links, in a package the Windows leg runs (a repeat of 544) (fixed before push)
568. run's and adhoc's hand-written usage lines had lost --allow-unchecked and --fail-on-rescue (a repeat of 145) (fixed)
569. remotefile.Stat passed a path to stat and readlink with no end of options, and file.stat takes the whole path from a task (a repeat of 148 and 450) (fixed)
570. The account undo judged whatever home passwd named at undo time, so a home moved since could be deleted in place of the one made (fixed before push)
571. Four comments, help texts and skill sentences claimed more than the code does, one of them a sealed file's refusal of surrounding text (a repeat of 228) (fixed before push)
572. engine.TaskReads' first draft left out a task's rollback: steps, which rollback_on_failure runs and renders inside the forward run (a repeat of 561) (fixed before push)
573. The first segmenter accepted two plans one device's dispatch cannot keep, and refused a third it could (fixed before push)
574. validate holds a rollback: step to three of the rules a task meets, not TemplateRule, so a misspelled register in an undo step passes (a repeat of 561) (fixed)
575. Docker Desktop carried a net lab node's SSH port over whichever network's name sorted first, so taking a link down cut the test's own session (fixed before push)
576. Docker's --internal networks dropped OSPF's multicast hellos, so no adjacency formed and wait.routes timed out (fixed before push)
577. Secret masking split words at spaces and tabs only, and its keyword list missed RADIUS and TACACS server keys, FHRP, RIP and NHRP authentication, and snmp-server host communities (a repeat of 458) (fixed before push)
578. A new output path skipped the masking beside it: net.frr.command's show output, vtysh and frr-reload errors, and the Controller's grant properties (a repeat of 522) (fixed before push)
579. A check of net.frr.config passed a change the account could not make, and a failed reload left the router in neither configuration (a sibling of 564) (fixed before push)
580. A masked configuration could be applied, and expect_sha256 matched it (fixed before push)
581. ovs-vsctl exits 1 both for "no port named" and for every fatal error, so a failed read was taken as absence (a repeat of 451) (fixed before push)
582. Open vSwitch undos were recorded as exact when they could not put back what the change removed (fixed before push)
583. The grant routes' scope tests ran with a fake admitter and an admin identity, so the routes' own scope was never exercised (a repeat of LESSONS 180) (fixed before push)
584. The documented accept_unrecognized example split at an unquoted comma in a YAML flow mapping, and validate passed it (a repeat of 548) (fixed before push)
585. Batfish parses Cisco GLBP, IP SLA, SLA tracking and BFD without a warning and does not model them, so a gate that refused only unrecognised lines would pass them (a sibling of 252 and 539) (fixed before push)
586. import-containerlab --trust-first-connect wrote an ephemeral lab's host keys into the user's own known_hosts (a repeat of 442) (fixed before push)
587. Net lab nodes still reached SSH over a link 2 starts in 10, after the networks were named so management sorted first (a repeat of 575) (fixed before push)
588. PredictPut refused, on FreeBSD, a group the file Put writes takes from its directory without a chgrp (a repeat of 521) (fixed before push)
589. A test left doctest's capability registered, so a later onboard ran its probe, and the re-run alone called it contention (a repeat of 188) (fixed before push)
590. An empty standard input's end could reach sshd before the exec request, so "cat > file" waited forever (a regression from 149's fix) (fixed before push)
591. A Batfish live test required "domainname localdomain", which FRR writes only where the Docker host gives a search domain (a repeat of 426) (fixed)
592. telnet_exec demanded a stored credential its transport never sent, so a console with no login was unreachable (fixed before push)
593. The IOS dialect waited for a privileged "#" prompt, so an account landing in user mode hung every net.ios method until its byte bound (fixed before push)
594. A Telnet login answered by a delayed refusal was taken as accepted, because the session read 500 ms of quiet as the answer (a sibling of 149) (fixed before push)
595. trust-host reported a refused connection as a host that presented no host key (fixed before push)
596. The generic CLI prompt pattern allowed only whitespace after the prompt, so a shell's terminal query hid it (fixed before push)
597. A tag change was recorded as a revision and never written to hosts.yaml, because the file repository's Save wrote properties only (a repeat of 261 and 274) (fixed before push)
598. An unknown flag before set-host's name took the name as its value, so the command said the name was missing (a repeat of 50) (fixed before push in set-host; fixed in the other commands on fix/cli-flag-splitting, 2026-11-09, on main since 57138643, 2026-08-04)
599. net.ios.command reported a read-only show command as changed, so a second run could not prove it changed nothing (fixed before push)
600. The insecure_telnet advice said to set it "on the task", and an agent wrote it as a task key the builder refused (a repeat of 270 and 488) (fixed before push)
601. The GNS3 fixture found console ports free and then let Docker bind them, and another test took one in between (a sibling of 123) (fixed before push)
602. The GNS3 2.2.61 container's API did not answer within two minutes once, with the machine idle, and answers in about 5 s every other time (open)
603. net.ios.config's first fallback recorded its undo lines as a Go []string, which the method's own reader refuses (a sibling of 529 and 534) (fixed before push)
604. Withholding net.ios.config's fallback undo made validate refuse rollback_on_failure on every net.ios.config task, and the docs said the in-run undo would replay the withheld lines (a repeat of 228 and 571) (fixed before push)
605. A container_host with no host declared SSH and a network address, so validate passed wait.port on it and the run asked for a credential (a sibling of 316 and 345) (fixed)
606. IOS scrolled a command longer than 80 columns sideways in its echo, so the echo was not stripped and stayed in the output (a sibling of 596) (fixed)
607. http.wait's not-ready test closed a listener and reused its port, and the released-port guard refused it (a repeat of 123, 177, 183 and 478) (fixed before push)
608. VarsRule refused a task with its own when:, which the run may skip (a repeat of 557) (fixed before push)
609. net.ios.config, net.cli.config and net.netconf.config applied lines holding a masked placeholder, and net.ios.config's new undo_lines would hand an author such lines (a repeat of 580) (fixed)
610. pkg/iosparse's ShowInterfaces keeps genieparser's failure on an "Encapsulation X, medium is Y" line (fixed 2026-11-06)
611. pkg/iosparse's ShowVlan joins wrapped lines quadratically on pathological output, bounded only by netcli's read limit (a sibling of 48) (fixed 2026-11-06)
612. net.ios.config's checkpoint files stay on the device until the change is undone, and nothing prunes them (fixed in 129a)
613. net.ios.replace deleted whatever file checkpoint or expect named after a replace, so checkpoint: nvram:startup-config would have erased the saved configuration (a repeat of 570) (fixed before push)
614. validate accepted reversible: and rollback_on_failure over net.ios.config calls that record no undo (checkpoint: false), so the in-run undo would stop halfway (a repeat of 156) (fixed before push)
615. Windows: the active-run reader skipped every record, because Windows reports every writable file as group and world writable (a repeat of 544 and 567) (fixed before push)
616. Windows: project.write compared all nine permission bits, so every run reported changed (a repeat of 544 and 567) (fixed before push)
617. A Telnet login resent the stored password when the device asked for it again, and each resend counted against the device's lockout (a repeat of 399) (fixed before push)
618. project.write's path could be rendered from device output, its mode could set execute bits, and the project's .pleiades refusal (in ReadProjectFile too) was by name, which a Windows 8.3 short name passes (a repeat of 313 and 438) (fixed; a hard link at the project root is still open)
619. The lab importers printed the server's version and node state text raw, under a comment saying every word was quoted (a repeat of 257 and 368) (fixed before push)
620. Telnet reads counted only data bytes and had no bound on a whole read, so a device that only negotiated, or sent a byte at a time, held a run until its context ended (a repeat of 175 and 351) (fixed)
621. VarsRule's advice named --extra-vars for adhoc and rollback, which have no such flag, and the rule never read rollback: steps (a repeat of 270 and 574) (fixed before push)
622. The GNS3 fixture retried a lost port race only on the kernel's wording, not Docker's "port is already allocated" (a repeat of 601) (fixed before push)
623. add-credential read a piped passphrase and a piped enable secret through two readers of one standard input, so the first reader's buffer took the second line (fixed before push)
624. import-cml --replace over SSH of a node first imported over Telnet kept transport: telnet (fixed before push)
625. net.trace chose its container path by a non-empty value while Manifest.For chose the call's shape by presence, so container: "" was checked as a container call and ran over SSH (a sibling of 115) (fixed before push)
626. A CML or GNS3 login error could quote the response body, which a 422 echoes with the password (a repeat of 457) (fixed before push)
627. net.ios.replace quoted every difference line in its errors, and telnetexec quoted up to 1 MiB of a device's question (a repeat of 431) (fixed before push)
628. Windows: a file standing where the active-run or journal directory belongs read as "no directory yet", because Windows reports a file opened as a directory as a path not found (a sibling of 615) (fixed before push)
629. The GNS3 fixture's image build failed when Docker Desktop's credential helper did, under make pr-check, though the image needed no credential (a sibling of 601 and 622) (fixed before push)
630. The image build context grew past its 32 MiB ceiling, because the deny-by-default .dockerignore still let test files and test data in (fixed before push)
631. A test in a package that only consumes a changed one failed only in CI: pr-check selects the packages a branch changes, not their dependents (a sibling of 559) (fixed 2026-10-24 by prscope's direct-importer rule; importers of importers still do not run)
632. A Telnet test's "device never goes quiet" depended on a 200 ms quiet window beating a 50 ms dribble, which a loaded Windows VM did not hold (fixed before push)
633. A Shell call's watcher could close the session after the call returned, when the caller cancelled the call's context at once: net.cli's next read found EOF (a repeat of 352) (fixed)
634. tools/failures' captured logs matched *.log in .gitignore, so they were never committed and its tests failed on every machine but this one (a repeat of 401) (fixed before push)
635. Windows: a file standing where the failure ledgers' directory belongs read as an empty ledger (a repeat of 628) (fixed before push)
636. Windows: a tools/failures test asserted the ledger directory's 0700 mode, which Windows does not keep (a repeat of 544, 567 and 615) (fixed before push)
637. A test file named login_seed_ios_test.go built only for GOOS=ios, so its tests never ran on Linux (a repeat of 374 and 428) (fixed before push)
638. virt.cml's lab.create, node.create and link.create dropped the undo of what they made when a later read failed, so rollback could not remove it (a repeat of 452 and 563) (fixed before push)
639. Console reads in pkg/cmlconsole had only a byte bound and the task's context, which the Crawl tier leaves unbounded, so a silent console server held a run forever (a repeat of 620) (fixed before push)
640. pkg/netcfg.Mask had no pre-shared-key rule, so an IKEv2 keyring's key went out unmasked, and it masked the command word after crypto key (a repeat of 577) (fixed)
641. kubernetes_cluster accepted tls_fingerprint and ignored it (a repeat of 441) (fixed before push)
642. An example and a test fake were written from remembered IOS wording that the device never prints (a repeat of 90) (fixed before push)
643. Closing a console session to abandon a refused question ran the logout, whose Enter answered the question yes (a repeat of 105) (fixed before push)
644. pkg/netcfg.Mask masked a GRE tunnel's `tunnel key 100`, a number that is not a secret, because the last word after `key` is always taken as the secret (a repeat of 640) (fixed in 129a)
645. net.batfish.check's reach from a node[interface] start location ended in Batfish's TERMINATEDABNORMALLY, and route and no_route cannot name a VRF (fixed in 129c)
646. IOS informational % lines from PKI and front-door VRF commands failed net.ios.config reruns and rollbacks, and a % line in show output failed net.ios.command (a repeat of run 1's notices, fixed in a2495ddb with no entry of its own; the class of 549) (fixed in 129a)
647. pleiades adhoc required a hosts argument for a method that needs no device, took the method name as the hosts, and accepted localhost only because the target is never resolved (fixed in 129d, except that hosts given to such a method are still ignored)
648. --help exits 1 with "missing positional argument" for adhoc, rollback, add-credential and seal, and journal --help exits 2 printing nothing (a repeat of 562) (fixed in 129d)
649. A YAML alias in a runbook failed with "got a YAML node of kind 16", because normalization never resolves aliases (a sibling of 486) (fixed in 129d; its messages still print other node kinds as numbers)
650. net.ios.command reported ping, traceroute, terminal and debug as changes, so diagnostic runs blocked rollback (a repeat of 599) (fixed in 129a; its rollback side is named in 129c and still needs --despite-run)
651. A net.ios.command or net.ios.config timeout dropped the device's last output, so the question it waited on was never shown (fixed in 129a)
652. Rollback refused a later run that left nothing to undo (a node stop, then a start) the same way as one that must be undone first, and check mode judged each undo against the device as it is now (a sibling of 551) (fixed in 129c, except that changes which cancel out are still read as undo first)
653. net.ios.command returned the device's text unmasked, so a show of the running configuration gave an IKEv2 pre-shared key in the clear (a repeat of 578) (fixed in 129a)
654. On a Cisco IOS console line, the device's own log message and the prompt after it were read as the next command's answer, leaving the session one response behind (a sibling of 596 and 606) (fixed)
655. The device root's properties were a strip list of secret names, so any property named otherwise, an operator's own included, reached a template (a repeat of 458 and 577) (fixed before push)
656. Unit blocks, rollback_on_failure undo steps and rendered within: targets ran a task reading device by fewer rules than the per-device path, though validate accepted all three (a repeat in kind of 572 and 574) (fixed before push)
657. A task reading device took lock_acquisition: all_at_plan_time and ran per device without it, with no word (a repeat in kind of 540) (fixed before push)
658. Two merge keys in one YAML mapping resolved first-wins in the runbook reader, where PyYAML takes the last, and the task-form warning read the runbook unexpanded (a repeat of 332 and 486, beside 649) (fixed before push)
659. net.pcap.summary's cleartext 0 read as "nothing in the clear" while frames it never examined (no IP, MPLS, unread link types) were counted nowhere (fixed before push)
660. net.pcap.summary's documented example called pleiades.builtin.assert, a method that does not exist, written from memory (a repeat of 642, the class of 514 and 515) (fixed before push)
661. Phase 129b was committed without running tools/gendocs' tests: two new methods had no examples, and the README and start-here counts were stale (a repeat of 459) (fixed before push)
662. pleiades validate replaced validate.Validate's own warnings with the file's header warnings, and the new unmatched-target warning fired on every rendered target (fixed before push)
663. A lone "-" was read as a flag, so the word after it was taken as its value (a repeat of 50) (fixed)
664. A GNS3 capture that read 0 bytes was blamed on gzip from a confounded experiment; the quiet link was the cause (open: GNS3 captures are set aside)
665. A reachability-question fallback for a Batfish traceroute that ends abnormally would have answered "not delivered" for a path that is delivered (fixed before push)
666. virt.cml.link.stop on a link that never ran recorded a start as its undo, so a rollback would start a link the run never found running (a repeat of 156) (fixed before push)
667. virt.cml.capture.stop's dest could name any project file, a runbook included, and replace it with a pcap (fixed before push)
668. testgate's run record kept a failure's last 40 lines, so tests/e2e's upgrade gate, which logs every command, left only its teardown and the cause was lost (a repeat of 414) (fixed; the upgrade gate's own failure is 686, fixed before push)
669. remove-host rewrote hosts.yaml before reading the host's state file, so an unreadable state file left the host removed and --credential and --forget-host-keys skipped (a repeat of 105) (fixed before push)
670. A rescue task reading device with lock_acquisition: all_at_plan_time ran per device without the lock; only validate refused it (a repeat of 657) (fixed before push)
671. vmrun start forks vmware-vmx, which inherits and holds vmrun's standard output, so a start read through a pipe or an SSH channel never ended (fixed before push)
672. macOS throttled a VMware Fusion VM started headless over SSH, so a Windows 11 ARM guest ran about five times slow, its first-logon VMware Tools install hung and keystrokes were lost (fixed before push)
673. vmrun getGuestIPAddress said VMware Tools were not running after a resume, while checkToolsState said running, so a resumed guest reported no address (fixed before push)
674. A killed vmcli left the VM's lock behind (<vm>.vmx.lck/M<pid>.lck), and every start then failed "The file is already in use" (open: recovery is by hand)
675. Windows 11 build 26300's bsdtar 3.8.9 cannot create a folder directly under a drive root, so win.archive.extract into C:\ failed (fixed before push)
676. winarmcheck ran wincheck's older private pleiades build, which could not load a project naming VMwareFusionCapable, because newLeg hard-coded the tool name (a repeat of 526) (fixed before push)
677. go generate ./internal/forge/catalogdata wrote eight scaffold files for the CML link and capture start and stop methods, which already exist in other files, so the methods would register twice (a sibling of 160) (fixed 2026-11-06; by then the run wrote 96 files, 88 of them for virt.clab)
678. README's check-mode count stayed at 191 of 204 after three Fusion methods were added (a repeat of 459 and 661) (fixed before push)
679. A scripted edit to box_test.go silently did not apply, so the Fusion fake never answered info and the first test case passed by accident (fixed before push)
680. The pr-check monitor showed the fast pass as pending while it ran, because it started that pass only while the mac leg was still pending (fixed before push)
681. Made concurrent, the Mac's container-free tests and its Podman container half would have shared one job file in the same tree, so stopping either could kill the other (fixed before push)
682. The Windows ARM VM had no C compiler, so go-sqlite3 was its stub; internal/ui/resources' shared fixture failed once, and 76 later tests failed with 404s the cgo-off skip could not tell from real failures (fixed before push; the classification gap is open)
683. With a C compiler installed, wincheck turned -race on and testgate's live tests said the race detector was there, and Go has none for windows/arm64 (a repeat of 502) (fixed before push)
684. Tests decided Docker was available from a docker on the PATH, so Homebrew's docker CLI on the Mac made them run where no daemon answers and no compose plugin exists (fixed before push)
685. Three lines added above a waived docker build call moved its gosec G204 finding from line 38 to 41, and the gate stopped on an unwaived finding and a stale waiver (fixed before push)
686. The monitor's make --trace reached the upgrade gate's own make up through MAKEFLAGS, which echoed a backup line that never ran, and the gate's no-second-backup check failed on the echo (fixed before push)
687. localauth's timing test averaged five calls in a row per side, and a stall on the busy Windows ARM VM failed the strict repeat (a repeat of 422) (fixed before push)
688. bsd-check stopped a gate at its readiness step with 3 GB free, because about 5 GB of go-build and Test* folders left by interrupted runs sat in /tmp and the check cleaned only its own trees (a repeat of 537) (fixed before push)
689. A for loop over pgrep -f "tools/monitor/monitor.py run" matched the agent's own shell, whose command line held that text, and killed it (a repeat of 153) (open: the guard does not catch pgrep -f)
690. On win-check, the full -race pass reported internal/ui/resources/credentialtypes as [build failed] with no compiler output, and the same build alone passed in 2.4 s (open)

691. Select copied the DAG field by field and dropped Vars and UsesSetFact, so a runbook run with a tag filter lost its runbook variables (fixed before push)
692. The Controller's rollback planning looked a journal entry's node id up in the DAG as it was, so the first rollback of a looped task was refused with 'runbook has no node tasks[1].item[2]' (fixed before push)
693. The Walk release-gate harness built its adapter without the template renderer cmd/runner passes, so the gate's first loop runbook failed to render only in the test (fixed before push)
694. An until task retried every kind of failure, so a refused login or a changed host key would be tried again up to a hundred times (fixed before push)
695. A run canceled while an until task waited to try again returned success, because the failed try had been cleared and the walk checked its context only at the top of a level (fixed before push)
696. The device vars filter dropped only top-level secret-named keys and secret-shaped strings, so a nested password in group_vars reached the dispatch payload (a repeat of 'allowlist, never a strip list') (fixed before push)
697. The vars loader followed symbolic links and read every file of a directory, so a link in a synced project pulled any readable YAML into every device's vars (fixed before push)
698. My own scripted edits silently did nothing three times because the text they replaced did not match the file's indentation, and the next step assumed they had applied (fixed before push)
699. An until task stopped after its first refused try, because the method had recorded a field before failing and the condition read it as holding (fixed before push)
700. The CLI listed a looped or retried task as not run after it had run, because report entries gained the item or try suffix and the not-run check compared whole ids (fixed before push)
701. Every CML error read `400: "{"` because the client kept the first line of the controller's indented JSON error (fixed before push)
702. The skill's example of a rendered target was `target: "{{ item }}"`, which validate refuses since the same branch (fixed before push)
703. net.ios.config failed a second, unchanged run on '% Policy already has the same matching fvrf statement' in three lab runs before the notice was added (fixed before push)
704. net.ios.config's backup stat was the running configuration masked only by value, so a secret shorter than eight characters reached every report, and net.netconf.config's backup was raw XML documented as not sanitized (a repeat of 653) (fixed before push)
705. IOS masking hid one word of a clear-text secret that runs to the end of its line, and missed wpa-psk, set-key, SNMPv3 auth and priv keys, OSPFv3 IPsec keys and manual IPsec session keys (a repeat of 640) (fixed before push)
706. A stat, a rendered param or an extra var crossed JSON on the Walk tier and came back as float64, so `result.r.count + 1 == 4` failed on a Runner while the CLI passed, and an integer above 2^53 lost digits (a repeat of 71) (fixed before push)
707. validate refused rollback_on_failure over every net.ios.config task from 2026-10-24, while the skill and docs/16 taught it, and nothing validated the skill's example (a repeat of 604 and 702) (fixed before push)
708. pkg/remotefile told a reader that this login may not write a directory that did not exist, which sent the reader after permissions (a sibling of 126) (fixed before push)
709. pkg/vmrun's parseKeyValues split a quoted vmx key at its own colon, so every disk key such as `'nvme0:0.fileName'` was misread (fixed before push)
710. Six methods recorded stats their Doc.Returns did not declare, one under another name than the documented one (a sibling of 228) (fixed before push)
711. Read-only lookups answered a missing object with an error, so an agent could not branch on absence (a sibling of 451 and 581) (fixed before push)
712. win.file.copy and win.file.download quoted unbounded host output in errors and carried an unchecked digest into the diff (a repeat of 627) (fixed before push)
713. validate counted `| default` anywhere in a filter chain as a guard, and one guarded read excused every read of the same name in the task (fixed before push)
714. The playbook converter put a value it had already resolved, holding Jinja delimiters, into a runbook parameter that renders again (fixed before push)
715. TestStopService_IsBounded took five seconds against a 300 ms bound because the test harness waited at close for the fake rm's sleep, not because the stop was unbounded (fixed before push)
716. The blind lab's grader failed every parallel teardown on another instance's lab, and the CML controller's node cap decided one instance's grade each run (fixed in the harness, `~/pleiades-cvd`)
717. The fixed-host URL rule ended a template's literal text at `{{` and `{%` but not at a comment `{#`, so launch or device data could choose a request's host (a repeat of LESSONS 244; a sibling of 714) (fixed before push)
718. pkg/netcfg.MaskXML masked a leaf only by its own or its parent's name, so RFC 8177 keystring and hexadecimal-string, OpenConfig key-clear-text and a deep SNMP community name reached net.netconf.config's backup (a repeat of 577, 640 and 655) (fixed before push)
719. container.list, container.info and container.image.info reported each container's full command and entrypoint argv, which often carries a secret (a repeat in kind of 455) (fixed before push)
720. net.ios.config's check looked for a line in the current block and then outward, so a line IOS adds in a submode was predicted unchanged when an enclosing block held it (a sibling of 564 and 579) (fixed before push)
721. validate's rollback_on_failure rule passed a templated crypto key line, and let any method with an UndoCall decide which calls' undo is partial (a repeat in kind of 614) (fixed before push)
722. A check after a predicted file.directory failed file.copy and file.template with "directory does not exist" where the run succeeds (a repeat of 564 and 708) (fixed before push)
723. wait.routes with state: absent read an empty or unparsed routing table as every route gone, and its own test used an empty table for "withdrawn" (a repeat of 13 and 451) (fixed before push)
724. pkg/farside.Output kept the first 300 bytes of a failed container.exec command's stderr, where the old code had kept the last 2048 bytes, so the reason was cut off (a repeat of 651 and 701) (fixed before push)
725. net.batfish.check registered its network's cleanup after a SetStat that could fail, so a failure there left the network on a shared Batfish (a repeat of 193) (fixed before push)
726. virt.vbox.vm.start through the autostart service, run right after an ACPI shutdown and a storage change, ran the service and left the VM powered off; the same start 30 s later worked (open: cause not proven)
727. wincheck's test script looked for the tree in the login's profile while its prepare script had put it on the data disk, because Test() built the script's parameters without the env the data directory rides in (fixed before push)
728. virt.vbox.vm.attach_disk and detach_disk recorded their undo after reads and writes that could fail, so a failed attach orphaned the disk it made and a failed read-back dropped the undo (a repeat of 452, 563 and 638) (fixed before push)
729. A headroom stop on Windows ended the go command alone and left its test binaries running, while the file's comment said they end with it (fixed before push)
730. gosec's G703 followed a file's content into the cgroup reads and the manifest's own bytes into its write, and only the gate's gosec had run over the new packages (a repeat of 475) (fixed before push)
731. The review fixes added failure branches to the disk methods and two helpers to pkg/vboxmanage with no test measured against a floor, and the gate's coverage-fast stage found both packages under (fixed before push)
732. The resources manifest read only this machine's passes, so a 4 GB lab VM ran two packages at once into tools/release's cold 2 GB link and the memory floor stopped the FreeBSD leg (fixed before push)
733. A lab VM's ACPI put-back printed no report in every gate and fell back to cutting the power, while the same stop by hand worked, and the check had thrown away the evidence (cause: the gate's own stop terminated it; diagnosed)
734. The FreeBSD lab VM reset itself through ACPI two minutes into a test run, with nothing in its syslog, and the next start emptied the console log that held the reason (most likely 735's damage; not proven)
735. Stopping each gate at its first failure terminated a lab VM's ACPI put-back, the power cuts that followed damaged its new data disk, and the VM would not boot (a repeat of 733's cause) (fixed before push)
736. testimages -served asked the registry for a container test's Image: "sha256:img", a short stand-in for an image ID the scanner's 64-digit rule let through (a repeat of 465 and 416) (fixed before push)
737. The v0.3.0 release page's links all 404ed, because the workflow published the notes file verbatim and its relative links resolve against the repository root on a release page (fixed before push)
738. gosec's G703 flagged the release-notes write of tools/release/notes.go, a repeat of 730 (and 475), because the new package was never scanned alone before the gate (fixed before push)
739. net.frr.config's check passed where the real apply failed, because canSave probed the file and the save writes a new file beside it (a repeat of 564 and 722) (fixed before push)
740. container.clab.exec and the containerlab client were first built and tested against a stub of shapes assumed, and the real server disagreed in four places (a repeat of 642 and 90) (fixed before push)
741. An onboarding test recorded only the last request's Authorization header, so a probe that added a credential-less request made it read an empty string (fixed before push)
742. The journal archtest refused the new JournalEntry.Become until it was classified, and the classification is a design decision the owner has not confirmed (open)
743. A field added to a journaled type reached the Crawl-tier file but not the Walk-tier store, so the new `Become` was lost on a round trip until the gate's reflection test found it (fixed before push)
744. become added code to pkg/sdk and internal/validate that only other packages' tests ran, and coverage-fast found both under their 100.0% floors (a repeat of 427 and 731) (fixed before push)
745. pr-check's durations stage failed although every test passed, because a new package had no recorded time and another had grown from 41 to 142 seconds (fixed before push)
746. A Go file named collections_linux.go was a GOOS build constraint, so the Linux-only name failed archtest (a repeat of 428 and 637) (fixed before push)
747. Two process traps in a pr-check session: a finished run's monitor kept port 8765 and answered the next run's wait, and files were edited while the gate was still in its last stage (a repeat of 153 and of LESSONS 288) (open: the guard does not catch an edit made through a script)
748. become on an external Collection's method was silently not applied: the call connected as the login while the stat and the journal said root (a repeat in kind of 578 and LESSONS 244, which LESSONS 358 did not reach) (fixed before push; two gaps open: the Walk tier refuses late, the playbook converter drops become)
749. A block's rollback_on_failure undo ran as the login while a later `pleiades rollback` replayed the same undo as the recorded account, so two tiers disagreed on one undo (fixed before push)
750. The clab client masked only the password, so under http_auth basic a server that echoed the login token put it into APIError.Message (a repeat of 626, 394 and 457) (fixed before push)
751. `import-containerlab --api` mapped a port published on every address to this machine's loopback, wrong for a lab on another host, and its own test asserted the wrong mapping (a repeat of 740, 642 and 90 in its test half) (fixed before push)
752. net.linux.route's undo was declared exact but recorded only destination, via, dev and metric, so an ECMP route would not be put back (a repeat of 582) (fixed before push)
753. `trust-host --first-connect --reason` recorded the reason before the scan and the trust, so a failed trust left a recorded reason (fixed before push)
754. net/linux's live test starts a container, so internal/catalog/net/linux was missing from DOCKER_DEPENDENT_PACKAGES and pr-check's devtools stage failed on TestEveryContainerPackageIsListed (a repeat of 239 and 447) (fixed before push)
755. The containerlab live test passed under Docker Desktop and failed on CI's native engine, because a bridged server could not see the bridge its own deploy made (a repeat of 426 and 538) (fixed in the pull request)
756. pr-check's new container opt-out dropped internal/archtest with the rest, so a released port reached CI that the archtest refuses in a minute here (a repeat of 123 and 478; a sibling of 631) (fixed in the pull request)
757. become was silently not applied on carriers sudo cannot reach (a device's CLI session, svc.* dispatched to WinRM, the engine's own actions), while the stat and the journal said root (a repeat of 748) (fixed before push)
758. A failed change that kept its undo dropped its become stat, so once the journal read the account from the stats a root change was undone as the login (a repeat of 749) (fixed before push)
759. Under become, gate.run reported a refused TCP precondition as a SKIP, and db.pg.* and net.netconf.config passed validate only to fail after the sudo probe (a repeat in kind of 557) (fixed before push; one gap open)
760. The collection coverage ratchet walked only the ledgers that exist, so deleting or renaming one took its floor out, and a ledger with no floor was only logged (a repeat of 403 and 397) (fixed before push)
761. The coverage ledger confined one read of four, so contract.json, map.yaml and a proof's test directory could be read through a committed symbolic link outside the repository (a repeat of 260 and 321) (fixed before push)
762. Untrusted ledger data panicked the archtest and the docs generator, and a proof naming a function go test never runs was accepted (a repeat in kind of 317) (fixed before push)
763. The OpenAPI reader skipped what it could not count, matched keys in any case and kept the last of repeated keys, each of which would raise a collection's coverage (a repeat of 325) (fixed before push)
764. A declared method could count an operation as mapped, and its proof skipped the reach check (the class of LESSONS 15) (fixed before push)
765. The coverage ledger's first records said more than the code and upstream support: the changelog, the floor file's rule, the published schema's field names and the vendored contract's copyright and version (a repeat of 571 and 228) (fixed before push)
766. Two "live" proofs rested on a refusal and on a log line, and node.start, which the live test runs, was declared fixture (a repeat of 467 and 491) (fixed before push)
767. An fp-reviewer launched seconds after its definition gained the triad tools ran with the new instructions and the old tool list, and reported the triad unavailable (open: cause unknown; the agent now checks its tools first)
768. validate refused pleiades.builtin.fail, assert and set_fact, which the engine runs, because the Collection rules kept their own list of dotted builtins (a repeat in kind of 661's doc_topics list and LESSONS 273) (fixed)
769. A flaky start-order assertion was committed because the test step was piped through `| tail` inside an `&&` chain (a repeat of LESSONS 100 and 192) (fixed before push)
770. A background job's goroutine read the task's result while startJob went on writing it (a repeat in kind of 64) (fixed before push)
771. --limit first narrowed every target, so a task naming a device outside the hosts would lose it under a limit (fixed before push)
772. serial: batches followed the devices' generated IDs, not their names, so a rollout ran s3 and s4 first (a sibling of 411) (fixed before push)
773. The Runner serial gate made its failure marker as root in sticky /tmp, so the task's login could not remove it and no task failed (fixed before push)
774. A task timeout never ended an SSH command whose child held its output, on Debian's sshd, and the task reported success (a sibling of 506, the class of 740) (fixed)
775. An async task ran attached in the Runner's child, because the detach mark was a context value no process boundary carries, and the Runner gate first passed without it (a repeat of 748 and LESSONS 363) (fixed before push)
776. The test binaries' child entry ran on a bare context.Background(), so SIGTERM killed it before any stop ran: 506's fix lived only in the real Runner's main (a repeat of 506 and 693) (fixed)
777. A detached job's stop did not run when the limit landed inside a poll, nor when the child's connection had already closed (a sibling of 506) (fixed before push)
778. remoteexec.run read a command's output buffers while x/crypto/ssh's copiers could still write them, once waitOrAbandon had abandoned the command (a repeat of 197, made by 774's fix) (fixed before push)
779. A detached command ran unlike the same command attached: the start script's umask leaked into it, it ran under sh rather than the login shell, and a login banner broke the job-directory parse (the class of 749) (fixed before push)
780. A detached command was an argument of the job's process for its whole life, visible in any process listing on the device (a repeat in kind of 455 and 719) (fixed before push)
781. Under become with a sudo that keeps the login's HOME, the job base sat in a directory the login owns, so the login could steer root's polls and kills (a repeat in kind of 761 and LESSONS 367) (fixed before push; the attack was reasoned, not reproduced)
782. A detached command's 20-second stop sat inside a 5-second kill, and an external Collection's program had no SIGTERM handling at all, so the stop never reached the device (a repeat of 776 at a sibling site, and of LESSONS 225) (fixed)
783. A background job ran on context.WithoutCancel, so a cancelled run waited out the job's whole async limit, and a failed job-id record reported a failed start while the job ran (the class of 777; its comment said the opposite) (fixed before push)
784. A handler with its own target, a handler whose call needs no device, a handler locking all devices at plan time, and a background task inside a rollback_on_failure block were all accepted and then skipped, failed or left un-undone on every run (a repeat of 252 and 324) (fixed before push)
785. A limit made only of separators parsed to the zero Limit, which admits every device (a sibling of 723) (fixed before push)
786. A Controller serial job with forks ran unbatched after one failed batch assignment, and a definition gone before its batches left the job running for ever (the class of 79) (fixed before push)
787. The CLI cut serial: percentages from every resolved host and the Controller from the admitted ones, and a serial: runbook with no hosts ran nothing, device-less tasks included (a repeat in kind of 749) (fixed before push)
788. The async release gates ordered two tasks by racing a fixed sleep, and bounded a timeout at 20 seconds against a 30-second sleep (a repeat of 211 and 769) (fixed before push)
789. Text that the branch's own changes made false: the stop's SIGKILL time, the list of Ansible keywords a runbook lacks, the converter's notes, a merged doc comment, and comments naming a removed list (a repeat of 571 and 765) (fixed before push)
790. Two tests in container packages still held behavior the branch had changed (an Ansible gate's argv expecting the dropped --limit, and the runbook guide's async example naming a tag its gate's inventory lacks), and pr-check, which leaves container packages to Actions, passed (a repeat in kind of 756) (fixed before the pull request)
791. Two detach tests dropped the connection when the job directory appeared, which the start script makes before it answers; reading the command from stdin widened that window, and fast-macos cut the start instead of a poll (a repeat of 211 and 788) (fixed in the pull request)
792. Generated files were written through a symlink at their name, and forge's "refuse to overwrite" was a check then a write (fixed in the pull request)
793. A lease's fencing token was about to order the jobs on a shared lease, whose holders overlap, so a job that joined first could change a device after a later one began and still read as earlier (fixed before push)
794. The rollback planner found an undo step's latest attempt by FinishedAt, a second clock comparison Phase 40a's text did not name (a repeat in kind of 551) (fixed before push)
795. The NATS lease's revision moves on every KeepAlive, so the "NATS KV revision" the phase named as the fencing token was not the acquisition's; a separate fence field holds it (a repeat in kind of LESSONS 82) (fixed before push)
796. The run counter's failure paths went in untested, and internal/journal measured 91.3% against its 94.1% floor (a repeat of 731 and 744) (fixed before push)
797. A run killed before its seal recorded no end tick and counted as later than every run after it, forever, and the refusal said it "was stopped, or is still running", which cannot be true while a rollback holds the run lock (a repeat in kind of 652 and 551) (fixed before push)
798. A job that kept running after its Runner lost the exclusive lease kept stamping the old, lower token, so it read as earlier than the job that took the device (a repeat in kind of 793 and 80) (fixed before push)
799. Fencing tokens were compared across lives of the lock bucket, whose stream sequence counts from one again when it is re-created, while the Controller's database keeps the old rows (a repeat in kind of 154) (fixed before push)
800. Reading the lock bucket's epoch with kv.Status raced with the shared KV handle's Get between concurrent acquisitions (fixed before push)
801. A target journaled before the run counter was ordered by clock against a ticked run, though every ticked run came after every unticked one (a repeat in kind of 794 and LESSONS 371) (fixed before push)
802. The run counter's rebuild read journals and seals with none of the strict reader's checks, and the counter's and the run lock's files were opened following links (a repeat of 395, 163 and 761) (fixed before push)
803. A recorded tick of the largest uint64 made the next tick wrap to zero, the value that means "no tick", and stopped the counter silently (a repeat in kind of 154) (fixed before push)
804. Every tick rescanned every journal while holding the counter's lock, so a large project made other runs wait, and a seal that timed out at the 10-second wait would have left a run with no end tick (fixed before push; suspected, not measured)
805. pleiades run's end-of-run warning that another run changed a device while this one worked still decided its window by wall clock (the class of 551, in a warning) (fixed before push)
806. internal/journal fell under its 94.1% floor again, at 93.0%, with the run marker code (a repeat of 796) (fixed before push)
807. The first predicted-state ledger keyed a path as the runbook wrote it, so a symbolic link, a merged /usr, a bind mount or a case-folding filesystem gave one place two keys (a repeat in kind of 313, and of LESSONS 229 and 367) (fixed before push)
808. The predicted-state ledger held claims that later tasks could make false, and three reviews of two designs kept finding more: each hole a "this is gone or empty" undone by something made (fixed before push, by recording removals only)
809. A check's directory listing ran through the account's login shell, so a POSIX script met zsh, fish or csh (a sibling of 779) (fixed before push)
810. The predicted_paths stat reached register: and reports (a sibling of 507) (fixed before push)
811. A removal the real run cannot make (a parent the login may not write) was carried into a later task's check, which then passed where main's failed (a repeat in kind of 564 and 739) (fixed before push)
812. BusyBox's test -w says writable to root on a read-only mount, because it reads the mode bits itself where dash, bash and FreeBSD's sh ask access(2); the project's own sshd test image runs BusyBox (a repeat in kind of 248 and 375) (fixed before push)
813. A Go test's os.FileMode(0o1777) was never sticky: Go's sticky bit is os.ModeSticky, and the octal 01000 bit maps to nothing (a sibling of 375) (fixed before push)
814. Locate's script ran set -- before reading $2, so a directory never listed its own identity (fixed before push)
815. filepath.Join cleaned away the `..` a test meant to pass (a sibling of 813) (fixed before push)
816. Two internal/engine test fixtures stood for something other than what they modeled: parallel branches on one device, and directory identities numbered by path length (fixed before push)
817. README's check-mode count stayed at "235 of the 257" while CLAUDE.md's was updated, and CLAUDE.md's own module count still said 210 beside its new 257; pr-check would not have run tools/gendocs, which reads the changed catalog but was not itself changed (a repeat of 678; 631's class) (fixed before push)
818. exec.winrm.shell's first creates/removes guard used PowerShell's Test-Path, which answers False with exit 0 for a path it cannot read, so a removes guard would have skipped the task's work (a repeat of 147 and 451; LESSONS 138) (fixed before push)
819. Several new virt.clab checks predicted a change the server refuses: a redeploy of a lab that is not deployed, host tools by a login that is not a superuser, veth endpoints not in x:y form, and veth and vxlan checks that never logged in (a repeat in kind of 811 and 564) (fixed before push)
820. TestGuards_RefusedBeforeAnythingDials could not fail: it asserted only that the error mentioned "creates", which the dial error after a missing refusal also did (a repeat of 467) (fixed before push)
821. windowsAbsolute accepted the device-namespace forms \\?\ and \\.\ and a UNC server named "..", so a guard path no one had measured reached the device (fixed before push)
822. exec.winrm.shell's timeout was documented as bounding the whole operation, while a guarded run made two operations, each given the full timeout (LESSONS 182's class) (fixed before push)
823. An empty creates was refused at run time but admitted by CheckCall, so validation and the run disagreed on one call (the sibling-guard class, LESSONS 244) (fixed before push)
824. The Windows guard gate used a fixed folder on the shared lab host (fixed before push)
825. A new test closed a listener and then used its port, and the released-port guard refused it (a repeat of 607, 123 and 478) (fixed before push)
826. A gate's PowerShell setup failed on a missing path although it used Remove-Item -ErrorAction SilentlyContinue, and exec.winrm.shell reported exit 1 (fixed before push)
827. On win-check, the Administrator login still read a file's attributes after icacls denied it Full Control on the file and its folder, so an ACL deny could not make an unreadable path for the gate (fixed before push)
828. A live clab test's command with single quotes (cut -d' ') broke through virt.clab.node.exec's wrapper (fixed before push)
829. The Runner's executor knew no secrets, so a fact holding a dispatched credential would have been stored in the Controller's database and served to inventory:read (a repeat of 394 and 578) (fixed before push)
830. The CLI's fact file store read, compared and appended with no lock between processes, and wrote its side file through one fixed temporary name, so two runs at once lost facts (a repeat of 135, 528 and 566) (fixed before push)
831. A late observation replaced a newer current value in both fact stores, which compared with the newest row rather than with the value as of the observation's own time (a repeat in kind of LESSONS 371) (fixed before push)
832. A large integer came back rounded from the Controller's fact table, because ent's JSON column decodes a map's numbers as float64 (a repeat of 706) (fixed before push)
833. The fact table's new key and last_seen_at columns were first NOT NULL with no default, a contracting migration the compatibility check refused (a repeat of 389) (fixed before push)
834. A Bash heredoc decoded a JSON-style escape for U+202E into a real RIGHT-TO-LEFT OVERRIDE inside a Go fuzz seed (a repeat of 256) (fixed before push)
835. A new architecture test, first written as "the engine must not reach internal/ent", failed on the engine's existing transitive dependency rather than on anything this phase added (fixed before push)
836. A field a later task's secret_mask marks secret was stored as a fact, because the earlier task's facts were stored at its level barrier before the mask had run (a repeat of 829) (fixed)
837. The Controller's check of a Runner's facts against each method's declaration was dead code in the shipped binary, which links no Collection, so collection.Lookup found nothing (a repeat of 52 and 578; in kind of 110, 180 and 693) (fixed before push)
838. On PostgreSQL every fact chain would have failed verification, because the chain hashed the observation time to the nanosecond and timestamptz keeps microseconds (a repeat of 371 and 92; in kind of 832) (fixed before push)
839. Equal run-order tokens never fell back to the clock, so within one Walk dispatch a second uptime gathered after a reboot was dropped as late, and a late batch of the same job could become current over a newer one (a repeat in kind of 831) (fixed before push)
840. The Crawl CLI ordered stored facts by each run's start tick, though CLI runs overlap, so a run that started earlier and observed later was ordered first (a repeat of 793 and LESSONS 372) (fixed before push)
841. The shipped Controller's device admission and rollback route read a method registry it never fills, so admission checked nothing and every recorded undo was refused, in v0.3.0 too (a repeat of 837, and so of 52 and 578) (fixed)
842. On macOS a device's facts lock open (os.Root OpenFile with O_CREATE) failed with "no such file or directory" while another store created the same lock file: macOS openat(2) relative to a directory fd reports ENOENT under concurrent create (cause reproduced 2026-11-03; mitigated by a bounded retry)
843. container.image.save's check predicted a digest the real save missed now and then, because classic Docker stamps the archive's blobs/sha256/ directory with the time of the save and lists several images in any order, and CI tolerated it three times as contention (a repeat in kind of 589) (fixed)
844. systemctl's options after its verb were read as unit names under POSIXLY_CORRECT, so facts.gather's services probe listed nothing in the SSH harness (the class of 494 and 420; caught by 494's harness setting) (fixed before push)
845. facts.gather's probes went to the account's login shell, so under csh, tcsh or fish a multi-statement probe failed to parse and the task succeeded with no package or service facts (a repeat of 809, and of 779's class) (fixed before push here; the same class in about 20 other send sites was open until 858 fixed it)
846. The macOS packages probe ran Homebrew's brew, a script under a prefix another account may own, as the gather login, possibly root (a repeat in kind of 781, and of vuln-corpus finding C4_auto-roboto_2026-11-01) (fixed before push)
847. dpkg's package probe kept only the "ii" state, dropping held and partly installed packages, and its test computed the expectation with the same filter, so it could not fail (a repeat of 54) (fixed before push)
848. The playbook converter passed Ansible's gather_subset through unchanged though the two meanings differ, so ['!facter', '!ohai'] converted cleanly and failed on every host (a repeat of 334) (fixed before push)
849. Package facts from two databases on one host overwrote each other, because names were told apart within one probe only, and the test proving it called a combination Gather never makes (a repeat of 54 and 571) (fixed before push)
850. Adding one container test to internal/catalog/facts moved the whole package to the container tier, so none of its tests ran on the non-Linux container-free legs (a sibling of 631 and 756) (fixed before push)
851. A literal U+200B (zero-width space) sat in a Go test string, and internal/archtest's invisible-character check passed it (a repeat of 256 and 834) (fixed before push; the archtest gap fixed by 905)
852. A test container with sleep as PID 1 never reaped children, so a daemon OpenRC started from docker exec became a zombie that start-stop-daemon would not stop (a repeat of 343) (fixed before push)
853. Past MaxFactsPerObservation (8192) an observation was stored as none of it while the task succeeded (a repeat in kind of 265) (fixed before push; a Runner's publish failing mid-split is open)
854. The secret-name exemption for item families applied to every Prefix declaration, so a future field-named family (env.DB_PASSWORD) would have been stored (the class of LESSONS 337 and FAILURE_PATTERNS 655) (fixed before push)
855. The Crawl file store's Record scanned the whole history once per fact, quadratic for thousands of packages against a growing history (the class of 490 and 611) (fixed before push)
856. A file named gather_windows.go built only on Windows (a repeat of 374) (fixed before commit)
857. A "this machine" test filtered on package.bash, which a Mac does not have, and failed only on the macOS runner (a repeat of 54) (fixed on the pull request)
858. Every command pkg/remoteexec sent over SSH went to the account's login shell, so under a fish or tcsh login the file methods, check mode, rollback, detach, scp, onboarding and the other platform scripts broke, some reporting success having done nothing (a repeat of 845, and of 809 and 779) (fixed; its open half, a fish host onboarded before the fix staying raw, fixed by 893)
859. sh-encoding a command dropped a NUL byte that sshd used to refuse, so /srv/app/.<NUL>./etc passed a Go ".." check and reached the device as /srv/app/../etc (CWE-158; a repeat in kind of 438, and of 317's NUL class) (fixed before push)
860. net.ssh.ping dialed through Runner.Run and skipped the device rule, so a Windows OpenSSH or network-CLI device got the sh-encoded line (a sibling of 757 and 823; LESSONS 244 and 363) (fixed before push)
861. The 128 KiB line limit admitted 131072 bytes, which Linux refuses because the closing NUL counts, and Start checked it after opening a session, so a refused command tainted a pooled connection (LESSONS 121's boundary class; a repeat in kind of 105) (fixed before push)
862. The release gates' own verification helpers dialed their own SSH client and sent POSIX checks raw to the login shell, so they failed under fish, zsh and tcsh logins (a sibling of 858) (fixed before push)
863. testsupport.UseLoginShell counted installer warnings as accounts and could not install fish on Rocky (a sibling of 779's banner parse) (fixed before push)
864. A new test file used a helper from a file built only off Windows, so the package's tests did not compile for Windows (fixed before push)
865. internal/redact imported pkg/remoteexec/shline, breaking its standard-library-only rule, because only the new archtest had been run (fixed before push)
866. facts.gather's Windows files probe used Get-Item -Force, which throws ItemNotFoundException for C:\pagefile.sys, a file that exists, so a present file read as absent (a repeat of 818; LESSONS 379) (fixed before commit)
867. The files path check accepted //server/share, /\server\share, //?/UNC/..., //./pipe/... and //./PhysicalDrive0, which Windows reads as network or device paths, because the facts copy of windowsAbsolute was weaker than exec.winrm.shell's (a repeat of 821; LESSONS 244) (fixed before push)
868. A stored, unkeyed SHA-256 and size of a task-named file would be readable by anyone with inventory:read, an offline guessing oracle for a small secret file (a repeat in kind of 829) (fixed before push; the keyed design is open)
869. /proc/net/udp lists an unbound UDP socket as 0.0.0.0:0 in state 07, which the Linux ports parser reported as a listener on port 0 (fixed before push)
870. FreeBSD sockstat prints one line per (process, descriptor), so a listener held by N workers became port...:80, port...:80#2, #3, churning with the worker count (a repeat of 849) (fixed before push)
871. macOS netstat -anv cuts an IPv6 address to its column width, so a listener's address was stored truncated (a sibling in kind of 606) (fixed before push)
872. Linux /proc/net byte order was guessed from uname -m, which cannot tell little-endian from big-endian MIPS (a repeat of LESSONS 343) (fixed before push)
873. The Windows files, ports and users scripts failed the whole task on a host lacking Get-LocalUser or the TCP/IP cmdlets, and [IO.File]::GetAccessControl does not exist in PowerShell 7, while their comments said every script read what any host has (a repeat of 789) (fixed before push)
874. Windows [IO.File]::OpenRead asks FileShare.Read, so a file open for writing elsewhere read unreadable until the writer closed it, drift on an unchanged file (fixed before push)
875. The Linux ports probe printed whole socket tables and every descriptor link, so a busy server would pass the 64 MiB output limit (fixed before push)
876. An IPv6 zone index (Windows %4) in a port key moved when an adapter was added, renaming an unchanged listener (a sibling of 870) (fixed before push)
877. The new tests repeated old test-quality classes: lab gates compared UDP counts read at different moments and waited with a fixed sleep, password asserts could not fail, the fact key bound was copied with no equality test, a filter dropping every path gathered nothing silently, and a malformed stat line was accepted (a repeat of 788, 211 and 422; LESSONS 136 and 134) (fixed before push)
878. A Runner counted the dispatched credential's username as a secret, so every fact naming the login was refused on the Walk tier (a repeat of 120; 394's identifier allowlist lived on the CLI only, and 829's fix carried the gap into the fact guard) (fixed)
879. The Runner invoker path returned before the digest rewrite, which a mapper's report said one call point covered on both tiers (a sibling of 860; LESSONS 244 and 363) (fixed before push)
880. A session script that copied a method's Doc into catalogdata cut the Go literal at the wrong closing brace, twice, leaving a file that did not compile (fixed before push)
881. The Controller read PLEIADES_FACTS_DIGEST_KEY after it had bound its listener and migrated the database (a repeat of 297; LESSONS 216) (fixed before push)
882. The chart's existingSecret path could not deliver the new digest key, so two combinations rendered a controller that would not start (a repeat in kind of 234) (fixed before push)
883. A digest the store could not keep was dropped without a word, in the engine and in the Controller (a sibling of 853) (fixed before push)
884. The raw-digest scrub looked only at the top level of a fact, a typed in-process value skipped it, and the store trusted the sender to leave out a private file's plain digest (a repeat in kind of 696; LESSONS 350) (fixed before push)
885. A sealed digest bound its device and fact but not its job, so one could be replayed into a later job, and a NUL in a bound field was accepted (the NUL class of 317 and 859) (fixed before push)
886. Text the branch made false still said a private file's digest is "withheld" (a repeat of 789) (fixed before push)
887. pleiades seal offered the fact-digest purpose, which only a Runner can use, so a hand-sealed value could only fail (a repeat in kind of 221) (fixed before push)
888. The branch's tests repeated old test-quality classes: the Controller's digest wiring was untested, gates checked a digest's shape and not its value or a change, a purpose check could not fail, and the Walk gate used one string for device ID and name (a repeat of 467; LESSONS 235, 351 and 381) (fixed before push)
889. The engine's result scrub wrote into an action's stats map, which an executor may share across the devices it runs on at once (fixed before push)
890. TestJournalAdmitsOnlyDeclaredKeys planted "0644", a value the entry's random hex IDs can spell, so it failed main by chance with nothing leaked (a repeat of 236) (fixed)
891. pkg/remoteexec returned a cancelled SSH command as a completed one: partial output with no error, or exit 143, and a context already done still sent the command (a sibling of 774; a repeat in kind of 352, of 149's fix and of 859) (fixed; its open part, stream.go's started commands, fixed by 896)
892. sdk.Connect, ConnectPort and ConnectFresh ignored the device's route, so a Collection method dialed a device behind a bastion directly, and so did net.ssh.ping (a sibling of 860; LESSONS 244 and 363) (fixed)
893. inventory.NotPOSIX kept the stored string "not posix" when #110 changed what it meant, so a fish or tcsh host onboarded before #110 had its commands sent raw to fish, where a quoted value is not a boundary (the open half of 858) (fixed)
894. ConnectVia's first version claimed a routed target's breaker probe and the route's own dial claimed it again, so after one cooldown the circuit reopened on its own refusal and a routed device stayed refused for good (a repeat of 146 and 398) (fixed before push)
895. A cancel's sever closed a routed target's client, whose Close waits on the tunnel's pumps, so a cancelled command through a stalled bastion had no bound (vuln-corpus C15; a repeat in kind of 352) (fixed before push)
896. A started SSH command (Conn.Start, Process, a subsystem) ran on after its context ended and Wait reported exit 0 with a nil error (the open part of 891) (fixed)
897. A command cut off for passing its output bound held the call until the command ended by itself (a sibling of 891) (fixed)
898. A command sshd refused to start came back as "ssh: command <line> failed", the whole command in the error, where a secret in shell-encoded form escapes masking by value (a repeat in kind of 23) (fixed)
899. A detached command that reached the device cut short was started as the job, because cat took the prefix and an end of input (a repeat in kind of 891) (fixed)
900. A login whose shell prints startup output (~/.ssh/rc) broke every poll of a detached job, which was abandoned as "may still be running" (a repeat of 779) (fixed)
901. On BusyBox ash with no setsid, a detached job had no process group, so the async limit stopped the wrapper and the command ran on (a repeat in kind of 504) (fixed)
902. A detached start that failed after reaching the device, a start with no job directory in its answer, and a poll that could no longer be made each left the job running, unnamed and unstopped (a repeat in kind of 777 and 783) (fixed)
903. internal/redact's Masker.Attr masked only string values, so an error, a byte slice, a Stringer, a URL with a password or a struct reached both log handlers whole, and a secret-named group's leaves were not masked by name (a repeat in kind of 23) (fixed)
904. The first fix of 903 rendered a byte slice as its decimal bytes, the secret in another spelling, and a panicking MarshalJSON ended the logging goroutine (a repeat in kind of 455 and 898) (fixed before push)
905. internal/archtest's invisible-character check passed U+2028, U+2029, zero-width and other format characters, tag characters, variation selectors and the Hangul fillers, and two test files held raw U+2028 and U+2029 (the archtest gap of 851) (fixed)
906. Two detach tests read where a command ran with [ -p /dev/stdin ], which on FreeBSD is not a pipe without fdescfs, so they failed on bsd-check, on main as well as on the branch (a repeat of 505) (fixed)
907. The detached stop script signalled whatever its pid file held, so a red-first test that wrote pid 1 through remoteexectest's local-sh server ran kill -KILL -1 as the developer account and killed the user session twice (a repeat in kind of 689) (fixed)
908. The detached stop reported "was stopped" for a job that had already finished, so an error could lead a reader to run the command a second time (a repeat of 789, 571 and 228; LESSONS 156) (fixed before push)
909. After a detached job's group was gone, the stop sent TERM and KILL to its pid alone, which by then may be an unrelated process, as root under become (a repeat in kind of LESSONS 327; the family of 153 and 689) (fixed)
910. A failed detached start threw away the stop's result, and a 4 KiB answer bound let a chatty ~/.ssh/rc cut a stop short (a repeat in kind of 777 and 783; LESSONS 244) (fixed)
911. A finished detached job whose output passed its limit or could not be read left its directory, which holds the command, on the device (a repeat in kind of 452, 563, 638 and 728; LESSONS 214) (fixed)
912. Masker.Attr masked a map or struct value only by its text, so a "password" map key or a Password field whose bytes were never registered reached both log handlers whole (a repeat in kind of 696 and 884; LESSONS 350) (fixed)
913. TestScanGoSource_FindsWhatItMustAndOnlyThat failed rather than skipped where symbolic links cannot be made, such as Windows without the privilege (a repeat of 567 and 544) (fixed before push)
914. TestSSHContainer_ACommandCutForItsOutputIsEndedNotWaitedOn bounded the call's elapsed time from above (a repeat of 479, 422 and 211) (fixed before push)
915. TestDetached_TheStopReconnectsWhenItsConnectionIsGone passed with nothing checked when the job's child pid file never appeared (a repeat in kind of 436 and 440) (fixed)
916. TestProcess_ErrorsNeverCarryTheCommandText's own command wrote the marker to standard error, which an error carries by design, so it failed on FreeBSD when that stderr arrived before the close (a repeat in kind of LESSONS 368) (fixed)
917. The docker host fixture's unpinned apk add of openssh-server broke when Alpine's index moved past the pinned dind image's own openssh client packages (a repeat in kind of 465) (fixed)
918. A registered secret held in a []byte inside a struct, a map entry or a list reached the logs and the stats as base64, since the value walk converts through encoding/json and Text saw only that spelling (a repeat in kind of 904) (fixed)
919. A facts batch whose job or device ID held a NUL failed every PostgreSQL query made with it, so the Controller handed it back for redelivery until its delivery budget ran out (a repeat of 208, through the dialect gap of 371) (fixed before push)
920. A fact whose value held a field named like a secret was stored, because Prepare held only the fact's own key to the secret-name rule (a repeat of 696, and in kind of 884) (fixed before push)
921. VerifyChain passed unchained values at the start of a device's fact history as rows from before the chain, so anyone who could write the store could add rows there that verified (a repeat in kind of 154 and 854) (fixed before push)
922. The CLI's fact file store renamed its state file into place unsynced, so a crash could leave an empty state file that refused the device's facts until removed by hand (a repeat of 566) (fixed before push)
923. A late change stored after a later confirmation ended the history at the late value, so the next run recorded a change nobody saw (a repeat of 831, a case its fix left) (fixed before push)
924. The run journal and job result consumers handed back for redelivery forever a message whose job or device ID held a NUL, and a result whose reason held one, so a device's own output could lose its result (a repeat of 919 at its sibling sites, and of 208) (fixed before push)
925. facts.gather stored a family source it could not read as an empty set, since over SSH a probe's exit status never reached Gather and every family parser read "could not read" as "nothing there", so every item read as removed and came back the next run (a repeat of 13, 451, 723 and 845) (fixed before push)
926. facts.gather's systemd probe read a host systemd did not boot, where systemctl lists unit files and no units, so every service read inactive, and a failed list-units read the same (a repeat of 13 and 723; LESSONS 244) (fixed before push)
927. An OpenRC supervise-daemon service's status line, which carries its uptime, was stored whole as its state, so the state was a new value on every gather (a repeat in kind of 870 and 874) (fixed before push)
928. FreeBSD with security.bsd.see_other_uids=0 and a non-root login stored a running daemon it could not see as stopped (a repeat in kind of 818) (fixed before push)
929. rc-update show -v put its option after the verb, so under POSIXLY_CORRECT glibc's getopt listed nothing and every OpenRC service read disabled (a repeat of 844) (fixed before push)
930. facts.gather's filter matched a family member before gatherNames.distinct told apart a name two databases share, so package.glibc:* matched nothing, and a filter naming a family alone passed the typo refusal and matched nothing (the first a repeat of 849, a case its fix left) (fixed before push)
931. gather_subset was read in order, so [!packages, all] gathered packages (a repeat of 848, a case its fix left) (fixed before push)
932. itemKey read a byte that is not UTF-8 as the printable U+FFFD, so two names differing only there shared one fact key, and facts.ValidKey accepted such a key (a repeat of 328 and 370) (fixed before push)
933. facts.holdsSecret missed a run's secret holding a byte that is not UTF-8, because the stored text holds U+FFFD where the secret holds that byte, so the fact was stored with the secret in it (a repeat in kind of 918 and 904) (fixed before push)
934. facts.gather's filter matched Ansible's fnmatch patterns with path.Match, which reads [!d] as a class holding ! and d where fnmatch reads every character but d (a repeat in kind of 334 and 848) (fixed before push)
935. launchd never reported a disabled daemon, so it vanished from the facts at reboot, and its status was the last exit status, which moves on an unchanged Mac (its status a repeat in kind of 870 and 874) (fixed before push)
936. After facts.Resumed wrote a value back at its last sighting, a late observation of the same value just before it added a second row of that value, which read as a change from A to A (a repeat of 923, a case its fix opened) (fixed before push)
937. file.remove's new check refusal took remotefile.Removable's no as the device's no, but Removable also answered no where it could not tell (root under a BusyBox shell), so every rollback check of a recorded file.remove undo on an Alpine root login would have failed, with advice a recorded undo cannot follow (a repeat in kind of 270, 488, 600 and 621; LESSONS 138) (fixed before push)
938. govulncheck failed main's push run after pull request #117 merged, on GO-2026-6865 in klauspost/compress v1.20.0, an advisory published after the pull request's checks had passed (a repeat in kind of 552) (fixed)
939. POST /jobs/{id}/rollback merged a request's method_settings over the job's own without the template's lock that a launch is held to, so a caller could set on a rollback what the template's author had locked (a repeat in kind of 268; LESSONS 363) (fixed before push)
940. internal/forge/collectionscaffold's renderDoc dropped the new collection.Param.RunSetting, and its every-field fixture would have failed only in CI's fast job, since pr-check selects neither that package nor its census (a repeat of 631, which is open; LESSONS 380) (fixed before push)
941. A run setting's refusal quoted the value it refused ("got %q"), so a value pasted into the wrong place would be printed back in the CLI's message and the API's 422 (a repeat of 457, the class of 298 and LESSONS 266) (fixed before push)
942. The proof's port holder dropped its connections and Go's finalizers closed them, so the first proof of 470's cause showed held ports working (a repeat in kind of 436 and 679) (fixed before push)
943. Startup output with no trailing newline (a printf in ~/.ssh/rc) ran into the first line of each answer a detached run reads, so the start's answer named no job directory and the job just started was stopped and its task failed (a repeat of 900, a case its fix left) (fixed before push)
944. withoutCommand took any start error holding the command line for the server's refusal, so a short Raw-view command inside another error ("session" in "ssh: session already started") came back as ErrCommandRefused and the real cause was lost (a repeat in kind of 820, at a case 898's fix opened) (fixed before push)
945. A detached start's answer was accepted as any job directory, not this job's own, so a damaged answer could have had the run poll, read and remove another job's directory (a repeat of 902, a case its fix left; the class of 313) (fixed before push)
946. A poll whose exit status was not a number stopped the job and threw away the stop's result, and quoted strconv's error, which holds the whole status line up to the poll's 64 KiB bound (a repeat of 910 and of 627; LESSONS 244) (fixed before push)
947. facts.gather reported ansible_distribution and its version from os-release NAME and VERSION_ID, which are not Ansible's values, and emitted no ansible_os_family, so a converted `when: ansible_distribution == "Debian"` was false on every Debian host (a repeat in kind of 848 and 334) (fixed)
948. On macOS every gather added one launchd job and removed the last one's, because launchd lists a job per SSH connection and per session (com.openssh.sshd.<UUID>), and a gather is itself a login (a repeat in kind of 935 and 870) (fixed)
949. On Windows a per-user service gets an instance per signed-in user (Template_<LUID>), so its instance keys changed across two boots of an unchanged machine, and the template Windows never lists was missing (a repeat in kind of 935, 870 and 874) (fixed)
950. A service's running state was part of its stable service.<name> fact, and it moves on its own, so an unchanged Mac or Windows host recorded changes between gathers (a repeat in kind of 927 and 870) (fixed)
951. Hardware facts held values Ansible does not report: a swap total on macOS, the page file size on Windows where Ansible reports 0, and Windows memory rounded down where Ansible rounds up (the class of 947) (fixed)
952. The Mac lab gate compared the launchd fact count with the services block's line count, which 935's fix had made unequal wherever an installed daemon is disabled, and the gate runs only where a Mac is configured, so nothing noticed (a repeat in kind of 790) (fixed)
953. win-check hung at a black screen on the boot after a guest restart, so win.reboot timed out after 600 s, and the sign-in test's cleanup could not reach the guest (possibly in kind of 362; open: cause not proven)
954. facts.gather listed systemd units that last only as long as a login, a connection or a process, so service.<name> keys came and went on an unchanged host, and a socket-activated sshd's per-connection key held the peer's address and port (a repeat in kind of 948 and 949) (fixed)
955. The 560 fix's settle read, when it failed after a successful start, stop or restart, failed the task, so the engine journaled a failure during the action with no undo, and with 551's fix a known, undoable change became an unknown_effect (a repeat of 563, 452 and 638) (fixed before push)
956. container.stop of an auto-remove container recorded container.start as its undo, which no rollback could ever run, since the engine removes the container when it stops (a repeat in kind of 666 and 156) (fixed)
957. 551's fix kept the "cancels out" skip ahead of its new rule, so a later run that had been rolled back was skipped although a task of it had failed during its action on the device, and its advice to roll that run back first could never clear it (a case 551's fix left; the class of 652) (fixed before push)
958. Three small defects from review: the Controller's rollback refusal had no kind or unknown_effects though the production guide says a refusal gives them (a repeat in kind of 571 and 789), the tftpxfer bind test passed with the fix reverted (the class of 54), and checkSystemd still asked for two listings after the probe grew a third (the class of 571) (fixed before push)
959. The strict journal reader kept its own copies of the engine's stages, skip kinds and node id grammar, so it refused a handler's entries and a parallel group's fan-out and join entries, and one such run made `journal list` and `rollback` refuse every journal (a repeat in kind of 768; the parallel case shipped in v0.3.0) (fixed)
960. A depth-1 CI checkout with `fetch-tags: true` holds no release tag, so `schema-compat` in `ci-remote` would have failed the `ci` job's Linux row on every pull request (a repeat of 418; the class of 416) (fixed before push)
961. A Runner terminated a newer dispatch it could not decode with no reported result, since the `payload_version` check ran only after a full decode, so the job would read dispatched until the Reaper republished it into the same refusal (the class of 208; its test a repeat in kind of 380) (fixed before push)
962. The rollback planner ordered one node's changes on two devices by Go's map order, so one journal planned two different rollbacks (fixed)
963. `pleiades collection list --describe` ran a program before checking its bytes were the approved build (fixed before merge)
964. The loader agreed a protocol version with a program and never told the program (a repeat in kind of 116) (fixed before merge)
965. A new Controller fact rule was held by an older test's batch, which named no emitting method (a repeat in kind of 837) (fixed before merge)
966. The live distribution test was rewritten to start all 22 images ansible.json records, four of them rolling, named only in JSON where tools/testimages cannot see them (a repeat of 125; in kind of 124, 917 and 745) (fixed before push)
967. The playbook converter copied setup's filter to facts.gather's, so once fact keys became native every converted ansible_* filter matched nothing and failed on every host (a repeat of 848 and 334) (fixed before push)
968. The converter's first fact conditions changed Ansible's meaning on every path but the comparison it was written for (fixed before merge, a repeat in kind of 848 and 967)
969. The two fact stores kept a value's last confirmation differently, and the shared conformance suite had no case that showed it (fixed before push, a repeat in kind of 113 and 749)
970. A stored fact's device edge had a setter, so an update could move a stored value to another device (fixed before push)
971. Dropping the fact table's last_seen_at and last_provenance columns was a contracting migration the compatibility check refused (a repeat of 833 and 389) (fixed before push)
972. A state-file test passed only when Go's map order ran its zero-filled case last (fixed before push, a repeat in kind of 962)
973. A fact read folded observations read after its values, so a run that replaced a value could read as having confirmed it (fixed before push, a repeat of 528 and 283)
974. An order source longer than the new column would have failed its facts batch on every delivery (fixed before push, a repeat of 208, 919 and 961)
975. The new observations file refused a device's facts over one damaged line, the data a crash-emptied state file had been forgiven for (fixed before push, a repeat of 922)
976. Six comments and a changelog sentence claimed more than the seen-versus-changed code does (fixed before push, a repeat of 571 and 789)
977. commitgate refused a comment-only ent schema edit, which go generate writes nothing for, so the only way through was --no-verify (fixed)
978. Two new ent tables reached CI with the backup listing fixture still holding the old table count (fixed before the pull request, a third time of a class two recaptures never indexed)
979. The Controller's check of which facts only a built-in may write kept the old family list when the Runner's floor grew (fixed before push, a repeat in kind of 837 and 749)
980. A new build tag could drop the catalog from a release build through GOFLAGS (fixed before push)
981. The playbook converter reported a missing native method as a missing parameter (fixed before push)
982. A new check in the loader's second pass refused a whole collections directory over a file name every earlier entry point admits (fixed before push, a repeat in kind of 823 and 887)
983. A program's own provider text on its capabilities moved its identity digest, then the loader overwrote it (fixed before push, the class of 113)
984. The engine journaled an external program's identity as the loader set it, so a name Valid refuses would make the run unreadable to the strict reader 982 planned, and invalid UTF-8 in it was rewritten by encoding/json (fixed before push, a repeat in kind of 982; its UTF-8 half the class of 370 and 932)
985. The strict journal reader's "refuse anything the writer could not have made" was not extended to format 2's fields (fixed before push, a repeat in kind of 959; LESSONS 408)
986. collection.UndoDigest's doc said it covers what decides whether a recorded undo still replays, and it left out a parameter's format, run setting and secret references (fixed before push, a repeat in kind of 885; LESSONS 348)
987. The changelog said 0.3 refuses a format-2 journal line by its format version, and v0.3.0 predates the format key (fixed before push, a repeat of 571 and 976)
988. Both fact stores' readers held a stored Collection identity to a rule a minor release may change, so a value stored correctly would have made a device's CLI history unreadable and shown a false chain break on the Controller after such a release (fixed before push, a repeat of 975 and 922; in kind of 959 and LESSONS 397)
989. The changelog and the runbook guide said every stored fact names its Collection, the same identity the journal records, and four cases store none (fixed before push, a repeat of 571, 976 and 987)
990. The setup filter's refusals and its wildcard review quoted the playbook's own pattern into the conversion report, and the branch's tests asserted the quoted text (fixed before push, a repeat of 330 and of 968's last case)
991. A filter of min facts under gather_subset ['!all', '!min'] converted and gathered them where Ansible gathers none, because setupSubset rewrote the subset before the filter's check read it (fixed before push, the class of 848; a sibling guard, LESSONS 244)
992. Two documented behaviors of the Windows and non-Windows setup split had no test, so removing either passed: the Windows half keeping the task's gather_subset, and a later read of the split's shared register converting (fixed before push, the kind of 888; LESSONS 213)
993. args.value's fixed advice "Quote the value in the playbook" was printed with every setup filter refusal and with refusals already on main that quoting cannot fix (fixed before push, a repeat in kind of 270, 488 and 600)
994. Three comments and the "decides on every host" wording claimed more than the code or its tests show (fixed before push, a repeat of 571, 789 and 976)
995. The system Go became go1.27.3 while go.mod's toolchain line named go1.26.10, and GOTOOLCHAIN=auto never switches down, so local builds, tests and scans ran on another Go than CI (fixed on this branch; on main until it merges, live here from 2026-11-08 10:29; a repeat of 552's second trap)
996. gosec-check read gosec's "Golang errors" as no findings, so a gosec that skipped 1,345 packages would have passed a module it had not read (fixed on this branch; on main since 968aac16, 2026-08-04)
997. Two diagnostic gosec runs ended by timeout left their go list and compile children running, five copies of internal/ent's compile at about 800 MB each, and free memory fell to 1.3 GB (fixed before push by stopping them by PID; a repeat in kind of 340, LESSONS 327)
998. hashivault's endpointFor joined reference elements with url.URL.JoinPath, which takes them as already escaped, so %2E%2E in a path reached Vault as `..` and %2F became a separator (fixed on this branch; on main since c6946faa, 2026-08-25, Phase 78b; the class of 72 and 438)
999. x/sys v0.49.0, brought in by x/tools v0.52.0, renamed LandlockRulesetAttr.Access_fs to Handled_access_fs and grew the struct, so the loader's confinement no longer compiled (fixed before push)
1000. The Makefile's new comment said every go command uses go.mod's toolchain, but before GNU make 4.4 an exported variable does not reach a parse-time $(shell), so CLEAN_ROOM_IMAGE still took the machine's Go (fixed before push, the class of 686; an outside review confirmed the claim, against LESSONS 392)
1001. The gosec pin's commit carried the G125 waiver for a line the next commit created, with a reason describing a fix not yet made (fixed before push, a repeat of 685; LESSONS 152)
1002. Dockerfile.runner and Dockerfile.controller still built from a golang:1.26-bookworm digest after the go line moved to 1.27.0, and no gate the branch ran builds them (fixed before push, the class of 559 and 631)
1003. The Crawl journal's strict reader held a stored Collection identity to wire.CollectionIdentity.Valid, whose format-character check reads Go's Unicode tables, so a Go release could make a journal one build wrote unreadable to another (fixed on this branch; on main since 73c94889, 2026-11-07, merged in #133; a repeat of 988)
1004. The JSON Schema builder wrote "required": null for an object with no required property, so the published run-report.json, with wire.CollectionIdentity in it, was invalid JSON Schema 2020-12 (fixed before push, the class of 765)
1005. Three intermediate commits failed their own checks: Builder.Object refused wire.CollectionIdentity for a missing doc comment, because clireport's ModelFiles gained pkg/wire/identity.go only in a later commit (fixed before push, a repeat of 1001 and 685; LESSONS 152)
1006. Comments and a guide paragraph the branch made false: two comments said readers hold Valid, the new Valid doc, and docs/13 saying only digests are compared and a same-declaration pair logs nothing (fixed before push, a repeat of 571, 789, 976 and 994)
1007. The Controller's journal store writes a Runner's batch with no check of its Collection identity, unlike the facts subscriber, so an identity no build writes can be stored and copied into a rollback's steps (fixed on this branch, Phase 136b PR 5; on main since 30f1d172, 2026-11-07)
1008. httpapi.Join decoded the ref it was given and then cleared RawPath, so a caller's url.PathEscape("r1/mgmt") reached the server as r1/mgmt, one name as two segments; catalystcenter.New did the same to its base path (fixed on this branch; on main since 35f391a1, 2026-09-24, and in catalystcenter since ad1ff164, 2026-08-06; the mirror of 998, the class of 72 and 438)
1009. httpapi.Join ran its control-character check on the ref as written and its dot check on the decoded path, so %00, %0A and %7F passed, and so did a "..;" segment, which a Java server reads as ".." (fixed on this branch; on main since 35f391a1, 2026-09-24; a repeat of 438)
1010. batfish, gns3 and aiprovider's Google kind appended a path already escaped to url.URL.Path, the decoded field, so every name was escaped twice ("prod west" went out as prod%2520west) (fixed on this branch; on main since b5baafd5, 2026-10-15, in aiprovider, 8444bb5b, 2026-10-22, in batfish, and 3fe90317, 2026-10-23, in gns3; the mirror of 1008)
1011. url.PathEscape leaves "", "." and ".." as they are, so pkg/cml sent GET /api/v0/labs/.. with the controller's token, and internal/ui's path.Join turned a record named ".." into a link to /ui (fixed on this branch; on main since db3b8b9f, 2026-09-13, in internal/ui, and 654537b1, 2026-10-23, in pkg/cml; a sibling in kind of 815)
1012. chi v5.4.0 routes on r.URL.RawPath whenever a request has one and records the parameter still escaped, so the API's link builder escaped sw%2F1 again into sw%252F1, and the 405 handler, matching r.URL.Path, found no route and sent no Allow header (fixed on this branch; on main since 5387aaa8, 2026-08-07, 72's fix)
1013. The API's handlers read chi.URLParam still escaped, so a device named sw/1 cannot be addressed, and 1012's links now point at /devices/sw%2F1, where the handler acts on the literal name sw%2F1 (fixed on this branch; on main since 9224cd24, 2026-08-04)
1014. Keeping catalystcenter's base path as written gave one controller two BaseURL() values, a%2fb and a%2Fb, and BaseURL is the synced controller's DeviceID (fixed before push)
1015. The database backup built its DSN host as host + ":" + port, so an IPv6 host such as ::1 gave postgres://...@::1:5433/..., which lib/pq reads as the host ::1:5433 on the default port (fixed on this branch; on main since ed27c14e, 2026-09-18)
1016. Two of the branch's first tests could pass with the fix removed: a FuzzJoin property that the old Join also met, and a dot-segment middleware test whose passing half asserted what a probe handler that echoed any id served (fixed before push, a repeat of 54 and 958; LESSONS 213, 338)
1017. The UI's first guard against building a path from an id outside paths.go matched one spelling, url.PathEscape called through the name url, so an aliased import, url.JoinPath, path.Join, an fmt format or a concatenation passed it (fixed before push, a repeat in kind of 194; LESSONS 114)
1018. A recorded undo value was never held to its parameter's declared Choices, since ParamFits checks only the type, the engine skips ChoiceProblem for a value with no template, and a rollback runbook is never validated, so a probe planned state: purged where the choices were [absent] (fixed on this branch; on main since 27f72c8f, 2026-09-28, Phase 40's planner; a sibling of 534)
1019. The Controller's facts subscriber erased a correct Runner's Collection identity in a mixed-version window, since CheckCollection compared the Runner's group with the Controller's own FirstPartyGroupOf, and logged that it "names one no build writes", which was false (fixed on this branch; on main since 7bee73af, 2026-11-08, merged in #134; a repeat in kind of 988, LESSONS 415)
1020. The Controller's facts subscriber drops every fact under collection.FactFloor() from a method missing from its catalog, with a warning that calls them spoofed, so a Runner newer than its Controller loses its new built-ins' facts (open: an owner decision; on main since f61076b7, 2026-11-07, Phase 135b; the class of 1019)
1021. The PR 5 plan deadlocked: its section 4 refused a needless --allow-skew, while its section 6 told the operator to add one where the Controller's declarations match the recording and the Runner's may not (fixed before push)
1022. The required-parameter check ran only once a skew was found, so an entry with no identity, or one on the group-digest fallback, planned an undo missing a parameter its method requires; the first fix still waived it when the undo digest matched (fixed before push, a repeat in kind of 557 and 823; LESSONS 244)
1023. The rollback planner and the Runner's Verify first held a stored Collection identity to Valid, whose format-character check reads Go's Unicode tables (fixed before push, a repeat of 1003 and 988; LESSONS 415)
1024. A fragment change moves an undo's UndoDigest but not its GroupDigest, since a manifest names its fragments as strings, so the plan's rule "group digests equal means no skew" would have missed it (fixed before push)
1025. The group-digest fallback compared a recorded group digest with the digest of the group this build routes the method to, which is release data (fixed before push, the class of 988 and 1019; LESSONS 415)
1026. The plan's warnings and the Runner's skew note reached only process logs, so a Walk operator never saw what a rollback accepted (fixed before push; two parts left open by design)
1027. The Controller computed GroupDigest eagerly for every rollback step, about 1 ms each, or about 5 s for 1,000 devices (fixed before push, the class of 490 and 855)
1028. Stored FQCNs and node ids were printed raw in rollback refusals, warnings, job events and the API's problem and warning fields, so a U+202E reached the 422 body (fixed before push, a repeat of 257 and 619)
1029. Text claimed more than the code did: "every recorded value is still checked", "a moved group is never a refusal", "a Runner under an older Controller refuses every changed step", a "matches" warning when nothing was compared, and a test comment citing "finding 1's deadlock" (fixed before push, a repeat of 228, 571 and 1006)
1030. The journal's EntStore.saveOne cleared an identity with no log, so a caller other than the subscriber lost an identity with no trace (fixed before push, a repeat in kind of 883; LESSONS 244)
1031. The Controller holds a Runner's delivered Collection identity to wire.CollectionIdentity.Valid, whose format-character check reads Go's Unicode tables, so a Controller built with a newer Go could clear an identity an older Runner's Valid accepted (open: an owner decision; low severity; in the facts subscriber on main since 7bee73af, 2026-11-08, and in the journal store since b62e4d37 on this branch; the class of 1003 and 1019)
1032. The API's request log and trace span recorded the decoded r.URL.Path, so GET /inventory/devices/sw%2Ffacts, the device "sw/facts", was logged as /inventory/devices/sw/facts, the facts of the device "sw" (fixed on this branch; on main since d907d123, 2026-08-01, in the log, and 25e59f3d, 2026-08-06, in the trace; the class of 1012)
1033. The UI's appearance forms built their return address from the decoded path, so a theme change on the page of "sw/1" returned to /ui/devices/sw/1, and on the page of "50%" sent Location: /ui/devices/50%, which is not a valid URL (fixed on this branch; on main since db3b8b9f, 2026-09-13; the class of 1012)
1034. A record named like a fixed segment beside {id}, "new" or "chart.json", linked to the UI's create form or chart data rather than its own page, under a comment in handler.go saying such a name could not exist (fixed on this branch; on main since 9de3c7fe, 2026-08-11; a sibling of 1011)
1035. url.URL.EscapedPath ignores a RawPath holding a raw |, ^, backtick, {, }, < or > and escapes the decoded path afresh, so each %2F became a separator: the UI's dot-segment refusal, and the branch's first request log and return address, judged a different path from the one chi routed (fixed on this branch; on main since 910123c3, 2026-11-08, in dotsegments.go, merged in #138; the class of 438 and 1009)
1036. pleiades inventory sync printed each quarantined and conflicted device's name and reason raw from the upstream source, so a crafted Catalyst Center hostname or AWS tag could send ANSI or direction-override sequences to the operator's terminal (fixed on this branch; on main since ad1ff164, 2026-08-06; a repeat of 257, 619 and 1028; LESSONS 266)
1037. TestDeviceHandler_PercentEncodedControlsArriveEncoded pinned 1013's defect as chi's behavior, asserting that a handler receives "%0a", "%0d" and "%2f" still escaped (fixed on this branch; on main since 5387aaa8, 2026-08-07; a repeat in kind of 751; LESSONS 213)
1038. The first TestOnlyPathParamReadsARoutedValue was narrower than its rule: it judged "chi routes this package" by an import of chi in the same directory, though chi sets each value still escaped through r.SetPathValue, and it missed a copied URL, a helper taking a *url.URL, RequestURI, RoutePath and a split EscapedPath (fixed before push, a repeat of 1017 and 194; LESSONS 114, 273 and 343)
1039. TestDeviceCreate_RefusesANameNoURLCarries passed with the create handler's name guard removed, since the store's guard refused the same names and mapped to the same 400 (fixed before push, a repeat in kind of 1016; LESSONS 135 and 213)
1040. The record-name rule was first put in a new pkg/recordname, public SDK surface, because internal/facts was said to import no internal package, a habit no record gave a reason for (fixed before push)
1041. Text the branch wrote claimed more than the code did: docs/09 said every other record is addressed by numeric id, a sentence said each segment is decoded once however a client spells its escapes, Reconcile's Reason doc left out refusals, and a gosec waiver cited the removed view.Addressable (fixed before push, a repeat of 571, 789 and 1029)
1042. An agent ran git stash push -- <path> on a committed file with no changes, which saved nothing and made no stash entry, and its git stash pop then popped and dropped the repository's only stash, the owner's parked GNS3 work, which the handoff said never to drop (recovered the same session; the class of 433)
1043. cmd/pleiades/main.go prints a command's error with %v, and some Reconcile errors carry an upstream name raw ("classifying %s", "building %s", "rebuilding %s"), so a crafted upstream name can still reach the operator's terminal unescaped (fixed on this branch, fix/cli-flag-splitting, 2026-11-09; the error print on main since 94ef873c, 2026-08-04, and the Reconcile errors since ad1ff164, 2026-08-06; a repeat of 257, 619, 1028 and 1036; LESSONS 266)
1044. Suspected, from reading only: the UI devices view's Update saves the item Bind built with a new random id, and the ent store's Save looks its row up by id, so editing a device in the web UI against a real store probably fails (fixed on this branch; reproduced as an edit that answered 303 and stored nothing, a repeat of 261; on main since 9de3c7fe, 2026-08-11)
1045. The toybox chmod test downloaded toybox 0.8.13 from landley.net on every run, so a CI containers shard failed when that server timed out, with nothing in the repository changed (fixed on this branch; on main since 17d17f8b, 2026-09-28)
1046. TestProcess_LeavesNoGoroutinesBehind took its goleak snapshot as soon as Connect returned, before the in-process test server had started its own DiscardRequests goroutine, so that goroutine read as a leak on fast-macos (a repeat of 169) (fixed on this branch; on main since 662ffb17, 2026-09-23)
1047. need.Require ignored a name in PLEIADES_TEST_REQUIRE that no test needs, so a typo or a renamed need turned a CI requirement off and the job stayed green (fixed on this branch; on main since dd0346ce, 2026-09-29; the class of LESSONS 417 and FAILURE_PATTERNS 930)
1048. testsupport.Download's verdict had no test: all six of the cache's first tests called fetchPinned, so the branch that fails on a changed checksum and the one that skips or fails on an outage could each be deleted with every test green (fixed before push; a repeat in kind of 1016 and 1039; LESSONS 158 and 213)
1049. The CI download cache was keyed on pins.go, the list of what should be cached, not on what the entry holds, and it is saved only on a miss, so an incomplete entry would have been a permanent hit that never refilled (fixed before push; the shard case is a written rule, not a guard)
1050. A relative PLEIADES_TEST_DOWNLOADS was used as given, so each test package wrote a download cache into its own directory inside the module tree (fixed before push; the class of 53 and 463)
1051. Download's messages named the wrong cause three ways: a malformed pin read as "the published file changed", a machine with no cache directory as "not cached and cannot be fetched" though no fetch was tried, and a body over 16 MiB as a changed checksum (fixed before push; in kind of 126 and 492)
1052. Two ci.yml comments said a test whose download is missing skips, above a step that requires toybox, where it fails (fixed before push; a repeat of 1029 and 1041, the class of 228)
1053. The UI conformance suite's fakes were more permissive than the stores they stand for: the device fake's Save answered nil and kept nothing, its reads handed out the stored item by pointer, and the device retire and inventory delete fakes answered nil for a record that was not stored (fixed on this branch; on main since 9de3c7fe, 2026-08-11, and the inventory delete fake since 46d6f734, 2026-09-13; a repeat in kind of 261)
1054. PATCH /inventory/devices/{name} answered 200 while ignoring name and type, so a rename or a type change stored nothing and said it had worked (fixed on this branch; on main since ea2fc4fd, 2026-08-10; a repeat of 111 and 261)
1055. Nine other PATCH endpoints answer success while ignoring fields their body can carry: announcements, bindings, contacts, credential types, credentials, inventories, projects, teams and templates (fixed: task 17; templates left the list on fix/template-patch-partial, 1069, teams, contacts and bindings on fix/patch-access-partial, projects on fix/patch-projects-partial, credentials and credential types on fix/patch-credentials-partial, and inventories and announcements, the last two, on fix/patch-inventories-announcements; a repeat of 111, 261 and 1054)
1056. PATCH /templates/{id} replaces a template's editable half from the body, so a client that omits survey deletes the survey, omitting defaults, prompts or required_capabilities blanks them, and omitting allow_simultaneous writes false (fixed on this branch; reproduced, and worse than read: a PATCH without name was refused, so no partial PATCH could succeed; on main since 5dbc35bc, 2026-08-12; a repeat in kind of 103)
1057. TestUI_FullCRUDRoundTrip's doc comment said it drove create, read, edit and delete through the rendered pages, and the test had no edit step (fixed on this branch; on main since 9de3c7fe, 2026-08-11; the class of 228, 1029 and 1041)
1058. An edit or a delete of a record that was not stored answered 500 on every writable UI view, as though the server were broken (fixed on this branch; on main since 9de3c7fe, 2026-08-11)
1059. About 30 hand-kept lists of each command's bool flags in cmd/pleiades duplicated its FlagSet, and run's list left out --fail-on-rescue, so in run, adhoc and rollback that flag took the next argument as its value (fixed on this branch; the gap on main since c9824c1f, 2026-10-20, and the lists since 90cdb0c7, 2026-08-05; a repeat of 50; the class of LESSONS 114 and 273)
1060. "--" was read as a value flag named "", so it swallowed the next argument instead of ending the flags (fixed on this branch; on main since 57138643, 2026-08-04; a repeat of 663)
1061. import awx-credential-types read its export through fs.Arg(0) with stdlib ordering, so a flag after the export (x.json --quiet) was counted as a second argument and failed with a usage error (fixed on this branch; on main since b32b5eb0, 2026-08-14; a repeat of 4)
1062. The first version of the parser changed --help output for every command, adding the flag package's "Usage of X:" list, against the owner's decision of 2026-10-22 that a bare --help stays as designed (fixed before push)
1063. The first quoting of upstream names fixed only Reconcile's own messages; the inner errors it wraps (the Save path's version conflict and write errors, factory.go, create.go) still carried the name raw (fixed before push; LESSONS 244)
1064. The first escape of CLI errors used termsafe.Escape at the print site only, which keeps newlines, so a device name holding a newline could still start a forged "pleiades:" line on stderr (fixed before push; the class of 1036)
1065. printError's escape was untested: with names quoted at the source, removing it changed nothing in any test (fixed before push; a repeat of 1016 and 1039; LESSONS 135)
1066. Three comments claimed more than the code (a test comment named the wrong test, a count said 99 where the golden held 93, and args.go said the bool test "cannot disagree" with the flags), and the table guard special-cased reviewFlags by name (fixed before push; a repeat of 571 and 1029)
1067. A writer's wait loop, while pgrep -f 'monitor.py run'; do sleep N; done, never ended, because pgrep -f matched the loop's own bash -c command line; it ran 33 minutes after the gate ended (open, as 689 is: no guard catches a self-matching pgrep -f; a repeat of 153 and 689)
1068. recordname.Check accepts a device name holding a control character such as a newline, so quoting each error is the only layer between a crafted name and a forged line (fixed on fix/name-control-chars, task 18: creation refuses such a name and a stored one stays reachable; the set was widened as 1081)
1069. PATCH /templates/{id} answered 200 for a kind, definition or inventory that differed from the stored one, and stored nothing for it (fixed on this branch; on main since 5dbc35bc, 2026-08-12; a repeat of 111, 261 and 1054)
1070. A survey question with no label answered 500 on create, PUT /templates/{id}/survey and PATCH, because the database's NotEmpty refused it while Survey.Validate did not; a label of spaces alone was accepted and stored (fixed on this branch; on main since 5dbc35bc, 2026-08-12; a repeat in kind of 974)
1071. A template name over 253 bytes answered 500 on create and PATCH, because the launchable row the store copies it into is MaxLen(253) and Template.Validate had no bound (fixed on this branch; on main since b044f0c2, 2026-09-20; a sibling of 1070, a repeat in kind of 974)
1072. Two of the branch's first PATCH tests passed with their protection removed, because the new label rule refused their unlabelled questions before the rule under test ran, and the survey refusal table had the same masking, seen only as a coverage drop (fixed before push; a repeat of 1016, 1039 and 1048; LESSONS 213 and 261)
1073. The first test of the fixed-field refusal sent the template's stored inventory id, so it could not see whether a changed inventory was ignored or refused (fixed before push; the class of 1016; LESSONS 213)
1074. Text the branch wrote claimed more than the code did: that a client can send a template back as it reads it, that an empty value clears every field, that null is refused everywhere, and that inventoryWriteDTO tracks presence with a pointer per field (fixed before push; a repeat of 228, 571, 1029 and 1041)
1075. Eight more PATCH handlers are full replaces, as 1056 was: organizations, teams, inventories, contacts, announcements, credential types, credentials and projects each write a field the body leaves out as empty (fixed: task 17; organizations, teams and contacts left the list on fix/patch-access-partial, with bindings, which it had missed, projects on fix/patch-projects-partial, credentials and credential types on fix/patch-credentials-partial, and inventories and announcements, the last two, on fix/patch-inventories-announcements; the class of 1056; a repeat in kind of 103)
1076. Suspected: a project name over 253 bytes answers 500, as a template's did (fixed on fix/patch-projects-partial: proven, with the description, scm_url and scm_branch bounds as well; a sibling of 1071)
1077. Suspected: the UI's template edit form round-trips extra variables through %v, so a typed value is saved back as a string (open, suspected from reading: task 19)
1078. Closing a create path to a crafted name broke three tests that stored the name through that path to reach an error, so every case ended "Reconcile succeeded" (fixed before push; a repeat in kind of 42; LESSONS 43)
1079. Moving a store lookup ahead of a validation, so a stored cluster under a refused name could be replaced, sent a NUL name to the database and would have answered 500 on PostgreSQL where main answered 400 (fixed before push; a repeat of 371 and 919, through the same dialect gap)
1080. API text said a create refuses only "." and "..", and the five device {name} routes said a name "contains a control character" where deviceName refuses only NUL, CR and LF (fixed on this branch; the {name} text on main since fda1792d, 2026-09-24, or earlier; the class of 228, 1029, 1041 and 1074)
1081. The name rule refuses U+00AD, U+200B, U+2060 and U+FEFF because each shows as nothing, and passes other characters that show as nothing (fixed on fix/name-control-chars by the owner's decision of 2026-11-09: the rule reads Go's unicode tables at creation only; a sibling of 905; LESSONS 114)
1082. A test list built with rg inside a git rebase -x script came out empty, because the script's PATH had no rg, so go test -run matched no test and reported ok with "no tests to run" (open, as 374 is: nothing fails a -run that matches no test; a repeat in kind of 374)
1083. The fact history route held the {key} it was asked to read to facts.ValidKey, the rule a new fact's key is held to, so a key stored before that rule grew would answer 400 at its own address (fixed before push; a repeat of 988 and 1003)
1084. Fourteen endpoints declared a request schema with no RequestContentType, so the OpenAPI document never published the body they take, and TestOpenAPIPaths_EveryDeclaredRequestSchemaIsPublished skipped each of them (fixed on this branch; on main since 5dbc35bc, 2026-08-12; a repeat of 116; LESSONS 114; the class of 1038)
1085. A partial PATCH reads the stored record, folds the body over it and writes every column, with no compare-and-swap, so a PATCH naming only a name that races an edit setting frozen writes frozen back to false (open; low severity while nothing reads frozen; credentials and credential types fold under the type lock since fix/patch-credentials-partial, where the race was not low severity, 1100, and inventories and announcements under the row lock since fix/patch-inventories-announcements, 1112; open also for the UI inventory form and a full member list from a stale GET, owner decision on a version token; a repeat in kind of 45 and 135)
1086. A code-writing agent ran git stash push --keep-index and git stash pop to test a commit alone, which LESSONS 423 forbids, because the rule was in the records, the handoff and memory but not in the agent's brief (open: nothing holds an agent's brief to the rule; nothing was lost; a repeat of 1042)
1087. Sending back the scm_url a project's GET showed replaced the stored URL with its redacted form, through PATCH /projects/{id} and through the UI's edit form, which prefills that text (fixed on this branch; on main; a repeat in kind of 580)
1088. A manual or archive project stored a source URL carrying a credential in plain text, because entStore.checkSource returned for every type but git before its embedded-secret check (fixed on this branch; on main; a sibling of 458, whose check it skipped)
1089. HasEmbeddedSecret and RedactedSourceURL were a list of credential shapes, and three review passes each found shapes the list missed, each one stored in plain text for a manual or archive project and shown raw by GET, the list and the UI (fixed on this branch by an allowlist; a repeat of 458; LESSONS 190 and 277)
1090. A refused git source and a recorded sync error repeated the caller's URL, password included, because ClassifySource built its message from go-git's parse error and scrubURL could not clean a URL it could not parse (fixed on this branch; on main; a repeat of 457 and 626; in kind of 72)
1091. A project could bind another organization's git credential, and a sync sent it to the host the project named (fixed on this branch on create, PATCH, the UI form and at sync; on main; a repeat of 456; LESSONS 276)
1092. A NUL in a project's name, description, scm_url or scm_branch reached the store, where on PostgreSQL it would answer 500 (fixed on this branch; on main; a sibling of 371, 919 and 1079)
1093. A refused UI project create or edit re-rendered the submitted scm_url raw, so the 422 page held the password just typed (fixed on this branch; on main; low severity)
1094. The UI harness's fake project store handed out len+1 ids while its Delete compacted the slice, so an id could repeat after a delete (fixed for the project fake on this branch; open: the inventory fake hands out ids the same way, and the announcement fake's Update and Delete keep nothing, and its new UpdateWith answers not found for the announcement it holds; a repeat in kind of 1053; LESSONS 424)
1095. A blank credential name answered 500, and a NUL in a credential's or a credential type's text, or in an input source's metadata, reached the store, where on PostgreSQL it would answer 500 (fixed on this branch; on main; a sibling of 1092, 371 and 1079)
1096. Three 403 refusals quoted another organization's credential or credential type by name, so a token holding credential:write without credential:read learned names it had no scope to read (fixed on this branch; on main; the class of 1091 and 72)
1097. A credential type edit made every stored value of a secret input readable in plain text through GET /credentials/{id} and the UI's edit form: clearing the input's secret flag did it in one request, and removing the input and adding it back as non-secret in two (fixed on this branch; on main; across organizations with the ordinary operator scopes, through 98)
1098. The type-edit rule's first version also ran in a managed type's reconcile, so in a mixed-version window an older build, at startup or through the kubeconfig import, deleted a newer build's input values in every organization (fixed before push; the import's rewrite of a stored managed type is on main; open: an older build still rewrites the managed row to its own schema, and the newer build's re-add then deletes non-secret values; a repeat in kind of 1019 and 1025; LESSONS 163 and 422)
1099. Fixing 1098, the kubeconfig import first used whatever type held the managed Kubernetes namespace, so a custom type there could have stored a cluster's token under an input that reads back in plain text (fixed before push; caught in review; LESSONS 244)
1100. A credential PATCH or UI form wrote back a record read before the type lock, so a reference a concurrent type edit's purge had deleted came back, and the secret it resolved to would fill an input nothing masks (fixed before push; the class of 1085 and 45)
1101. A secret resolved from an external source into an input its type does not mark secret was never masked, because CheckValues took a reference on any declared input and checkSourceField returned early for an external-kind source (fixed on this branch; on main; open: the file source's one directory lets a credential in any organization name a file put there for another, owner decision pending with 98; a repeat in kind of 1088; LESSONS 244)
1102. Reads of a credential and its resolution for a run read the credential and its type in two snapshots, so a type edit committed between them paired a value stored as secret with a schema that showed it (fixed before push; the two reads are on main, where 1097 needed no race; found by an outside review; the class of 283)
1103. In a rollback window a credential of a managed type whose input the older build left out could not be updated at all, rotating a compromised token included, and every run binding it failed (fixed on this branch; on main; a repeat in kind of 1019; LESSONS 415 and 422)
1104. A custom credential type of kind vault or database could mark its identifier input (vault_id, name) secret, and refusing to bind two such credentials to one template then quoted the secret value (fixed on this branch; on main; a repeat in kind of 1090)
1105. A PATCH body naming a field twice, in any mix of case and through the Kelvin sign, was accepted: a later key won for most fields, and a template's survey and a credential's external took both values (fixed on this branch; on main; a repeat in kind of 473 and 486)
1106. Text claimed more than the code did: the credential type bodies said "one of the twelve kinds" where there are thirteen, the read schema's kind list left out database, and comments said the managed reconcile was the only writer of a managed row once the type lock wrote that row (fixed on this branch; the kind text on main, the writer comments before push; a repeat of 459 and 1074)
1107. UpdateTypeWith handed its fold slices shared with the stored record it compares against, so a fold clearing secret in place would have passed the secret-flag rule (fixed before push; the class of 1053's read by pointer)
1108. The type lock's first PostgreSQL tests shared one database and left their container to the reaper, and put internal/credstore in the container tier (fixed before push; the class of 188 and 423)
1109. Credential refusals quoted input values a custom type may mark secret, so a secret could reach a job's failure reason or an API error (fixed before push; a repeat in kind of 1090 and 1104; vuln-corpus C7)
1110. The binding check read carried, undeclared inputs the run never uses, and carried references and input-source bindings were shown and then dropped on the next binding write (fixed before push; a repeat in kind of 113 and 1087)
1111. Inventory and announcement creates answered 500 for a blank name, title or body or a missing organization, and an unknown organization, an unknown member or a member listed twice reached a database constraint the inventory store read as a taken name, so the caller got 409 "already exists" (fixed on this branch; on main; a repeat of 106; a sibling of 1095)
1112. An inventory PATCH wrote back the membership it read before another PATCH committed, so a PATCH naming only the name could put back a device another PATCH had just removed, undoing a revocation on a grant surface (fixed on this branch for the API PATCH; on main; open: the UI's inventory edit form and a PATCH naming a full list from a stale GET still write from an earlier read, 1085; the class of 1085 and 1100; 97)
1113. A NUL in an inventory's name or description, or in an announcement's title or body, reached the store, where on PostgreSQL it would answer 500 (fixed on this branch; on main; a repeat of 1092 and 1095; the class of 371 and 1079)
1114. The store's first blank-name rule was held to the merged record, so an inventory whose stored name was all spaces, which the old PATCH could store, refused a PATCH that only removed a device (fixed before push; caught in review; a repeat of 1103, and of the rule in 1068's fix; LESSONS 415 and 422)
1115. Doc comments, the route text and the changelog said two UpdateWith calls never interleave, so neither can undo a membership change the other made, but Update and any PATCH naming a full list from an earlier read still replace the list whole (fixed before push; refuted by the claim-refuter agent; a repeat of 1074 and 1106)
1116. The first row-lock tests proved the wait only by a 200 ms sleep, and could block forever if the first update failed before its fold (fixed before push; caught in review; a repeat of 211 and 788)
1117. Two stub-backed handler tests could not fail once the stub's UpdateWith reset the organization and owner itself (fixed before push; caught in review; a repeat of 54 and 1016; LESSONS 213 and 424)
1118. A background rg with no path read its standard input, which was not a terminal, and waited 40 minutes as the first step of a git rebase -x verification, so nothing was checked (open, as a practice: nothing stops a command in a background shell from reading its stdin; a repeat in kind of 245 and 1082)
1119. PATCH with an empty body answers the full stored record and changes only updated_at, so a token holding inventory:write or announcement:write without the matching read scope can read any inventory or announcement (open: owner decision; new on this branch for these two routes; reasoned to hold for the earlier partial PATCH routes too; the class of 1096)
1120. The inventory member check answers 400 for an id that does not exist, 403 for another organization's and 200 for one's own, so a token holding inventory:write can learn which device and group ids exist (the 400 and 403 merged into one 400 on fix/inventory-member-checks, owner decision 2026-11-10; open: other routes still show ids and owners while authorization is by token scope, 98 and 1121; on main as 409, 403 and 200; the class of 1096)
1121. The web UI answers every inventory store refusal to an edit with a 500, as though the server were broken (open; on main; reasoned, not run; the class of 1058)
1122. PR 149's coverage job failed although every test passed, because the new container package internal/storage/rowlocktest had no recorded time in durations.json (fixed on the branch; a repeat of 745)
1123. A source URL's query or fragment was refused only as a literal "?" or "#", so a signature written behind "%3F", "%23", a double encoding, a fullwidth percent sign or a Unicode lookalike was stored in plain text and shown raw, even in the redacted form; the branch's first fix, a longer list, still missed nine forms (fixed on this branch; on main since #147; a repeat of 1089 and 458; LESSONS 190 and 375; vuln-corpus C13, as 354)
1124. The project store held every edit to the source rules, so a project stored with a URL a later rule refuses (a credential, or a source the deployment's policy now refuses) could not be renamed or described until its URL changed (fixed on this branch; on main since #147 by design, reversed by owner decision 2026-11-10; a repeat in kind of 1114 and 1103; LESSONS 415 and 422)
1125. The first checkSourceChange skipped the embedded-secret refusal when an edit changed only the type, so turning a manual project whose stored URL carries a password into a git one would have saved, and its next sync dialed with the password (fixed before push; caught in review; a repeat in kind of 1022; LESSONS 421)
1126. The project store's Update read the stored URL to decide whether its rules run, with no lock, so on PostgreSQL an edit could write back a credential URL another edit had just replaced, unchecked as unchanged (fixed before push; a repeat of 1112 and 1100; LESSONS 429)
1127. The withheld placeholder "(withheld: the URL carries a credential)" passed RefuseEmbeddedSecret, so a stale UI form, whose URL had changed since it was shown, saved the placeholder as a manual project's source URL (fixed on this branch; on main; a repeat in kind of 1087 and 580)
1128. A sync error stored before this branch's rules kept the signature it quoted, and the API's sync_error, the UI's project row and the UI's sync history showed it raw, because a stored error was scrubbed only when stored, by that release's rules (fixed on this branch; on main; the class of 1090)
1129. `head -25 FILE | rg PATTERN < /dev/null` searched the whole working tree instead of the pipe and printed 38 MB, because the redirect replaced the pipe as rg's input (open, as a practice; a repeat of 1118, the case LESSONS 430's second half names)
1130. A job launched against one organization's inventory dispatched to another organization's device, because a member the inventory held could later move into that organization and dispatch read the stored membership with no organization filter; the branch's first fix filtered only SetsForDevice, which nothing in production calls (fixed on this branch; on main; the first fix caught in review before push; a repeat of 97; LESSONS 191, 244 and 434)
1131. A code comment, the OpenAPI text and the security changelog fragment said a drifted inventory member grants nothing, which was false until dispatch filtered it (fixed before push; caught in review with 1130; a repeat of 1115 and 1074)
1132. An inventory update checked every member it held, not only those it added, so a member that had since moved into another organization blocked every edit that kept it, a rename included (fixed on this branch; on main; a repeat of 1114 and 1124; LESSONS 415 and 422)

Each line above is a failure pattern's title only. Full Symptom / Root cause / Fix / Lesson detail lives in [`FAILURE_PATTERNS_ARCHIVE.md`](FAILURE_PATTERNS_ARCHIVE.md), same entry numbers. Scan the index above before debugging anything new; open the matching archive entry only if a title looks relevant.

Append a new entry to the archive first, in full, then add its one-line, same-numbered title here. Never renumber an existing entry. Before writing one, search the archive for its class, by symptom and by cause: when it has happened before, the title says "(a repeat of N)" and the entry says why N's fix did not hold (LESSONS 274).
