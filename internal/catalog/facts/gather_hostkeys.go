// Package facts: the ssh.host_key.* facts facts.gather records for a device
// it reached over SSH, from what that session's known_hosts check accepted
// (pkg/sshfacts). They belong to the min subset, beside the device's other
// identity facts, and the filter narrows them as it narrows any other key.
package facts

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sshfacts"
)

// hostKeysWanted reports whether choice records any ssh.host_key fact: the
// min subset is chosen and the filter wants one of its keys.
func (choice gatherChoice) hostKeysWanted() bool {
	if !choice.chosen[gatherSubsetMin] {
		return false
	}
	for _, key := range sshfacts.FactKeys {
		if gatherMatches(choice.patterns, key) {
			return true
		}
	}
	return false
}

// hostKeyFacts returns the ssh.host_key facts the session way opened calls
// for, narrowed by the filter, and the warnings to carry for device: a session
// that skipped verification, a key of a type no fact names, or more than one
// key of one type. A device reached over WinRM has no host key and none of
// either. Nothing is returned when choice wants no such fact, so a task that
// filtered them out is not told why none was recorded.
func (choice gatherChoice) hostKeyFacts(way gatherWay, device string) ([]gatherFact, []string) {
	if way.hostKeys == nil || !choice.hostKeysWanted() {
		return nil, nil
	}
	found, warnings := sshfacts.Read(device, way.hostKeys())
	var out []gatherFact
	for _, f := range found {
		if gatherMatches(choice.patterns, f.Key) {
			out = append(out, gatherFact{key: f.Key, value: f.Value})
		}
	}
	return out, warnings
}
