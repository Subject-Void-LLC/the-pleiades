Every `pleiades` command that takes a positional argument now reads which of its flags are boolean from the flags it really defines, so a boolean flag placed before the argument can no longer take the argument as its value. A flag error, such as an unknown flag, is now reported before a missing or extra argument.

`--` now ends a command's flags, as it does for Go's flag package: every argument after it is positional, even one starting with `-`. It used to take the next argument as its value.
