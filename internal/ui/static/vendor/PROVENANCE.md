# Vendored third-party assets

Every file in this directory is committed rather than fetched, because the
controller binary must serve its own UI with no network reachable at all.
An air-gapped install has no CDN, and a CDN reference in an air-gapped
deployment is not a slow page — it is a page that never renders.

Committing them also makes the supply chain reviewable. These bytes went
through the same pull request as the code that serves them, which is not
true of anything resolved at build time.

`checksums_test.go` asserts the embedded bytes still match the digests
below, so a silent substitution fails CI rather than shipping.

## Compliance

The controller binary **redistributes** both files. That is a distribution
event, not a development-time dependency, so the licence and notice files
here ship with the binary and must not be removed. Apache-2.0 in
particular obliges a redistributor to carry the NOTICE file forward.

| | Apache ECharts | htmx |
| --- | --- | --- |
| Licence | Apache-2.0 (`LICENSE-echarts.txt`, `NOTICE-echarts.txt`) | 0BSD (`LICENSE-htmx.txt`) |
| Attribution required | Yes — NOTICE must accompany distribution | No, but retained anyway |

## echarts.min.js

- **npm package:** `echarts@5.6.0`
- **Retrieved from:** `https://unpkg.com/echarts@5.6.0/dist/echarts.min.js`
- **Retrieved on:** 2026-08-10
- **SHA-256:** `bf4a223524e40b77c304bec67e1222cf551f14880cf42c69dc046558e11c07b1`
- **Size:** 1,034,102 bytes

**Version discrepancy, recorded rather than resolved.** The npm package is
published as 5.6.0, but the bundle's own internal constant reads
`version:"5.6.1"`. That is an upstream packaging inconsistency, not a
substituted file: fetching the `@5` dist-tag and fetching `@5.6.0`
explicitly produce byte-identical output with the digest above. The npm
coordinate is the authoritative one for reproducing this file; the
internal string is noted so nobody later reads it as evidence of tampering.

ECharts 6.x exists and was not taken. This is a deliberate hold rather than
neglect: 5.6 is the widely deployed line, and the size figure this phase
measured and recorded in its changelog is 5.6's. Moving to 6 is a decision
with its own size and API consequences, and it should be made on its own
merits rather than absorbed silently into a UI phase.

## htmx.min.js

- **npm package:** `htmx.org@2.0.10`
- **Retrieved from:** `https://unpkg.com/htmx.org@2.0.10/dist/htmx.min.js`
- **Retrieved on:** 2026-08-10
- **SHA-256:** `71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de`
- **Size:** 51,238 bytes

## Updating one of these

1. Fetch the new file with an **explicit version pin**, never a dist-tag:
   a tag resolves differently over time and makes this document a record of
   nothing.
2. Verify the digest against the upstream-published one.
3. Update the version, URL, date, digest and size above.
4. Update the constant in `checksums.go`.
5. Re-fetch `LICENSE-*`/`NOTICE-*` if the upstream licence changed.
6. Run `go test ./internal/ui/static/` and confirm the checksum test passes
   against the new value rather than having been loosened to accept it.
