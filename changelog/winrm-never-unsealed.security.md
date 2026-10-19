WinRM over plain HTTP (port 5985, the default) no longer sends a message unencrypted. The client library it was built
on, when its NTLM handshake failed (under load, or with the handshake split across two connections), silently posted
the message without encryption: the script, its environment values and any stdin crossed the network in the clear
before Windows refused it with "401 - invalid content type". Pleiades now uses its own transport that seals every
message with the handshake's session and fails, saying nothing was sent, when the handshake does not complete. HTTPS
and client-certificate connections were not affected.
