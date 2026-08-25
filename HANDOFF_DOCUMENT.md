# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Phase 101a is built and its Release Gate passed.** It is uncommitted on branch
`feature/Phase-101a-Dispatch-Subject-Namespace`, cut from `438484f` (78c's tip) rather than from
`main`, deliberately: `main` still lacks `b1a63ba`, without which `make ci` fails there for reasons
unrelated to any current work.

### What 101a is, and what it is not

Phase 101 (Mesh Identity) was split into three stages this session, following 78a/78b/78c. 101a is
the dispatch subject namespace and the consumer rule that follows from it. 101b is operator-mode
NKey/JWT with the account signing key in Phase 78a's store. 101c is time-boxed authority and
revocation. **101a is the only stage carrying no authentication at all**, which is exactly why it
could be tested against a real broker on its own.

`topology.DispatchSubject()` took no parameters and returned the flat literal
`pleiades.jobs.dispatch`. It now takes the device and returns
`pleiades.jobs.dispatch.<token>`. `DispatchSubjectAll()` declares the wildcard the fleet consumer
filters on.

### The finding that shapes 101b, verified against the real driver

**A subject permission does not restrict what a PULL consumer receives.** A pull consumer fetches
through `$JS.API.CONSUMER.MSG.NEXT.<stream>.<consumer>` (`nats.go@v1.52.0/jetstream/pull.go:225`)
and messages arrive on a reply inbox, so a Runner whose JWT names one device's subject can still
drain every device's dispatch from the shared `runner-agent` consumer.

The device token therefore buys two things, both real: it scopes what a Runner may PUBLISH, and it
makes a filtered consumer expressible at all, since `FilterSubject` is the only thing that scopes
delivery. Delivery-side scoping is a permission on WHICH CONSUMER a Runner may bind, and that is
101b's. This is written into `DispatchSubject`'s own doc comment, because 101b would otherwise be
designed against an assumption that is false.

### The consumer-group rule, resolved rather than discovered

Recorded in `DispatchConsumerConfig`'s doc comment. The fleet group's exactly-once guarantee is
unchanged: one durable, one filter, still matching every dispatch exactly once. The rule is that
**exactly one consumer may match any given dispatch subject**. NATS has no negative filter, so a
scoped consumer cannot be carved out of the fleet's `>`; introducing one is a change to the
PUBLISHER, routing those devices to a different prefix. Discovering that inside Phase 93 would have
been expensive.

### The hazard that turned out to be live

A device id is operator-supplied and `pkg/inventory` documents it as opaque, so an ordinary
`router1.example.com` would have expanded a three-token subject into a six-token one, matched no
filter this package declares, and silently stopped that device being dispatched to anybody.

`topology.SubjectToken` closes it, sharing one unexported `legalIdentifier` with `DurableName` so
the package holds one sanitize-and-hash implementation rather than two. `LogSubject` and
`ResultSubject` were concatenating job ids the same unhardened way and now go through it too, INSIDE
the builder, so no call site changed and the Runner that publishes a log subject and the Controller
that subscribes to it agree by construction. `DeadLetterSubject` and `EventSubject` are deliberately
excluded (both take an already-dotted value on purpose) and say so in their own comments.
`LESSONS_LEARNED.md` #169 is the general rule this produced.

### Verified

`TestReleaseGate_TheDeviceTokenScopesDeliveryWithoutCostingTheFleetGroup` runs against a real
`nats:2.14.4-alpine` broker in three acts and **passed in 15.2s**. Act three is the deliverable: the
scoped consumer receives nothing but its own device, and the gate gives that negative its own
positive control by proving the other dispatches reached the fleet consumer, so "received nothing
else" cannot be satisfied by "nothing else was published".

**The gate was falsified deliberately before being believed.** Widening the scoped filter to
`DispatchSubjectAll()` makes it fail with "per-device consumer received 8 dispatches, want exactly
1". Its device ids are dotted hostnames on purpose, so it runs on the id shape that used to break.

`FuzzDispatchSubject` takes two device ids, because the property that matters most needs a pair:
**523,318 executions, 95 corpus entries, no failures**. Benchmarks: 1.68 us / 450 B / 10 allocs per
subject, and 13.4 ms / 3.55 MB / 90,011 allocs for a 10,000-device fan-out, which is around one
percent of the 10,000 JetStream publishes it sits beside.

Passing: `internal/topology`, `internal/dispatch`, `internal/api`, `internal/event`,
`internal/runner`, `internal/archtest`, `internal/adapters/...`, `cmd/runner`. `go build`, `go vet`
and `gofmt` clean.

### Gates run after the commit

All green, and the numbers rather than the fact:

- **`make test-integration`: PASSED.** 151 packages, 0 failures, and `tests/e2e` green in 506s. That
  is the Grand Integration Test driving the real controller and runner binaries against real
  containers, so it is the RULE 0 proof the subject change works through the binaries rather than
  only through package tests. `internal/ent/migrate`'s parity check passed alongside it.
  Recorded because the first attempt at this claim was WRONG and the correction is the useful part:
  the run was piped through `tail`, so the exit code belonged to `tail` rather than to `make`, and
  the filter would have swallowed a `--- FAIL:` line. It was re-run capturing the real exit code.
  A piped exit code is not evidence.
- **`make docs-gen-check`: clean.** No diff and nothing untracked under `docs/reference` or
  `internal/api/wellknown`.
- **`make gosec`: clean.** 9 findings, all individually waived in `gosec-waivers.json`, none new.
- **`make coverage`: clean.** 201 packages, none below their recorded floor. `internal/topology`
  measures 96.0% against its floor of 95.8.
  The floor was deliberately NOT raised, unlike Phase 78's habit of raising every floor it improved.
  The gain is 0.2 points and 95.9 would leave 0.1 of headroom on a package whose tests provision
  real Docker containers, where one container-timing miss moves the number by more than that. A
  floor that flakes teaches people the gate can be ignored.

### Next step

**`make ci` end to end.** What it still adds beyond the above: `test-race` (the untagged suite under
`-race`, which has NOT been run; only the tagged integration suite ran with it), `test-repeat`,
`govulncheck`, `helm-lint` and `templ-gen-check`.

### Loose ends

- **The whole branch is still not on `main`**, `b1a63ba` and the three Phase 78 commits included.
  101a is committed as `a06dff2` and pushed to its own remote branch.
- **`make ci` has not been run end to end** in this session or the previous one, only its
  constituent parts. Worth one run before merging. See Next step for exactly what is unproven.
- **Phase 78d (PFX/PKI) is planned and not built.** See `HANDOFF_ARCHIVE.md`'s top entry for the
  three findings that shrank it and the one correction that grew it.
- **101a authenticates nothing**, so Phase 96a's and 96d's "the bus is unauthenticated" statement is
  still true as written and was deliberately left alone. Correcting it is 101b's.
