---
status: beta
---

# Collection coverage

How much of each endpoint's own contract (its OpenAPI document or CLI reference) a collection implements, and how much of that is proven against the real thing. **Mapped** is the share of the contract's operations in scope that some method implements; **live** is the share whose every method has a test against a real server, device or tool. An operation is out of scope only with a written reason, shown on the collection's page. A proof is declared, and checked only to name a test that exists and reaches the method: it does not say the test ran, or that it covers every option of the operation.

Each collection's numbers may not drop below its floor (`coverage-floor-collections.json`).

| Collection | Endpoint | Version | Operations in scope | Mapped | Live | Floor (mapped / live) |
| --- | --- | --- | --- | --- | --- | --- |
| [pkg.apt](pkg.apt.md) | APT (apt-get) | `2.6.1` | 14 | 14.3% | 14.3% | 14.3% / 14.3% |
| [virt.clab](virt.clab.md) | containerlab API server | `v0.9.0` | 86 | 51.2% | 39.5% | 51.2% / 39.5% |

## Not measured yet

These methods belong to no collection with a ledger, so their coverage of their endpoint is not measured. Not measured is not zero: it is unknown.

| Namespace | Methods |
| --- | --- |
| `ai` | 11 |
| `archive` | 2 |
| `build` | 3 |
| `cloud` | 4 |
| `container` | 37 |
| `db` | 6 |
| `edge` | 3 |
| `exec` | 3 |
| `facts` | 1 |
| `file` | 14 |
| `fs` | 2 |
| `fw` | 3 |
| `gate` | 2 |
| `http` | 3 |
| `identity` | 6 |
| `net` | 30 |
| `pkg` | 6 |
| `pleiades` | 3 |
| `scan` | 3 |
| `svc` | 16 |
| `virt` | 38 |
| `wait` | 4 |
| `win` | 12 |
