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

// netconfLock writes Ansible's spelling of lock's if-supported as the
// native if_supported. Ansible's default, always, is the call's fixed
// parameter.
func netconfLock(params map[string]any) string {
	if params["lock"] == "if-supported" {
		params["lock"] = "if_supported"
	}
	return ""
}
