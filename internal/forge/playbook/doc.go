// Package playbook converts an Ansible playbook into native Pleiades
// runbooks, the mechanical half of a migration, and reports the rest as a
// worklist a person finishes, usually in an editor.
//
// It is an Anti-Corruption Layer. Ansible's vocabulary (a module named as
// a mapping key, desired state inside a state: argument, Jinja in every
// string) is read here and never travels past it: the output is an
// unchanged engine.WorkflowDef, checked by building it with the engine's
// own BuildFromYAML before anything is written.
//
// # What happens to each construct
//
// Every construct in the playbook ends one of four ways, and the report
// says which, once per construct, with the position it came from and the
// position it landed at in the output:
//
//   - Converted: the native runbook says what the playbook said.
//   - Info: dropped or resolved in a way that changes nothing a run does
//     (a debug task, a variable read from the play's own vars).
//   - Review: converted or dropped with a stated, bounded difference a
//     person should read (a notify whose handler will not run).
//   - Blocked: not converted. The task stays in the output as a
//     placeholder calling ansible.unconverted.<module>, a name nothing can
//     register, so neither tier will run the runbook until a person
//     resolves it; its parameters are not copied, since they may hold
//     secrets, and its comment points at the playbook line instead.
//
// # The drop rule
//
// A construct may be dropped from a task only when that makes the task do
// less or fail sooner: notify (the handler does not run), become (the task
// fails without the privilege), ignore_errors (the task fails instead of
// continuing). Anything whose absence would make the task do more, act
// somewhere else, or reveal something is blocked instead: delegate_to,
// run_once, failed_when, retries, no_log, a when it cannot express.
//
// # What it never does
//
// It never guesses a desired state nobody wrote (a command stays
// imperative), never resolves a variable that has more than one possible
// value, never reads a vault value, and never copies a value whose name or
// shape looks secret.
package playbook
