// Package playbook: the Adjust functions module tables name, for modules
// whose defaults cannot be written as fixed parameters.
package playbook

// firewalldTimes writes the permanent and immediate firewalld's Ansible
// module means. Ansible leaves permanent off and immediate off unless
// given, then turns immediate on whenever permanent is off; the native
// methods default both to on, so a task giving neither would otherwise
// also persist the rule.
func firewalldTimes(params map[string]any) string {
	permanent, _ := params["permanent"].(bool)
	immediate, _ := params["immediate"].(bool)
	params["permanent"] = permanent
	params["immediate"] = immediate || !permanent
	return ""
}

// copyMode notes the mode a file created without one gets: 0600 here,
// the device's umask (usually 0644) under Ansible.
func copyMode(params map[string]any) string {
	if _, ok := params["mode"]; ok {
		return ""
	}
	return "a file this task creates gets mode 0600, where Ansible leaves it to the device's umask (usually 0644); give mode to keep the old result"
}

// netconfAdjust writes Ansible's spellings for netconf_config as the
// native ones. lock's if-supported becomes if_supported (Ansible's
// default, always, is the call's fixed parameter). target, which arrives
// here renamed datastore because target is the engine's device selector,
// keeps candidate and running; Ansible's auto (candidate when the device
// offers one) has no native equivalent, so it is dropped for the native
// default, running, and the change is noted for review.
func netconfAdjust(params map[string]any) string {
	if params["lock"] == "if-supported" {
		params["lock"] = "if_supported"
	}
	if params["datastore"] == "auto" {
		delete(params, "datastore")
		return "target: auto edits the candidate datastore and commits when the device offers one; this task edits running instead, so write datastore: candidate to keep a staged, committed change"
	}
	return ""
}
