# Lessons Learned

Architectural rules derived from hard experience. Append a numbered entry focusing on the rule, not the
story, per `.AGENTS/AGENTS.md`.

1. **A corrected scaffold does not mean corrected code.**
2. **A capability or interface vocabulary lives in exactly one package.**
3. **Storage adapters own their own row-to-domain conversion; the factory never imports the storage package.**
4. **Embed synchronization primitives by pointer, always.**
5. **A Release Gate that says "drives a real execution" cannot honestly close before the executor and transport it depends on exist**
6. **A narrow, single-purpose utility can be built ahead of the phase that "owns" the larger feature it feeds**
7. **`gofmt -w` across an entire repository is unconditionally safe.**
8. **Test the exact invocation the spec documents, not the invocation that happens to work with a library's default behavior.**
9. **When a proposed terminology or field-naming decision touches a spec document, read the spec's existing usage before inventing a new convention.**
13. **A value tied to a product name will need to change again the next time the product is renamed; a value that names what the thing IS rather than what it is CALLED survives that rename for free.**
10. **A rename is only as good as the unscoped grep that follows it.**
11. **Four agents can safely write to the same working directory in parallel only if their file sets are genuinely disjoint by design, and each agent's own verification should be scoped to the files it owns, not the whole repository.**
12. **When Go's own visibility rules (`internal/`) block a promise the spec makes (third parties can add new concrete types without touching core), a package-layout refactor cannot satisfy that promise by itself.**
13. **Two features that both sound like "migration" will be conflated until a document states the boundary in a table.**
14. **Build the generator before the content it generates, and let the content be the generator's test.**
15. **A declared-but-unimplemented capability must fail loudly, and the linter must know about it.**
16. **A store with more than one writer cannot be an ownership ledger, and that decides whether a diff engine is possible at all.**
17. **Unknown values are containable; unknown actions are contagious.**
18. **Verify a type is called before describing what it does.**
19. **`go build ./...` does not build tests, so it cannot tell you a port change broke an implementation.**
20. **A test that has never been shown to fail has not been shown to test anything.**
21. **A port with zero production callers can be proven substitutable by a test suite, but not proven "selected by wiring in the composition root" until something in the real call graph actually makes that selection.**
22. **Build a graph-walking primitive against the general shape the type declares, not the special-case shape its one current producer happens to emit.**
23. **A port's own doc comment can describe a payload shape ("a marshaled JSON string of the Event struct") that is easy to violate by accident once a second caller writes to it.**
24. **Retry belongs strictly before the point of no return, never after it.**
25. **A security-critical default (host key verification, credential exposure) must fail closed on every branch, not just the branch you tested.**
26. **When an entity needs a stable, opaque public identity but already has a heavily-relied-on internal primary key, add a second column rather than changing the primary key's type.**
27. **When a framework's most-documented path for a feature assumes infrastructure your deployment target does not have, look for the simpler primitive the framework already generates before reaching for a heavier dependency.**
28. **RULE 0 ("run the same config the platform runs") applies to the one test whose own stated purpose is proving a mechanism, not to every test that happens to touch that mechanism's byproduct.**
29. **When a value's secrecy is only known at runtime, the safety check for whether it is even safe to substring-mask has to live at the exact call site that adds it to the mask set, not deferred to print time.**
30. **A new cross-cutting validation rule's blast radius is every existing test fixture with a permissive zero value, not just the tests written for the rule itself.**
31. **A default idempotency/dedup key must be derived from operation identity, not operation content.**
32. **An ephemeral JetStream pull consumer with no pull request against it is cleaned up by the server after `InactiveThreshold` (default 5s), and a test that creates one long before its first `Fetch` call will silently read from a consumer that no longer exists.**
33. **Reusing a fixed message ID across repeated `Publish` calls in a loop (a benchmark, a retry helper) silently engages producer-side dedup, and for anything that also waits on a resulting delivery, this doesn't just understate a number -- it hangs outright.**
34. **Reordering a handler's writes to fix a correctness bug can turn a background goroutine's existing, previously-benign concurrency into an active race, and only `-race` on the specific reordered path will catch it.**
35. **Wrapping every publish's payload in a shared envelope is a wire-format change binding on every consumer of the raw bytes, including ones that deliberately bypass the port whose own signature changed.**
36. **A wrapper library's public method signature is a promise about what it exposes, not about what the underlying protocol actually supports -- read the library's own source for the lower-level primitive before assuming the wrapper cannot do what you need.**
37. **A third-party server's version is real, load-bearing API surface for a feature the client library merely wraps -- verify the minimum version empirically against a real instance before designing around a capability, rather than assuming whatever version existing tests happen to pin already supports it.**
38. **In a CAS-based system, losing a compare-and-swap race is not by itself evidence of real contention -- only the caller can know whether the operation it lost against was compatible or conflicting, and conflating the two silently breaks whichever operation is more common under load.**
39. **When a spec's contention policy asks for revoking a live, in-use resource and the underlying mutual- exclusion guarantee has no way to detect the holder is even still doing something with it, narrow the feature to only ever act on what is provably abandoned, and say so explicitly, rather than build the literal ask and create a new correctness hazard.**
40. **A `select` between a cancellation case and a periodic-work case must treat "canceled the instant a tick also fires" and "canceled while the tick's own dispatched call was already in flight" as the same expected outcome, not two different code paths that happen to converge by luck.**
41. **A benchmark that provisions its own external test infrastructure (a container, a subprocess) inside the benchmarked function must register its teardown via `b.Cleanup`, never a naked `defer`.**
42. **`go test` caches a PASS result per package, build flags, and test binary hash, regardless of whether the test itself has real external side effects (starting a container, killing a real process), and silently prints `(cached)` instead of re-executing on a later, identical invocation.**
43. **Adding a new required env var to a composition root's fail-closed startup check is a breaking change to every existing test that spawns that binary as a real OS subprocess, not just to hand-run invocations.**
44. **A "did we already process this" failsafe keyed on a single marker field's presence is bypassable by anything that controls that field's name.**
45. **An interceptor sitting in front of a generated ORM's batch query method must not fail the whole batch for one bad row, and single-result methods built on top of a batch query inherit that same exposure.**
46. **A maintenance/rotation write that bypasses a resource's normal optimistic-concurrency write path must still honor that same compare-and-swap, or it becomes an unconditional last-writer-wins that silently discards concurrent legitimate changes.**
47. **`O_CREATE|O_EXCL` alone does not make a file's *content* atomic, only its *existence*.**
48. **A composition root gaining new one-time startup work (a migration, a rotation pass, a cache warm) must run it concurrently with, never strictly before, whatever makes the process's health probes pass.**
49. **A function walking every prefix of a caller-controlled sequence must build its accumulated key incrementally, never rejoin the whole prefix from scratch on each step, once that sequence can be externally supplied.**
50. **An adversarial review's suggested code fix must itself be checked against the invariant it's meant to protect before applying it, especially when the review is really pointing at a doc/code mismatch rather than a functional bug.**
51. **A nested command dispatcher needs a shared sentinel error to keep its own "unknown subcommand" failure the same shape as the outer dispatcher's, because a plain `error` return type erases that distinction on the way up.**
52. **A roadmap phase's own checklist prose can go stale relative to a shared primitive an earlier-numbered phase already built, when phases execute out of their originally-drafted order -- verify the primitive's real existence in code before trusting what a phase's own Pattern Entry Gate says about it.**
53. **A checklist item can name the right shared primitive and still cite the wrong mode of it, when the item's own prose conflates two distinct call sites that merely sit near each other in the specification -- cross-check a cited test/example against what it actually proves, not just against whether the primitive it names is the correct one.**
54. **A shared helper's implicit precondition survives unnoticed until a caller finally violates it -- audit what a function silently assumes about every past caller, not just what its signature says, before adding a new caller that differs from all of them in one respect.**
55. **A source filename ending in `_GOOS.go` or `_GOARCH.go` is an implicit build constraint in Go, with no `//go:build` line required, and `go build ./pkg` succeeding proves nothing about whether every file in it was actually compiled.**
56. **A generated, per-package `init()`-registration pattern needs its own composition-root aggregator from day one, proven by an integration-level test, not by each generated package's own isolated unit test.**
57. **`go test ./...`'s default cross-package concurrency means a test that mutates the real module tree can race a different package's `go list`-based architecture test, and the fix is not always worth building.**
58. **A fixture that sets a field the code under test never reads is not evidence a feature works; it is evidence the test doesn't fail.**
59. **Namespacing a shared directory on the way in is only half the job; the cleanup has to be namespaced too.**
60. **A port is not complete because every method on it works; it is complete when every operation its consumers need exists.**
61. **A default value that names the wrong kind of thing stays harmless exactly until something compares against it.**
62. **"Refuse loudly" and "tell me what would happen" are different requests, and one guard can serve both only if the caller decides which it wanted.**
63. **A second consumer is what turns an interface from a guess into a design, and the two consumers have to be unalike for the evidence to count.**
64. **A shared fold-and-merge primitive's safety comes from the caller's combine function, not from the primitive; a "more specific wins" mode and a "the strongest statement wins" mode look identical until the case where they disagree.**
65. **A schema-diff codegen tool's own safe-by-default option can make a schema removal silently incomplete, and the tool exiting zero looks identical to "nothing needed doing."**
66. **A `make ci`/coverage regression that predates a session's own diff is still worth finding, but is not that session's to fix.**
67. **Adding a cache to a function silently invalidates any benchmark that assumed every call does real work, and nothing fails to flag it.**
68. **A "closed by construction" discriminator needs an explicit exemption list the moment one case legitimately crosses it, and the doc comment claiming closure must be edited in the same change.**
69. **`tools/gencatalog`'s `go generate` target is not incrementally safe: it re-runs `forge new-collection` for every catalog entry, and the real CLI refuses to overwrite a file that already exists.**
70. **"Masked at the moment of registration" is a property of *when* a task marks its own output secret, not of *what shape* the marked field name can take - the two are independent axes and only one of them needed to change.**
71. **A masking feature that silently fails to mask is worse than one that never shipped, and a real hand-written usage example is what caught it, not the tests written for the feature.**
72. **A roadmap checklist item's "Expected" pattern list is a draft prediction to verify, not a mandate to satisfy, and a Pattern Entry Gate's own Adversarial Pattern Justification line can reveal that the "light" version of an item is an active regression, not merely an inert one.**
73. **A cross-process concern is only "built" once it is proven at the far end; the boundary crossing is the feature, not the header.**
74. **Prefer one wire format decided in one place over per-boundary "reasonable defaults," and encode the format the specification mandates rather than the one the language makes convenient.**
75. **Changing where the default logger writes is a behavior change with a blast radius well past logging, and both directions of it bite.**
76. **A mechanism with a passing unit test suite and zero production callers is not "adopted," and `PATTERNS.md` saying `YES` does not make it so.**
77. **Wrapping `http.ResponseWriter` is lossy by construction, and the loss is invisible to every test that does not exercise the specific optional interface it dropped.**
78. **Compute an affordance and enforce it from the same object, or the two will drift.**
79. **A speculative authorization probe is not an access decision and must not be recorded as one.**
80. **Never reflect a caller-controlled path back as a URL the client is invited to act on.**
81. **A security test that fails intermittently is not flaky infrastructure, it is a test whose own setup is wrong, and the direction of the failure tells you how much luck you had.**
82. **A wire field must be named for the property key it actually reads, never for the value someone hopes is there.**
83. **An idempotency guard that only answers "has this started" is not a crash-recovery story, and a lease that reclaims by timestamp alone is not a fencing token.**
84. **A validated boundary at one entry point to a value does not validate every other entry point the same value later reaches; trace a value through every consumer, not just the one an earlier fix already covered.**
85. **A handler your own code does not control (a plugin, an adapter, anything implementing an interface a future caller could satisfy however it likes) needs a `recover()` at the boundary that calls it, not just careful cleanup code that assumes it returns normally.**
86. **A context canceled by the very event you need to react to cannot also be the context that reaction depends on staying alive.**
87. **An idempotency key must be derived from the logical operation, not minted fresh per attempt at recording it, or two attempts describing the same real-world event become indistinguishable from two different events.**
88. **A document that has already solved a structural problem writes the solution down. Read its own policy before restructuring it.**
89. **"No value was specified" and "no value is needed" are different states, and collapsing them at the point of lookup silently disables whatever was supposed to supply the default.**
90. **A branch reached only by winning a race is not a covered branch, and a coverage floor measured against one is a scheduled CI failure.**
91. **The module tree is shared mutable state, and `go test ./...` runs packages in parallel: a test that writes into it and a test that reads all of it are a data race with no race detector watching.**
92. **A test pinned to a different version of a dependency than the deployment runs is not testing the deployment, and `latest` on either side means nobody knows which version was tested.**
93. **A specification's own prose describing a third-party CLI tool's interface can describe a version of that tool that no longer exists; verify against a real, currently-installed instance of the exact dependency before designing a parser or an invocation around it.**
94. **A test fixture that no production code path reaches proves nothing, and its presence actively disguises the gap by making the test look thorough.**
95. **Prove an assertion can fail before believing it passes; a negative control that does not fail may have found real defense in depth, so keep opening layers until it does.**
96. **A test that reaches its subject through a subprocess build has no import edge, so Go's test cache will replay a stale pass: such targets must pass `-count=1`.**
97. **A gitignored document has no `git checkout` to undo it: copy it before any scripted edit, bound every search to the section being edited, and never anchor a replacement on a string that is merely a prefix of the same line elsewhere.**
98. **Absent and empty are different instructions in both directions: `omitempty` collapses them on the way out and `encoding/json` collapses them on the way in, so any field whose absence must differ from its emptiness needs a pointer on the response type AND on the request type.**
99. **A consistency test whose two sides both derive from the code under test proves internal agreement and nothing about completeness; anchor one side outside the implementation, and give any affordance whose only failure mode is silence a test that names the visible outcome.**
100. **A shell reports the last command in any compound, so `| tail`, `; echo` and `&& x` all discard the status that mattered; confirm a gate's own success line in a captured log rather than trusting an exit code or a fixed-length tail.**
101. **Anything that must happen on every write to a port belongs in a decorator over that port, composed once: list the port's callers first, because if there is more than one kind, the call site is the wrong place and the second kind is the one that gets forgotten.**
102. **When which controls a form shows is stored in the row rather than written in the code, the field declaration has to become a function of the row: rendering all of them and reporting the refused ones afterwards is an affordance that does nothing, and disabling them is the same lie with better manners.**
103. **Do not measure coverage of code whose branches are chosen by a timer: separate the loop from the work, so the loop's test owns the scheduling and the work's test calls the work. A test that passes reliably can still cover unreliably, and a ratchet reads that as a regression.**
104. **A conformance measurement is code that can be wrong, and its characteristic failure is a hole shaped like the thing it measures: every "not worth representing" verdict is a place it stops looking, so record what makes a field dismissible and re-derive those verdicts whenever new input arrives.**
105. **A struct returned by a resolver is a checklist, not a report: before calling a resolve-and-persist path complete, grep for a second reference to every field the resolver's own return type declares, downstream of the call site that received it.**
106. **A parameter that merely shares a field's name and domain concept (forks ~ concurrency) is not wired to it until the parameter's actual arity at *this* call site is checked — a resolver that can only ever return one item makes any concurrency bound over its result unobservable, no matter what value is passed.**
107. **A shared-primitive table's "Build by" column ages against the phase bodies it summarizes: before consuming a primitive, open the earliest phase named in its own "Call sites" column, and if that phase precedes the stated builder, correcting the table is the first commit of the work rather than a cleanup afterwards.**
108. **A package that generated code imports can never import anything that imports the generated code: run `go list -deps` on a candidate import before designing against it, and when the answer forces a contract to be restated in two places, put the agreement test in an external test package, which is the one place both sides are importable at once.**
109. **A plan's count of what an external system offers ("about twenty of its types are pure data") is a factual claim about somebody else's code, usually written from its documentation and believed from familiarity: fetch that system's own source and count before sizing the deliverable around the number, because transcribing the planned quantity from memory produces artifacts that pass their own tests and differ from the system they exist to be compatible with.**
110. **"Pre-existing", "unrelated to this work" and "flaky" explain a red gate but never authorize pushing past one — CI re-runs the identical target and has no concept of provenance; and because `make ci` stops at its first failure, deferring an early gate leaves every check behind it unobserved rather than passing, turning one red build into a queue of them.**
111. **Declaring a component and scheduling one are different acts, and the missing second one is invisible from either document because a specification has no schedule column and a roadmap has no unclaimed-requirements section: audit the pair by naming something a user does end to end and asking which phase owns every step, never by checking that both files look complete.**
112. **A debt with a deadline needs the deadline checked against whether the named owner can actually pay it, by counting the individual entries against that owner rather than reading the summary sentence that goes stale: a due date nobody can meet is discharged by everybody ignoring it, taking the surrounding rules' credibility with it.**
113. **A file excluded from the build by a tag like `//go:build ignore` is excluded from every guard built on the compiler, so it must import a shared value rather than copy it: the tag removes the file from `go build`, `go vet` and every test, but not from the module, and the doc comment promising the copy still matches is the first thing to go stale.**
114. **A guard written after an incident gets written against that incident's literal, so it ends up narrower than the rule its own documentation claims: state the rule as a predicate, give the predicate a table test containing the near-misses, and watch the guard fail on the regression before trusting it to pass.**
115. **An escape hatch has more consumers than setters: delete it by making the old behavior impossible and letting the compiler and the gates enumerate the fallout, grep its NAME for the comments and YAML no compiler reads, land the replacement in the SAME change, and keep the reasoning it carried as a comment on whatever survives.**
116. **A test harness that publishes a base URL but not a client has hidden the transport at every call site: hand out the client, so the trust decision is made once and cannot be skipped five times.**
117. **A fail-closed default protects against the mistake a person is making, never against the step they have not reached yet: refuse a stated intention that cannot be honored, provision a safe answer for an absent one, and keep announcing whatever the safe answer gave up.**
118. **Production-quality logic sitting in a test-support package is invisible until a shipped binary imports it and drags `testing` in with it: move the logic to an ordinary package, leave the test wrapper delegating to it rather than duplicating it, and add the dependency-graph test that forbids the import, having watched it fail first.**
119. **A retry loop's LAST action decides its failure mode: when the thing being retried around is other writers, losing a round is evidence the resource is about to exist, so serialize the write with an exclusive create, back off with jitter, end the loop on a READ rather than on the operation being retried, and return the material that was verified rather than a path something else will read again.**
120. **A gate that installs a product has to type the operator's literal command line rather than call an API that performs the same steps, and its skip/fail boundary is a POSITION in the test rather than a class of error: everything before the first step that touches an artifact this repository produces may be retried and then skipped, everything after it gets one attempt and fails hard.**
121. **A name that has to fit a length limit is built by truncating the PREFIX and appending the meaning, never the reverse: spend the limit on the part nobody reads, share one budget across components, and prove it at the last length that works and the first that does not.**
122. **Truthiness is the wrong question for any setting where 0 is a legal answer: ask whether the value was STATED, refuse the combinations that cannot render into a working object, and assert the COUNT of objects a configuration must produce, because every check that iterates over rendered objects passes when the object is missing.**
123. **An object that deliberately outlives its release is state the next install inherits blind: stamp it with a fingerprint of what initialized it, compare before rendering, refuse only a PROVEN mismatch, and say plainly which choice destroys data and which does not.**
124. **A lock is the wrong primitive for work that is cheap, idempotent and self-verifying: make the published RESULT atomic instead, let every writer race, and end on a read so the loser adopts the winner rather than failing.**
125. **Provenance belongs inside the atomic unit it describes; a set that grows needs one file per member named after the member, because any read-modify-write on a shared file loses a concurrent writer's entry.**
126. **A written waiver has to carry the condition it depends on, checked by the tooling on every run, or it outlives the reason it was granted: "acceptable BECAUSE X" expires when X does, and nobody re-reads a waiver they did not write.**
127. **A protection must name the specific thing whose loss cannot be undone, not the category it belongs to; and refusing to overwrite a file is a cheap promise while refusing to start is an expensive one, so they must never be made by the same rule.**
128. **A set that decides whether a live process is healthy must be governed by a fact about that process, never by the size or age of the set, and every process judged against such a set must put its own answer into it.**
129. **A run whose infrastructure is removed reports the assertion that was executing, never the removal, so a cleanup with no liveness check does not just break a run, it fabricates a defect in whatever that run was testing: key the guard on a live process, and give shared infrastructure an owner rather than a constant name.**
130. **A guard whose only effect is declining to assign the zero value is invisible at the field it guards, so the obvious test passes against both versions: its real contract is about order, so apply the real value first and the skipped one second, and assert the real one survived.**

---

Each entry above is the rule only. Full reasoning and the incident that produced it lives in [`LESSONS_LEARNED_ARCHIVE.md`](LESSONS_LEARNED_ARCHIVE.md), same entry numbers. Read an archive entry when its rule is directly relevant to what you're doing and you need the *why*; the rule sentence above is usually enough on its own.

Append a new entry to the archive first, in full, then add its one-line, same-numbered rule here. Never renumber an existing entry.
