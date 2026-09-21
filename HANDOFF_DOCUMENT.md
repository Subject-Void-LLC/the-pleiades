# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-78d-Certificate-Path`, cut from `feature/Phase-46-Simulation-Modes` at
`2bc2b5c`. Phase 78d is BUILT and the work is UNCOMMITTED. `make ci` has NOT been run end to end.**
The user asked for Phase 78 next; 78a, 78b and 78c were already in this history, so the whole of the
work was 78d, the one stage never built.

### What 78d is, in one paragraph

Section 17.4 wants a PFX bundle unlocked just in time to present a client certificate. 78b found that
blocked and recorded why: nothing in this platform could present a client certificate to anything, so
a decoder would have had no caller. Re-verified this session, still true before the change: the only
`Certificates` assignment in non-test code was the Controller's own listener. So the certificate path
was built FIRST taking PEM, and PKCS#12 landed LAST as an input adapter into a path already tested.

### What was built, in build order

- **One secret-key vocabulary.** `pkg/wire`, `internal/credential` and `internal/credtype` each
  declared the same literals; the latter two now ALIAS `pkg/wire`. Two keys added:
  `certificate_pem` and `pfx_base64`. `machine_test.go` became tautological and now pins the literal
  values instead, which is the property that still matters (this is a wire format queued JetStream
  messages already agree on).
- **`pkg/winrmexec` client-certificate authentication** in a sibling file, `certauth.go`, because the
  main file was already 508 lines. `Auth` gained the pair, the guard became "exactly one complete
  credential" with a separate refusal per shape, and a third `TransportDecorator` branch returns
  this package's OWN `certificateTransport` (`pkg/winrmexec/certtransport.go`, ~250 lines).
  It started as `masterzen/winrm`'s `ClientAuthRequest`, which was already in the module and never
  referenced, and had to be replaced: that transport builds its `tls.Config` on an unexported field,
  so there was no seam to cap the TLS version through, and the cap is what makes this work at all
  (see finding 4). The replacement also carries the error reporting, the Insecure refusal and the
  version cap, none of which the library's version has, so it is the thing to review rather than a
  thin wrapper.
  `AuthFromSecrets` is new and is now the single place the key vocabulary is read. Its three callers
  are the three catalog packages; `pkg/winrmsvc` and `pkg/winrmdism` were NOT changed and do not
  call it, they carry an `Auth` their caller fills.
- **`machineTarget` accepts `KindCryptography`**, so a certificate becomes the machine identity.
- **An input can be filled from a linked credential's field** (`source_field` metadata), which is
  what 78b wrongly recorded as already built.
- **`pkg/pfx`**, the PKCS#12 decoder, unlocked in the Runner's per-task child.
- **`add-credential --certificate/--pfx`**, without which the Crawl tier could store no certificate
  and the new `Credential` fields would have been unfillable.

### Findings: report each to the user as its own item

1. **Four of the roadmap's own 78d claims were stale** (LESSONS 210), and they did not all point the
   same way. Three made the work BIGGER: the three catalog packages did not inherit the capability
   for free; the secret key landed in three places, not two, and the third had no drift test; and
   `internal/catalog/http` is a worse fallback than the thing it was offered as an alternative to.
   One made it SMALLER: the AWX parity test walks shipped types only, so the certificate type needs
   no exemption and the reserved-prefix question stays deferred. Since they point both ways, "check
   the claims that would cost me" would not have been the right filter.
2. **A latent correctness bug, fixed** (FAILURE_PATTERNS 273). `Unflatten` used `[]byte("")`, which
   is non-nil, so it was never the inverse of `Flatten` that its own test claimed, and the test had
   written the workaround into its expectations. Fixed at the cause.
3. **Two things the plan did not predict, both load bearing.** Certificate authentication is HTTPS
   only (the transport sends no Basic header), so HTTPS is selected from the credential rather than
   by a caller flag. And a Windows device's port defaults to 5985, the cleartext listener,
   indistinguishably from a deliberate choice, so the ordinary path to certificate authentication
   hits the wrong port; that is refused by name rather than attempted, because the TLS error it would
   otherwise produce reads like a broken certificate.
4. **The AWX parity blocker 78b predicted does not exist.** The parity test walks shipped types only,
   so the first mTLS type can be user-defined. No exemption, no reserved prefix, no new kind.
5. **`golang.org/x/crypto/pkcs12` was already in the module and cannot serve**, and the REASON I
   first wrote down was wrong. It exports `Decode` and `ToPEM` only, so it returns one certificate
   and cannot return a chain, and it refuses a safe holding more than two items. `pkg/pfx` emits the
   leaf plus intermediates, which that API cannot express. My original justification said Windows
   defaults `Export-PfxCertificate` to AES-256, which the older library cannot read. Testing a real
   Windows 11 export disproved it: the default is 3DES, which it CAN read. Corrected in the source,
   the roadmap and the commit message rather than quietly dropped.

