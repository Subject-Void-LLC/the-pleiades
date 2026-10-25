A task given `insecure_telnet: true` for a device whose record says it is reached over SSH now fails saying so:
`insecure_telnet` only accepts Telnet for a device whose `transport` is `telnet`, and the error names the
`pleiades set-host <name> --set transport=telnet` command that changes it.
