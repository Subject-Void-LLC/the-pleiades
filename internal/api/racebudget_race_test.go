//go:build race

package api_test

// raceTimeScale multiplies the wall-clock budgets this package's polling
// helpers allow, because the race detector makes the work under those
// budgets roughly an order of magnitude slower and `make ci` runs
// `go test -race`, so the race build is the one CI actually judges.
//
// The value is measured, not guessed. TestDispatcher_ReleaseGate fans a
// job out to 10,000 devices and records each outcome individually; the
// package runs in about 5 seconds without the detector and about 50 with
// it, on an idle machine. Its budget was written as a flat 60 seconds,
// which left roughly 20% headroom in the best case the race build ever
// sees, and none at all once `go test ./...` is running a dozen other
// packages (several of them starting Docker containers) on the same
// host. It failed in CI exactly there, reporting a job still in
// "fanning_out" after 60 seconds, followed by a cascade of "sql:
// database is closed" errors that were the test's own cleanup running,
// not the cause.
//
// Scaling here rather than at each call site keeps the call sites
// expressing the thing a reader can reason about -- how long this work
// should take on a normal machine -- and means a new caller cannot
// forget to account for the detector.
const raceTimeScale = 10
