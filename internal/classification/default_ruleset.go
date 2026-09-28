package classification

import "github.com/Subject-Void-LLC/the-pleiades/pkg/capability"

// DefaultRuleSet returns the built-in classification rules PLAN.md Section
// 7 promises every `pleiades init` scaffold ships with ("built-in
// classification rules for common OS families"). Every rule below is
// grounded in a device type internal/inventory/record.Types actually
// holds: a resolved Type always successfully hydrates through the real
// ItemFactory, rather than inventing a WindowsDesktop or generic
// NetworkDevice type nothing registers. catalyst_center and aws_account
// were each added later, when the sync plugin that needed them was built,
// not when their device types first landed; windows_server was added
// later still, when svc.windows.*/win.feature.* gave windows.Server real
// capability accessors to classify into - the same pattern this file
// itself follows for whatever the next plugin or method batch needs.
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
			Capabilities: []capability.Name{capability.NameApt, capability.NamePosixAccount},
		},
		"linux_server.rhel_family": {
			Capabilities: []capability.Name{capability.NameDnf, capability.NamePosixAccount},
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
		// An AWS account/region context, added when the "aws" sync plugin
		// was built (internal/inventory/plugins/aws), the same way the
		// catalyst_center rule above was added when that plugin was built
		// rather than when the device type itself first landed. It answers
		// the AWS API rather than a device transport, exactly like
		// catalyst_center answering a REST API rather than IOS's CLI, so it
		// sits at its own root rather than under linux_server or any
		// network_device branch.
		"aws_account": {
			Type:           str("aws_account"),
			ConnectionMode: str("agentless"),
			Onboard:        str("configure_polling"),
			Capabilities:   []capability.Name{capability.NameAWSAPI},
		},
		// A stock Windows server, added when svc.windows.*/win.feature.*
		// landed and windows.Server gained real accessors for the three
		// capabilities those methods need. It sits at its own root, the
		// same reasoning aws_account's own comment gives: it is not a more
		// specific linux_server, so it inherits nothing from that branch.
		// Capabilities matches windows.NewServer's own vendor baseline
		// exactly (Phase 32's capability granularity decision), which is
		// also the set that makes the aws sync plugin's Classify able to
		// resolve a discovered Windows EC2 instance here instead of
		// quarantining it.
		"windows_server": {
			Type:           str("windows_server"),
			ConnectionMode: str("agentless"),
			Onboard:        str("configure_polling"),
			Capabilities: []capability.Name{
				capability.NameWindows, capability.NameWinRM,
				capability.NameWindowsService, capability.NameWindowsFeature,
			},
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
