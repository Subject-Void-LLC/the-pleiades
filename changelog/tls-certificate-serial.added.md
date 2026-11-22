`net.gnmi.facts`, `net.gnmi.config`, `net.restconf.facts` and `net.restconf.config` now record the
serial number of the certificate a device presents as the stable fact `tls.certificate.serial`, in
lowercase hex of its bytes (the form `openssl x509 -serial` prints, lowercased, and the new
`serial_hex` of `filters.parseX509Certificate`), so a revoked serial can be looked up on each device
with `pleiades facts <host> tls.certificate.serial`. Other tools may write a serial with a leading
`00` or without a leading `0`: to compare one, lowercase it, remove colons and spaces, drop one
leading `00` before a first digit 8 to f, and add a leading `0` to an odd number of digits. A serial
is unique only per issuer, so match it together with `tls.certificate.issuer`, never alone. A drift
baseline taken before this release that covers every stable fact, or `tls.certificate.*`, reports
`tls.certificate.serial` as added once a read records it: take the baseline again, or take it with
`--keys` naming only the facts to compare.
