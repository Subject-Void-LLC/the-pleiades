IOS output fixes from the blind CVD lab. A command that only reads (`show`, `dir`, `more`, `ping`, `traceroute`,
`terminal`) fails only on IOS's own refusal of the line, so a `% ` line in what it shows (a CA certificate's
heading) no longer fails it, and `ping`, `traceroute` and `terminal` report unchanged. `net.ios.command` takes
`accept` as `net.ios.config` does. Four notices measured on IOL XE 17.15.1 (a certificate map or keyring already
there, and what removing an enrolled trustpoint says) are notices. A timed-out IOS command says what the device last
printed, masked. `tunnel key 100` is no longer masked as a secret, and `net.ios.parse` masks a field Genie's schema
names as a secret.
