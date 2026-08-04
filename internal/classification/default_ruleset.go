package classification

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
// ios), including levels that carry no rule at all (debian_family and
// ubuntu add nothing here, the same as Section 6d's own windows/ and
// windows/desktop/ levels), to demonstrate real root-to-leaf inheritance:
// classifying ["linux_server", "debian_family", "ubuntu"] pulls its Type
// and ConnectionMode from the root rule two levels up, unchanged by the
// two no-op levels in between.
func DefaultRuleSet() *RuleSet {
	str := func(s string) *string { return &s }

	rs, err := NewRuleSet(map[string]Rule{
		"linux_server": {
			Type:           str("linux_server"),
			ConnectionMode: str("agentless"),
			Onboard:        str("configure_polling"),
		},
		"network_device.cisco.ios": {
			Type:           str("cisco_router"),
			ConnectionMode: str("agentless"),
			Onboard:        str("configure_polling"),
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
