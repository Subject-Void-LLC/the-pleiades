`net.ios.config` answers the questions a configuration line asks, from `prompts` or a model through `ask`, as
`net.ios.command` does, and a question nothing answers now fails the line at once, naming it, instead of waiting until
the session's read limit (five minutes). IOS's `% ` notices for a line it accepted (a duplicate access list entry,
an IKEv2 proposal or profile match already there, a key pair being made or replaced, a removal's preamble) are reported under `notices` rather
than failing the task, and `accept` adds more. `net.ios.replace` fails on a `% ` line only when the device does not
match the checkpoint afterwards, and otherwise reports it as a warning.
