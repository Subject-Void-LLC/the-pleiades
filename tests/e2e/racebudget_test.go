//go:build integration && !race

// This file and its sibling racebudget_race_test.go hold one constant,
// split by build tag, mirroring internal/api's own pair.
package e2e

// raceTimeScale multiplies every wall-clock budget in this package.
//
// This is the plain (non-race) build, where a budget written against
// observed timings is already correct, so the factor is 1. See
// racebudget_race_test.go for why the race build needs more.
const raceTimeScale = 1
