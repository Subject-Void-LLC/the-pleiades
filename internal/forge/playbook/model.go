// Package playbook: the report model, which the text view and --json
// both render, so the two cannot disagree.
package playbook

import (
	"encoding/json"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// SchemaVersion is the version of the Report's JSON shape. It changes
// when a field is renamed or removed, never when one is added.
const SchemaVersion = 1

// Class is a converted task's state semantics, declared by the module
// table for the call it maps to, never inferred.
type Class int

const (
	// ClassUnclassified is a task that was not converted, so it has no
	// class. It renders as JSON null.
	ClassUnclassified Class = iota
	// ClassAsserted is a static desired state: the task names what the
	// device should look like (a package present, a service started).
	ClassAsserted
	// ClassComputed is a desired state decided only at run time: the
	// playbook chose the state from a variable nothing here could fix.
	ClassComputed
	// ClassImperative has no desired state: the task does something (a
	// command) whose effect cannot be known without doing it.
	ClassImperative
	// ClassObserve reads and changes nothing (facts, a GET, a wait).
	ClassObserve
)

// classNames are Class's text forms, in order.
var classNames = [...]string{"unclassified", "asserted", "computed", "imperative", "observe"}

// String names c.
func (c Class) String() string {
	if int(c) < len(classNames) {
		return classNames[c]
	}
	return fmt.Sprintf("Class(%d)", int(c))
}

// MarshalJSON renders c by name, and ClassUnclassified as null: a class
// that was never decided must not read as one that was.
func (c Class) MarshalJSON() ([]byte, error) {
	if c == ClassUnclassified {
		return []byte("null"), nil
	}
	return json.Marshal(c.String())
}

// UnmarshalJSON reads what MarshalJSON writes.
func (c *Class) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*c = ClassUnclassified
		return nil
	}
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	for i, n := range classNames {
		if n == name {
			*c = Class(i)
			return nil
		}
	}
	return fmt.Errorf("unknown class %q", name)
}

// Outcome is what happened to one construct.
type Outcome int

const (
	// OutcomeConverted means the runbook says what the playbook said.
	OutcomeConverted Outcome = iota
	// OutcomeInfo means dropped or resolved with no effect on a run.
	OutcomeInfo
	// OutcomeReview means converted or dropped with a bounded difference a
	// person should read.
	OutcomeReview
	// OutcomeBlocked means not converted: the runbook cannot run until a
	// person resolves it.
	OutcomeBlocked
)

// outcomeNames are Outcome's text forms, in order.
var outcomeNames = [...]string{"converted", "info", "review", "blocked"}

// String names o.
func (o Outcome) String() string {
	if int(o) < len(outcomeNames) {
		return outcomeNames[o]
	}
	return fmt.Sprintf("Outcome(%d)", int(o))
}

// MarshalText renders o by name.
func (o Outcome) MarshalText() ([]byte, error) { return []byte(o.String()), nil }

// UnmarshalText reads what MarshalText writes.
func (o *Outcome) UnmarshalText(text []byte) error {
	for i, n := range outcomeNames {
		if n == string(text) {
			*o = Outcome(i)
			return nil
		}
	}
	return fmt.Errorf("unknown outcome %q", text)
}

// Position is a place in a file: the playbook a finding came from, or the
// runbook it landed in.
type Position struct {
	// File is the file's name, relative to the playbook's directory for a
	// playbook position and to the output directory for a runbook one.
	File string `json:"file"`
	// Line is the 1-based line.
	Line int `json:"line"`
	// Column is the 1-based column.
	Column int `json:"column"`
}

// String renders p as file:line:column, the file name escaped: it comes
// from the playbook's own directory and may hold anything a terminal
// acts on.
func (p Position) String() string {
	return fmt.Sprintf("%s:%d:%d", termsafe.EscapeLine(p.File), p.Line, p.Column)
}

// Finding is one construct that did not convert cleanly, or converted in a
// way a person should know about. Every text field holds names and
// positions only, never a value from the playbook, so a report can be
// shared without leaking what the playbook held.
type Finding struct {
	// ID is this finding's name in this report ("F007"), which the
	// emitted runbook's comment cites.
	ID string `json:"id"`
	// Code is the finding's stable machine code, from the closed set in
	// codes.go, so an editor can attach one action per code.
	Code Code `json:"code"`
	// Outcome is what happened to the construct.
	Outcome Outcome `json:"outcome"`
	// At is where the construct is in the playbook.
	At Position `json:"at"`
	// Emitted is where the construct's task landed in the output, or nil
	// (null in JSON) when it produced no task.
	Emitted *Position `json:"emitted"`
	// Task is the name of the task the construct belongs to, escaped for a
	// terminal, or "" for a play-level construct.
	Task string `json:"task,omitempty"`
	// Message says what happened, in a sentence.
	Message string `json:"message"`
	// Native names the native construct to use instead, or what will add
	// one, when there is something to say.
	Native string `json:"native,omitempty"`
}

