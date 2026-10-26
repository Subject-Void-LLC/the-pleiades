`net.pcap.summary` returns `unexamined`, the frames it could not judge (frames carrying no IP, such as CDP, or IP inside
MPLS or PPPoE, and frames of a link type it does not read); `cleartext: 0` proves nothing crossed in the clear only when
`unexamined` is 0 too. `virt.cml.capture.stop`'s `dest` must name a `.pcap` or `.pcapng` file, so a capture can never
replace a runbook or another project file. `virt.cml.link.stop` leaves a link that never ran alone and records no undo.
