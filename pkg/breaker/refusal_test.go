// Tests for the third outcome, a refusal: the target answered and turned
// the call down, which is neither an outage nor proof the target serves.
package breaker_test

import (
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
)

// TestBreaker_ARefusalLeavesAClosedStreakAsItIs proves a refusal between
// failures neither clears the streak nor adds to it: the failures on
// either side of it still add up to the threshold.
//
// This is the AAA server that times out some logins and refuses others.
// When a refusal was recorded as a success it cleared the count each
// time, so the circuit never opened however many logins timed out.
func TestBreaker_ARefusalLeavesAClosedStreakAsItIs(t *testing.T) {
	b := breaker.New(3, time.Hour)
	b.RecordFailure(key)
	b.RecordFailure(key)
	for i := 0; i < 10; i++ {
		b.RecordRefusal(key)
	}
	if !b.Permitted(key) {
		t.Fatal("ten refusals after two failures opened the circuit: a refusal must not add to the streak")
	}
	b.RecordFailure(key)
	if b.Permitted(key) {
		t.Fatal("the third failure did not open the circuit: the refusals between the failures cleared the streak")
	}
}

// TestBreaker_RefusalsNeverOpenACircuit proves a target that only ever
// refuses is never refused for it, at any threshold.
func TestBreaker_RefusalsNeverOpenACircuit(t *testing.T) {
	b := breaker.New(1, time.Hour)
	for i := 0; i < 50; i++ {
		b.RecordRefusal(key)
	}
	if !b.Allow(key) {
		t.Fatal("refusals opened the circuit: a refusal is an answer, not an outage")
	}
}

// TestBreaker_ARefusalLeavesAnOpenCircuitOpen proves a refusal recorded
// while the circuit is open, which no call this breaker allowed could
// produce, changes nothing: the cooldown still runs.
func TestBreaker_ARefusalLeavesAnOpenCircuitOpen(t *testing.T) {
	b := breaker.New(1, time.Hour)
	b.RecordFailure(key)
	b.RecordRefusal(key)
	if b.Permitted(key) || b.Allow(key) {
		t.Fatal("a refusal closed an open circuit, or ended its cooldown")
	}
}

// TestBreaker_ARefusedProbeIsHandedBack proves what a half-open probe
// answered with a refusal does: the circuit does not close, since nothing
// proved the target serves, and the probe goes to the next caller at once,
// since the target did answer. Whatever that next probe proves decides.
func TestBreaker_ARefusedProbeIsHandedBack(t *testing.T) {
	const cooldown = 20 * time.Millisecond
	b := breaker.New(2, cooldown)
	b.RecordFailure(key)
	b.RecordFailure(key)
	time.Sleep(cooldown + 10*time.Millisecond)

	if !b.Allow(key) {
		t.Fatal("expected the probe once the cooldown passed")
	}
	b.RecordRefusal(key)

	// Handed back: the next caller is the probe now, with no new cooldown.
	if !b.Permitted(key) || !b.Allow(key) {
		t.Fatal("the probe was not handed back: a refusal is an answer, so the next caller should not wait a cooldown")
	}
	// Still one probe at a time: the circuit did not close.
	if b.Allow(key) {
		t.Fatal("a second caller was let through beside the probe: the refusal closed the circuit")
	}
	// And that probe's own outcome decides: a failure opens it again.
	b.RecordFailure(key)
	if b.Permitted(key) {
		t.Fatal("the probe after the refusal failed and the circuit did not open again")
	}
}

// TestBreaker_ARefusedProbeThenASuccessCloses proves the probe handed back
// by a refusal is a real one: the next caller's success closes the circuit,
// so a wrong credential costs a right one nothing but its turn.
func TestBreaker_ARefusedProbeThenASuccessCloses(t *testing.T) {
	const cooldown = 20 * time.Millisecond
	b := breaker.New(1, cooldown)
	b.RecordFailure(key)
	time.Sleep(cooldown + 10*time.Millisecond)
	if !b.Allow(key) {
		t.Fatal("expected the probe once the cooldown passed")
	}
	b.RecordRefusal(key)
	if !b.Allow(key) {
		t.Fatal("expected the probe to be handed back")
	}
	b.RecordSuccess(key)
	if !b.Allow(key) || !b.Allow(key) {
		t.Fatal("a success after a refused probe did not close the circuit")
	}
}

// TestBreaker_ARefusalWithNoHistoryIsANoOp proves a refusal for a target
// never seen leaves nothing behind that changes how its failures count.
func TestBreaker_ARefusalWithNoHistoryIsANoOp(t *testing.T) {
	b := breaker.New(2, time.Hour)
	b.RecordRefusal(key)
	b.RecordFailure(key)
	if !b.Permitted(key) {
		t.Fatal("one failure opened a threshold-2 circuit after a refusal")
	}
	b.RecordFailure(key)
	if b.Permitted(key) {
		t.Fatal("two failures did not open a threshold-2 circuit after a refusal")
	}
}
