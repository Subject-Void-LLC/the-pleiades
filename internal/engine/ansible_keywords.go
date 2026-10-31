// Package engine: the Ansible keywords a native runbook does not accept,
// and the message an author sees when one is written anyway.
//
// A native task already refuses any key it does not know, but it used to
// refuse loop: next to fqcn: with a sentence about module-as-key syntax,
// which is true and useless: the author wrote an Ansible keyword, and the
// useful answer is that a runbook does not have it. These sets let the
// parser say that instead. They list only the keywords a native runbook
// lacks; the ones it shares (name, register, when, check_mode, timeout,
// block, rescue, always, hosts, tasks) are ordinary runbook keys.
package engine

import "strings"

// ansibleTaskKeywords are Ansible's task and block keywords that a native
// task does not accept, from Ansible's own "Playbook Keywords" reference.
// The with_* family is matched by prefix in isAnsibleTaskKeyword instead.
var ansibleTaskKeywords = map[string]bool{
	"action":             true,
	"any_errors_fatal":   true,
	"args":               true,
	"async":              true,
	"become_exe":         true,
	"become_flags":       true,
	"collections":        true,
	"connection":         true,
	"debugger":           true,
	"delegate_facts":     true,
	"delegate_to":        true,
	"diff":               true,
	"environment":        true,
	"ignore_unreachable": true,
	"local_action":       true,
	"module_defaults":    true,
	"no_log":             true,
	"poll":               true,
	"port":               true,
	"remote_user":        true,
	"run_once":           true,
	"throttle":           true,
}

// ansiblePlayKeywords are Ansible's play keywords that a native runbook's
// top level does not accept. The task keywords a play may also carry
// (become, vars, tags, environment and the rest) are in
// ansibleTaskKeywords, and isAnsiblePlayKeyword checks both.
var ansiblePlayKeywords = map[string]bool{
	"fact_path":           true,
	"force_handlers":      true,
	"gather_facts":        true,
	"gather_subset":       true,
	"gather_timeout":      true,
	"max_fail_percentage": true,
	"order":               true,
	"post_tasks":          true,
	"pre_tasks":           true,
	"roles":               true,
	"serial":              true,
	"strategy":            true,
	"vars_files":          true,
	"vars_prompt":         true,
}

// migrationGuide is the shipped document that says what each Ansible
// keyword becomes in a native runbook, cited by every refusal here.
const migrationGuide = "docs/03-migrating-from-ansible.md"

// migrateCommand is the command that converts a playbook into a runbook.
const migrateCommand = "pleiades forge migrate-playbook"

// UnconvertedPrefix begins the method name of the placeholder a task
// pleiades forge migrate-playbook could not convert becomes
// (ansible.unconverted.<module>), and IncompleteGuard is the task an
// incomplete conversion starts with. Both are in the reserved ansible
// namespace, so nothing can register either and no run can reach past
// them; the guard sits outside the prefix, so no module's placeholder can
// ever share its name. Validation names each of them.
const (
	UnconvertedPrefix = "ansible.unconverted."
	IncompleteGuard   = "ansible.incomplete"
)

// isAnsibleTaskKeyword reports whether key is an Ansible task or block
// keyword a native task does not accept.
func isAnsibleTaskKeyword(key string) bool {
	// with_items is accepted as loop's older name; the rest of the family is not.
	return ansibleTaskKeywords[key] || (strings.HasPrefix(key, "with_") && key != "with_items")
}

// isAnsiblePlayKeyword reports whether key is an Ansible play keyword a
// native runbook's top level does not accept.
func isAnsiblePlayKeyword(key string) bool {
	return ansiblePlayKeywords[key] || isAnsibleTaskKeyword(key)
}
