# Design note: the `build` collection

Status: built (build.digests.check, build.digests.refresh and build.lock.verify, 2026-10-15). What a build needs that the container engine methods do not already give: proof
that every image a build starts from is pinned, a way to move those pins forward deliberately, and proof that a
lockfile covers what its manifest asks for from sources a project trusts.

## Methods

| Method | Does | Site, device |
|---|---|---|
| `build.digests.check` | Reads the named files on the device and lists every image reference that is not named by both tag and digest (`name:tag@sha256:...`), with file, line and why. With `resolve: true` it also asks the registry, through the device's engine, whether each pinned digest is still what its tag names, and lists drift. Writes nothing. | target, required |
| `build.digests.refresh` | Resolves each reference's tag to the digest it names now (a multi-platform image's index digest, never one platform's), through the device's engine, and rewrites the reference as `name:tag@sha256:...`. Reports every change as file, line, from and to. With `set` it makes exact rewrites (file, line, the reference there now, the one to write) instead of resolving any; with `undo` it writes back what an earlier run replaced. | target, required |
| `build.lock.verify` | Checks a lockfile against its manifest: every dependency the manifest declares is locked, every locked entry carries an integrity hash, and every entry comes from a source on the task's allowed list. `package-lock.json` (with `package.json`) and `Cargo.lock` (with `Cargo.toml`) first; a format is a registered plug-in. | target, required |

`build.run` is not built. Building an image belongs to the container collections' planned `container.image.build`
(119f), and a second method that builds would be the near duplicate this catalog avoids; until then
`exec.command` running the engine's own build is the honest way to build.

## Which files and which references

`build.digests.*` take `files`, a list of paths on the device, each read whole (at most 1 MiB) and recognized by
its name and content:

- **Dockerfile / Containerfile**: each `FROM` (with `--platform` flags read past), and `COPY --from=` and
  `RUN --mount=from=` naming an image rather than a stage. `FROM scratch` and a stage named earlier in the file
  are not images. A reference built from an `ARG` is read from the ARG's default in the same file; one with no
  default is reported as unresolvable rather than guessed. ARGs are scoped as a build scopes them: a `FROM` reads
  only the ARGs before the first `FROM`, and `COPY --from` and `RUN --mount` read the stage's own (a bare `ARG` in
  a stage imports the global value). A here-document opens only where BuildKit opens one, at a word starting with
  `<<` in `RUN`, `COPY` or `ADD`. One ARG several lines use is one reference, at the ARG.
- **Compose**: each service's `image:`, through YAML merge keys (`<<: *base`) and aliases as Compose reads them;
  an image an anchor holds is one reference, at the anchor.
- **Kubernetes manifests**: every `image:` under a pod template's `containers`, `initContainers` and
  `ephemeralContainers`, in every document of the file, through aliases.

A YAML file that gives a key twice in one mapping is refused, since readers keep different values (Compose refuses
it too).

A file of none of these kinds is refused by name rather than skipped, so a typo in `files` is not a silent pass.

## Capabilities

| Method | Capabilities | Why |
|---|---|---|
| `build.digests.check` | `CommandExecCapable`; with `resolve`, also `ContainerEngineCapable` | reads files over SSH; asks the registry through the engine |
| `build.digests.refresh` | `CommandExecCapable`; when it resolves (no `set`, no `undo`), also `ContainerEngineCapable` | writing; asks the registry through the engine. The engine is not in the manifest's requirements, so a `set` or an `undo` (a rollback) still runs on a device whose engine is gone, and a resolve there is refused when it opens the engine. |
| `build.lock.verify` | `CommandExecCapable` | reads the lockfile and manifest |

Resolving goes through the device's engine (the engine API's distribution route, a new operation on the engine
port), so a digest is resolved through the device's own network, proxy, mirrors and registry logins: the same
view the build will have. A device whose engine has no such route (to be measured for Podman) is refused by name.

## Check and Reversibility

