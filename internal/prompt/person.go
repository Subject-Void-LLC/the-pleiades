// Deciding whether a person is at the keyboard: a terminal on standard
// input, and no agent harness driving the command.
package prompt

import (
	"fmt"
	"os"
)

// agentEnv are environment variables agent harnesses set in the commands
// they run. The terminal is the real gate (an agent's tool calls run with
// no terminal on standard input); these are a second check for a harness
// that gives its commands one. PLEIADES_AGENT is for any harness not named
// here: set it, and every command that needs a person refuses.
var agentEnv = []string{
	"PLEIADES_AGENT",
	"CLAUDECODE",
	"CLAUDE_CODE_ENTRYPOINT",
	"CODEX_SANDBOX",
	"CODEX_SANDBOX_NETWORK_DISABLED",
	"GEMINI_CLI",
	"CURSOR_AGENT",
}

// AgentCaller returns the variable that says an agent harness runs this
// process, false when none does.
func AgentCaller() (string, bool) {
	for _, name := range agentEnv {
		if os.Getenv(name) != "" {
			return name, true
		}
	}
	return "", false
}

// OpenPerson returns the terminal of the person at the keyboard, refusing
// when an agent harness runs this process or standard input is not a
// terminal: what calls it (signing a prompt, deciding a held answer) is a
// person's decision, and an agent or a pipe deciding it would defeat it.
func OpenPerson(action string) (*Terminal, error) {
	if name, ok := AgentCaller(); ok {
		return nil, fmt.Errorf("%s is a person's decision, and this command runs under an agent (%s is set); run it yourself, in a terminal", action, name)
	}
	t, err := OpenTerminal()
	if err != nil {
		return nil, fmt.Errorf("%s is a person's decision, and %w; run it yourself, in a terminal", action, err)
	}
	return t, nil
}
