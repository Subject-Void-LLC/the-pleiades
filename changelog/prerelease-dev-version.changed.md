Development and dry-run builds now report the release line they come from, `0.4.0-dev+<commit>` and `0.4.0-dryrun`,
instead of `0.0.0-dev+<commit>` and `0.0.0-dryrun`; a development build is still never compared as a release, and a
release build reports its version as before.
