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
448. pkg/tftpxfer's Get timed out on macOS twice running, and the cause is still unknown
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
470. The openssh fixture's mapped port refused every connection for two minutes while sshd said it was listening (open)
471. The Postgres fixture's mapped port never answered while the server had logged ready (a repeat of 470) (open)
472. file.yaml read every key behind a line comment as changed, because the comparison kept the comment
473. yaml.v3 accepts a duplicate key when decoding into nodes, which pkg/docedit had to refuse itself
474. A fuzz target that started a server per input exhausted the ephemeral ports, and the "crash" was the harness
475. gosec's taint analysis followed a CLI argument into a read and a write beside an arbitrary file (G703)
476. The gate tests ran gate.run's own timeout command on the host, and macOS has none that takes -k (a repeat of 420) (fixed)
477. A release gate's fake server read its data while the test wrote it, with no lock (a repeat of 6 and 33) (fixed)
478. The gate's TCP precondition test made its closed endpoint by releasing a port (a repeat of 123, 177 and 183) (fixed)
479. The gate's parallelism test asserted an upper bound on elapsed time (a repeat of 211 and 422) (fixed)
480. The test SSH server's forward dialed with no bound, and Close waited for it (a repeat of 339 and 444) (fixed)
481. A Debian sshd fixture's mapped port never answered while sshd said it was listening (a repeat of 470 and 471) (open)
482. A test of a package pkg/external reaches imported internal/testsupport, putting testcontainers-go into every external author's go.sum (fixed)
483. testcontainers' reaper container never became ready within its 60 s wait on a CI containers shard (open)
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
551. The rollback gate's "beneath a later run" check let a rollback run, intermittently, on the commit before this work too (open: its wall-clock reading is closed by Phase 40a on feat/c2a-rollback-ordering; a later run that failed before acting is still the gate's to name)
552. govulncheck was killed for memory at the gate three times, and the memory was a new advisory making it trace a vulnerable call (fixed)
553. The Controller's result consumer shipped without its dead-letter grant (a repeat of 258) (fixed)
554. Dead letters copied the whole message, so every dead-lettered dispatch put its secrets on the stream twice (fixed)
555. A run longer than the ack wait was delivered again while it ran, and the copies spent its delivery budget (fixed)
556. A template's machine credential reached every device in the fan-out, API-only ones included, because the exemption was a list of types (fixed)
557. validate and a Controller's admission checked no Collection method's required capabilities, only the engine did (a repeat of 151) (fixed)
558. The reaper's leadership test gave the leader a fixed 60 ms, and a slow machine spent it on the first query (fixed)
559. The capability check reached validate with no census of what already validates, and two cmd/pleiades gates failed in CI (a repeat of 550) (fixed)
560. A check of container.prune listed no stopped container while inspect said it had exited (open; seen again on 2026-10-30, a repeat of 560; the test now prints the engine's list)
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
598. An unknown flag before set-host's name took the name as its value, so the command said the name was missing (a repeat of 50) (fixed before push in set-host; open in the other commands)
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
610. pkg/iosparse's ShowInterfaces keeps genieparser's failure on an "Encapsulation X, medium is Y" line (open)
611. pkg/iosparse's ShowVlan joins wrapped lines quadratically on pathological output, bounded only by netcli's read limit (a sibling of 48) (open)
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
631. A test in a package that only consumes a changed one failed only in CI: pr-check selects the packages a branch changes, not their dependents (a sibling of 559) (open)
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
677. go generate ./internal/forge/catalogdata wrote eight scaffold files for the CML link and capture start and stop methods, which already exist in other files, so the methods would register twice (a sibling of 160) (open)
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

Each line above is a failure pattern's title only. Full Symptom / Root cause / Fix / Lesson detail lives in [`FAILURE_PATTERNS_ARCHIVE.md`](FAILURE_PATTERNS_ARCHIVE.md), same entry numbers. Scan the index above before debugging anything new; open the matching archive entry only if a title looks relevant.

Append a new entry to the archive first, in full, then add its one-line, same-numbered title here. Never renumber an existing entry. Before writing one, search the archive for its class, by symptom and by cause: when it has happened before, the title says "(a repeat of N)" and the entry says why N's fix did not hold (LESSONS 274).