### Verified, and how

- **Both Release Gate halves written; one passed, one is OPEN.**
  `TestReleaseGate_TheCertificateIsPresentedAndVerified` passes and runs anywhere: the real
  `certificateTransport` against a real TLS server with `RequireAndVerifyClientCert`, three acts
  including two negative controls, plus `TestReleaseGate_ABundlePresentsTheSameCertificateAsThePEMPath`.
  `TestWinRMGate_ACertificateAuthenticatesAndAStrangerDoesNot` needs a real Windows host with a
  cert-mapped account and SKIPS here, so its checkbox stays open per checkbox rule 1. That is the
  same call 78b made, and 78b's gate then passed first try once it could run.
- **Four mutations, each turned a named test red**, then reverted and re-confirmed green: the
  certificate transport branch dropped; HTTPS no longer forced; the blanket `KindExternal` refusal
  restored; the decoder ignoring its passphrase.
- **Fuzz and benchmark**, per checkbox rule 2: `FuzzDecode` 15,273,485 executions, 77 new interesting
  inputs, 60s, clean. `BenchmarkDecode` 533,106 ns/op, 101,105 B/op, 2,374 allocs/op.
- Green: `go build ./...`, `go vet ./...`, `gofmt`, `go test ./internal/... ./pkg/...` in full,
  `-race` on every touched package, `internal/archtest`, `tools/docs-lint`, `make gosec`.
- Coverage floors RAISED: `internal/credential` 91.6 to 92.0 (measured 92.2),
  `internal/credstore/resolve` 96.0 to 97.0 (measured 97.1). New: `pkg/pfx` 91.0 (measured 91.7),
  `pkg/winrmexec` 85.0 (measured 85.2, and it had NO floor at all before, so it was unratcheted).

### What has NOT been run, and the one expected failure

- **`make ci` has not run end to end.** It should, before this is called verified.
- **`tools/coverage-check` has not run**, because it runs the full suite internally.
- **`make docs-gen-check` passes.** It does `git diff --exit-code -- docs/reference`, which compares
  the working tree against the INDEX rather than against HEAD, so staging is what satisfies it and
  the work is staged. An earlier note here said it fails until committed, which was wrong about
  which git comparison the target makes.

### The reviews found what review is for, and the second one is the more interesting

Two adversarial multi-agent reviews ran over this work. The first, before any gate was ticked, raised
27 findings of which 17 survived verification, including three CRITICAL ones sharing a root cause:
`add-credential --certificate/--pfx` reported success and stored nothing.

The second ran over the FINISHED tree and hunted one thing specifically: claims corrected in one
place and not another. It found four, three of them exactly that shape. A Release Gate header and
three handoff sentences still named `masterzen/winrm`'s `ClientAuthRequest` as the shipped transport,
a week after it was replaced; the changelog still said the server-side TLS 1.3 workaround "sidesteps
the problem entirely" while two other documents correctly noted the cap makes it not yet help; and
the lab setup script destroyed any pre-existing HTTPS listener while its teardown carefully preserved
foreign ones and its own docstring claimed it touched nothing else. All four are fixed.

**Read the second review's coverage honestly: 78 of its 109 agents died on session limits.** Two of
its four lenses report zero survivors, and that is NOT evidence they were clean, because a finding
whose verifiers all failed is indistinguishable from a refuted one in that workflow's own logic. The
`code-vs-prose` and `gaps-honesty` lenses raised 8 and 6 findings respectively and none were verified
either way. Re-running those two is worth doing before anyone treats this tree as audited.

### Two residuals, both recorded in the roadmap rather than left implicit

1. **`winrmexec.Options` is unreachable.** `HTTPS`, `Insecure` and `CACert` have never been settable
   from a runbook or a device, and 78d added a TLS version cap in the same place. The Release Gate hit
   both consequences: it needed `SSL_CERT_FILE` to trust the lab authority, and a target configured
   for upfront certificate negotiation still could not be reached over TLS 1.3. The fix is device
   properties, the way `port` already works. Its own piece of work, deliberately not smuggled in here.
2. **A `crypto/tls` fork is the only route to TLS 1.3 on this path.** Go issue #40521 is on Hold and a
   native fix is unlikely. The user wants a fork eventually; nothing in this phase depends on it.

### Next step

Run `make ci`, commit, and decide whether the Windows gate can be run against a lab host. Phase 78 is
complete except that one checkbox. The exposure Phase 78's own preamble names is unchanged and is
Phase 105's: a resolved secret still rides JetStream. This stage deliberately did not widen it, which
is why the PFX unlock happens in the Runner's child and the sealed bundle is what crosses the broker.
