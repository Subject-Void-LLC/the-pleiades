`virt.cml.node.create` takes `interfaces`, the least number of physical interfaces the node gets: beyond the few its
node definition gives, more are added slot by slot and named as the definition names them (an `iol-xe` node's fifth
is `Ethernet1/0`), up to 128 and no more than the definition allows.
