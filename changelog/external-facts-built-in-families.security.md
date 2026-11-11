An external Collection can no longer declare a fact a built-in method holds, or one inside a built-in family
(`package.`, `service.`, `service_state.`, `file.`, `port.`, `user.`, `group.`, `os.`, `host.`, `hardware.`,
`network.`, `catalyst.`, `ansible_`, compared without case): it could have made a device's facts say what the device
is not, and so steered targeting and baselines. Such a program is refused at load and its fact names must be ASCII.
The Runner makes that refusal, so Runners must be of this release or later; the Controller keeps a built-in fact from
a node whose first-party method its own catalog lacks (a Runner of a newer release may run a built-in it does not know)
and logs at info level how many there were, their families and a bounded list of their names, never their values.
