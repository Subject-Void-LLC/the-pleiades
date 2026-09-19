//go:build !race

// The race-disabled half of raceEnabled. The Go toolchain exposes no
// runtime answer to "was this binary built with -race", so the answer is a
// build-tagged constant, one file per side.
package external_test

// raceEnabled reports whether this test binary was built with -race.
const raceEnabled = false
