`net.gnmi.facts`, `net.gnmi.config`, `net.restconf.facts` and `net.restconf.config` now record the
serial number of the certificate a device presents as the stable fact `tls.certificate.serial`, in
lowercase hex, so a revoked serial can be traced to a device; a serial is unique only per issuer, so
match it together with `tls.certificate.issuer`, never alone.
