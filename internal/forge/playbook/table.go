// Package playbook: the types the module tables are written in. The
// tables themselves (modules_*.go) are data; apply.go applies them.
package playbook

// Rating says whether a module converts at all.
type Rating int

const (
	// RatingMapped converts through Selectors, Default and Args.
	RatingMapped Rating = iota
	// RatingManual never converts: a person writes the task. Code and
	// Reason say why.
	RatingManual
)

// FreeForm says how a string argument (`apt: name=curl`) is read.
type FreeForm int

const (
	// FreeFormKV reads every token as key=value, and refuses anything
	// else.
	FreeFormKV FreeForm = iota
	// FreeFormCommand keeps every token that is not one of RawKeys as the
	// command text, which becomes RawArg, as Ansible's command and shell
	// modules do.
	FreeFormCommand
)

// Entry maps one Ansible module onto native methods.
type Entry struct {
	// Module is the module's full name ("ansible.builtin.apt"); Aliases are
	// the other names it is written under ("apt", "ansible.legacy.apt").
	Module  string
	Aliases []string
	Rating  Rating
	// Code and Reason explain a RatingManual entry.
	Code   Code
	Reason string
	// FreeForm, RawKeys and RawArg say how a string argument is read.
	FreeForm FreeForm
	RawKeys  []string
	RawArg   string
	// Selectors pick the native method from the arguments that carry
	// Ansible's desired state (state, enabled); each one present emits one
	// call, in this order. Default is the call when none applies.
	Selectors []Selector
	Default   *Call
	// Args lists every argument the module accepts here. Any other
	// argument blocks the task.
	Args []Arg
	// Returns maps an Ansible result field onto the native method's stat,
	// for a condition that reads a registered result.
	Returns map[string]string
	// Adjust, when set, rewrites each call's mapped parameters for a module
	// whose defaults depend on each other (firewalld's permanent and
	// immediate), and returns a module.semantics note, or "". Each one
	// lives in adjust.go, never in a table file.
	Adjust func(params map[string]any) string
}

// Selector picks a call from one argument's value.
type Selector struct {
	// Arg is the Ansible argument.
	Arg string
	// Absent is the value to use when the argument is not given, as the
	// module's own default; "" means the selector does not apply then.
	Absent string
	// Bool says the argument is a boolean, read by Ansible's boolean
	// rules (yes, on, t, 1 ...) into the choice values true and false.
	Bool    bool
	Choices []Choice
}

// Choice maps some of an argument's values onto one call, or blocks them.
type Choice struct {
	Values []string
	// Call is the native call, or nil when these values block the task,
	// with Code and Reason saying why.
	Call   *Call
	Code   Code
	Reason string
	// Ignores lists the arguments Ansible itself ignores for these values
	// (src and fstype when a mount is absent). They are dropped with an
	// args.ignored finding rather than blocking the task.
	Ignores []string
}

// Call is one native method call a task becomes.
type Call struct {
	FQCN string
	// Class is the call's state semantics, declared here rather than
	// inferred, and Basis says why in a phrase.
	Class Class
	Basis string
	// Fixed are parameters this call passes unless the task gives them:
	// Ansible's default where the native default differs.
	Fixed map[string]any
	// Note, when set, is a module.semantics review this call raises: it
	// does what the module does except in the way the note says.
	Note string
}

// ArgHandling says what happens to one Ansible argument.
type ArgHandling int

const (
	// ArgMap passes the value to the native parameter To.
	ArgMap ArgHandling = iota
	// ArgSelector is consumed by a Selector.
	ArgSelector
	// ArgUnroll passes one item per call when the value is a list (a
	// package module's list of names), with an args.list_unrolled review.
	ArgUnroll
	// ArgDrop drops the argument, raising Code (an info or review code).
	ArgDrop
	// ArgBlock blocks the task, raising Code.
	ArgBlock
)

// Arg is one Ansible argument an Entry accepts.
type Arg struct {
	Name     string
	Aliases  []string
	To       string
	Handling ArgHandling
	Code     Code
	Reason   string
	// Note, when set on a mapped argument, is a module.semantics review
	// raised when the task gives it.
	Note string
}
