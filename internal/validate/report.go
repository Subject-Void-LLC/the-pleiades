package validate

import (
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// Finding is one validation problem. Message is written in runbook and
// YAML terms (device name, node name, capability name) rather than Go
// interface terms, per PLAN.md Section 5: an operator reading this never
// needs to read the Go source to understand or fix it.
type Finding struct {
	RuleName string
	Node     string
	Device   inventory.DeviceID
	Message  string
}

// Report is the result of running every registered Rule against one
// WorldView. It is the one value every surface (CLI today, the IDE
// plugin and backend plan-time check later) renders, per Section 25's
// shared validation package.
type Report struct {
	Findings []Finding
}

// HasErrors reports whether any rule produced a Finding.
func (r Report) HasErrors() bool {
	return len(r.Findings) > 0
}

// String renders the report for the CLI surface.
func (r Report) String() string {
	if len(r.Findings) == 0 {
		return "validate: no issues found\n"
	}

	var b strings.Builder
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "[%s] node %q: %s\n", f.RuleName, f.Node, f.Message)
	}
	return b.String()
}
