`--help` and `-h` exit 0 on every command (they exited 1, and `journal --help` exited 2 printing nothing). A task's
`timeout:` takes a duration of whole seconds (`150s`, `30m`) as `adhoc --timeout` does, not only a number.
