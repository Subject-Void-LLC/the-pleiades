Added `net.yang.schema`, which fetches the YANG source of a device's modules over NETCONF (`get-schema`) or RESTCONF (the
URL its YANG library names, at the device's own origin only) and saves each into the project as `name@revision.yang`,
masked by the run's secret values only, never by the configuration masker that read a schema's `key` and `password`
words as secrets.
