`net.batfish.check`: `route`, `no_route` and `routes_match` take `vrf`, narrowing them to one VRF. `reach.from:
node[interface]` is now the node sending from that interface, in its VRF; a packet arriving on it is
`@enter(node[interface])`, and an interface may be quoted as Batfish writes it. A trace Batfish cannot make (a packet
entering an interface in a VRF, which ends abnormally on the pinned release) fails naming the form to use instead. NHRP is
refused until accepted by name, since the model resolves nothing over a DMVPN cloud.
