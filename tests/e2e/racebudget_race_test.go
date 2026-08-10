//go:build integration && race

// This file and its sibling racebudget_test.go hold one constant, split
// by build tag, mirroring internal/api's own pair.
package e2e

// raceTimeScale multiplies every wall-clock budget in this package.
//
// The race detector makes instrumented Go roughly an order of magnitude
// slower, and `make ci` judges the -race build, so a budget sized against
// a plain `go test` run has no margin left by the time CI runs it
// alongside a dozen other packages. FAILURE_PATTERNS.md #89 records that
// exact failure: internal/api's dispatcher Release Gate sized a
// 60-second fan-out budget against a plain run and went negative under
// real CI load.
//
// This package is more exposed to that than internal/api is, because its
// budgets cover two real subprocesses and two containers rather than
// in-process work, so the factor is kept at internal/api's own measured
// value rather than guessed downward.
const raceTimeScale = 10
