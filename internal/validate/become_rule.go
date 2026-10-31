// Package validate: the rule that refuses a become a run could not honor.
package validate

import (
	"fmt"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// BecomeRule refuses a task that asks to run as another account and could not.
//
// Become runs every command a method sends over its SSH connection through
// sudo, so it means something only for a Collection method that reaches its
// device over SSH. A built-in action, a method reaching its device another
// way (an API, a container engine, a terminal), and a method whose question is
// the login itself (net.ssh.ping, wait.connection: NoBecomeReason) would
// otherwise run as the login with the author believing otherwise, which is the
// failure a refused key exists to prevent.
func BecomeRule(world WorldView) []Finding {
	var findings []Finding
	for id, task := range world.DAG.Nodes {
		if task.FQCN == "" {
			continue
		}
		user, on := engine.BecomeFor(task)
		if !on {
			continue
		}
		name := task.Name
		if name == "" {
			name = id
		}
		refuse := func(why string) {
			findings = append(findings, Finding{
				RuleName: "become",
				Node:     id,
				Message:  fmt.Sprintf("task %q runs as %s (become), but %s", name, user, why),
			})
		}
		desc, ok := collection.Lookup(task.FQCN)
		switch {
		case !ok:
			refuse(task.FQCN + " is not a Collection method: become applies to a method that reaches its device over SSH, and a built-in action or a transport action does not take it")
		case desc.Provider != nil:
			refuse(task.FQCN + " is an external Collection's method, which runs in a process of its own that is not carried the account: it would run as the login while the journal said " + user)
		case desc.Manifest.NoBecomeReason != "":
			refuse(fmt.Sprintf("%s cannot run as another account: %s", task.FQCN, desc.Manifest.NoBecomeReason))
		case !slices.Contains(desc.Manifest.For(task.Params).SupportedTransports, capability.TransportSSH):
			refuse(fmt.Sprintf("%s does not reach its device over SSH, so there is no command to run as %s", task.FQCN, user))
		}
	}
	return findings
}

// init registers BecomeRule with Validate.
func init() {
	Register(BecomeRule)
}
