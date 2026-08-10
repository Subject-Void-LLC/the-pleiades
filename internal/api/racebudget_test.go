//go:build !race

package api_test

// raceTimeScale multiplies the wall-clock budgets this package's polling
// helpers allow. Without the race detector, a budget written at a call
// site is already the right one, so this is 1. See the race-enabled
// sibling of this file for why the multiplier exists at all.
const raceTimeScale = 1
