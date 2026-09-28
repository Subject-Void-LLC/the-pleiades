Added client certificate authentication for WinRM, and PKCS#12 bundle handling behind
it. A Windows host can now be reached with a certificate instead of a username and a
password, which is the first time this platform presents a client certificate to
anything.

Store the certificate as a credential of a `cryptography`-kind type carrying
`certificate` and `private_key`, and bind it to a template as you would a machine
credential. Certificate authentication is HTTPS only, so The Pleiades selects HTTPS itself
rather than asking you to set a flag whose only correct value is true, and it refuses a
device still pointed at the cleartext WinRM port rather than attempting a TLS handshake
against it. The Windows side needs a matching certificate-to-account mapping; Running
in production describes what to configure there.

A PKCS#12 bundle is the other way to supply the same identity. Put the base64 of the
`.pfx` in a `pfx_bundle` input and bind its passphrase to an ordinary password
credential, so the passphrase is stored once and rotated in one place. The bundle stays
sealed until the moment it is used: it is unlocked inside the short-lived process that
runs the task rather than on the Controller, so what crosses the message broker is the
sealed bundle and not an unlocked private key.

A credential input can now also be filled from another credential's own field, named in
the binding's `source_field` metadata. Previously a binding's source had to be an
external secret source, so pointing one at an ordinary stored password was refused when
you wrote it. A field the source's type does not declare is still refused at that point;
a field that is declared but empty is reported by name when the job runs. The two source
forms compose, so the credential a field is read from may itself read that field out of
HashiCorp Vault.

Four limits are worth knowing, each enforced rather than documented and hoped for. A run
authenticates as exactly one identity, so binding both a machine credential and a
certificate credential to one template is refused when you write it, and one template
therefore cannot reach Linux over SSH and Windows by certificate in the same run. A loose
private key must be unencrypted, because nothing decrypts one on that path; a
passphrase-protected key is refused by name, and a PKCS#12 bundle is the form that does
accept a passphrase. A binding may only read a secret field into an input that is itself
declared secret, so that a value the masking ruleset protects in one credential cannot be
read out through another that does not. And the bundle decoder reads DER and not BER, so
a `.pfx` written by older Windows tooling may need re-exporting even though other
software opens it.

Two further notes for anyone deploying this. The certificate path is capped at TLS 1.2,
because Go's TLS stack does not implement the post-handshake authentication that TLS 1.3
uses to request a client certificate, and has deliberately chosen not to. The target is
not the limitation, password authentication is unaffected, and the cap should be read as
permanent rather than as a pending fix. If you control the target, binding its WinRM
certificate with `clientcertnegotiation=enable` makes Windows ask for the certificate
during the initial handshake instead, which removes the server side of the obstacle. Note
that it does not yet get you TLS 1.3: this release caps the version unconditionally, and
lifting it per device is listed as a known gap. Running in production has the detail. And the server's own certificate
must be trusted by the Runner's system trust store, since there is no way to supply a
certificate authority to this transport from a runbook or a device yet.

`examples/windows_lab/winrm-cert-setup.ps1` configures a Windows target for this, and
`winrm-cert-teardown.ps1` removes it again.
