// Package prompt: tests that a person's decision is refused to an agent and a pipe.
package prompt

import (
	"strings"
	"testing"
)

// TestOpenPerson_RefusesAnAgent: any agent variable refuses, naming it,
// before the terminal is even looked at.
func TestOpenPerson_RefusesAnAgent(t *testing.T) {
	for _, name := range agentEnv {
		for _, other := range agentEnv {
			t.Setenv(other, "")
		}
		t.Setenv(name, "1")
		if _, err := OpenPerson("approving"); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestOpenPerson_RefusesAPipe: with no agent, standard input that is not a
// terminal (a test's) refuses.
func TestOpenPerson_RefusesAPipe(t *testing.T) {
	for _, name := range agentEnv {
		t.Setenv(name, "")
	}
	if _, err := OpenPerson("approving"); err == nil || !strings.Contains(err.Error(), "not a terminal") {
		t.Errorf("%v", err)
	}
}
