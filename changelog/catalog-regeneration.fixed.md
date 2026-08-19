Regenerating the module catalog over an existing checkout works. `forge new-collection`,
`new-device` and `new-plugin` take `--skip-existing`, which leaves an already-generated
entry alone instead of refusing, so `go generate ./internal/forge/catalogdata` is now a
no-op on an unchanged catalog rather than a failure on the first file it meets.
