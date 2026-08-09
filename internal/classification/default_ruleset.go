package classification

import "github.com/Subject-Void-LLC/the-pleiades/pkg/capability"

// DefaultRuleSet returns the built-in classification rules PLAN.md Section
// 7 promises every `pleiades init` scaffold ships with ("built-in
// classification rules for common OS families"). It is grounded only in
// the two device types internal/inventory/record.Types actually holds
// today (cisco_router, linux_server): a resolved Type always successfully
// hydrates through the real ItemFactory, rather than inventing a
// WindowsDesktop or generic NetworkDevice type nothing registers.
//
// The tree shape mirrors PLAN.md Section 6d's own worked example
// (linux_server -> debian_family -> ubuntu; network_device -> cisco ->
// ios). Capabilities matches what Router/Server's vendor constructors
// already hardcode at the root level (Phase 32's capability granularity
// decision: this makes classification and the vendor literal agree, not
// diverge), plus one real sub-rule at debian_family granting AptCapable --
// the checklist's own worked example ("the mechanism where
// debian_family/_rule.yaml adds AptCapable") made concrete: classifying
// ["linux_server", "debian_family", "ubuntu"] unions AptCapable into the
// LinuxCapable/SSHTransportCapable the root already granted, unchanged by
// the ubuntu level, which still carries no rule of its own.
func DefaultRuleSet() *RuleSet {
	str := func(s string) *string { return &s }

	rs, err := NewRuleSet(map[string]Rule{
		"linux_server": {
			Type:           str("linux_server"),
			ConnectionMode: str("agentless"),
			Onboard:        str("configure_polling"),
			Capabilities:   []capability.Name{capability.NameLinux, capability.NameSSHTransport},
		},
		"linux_server.debian_family": {
			Capabilities: []capability.Name{capability.NameApt},
		},
		"network_device.cisco.ios": {
			Type:           str("cisco_router"),
			ConnectionMode: str("agentless"),
			Onboard:        str("configure_polling"),
			Capabilities:   []capability.Name{capability.NameCiscoIOS, capability.NameSSHTransport},
		},
		// A switch is a more specific IOS device, so it inherits the level
		// above (agentless, configure_polling, CiscoIOSCapable plus
		// SSHTransportCapable) and changes only what actually differs: the
		// device type, and NetworkCLICapable, which is what net.cli.command
		// and net.cli.config require. This is the Section 6d
		// most-specific-wins merge doing exactly what it exists for, rather
		// than a second rule restating the first.
		"network_device.cisco.ios.switch": {
			Type:         str("cisco_switch"),
			Capabilities: []capability.Name{capability.NameNetworkCLI},
		},
		// A Catalyst Center controller is not an IOS device and does not
		// sit under the ios level: it answers a REST API and has no CLI, no
		// SSH transport, and no IOS version. It hangs off cisco directly so
		// it inherits nothing the controller cannot do.
		"network_device.cisco.catalyst_center": {
			Type:           str("catalyst_center"),
			ConnectionMode: str("agentless"),
			Onboard:        str("configure_polling"),
			Capabilities:   []capability.Name{capability.NameCatalystAPI},
		},
	})
	if err != nil {
		// The map above is a compile-time literal built entirely from this
		// package's own string constants; a validation failure here would
		// mean this file itself is wrong, not a runtime condition any
		// caller could hit. Panicking (rather than threading an error
		// through every DefaultRuleSet call site) matches pkg/capability's
		// identical init-time-programmer-error convention.
		panic("classification: built-in default rule set is invalid: " + err.Error())
	}
	return rs
}
