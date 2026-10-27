`pleiades remove-host` removes a host from the inventory (its row and its state), and with `--credential` its stored
credential and with `--forget-host-keys` the known_hosts lines trusting its address, unless another device is reached
there; a host another host names in a property is refused until `--leave-references`. `pleiades remove-credential`
removes a device's credential alone. `pleiades adhoc` takes a comma list of devices (one task per device, each its
`target:`), and `-`, or no hosts at all, for a method that needs no device. `pleiades journal list` takes `--since` (a
duration back or a time) and `--changed`, and names an ad-hoc run by its method.
