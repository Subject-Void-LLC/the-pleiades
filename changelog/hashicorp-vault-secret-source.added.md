Added HashiCorp Vault as an external secret source. A credential input can now be
supplied by a Vault key/value secret, read at the moment a job dispatches, by binding
the input to a credential of the shipped `hashivault_kv` type.

Both key/value engine versions are supported, and a specific secret version can be
requested on a v2 mount. The binding's metadata uses AWX's own field names, so an AWX
`CredentialInputSource` row carries across without translation.

The server's certificate chain is always verified. There is no option to skip it and
no field that could carry one; a Vault with a private certificate authority is reached
by giving that authority to the source credential.

Rotating the secret in Vault, or the token that reads it, takes effect on the next run
of every credential bound to it, with no edit to any of them.

Seven further sources are now recognised as credential types rather than only as
reference names, so an AWX export carrying one reports what is missing and why instead
of reporting a namespace this platform has never heard of.
