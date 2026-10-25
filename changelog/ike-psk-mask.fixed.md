Fixed an IKE pre-shared key going out unmasked: `pre-shared-key` lines in an IKEv2 keyring (with or without `local` or
`remote`), an ISAKMP keyring's `pre-shared-key address ... key`, and an IKEv2 profile's `authentication local pre-share
key` are now masked wherever IOS configuration text is masked, which covers `net.ios.config`'s differences and
`undo_lines`, `net.ios.replace`, `net.ios.parse` and `net.batfish.check`. A key that went through one of those in an
earlier run should be rotated.
