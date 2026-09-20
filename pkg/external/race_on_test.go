//go:build race

// The race-enabled half of raceEnabled. See race_off_test.go.
package external_test

// raceEnabled reports whether this test binary was built with -race.
// program_test.go builds its child program with the same setting, so a
// race run checks the child's side of the boundary as well as this one.
const raceEnabled = true
