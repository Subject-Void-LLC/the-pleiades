// What a read-only method answers about something that is not there
// (Phase 131, R4, absence as data): either it declares an exists return,
// which is false for a thing that does not exist, or its manifest says in
// NoExistsReason why it answers none.
package collection

import "errors"

// ExistsReturn is the Returns entry a lookup declares to answer whether the
// thing it was asked about is there.
const ExistsReturn = "exists"

// Shared NoExistsReason texts, for the kinds of read-only method that answer
// no exists. Each says what the method answers instead.
const (
	// NoExistsLists is a method that lists.
	NoExistsLists = "it lists; when there is nothing, an empty list is the answer"
	// NoExistsDevice is a method that describes its target itself.
	NoExistsDevice = "it describes the device itself; a device not there is unreachable, not absent"
	// NoExistsGate is a method that asserts something and fails when it is
	// not so.
	NoExistsGate = "it is a gate: failing on what it asserts is not so is its purpose"
	// NoExistsWait is a method that waits for something.
	NoExistsWait = "it is a wait: failing at its timeout is its purpose"
	// NoExistsProbe is a method that sends a probe and reports its answer.
	NoExistsProbe = "it is a probe: its result is the answer, a lost reply included"
)

// DeclaresExists reports whether doc declares an exists return.
func (d Doc) DeclaresExists() bool {
	for _, r := range d.Returns {
		if r.Name == ExistsReturn {
			return true
		}
	}
	return false
}

// AbsenceAnswer is what a read-only method answers about something that is
// not there, as `pleiades doc` and the reference pages say it: exists false
// for a method that declares exists, else its NoExistsReason. It is empty
// for a method that is not read-only, which is asked nothing to answer.
func (m Manifest) AbsenceAnswer() string {
	switch {
	case !m.Reversibility.ReadOnly:
		return ""
	case m.Doc.DeclaresExists():
		return "a thing that is not there is exists false, not a failure, in a run and a check"
	case m.NoExistsReason != "":
		return "no exists return: " + m.NoExistsReason
	}
	return "no exists return, and the method says nothing of absence"
}

// WithNoExistsReason returns m with NoExistsReason set to reason, for a
// method whose manifest a shared helper builds.
func (m Manifest) WithNoExistsReason(reason string) Manifest {
	m.NoExistsReason = reason
	return m
}

// ValidateNoExistsReason refuses a NoExistsReason that contradicts the rest
// of the manifest: one on a method that is not read-only, which is asked
// nothing to answer about, and one beside an exists return, which answers
// what the reason says the method does not. Register and the loader both
// call it, so the two cannot drift.
func ValidateNoExistsReason(m Manifest) error {
	switch {
	case m.NoExistsReason == "":
		return nil
	case !m.Reversibility.ReadOnly:
		return errors.New("gives a reason it answers no exists (NoExistsReason) but is not read-only")
	case m.Doc.DeclaresExists():
		return errors.New("declares an exists return and also a reason it answers none (NoExistsReason)")
	}
	return nil
}
