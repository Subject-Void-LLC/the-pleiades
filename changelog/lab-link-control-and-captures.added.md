`virt.cml.link.stop` and `virt.cml.link.start` take a link of a CML lab down and back up, each the other's undo.
`virt.cml.capture.start` and `virt.cml.capture.stop` capture a link's packets on the controller, bounded by a packet count
or a number of seconds and optionally a packet filter, and write the pcap into the project. `net.pcap.summary` counts a
pcap or pcapng file in the project by protocol and address pair, encrypted (ESP, ESP in UDP), key exchange (IKE) apart
from cleartext, with no device.