// TaskResult is one task the output holds: a converted task, or a
// placeholder for one that was not.
type TaskResult struct {
	// At is where the task is in the playbook.
	At Position `json:"at"`
	// Emitted is where the task landed in the output runbook.
	Emitted Position `json:"emitted"`
	// Name is the task's name in the runbook, escaped for a terminal.
	Name string `json:"name"`
	// Module is the Ansible module the playbook task named.
	Module string `json:"module"`
	// Runbook is the id of the runbook the task landed in.
	Runbook string `json:"runbook"`
	// NodeID is the task's node in the built runbook, as a run reports it.
	NodeID string `json:"node_id"`
	// FQCN is the native method the task calls, or the unrunnable
	// ansible.unconverted.<module> placeholder a blocked task becomes.
	FQCN string `json:"fqcn"`
	// ParamKeys names the parameters the task passes, never their values.
	ParamKeys []string `json:"param_keys"`
	// Params are the values, for a caller in this process (the check
	// cross-check), and are never serialized.
	Params map[string]any `json:"-"`
	// Class is the task's state semantics, declared by the module table
	// rather than inferred, or null for a blocked task.
	Class Class `json:"class"`
	// ClassBasis says in a phrase why the task has its class.
	ClassBasis string `json:"class_basis,omitempty"`
	// Outcome is converted, review or blocked.
	Outcome Outcome `json:"outcome"`
	// CanCheck says whether the task can run in check mode; nil (null in
	// JSON) for a blocked task, which was not asked.
	CanCheck *bool `json:"can_check"`
	// CheckReason says why a task cannot run in check mode.
	CheckReason string `json:"check_reason,omitempty"`
	// Findings are the IDs of the findings the task cites.
	Findings []string `json:"findings,omitempty"`
}

// RunbookResult is one runbook the conversion wrote.
type RunbookResult struct {
	// File is the runbook's file name: <id>.yaml, or <id>.incomplete.yaml
	// when a task in it is blocked.
	File string `json:"file"`
	// ID is the runbook's id.
	ID string `json:"id"`
	// Hosts is the runbook's hosts: one device name or tag, or "" when the
	// plays named none a runbook can hold.
	Hosts string `json:"hosts"`
	// Plays are the 1-based numbers of the plays the runbook holds.
	Plays []int `json:"plays"`
	// Runnable is false when the runbook holds a blocked task, and so
	// cannot run until a person resolves it.
	Runnable bool `json:"runnable"`
	// DAGVersion is the built runbook's version, which a run reports.
	DAGVersion string `json:"dag_version"`
}

// Resolution records a template resolved from the playbook itself: the
// variable, where its one definition is, and where it was used. Never the
// value.
type Resolution struct {
	// Variable is the variable's name.
	Variable string `json:"variable"`
	// DefinedAt is where its one definition is.
	DefinedAt Position `json:"defined_at"`
	// UsedAt are the places a template read it.
	UsedAt []Position `json:"used_at"`
}

// Counts summarizes the report.
type Counts struct {
	// Tasks counts every task in the output.
	Tasks int `json:"tasks"`
	// Converted counts the tasks that converted, reviews included.
	Converted int `json:"converted"`
	// Review counts the converted tasks a person should read.
	Review int `json:"review"`
	// Blocked counts the placeholders a person must replace.
	Blocked int `json:"blocked"`
	// Asserted counts converted tasks naming a static desired state.
	Asserted int `json:"asserted"`
	// Computed counts converted tasks whose desired state is decided at
	// run time.
	Computed int `json:"computed"`
	// Imperative counts converted tasks with no desired state.
	Imperative int `json:"imperative"`
	// Observe counts converted tasks that only read.
	Observe int `json:"observe"`
	// CannotCheck counts converted tasks that cannot run in check mode.
	CannotCheck int `json:"cannot_check"`
}

// Report is everything the conversion found, as one model for the text
// view and --json alike.
type Report struct {
	// SchemaVersion is this report's shape; it changes only when a field
	// changes meaning or is removed.
	SchemaVersion int `json:"schema_version"`
	// Playbook is the converted playbook's file name, escaped for a
	// terminal.
	Playbook string `json:"playbook"`
	// Runbooks are the runbooks the conversion wrote.
	Runbooks []RunbookResult `json:"runbooks"`
	// Tasks are the tasks the runbooks hold, in order.
	Tasks []TaskResult `json:"tasks"`
	// Findings are the constructs that did not convert cleanly, each once.
	Findings []Finding `json:"findings"`
	// Resolutions are the variables written into the runbooks from the
	// playbook's own definitions.
	Resolutions []Resolution `json:"resolutions"`
	// CannotCheck lists the node IDs of converted tasks that cannot take
	// part in check mode.
	CannotCheck []string `json:"cannot_check"`
	// Counts summarizes the report.
	Counts Counts `json:"counts"`
}

// NeedsHuman reports whether any finding asks a person to act: a review
// to read or a blocked construct to resolve.
func (r Report) NeedsHuman() bool {
	for _, f := range r.Findings {
		if f.Outcome >= OutcomeReview {
			return true
		}
	}
	return false
}

// Runbook is one converted runbook, ready to write.
type Runbook struct {
	// File is the file name to write it under: <stem>.yaml, or
	// <stem>.incomplete.yaml when anything in it is blocked.
	File string
	// ID is the runbook's id: key.
	ID string
	// YAML is the runbook itself.
	YAML []byte
	// Incomplete reports whether it holds a placeholder.
	Incomplete bool
}

// Result is a conversion's output: the runbooks and the report about them.
type Result struct {
	Runbooks []Runbook
	Report   Report
}
