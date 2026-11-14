`pleiades add-credential` warns when a password or enable secret is the username itself, since masking that secret masks
the username, and every word holding it, in all output.
