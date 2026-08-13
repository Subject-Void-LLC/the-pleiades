package dispatch

import "context"

// Sweep runs exactly one of the Reaper's scan-and-republish passes.
//
// Exported to this package's tests only. The behaviour worth pinning here
// is what one pass does with each answer its store can give -- nothing
// stale, a failed listing, a job it cannot republish -- and driving that
// through Run means driving it through a ticker: the test would have to
// pick an interval, pick a timeout, and hope the two produce the pass it
// wanted. They did not reliably. Which branches a timed run happened to
// reach varied between runs, which showed up as this package's measured
// coverage moving a point either side of its recorded floor depending on
// nothing but scheduling.
//
// Run's own loop still has its own test. What it owns is the leadership
// gate and the ticker; what a pass does is this.
func (r *Reaper) Sweep(ctx context.Context) { r.sweep(ctx) }
