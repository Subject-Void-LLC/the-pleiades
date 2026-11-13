An SSH connection whose `known_hosts` holds only one of the device's host keys (its ed25519 key, say) now
verifies it: the connection offers the key types `known_hosts` holds for that host first, as OpenSSH does,
instead of being refused as "key mismatch" when the device would have answered with its ECDSA key.
