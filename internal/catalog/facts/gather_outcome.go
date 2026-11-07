// Package facts: whether a family's source was read (FAILURE_PATTERNS 925). A family (package., service.,
// port., user., group.)
// lists items, so an empty answer means "none" only when the source was
// there and its listing succeeded. Over SSH an exit status never reaches
// Gather (the run closure keeps stdout alone, by design for the single
// facts), so each family probe says itself what happened: Gather runs its
// listing only where its source is present, printing gatherAbsentMark
// otherwise, and each listing whose failure matters prints gatherFailedMark
// when it fails. A source whose output lacks the structure it always has
// (systemd's three sections, a JSON document) failed too, whatever it exited
// with.
package facts

import (
	"fmt"
	"strings"
)

// The lines a family probe prints about its own run. Neither can be an item:
// no package, service, port or account is named with two leading dashes and
// the word pleiades.
const (
	// gatherAbsentMark says the probe's source is not on the device.
	gatherAbsentMark = "--pleiades-absent--"
	// gatherFailedMark says a listing the probe depends on failed.
	gatherFailedMark = "--pleiades-failed--"
)

// orFailed is listing followed by the failed mark when it fails: what a
// family probe appends to each read whose failure means it has no answer.
func orFailed(listing string) string {
	return listing + " || echo '" + gatherFailedMark + "'"
}

// gatherOutcome is what one family probe's run found.
type gatherOutcome int

// The outcomes, in the order a family's are combined: one source read is
// enough for a family to be read, and one failed fails it.
const (
	outcomeRead gatherOutcome = iota
	outcomeAbsent
	outcomeFailed
)

// guarded is command run only where present succeeds on the device, and the
// absent mark otherwise. One line, since a login shell of csh refuses a
// newline inside quotes, and every probe runs under sh -c.
func guarded(present, command string) string {
	return "if " + present + "; then " + command + "; else echo '" + gatherAbsentMark + "'; fi"
}

// readOutcome splits a family probe's stdout into what happened and its
// answer with the marks taken out. check, when set, says whether the answer
// has the structure its source always prints.
func readOutcome(stdout string, check func(string) error) (gatherOutcome, string, error) {
	lines := strings.Split(stdout, "\n")
	kept := lines[:0]
	absent, failed := false, false
	for _, line := range lines {
		switch strings.TrimSpace(line) {
		case gatherAbsentMark:
			absent = true
			continue
		case gatherFailedMark:
			failed = true
			continue
		}
		kept = append(kept, line)
	}
	body := strings.Join(kept, "\n")
	switch {
	case failed:
		return outcomeFailed, "", fmt.Errorf("its listing failed")
	case absent:
		return outcomeAbsent, "", nil
	}
	if check != nil {
		if err := check(body); err != nil {
			return outcomeFailed, "", err
		}
	}
	return outcomeRead, body, nil
}

// familyState is what a run learned of one family across its probes.
type familyState struct {
	// subset is the gather_subset the family belongs to.
	subset string
	// read says some source answered.
	read bool
	// failures names each probe that failed, and why.
	failures []string
}

// familyVerdict is the error a family's state fails the task with, or the
// warning it is recorded with, or neither. A failed source fails the task
// whatever else answered, since its items would read as removed; a family
// no source on the device answers fails a task that named its subset and is
// a warning for one that reached it through all, so all keeps working on a
// device that has, say, no service manager this reads.
func familyVerdict(family string, st familyState, explicit bool) (warning string, err error) {
	switch {
	case len(st.failures) > 0:
		return "", fmt.Errorf("could not read %s facts, so none of them is recorded (a failed listing would read as every item removed): %s",
			strings.TrimSuffix(family, "."), strings.Join(st.failures, "; "))
	case st.read:
		return "", nil
	case explicit:
		return "", fmt.Errorf("%s %s: no source this method reads for %s facts is on the device; leave the subset out with !%s",
			gatherParamSubset, st.subset, strings.TrimSuffix(family, "."), st.subset)
	}
	return fmt.Sprintf("no source this method reads for %s facts is on the device, so none was gathered", strings.TrimSuffix(family, ".")), nil
}
