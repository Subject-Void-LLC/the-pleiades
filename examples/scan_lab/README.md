# Release scans with the scan methods

The scan methods ask, on the machine that holds a release, what it ships: known vulnerabilities in its
dependencies, leaked credentials in its tree, and an SBOM of what it is made of.

## What is here

- `pleiades/inventory.yaml` names one Linux server, `release-box`, that holds the release and runs Docker or
  Podman. Onboard it so its engine is recorded: each scanner runs there in a fresh container, from an image this
  release of The Pleiades pins by digest (OSV-Scanner, Syft and Gitleaks), so nothing is installed on the server.
- `pleiades/runbooks/release.yaml` gates the image archive on fixable high and critical vulnerabilities, the tree
  on leaked credentials, and writes an SPDX 2.3 SBOM with its `SHA256SUMS` into a new directory.
- `scan-ignore.yaml` shows the ignore file both scans read: every entry has a reason and an end date.

## Running it

```sh
cd pleiades
pleiades validate runbooks/release.yaml
pleiades run runbooks/release.yaml --mode check   # every scan runs; the SBOM is generated and checked, not written
pleiades run runbooks/release.yaml
pleiades rollback <run-id>                         # removes the SBOM directory; the scans changed nothing
```

`scan.vuln` reaches osv.dev from the engine's default network. On a server with no internet, download
OSV-Scanner's offline databases elsewhere (`osv-scanner --download-offline-databases`), copy the directory to the
server, and pass it as `offline_db`; the scan then joins no network. The SBOM names the server's release by the
task's `name`, `version` and `supplier`; dependencies whose supplier the generator could not name are counted,
and fail the task only with `strict_suppliers`.

## What this example has been run against

`pleiades validate` accepts the runbook in the repository's own tests. It targets a machine of yours, so it has
not been run as written; `cmd/pleiades`'s scan release gate runs the same methods through the real binary against
a real SSH server and a real Docker engine, checking first that a check writes nothing.
