`win.firewall.rule` makes, changes, removes or reports a Windows Defender Firewall rule over WinRM, in the machine's
own rule store. A rule is named by its Name, so a built-in one such as `OpenSSH-Server-In-TCP` can be managed, and an
existing rule is compared and changed only on the settings a task gives: `profiles: [domain, private]` widens it
without restating its ports or program. A rule set by Group Policy is refused. It supports check mode, and its undo
removes a rule it made, puts back the settings it changed, or makes a removed rule again.
`win.firewall.profile` sets or reports one firewall profile (domain, private or public): whether it is on, its
default inbound and outbound actions, and its logging. A setting Group Policy holds at another value fails the task,
naming it. It supports check mode, and its undo puts back each setting it changed.
