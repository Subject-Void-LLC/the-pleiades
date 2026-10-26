`net.ios.command` no longer reports a device's secrets in the clear. A `show running-config` through it returned
whatever keys, passwords and communities the configuration held, unmasked, in the run report and its text output.
Its output is now masked as `net.ios.config` masks a configuration's differences, every named secret of the project
is masked by value in every run, and `net.cli.command`'s output is masked the same way. A secret `net.ios.command`,
`net.cli.command`, `net.ios.config` or `net.ios.parse` finds in what a device prints, of eight characters or more and
not shaped like a configuration word, is masked wherever else that run prints it. A key that went through `net.ios.command` in an earlier run is in that run's report and output: treat it as exposed and rotate it.
