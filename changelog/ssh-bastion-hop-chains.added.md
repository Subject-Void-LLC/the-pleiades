A device reachable only through a jump host no longer needs a workaround. Set a
`route` property (an ordered list of device names) at the device, group, or inventory
level, most specific wins, and SSH tasks tunnel through each named hop before
reaching the target. Every hop resolves its own credential and gets its own real
host key check, so a compromised bastion sees ciphertext only past its own hop.
