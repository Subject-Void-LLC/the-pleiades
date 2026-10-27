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
	// Routes say how each task that reaches its device more than one way
	// will reach it, decided from the record the way the run decides it.
	// They are the plan, not problems, and never fail validation.
	Routes []Route
	// Warnings are advice: a target that matches nothing yet
	// (UnmatchedTargetWarnings), and about the runbook as a file
	// (HeaderWarnings and the rest), which the CLI adds from its source.
	// They are printed beside the findings and never fail validation.
	Warnings []Finding
}

// Route is how one task will reach one device: the transport, and the
// detail behind it in runbook terms (for a container method, which engine).
type Route struct {
	Node string
	// Device is the device's inventory id; DeviceName is the name a person
	// reads, and the one the report prints when it is set.
	Device     inventory.DeviceID
	DeviceName string
	Transport  string
	Detail     string
}

// HasErrors reports whether any rule produced a Finding. A warning is not
// one.
func (r Report) HasErrors() bool {
	return len(r.Findings) > 0
}

// String renders the report for the CLI surface: the findings, or that
// there are none, then the warnings, then the routes.
func (r Report) String() string {
	var b strings.Builder
	if len(r.Findings) == 0 {
		b.WriteString("validate: no issues found\n")
	}
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "[%s] node %q: %s\n", f.RuleName, f.Node, f.Message)
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "[warning] %s: %s\n", w.RuleName, w.Message)
	}
	for _, rt := range r.Routes {
		device := rt.DeviceName
		if device == "" {
			device = string(rt.Device)
		}
		fmt.Fprintf(&b, "[route] node %q on %s: %s, %s\n", rt.Node, device, rt.Transport, rt.Detail)
	}
	return b.String()
}