| Method | Check | Reversible |
|---|---|---|
| `build.digests.check` | Same as Invoke: it only reads (and, with `resolve`, asks the registry). | Read-only. |
| `build.digests.refresh` | Resolves every reference exactly as a run does and reports each change it would make, writing nothing. | Yes: before writing, a run keeps the references it replaces in an undo record on the device (the login's state directory, `${XDG_STATE_HOME:-~/.local/state}/pleiades/build-digests`, 0700, one 0600 file per run), and its undo is `build.digests.refresh` with `undo` naming that record. Replaying it writes each reference back only where the file still holds what the run wrote (a reference that had no digest gets none back), removes the record, and keeps one of its own. |
| `build.lock.verify` | Same as Invoke: it only reads. | Read-only. |

## Decisions, by edge case

- **The undo is a record on the device, named by an id.** A run's journal keeps only short scalar values (at most
  256 characters each), so it could never hold the references a refresh replaced; a list of them, or one reference
  with its digest, is past that. The record holds only image references (never a file's content, which in a
  Compose file can hold secrets), is private to the login, is written before any file changes (so a run that
  changed a file always has one), and is removed when replayed or after 90 days unused. Declaring the method
  irreversible instead would make every rollback of a run that refreshed pins refuse until the gap is accepted.
- **A file keeps its owner, group and mode.** The new content is renamed over the file when a new file can be
  given the old one's owner and group by the login, and copied into it in place otherwise (a project file in
  root's group, edited by the account that deploys it), which keeps everything the file was; the run names each
  in-place write in `warnings`.
- **Both tag and digest, not digest alone.** A digest alone is pinned but unreadable, and refresh cannot move a
  pin forward without the tag that says what it means; a tag alone is readable and moves underneath a build.
- **An index digest for a multi-platform image.** Writing one platform's digest would pin every other platform
  to an image that is not theirs; the index digest pins all of them, and an engine resolves it per platform.
- **Resolve everything before writing anything.** A registry that refuses one reference (gone, rate limited,
  unauthorized) fails the task with nothing written, rather than leaving files half refreshed.
- **A file changed since it was read is not overwritten.** Each file is written by replacing it whole, only if
  its content still hashes to what was read; otherwise the task fails naming it.
- **A lockfile entry with no integrity hash is an offender**, a `file:` tarball's included, even when the task
  allows its source: allowing a source says where an entry may come from, not that it need not be checked. Two
  kinds of entry are not sources at all and are not checked as one: an npm workspace `link`, which is code in the
  project itself, and an entry bundled inside its parent, which carries the parent's integrity. A git dependency
  pinned to a full commit needs no integrity hash, since npm records none and the commit is the pin. Keys are read
  exactly as npm and Cargo read them, so a key differing only in case (`Resolved`, `SOURCE`) is not the key they
  use. An unsupported lockfile version (npm's version 1) is refused rather than half read.

## One new dependency, and why

`Cargo.toml` is TOML that people write: inline tables, dotted keys, target-specific dependency tables, multi-line
strings. Reading it with a parser written here would be a second TOML implementation of the kind that gets the
edge cases wrong, which is the failure this method exists to prevent; running `cargo` on the device would make
the check depend on a toolchain the device may not have. So `build.lock.verify` adds one dependency,
`github.com/pelletier/go-toml/v2` (MIT, compatible with this project's GPLv3; no dependencies of its own),
checked by `govulncheck` and `gosec` like the rest, and used for both `Cargo.toml` and `Cargo.lock`. The npm
format needs none (`encoding/json`).

## Failure modes

| Failure | Answer |
|---|---|
| A file missing, over 1 MiB, not UTF-8, or of no known kind | Refused naming it, before any registry is asked. |
| A reference built from an ARG with no default | Offender (check), refused (refresh). |
| The registry refuses, is unreachable or rate limits | Refresh fails with nothing written, naming the reference and the registry's answer. |
| A tag no longer exists | Drift (check with resolve); refresh fails naming it. |
| A file whose owner or group the login cannot give a new file (a project file in root's group, edited by the deploying account) | The new content is copied into the file in place, from a complete copy already on the device, so it keeps its owner, group, mode and links; the run names it in `warnings`, since a reader at that instant could see it part written. A file the login cannot write at all is refused before anything is truncated. |
| A file edited between read and write | Refresh fails naming it, and puts back every file it already rewrote (it holds their originals), since a failed task's undo is not journaled. A file it could not put back is named, and the run's undo record is then kept and named in the error with the replay that writes the files back; replaying it skips a file the run never wrote. |
| The undo record would be over 1 MiB, the most a replay reads | Refused before any file is written. |
| A path given twice in `files` | Refused by name. |
| A Cargo.toml or Cargo.lock nested more than 100 deep | Refused before it is parsed, since the TOML parser recurses with no limit and a deep enough file would end the process. |
| Device text in an error or a return (a reference, a package) | Cut to 512 bytes and escaped. |
| A manifest dependency not in the lockfile, an entry with no integrity, a source not allowed | One offender each, with the package, version and source; any offender fails the task. |
| A lockfile version or format no plug-in reads | Refused naming it. |
