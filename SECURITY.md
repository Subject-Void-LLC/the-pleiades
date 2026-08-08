# Security policy

Pleiades stores device credentials (AES-256-GCM, a locally held master key) and
executes commands against real infrastructure over SSH. Take a report seriously
before it becomes a real incident.

## Reporting a vulnerability

Do not open a public GitHub issue for a security vulnerability. Instead, use
[GitHub's private vulnerability reporting](https://github.com/Subject-Void-LLC/the-pleiades/security/advisories/new)
for this repository.

Include what you'd include for any bug report: affected version or commit, a
reproduction, and the impact as you understand it.

## What to expect

This is a pre-1.0 project. There is no formal SLA yet. A report will be acknowledged
and triaged; a fix ships as soon as one exists, with credit to the reporter unless
they ask otherwise.

## Known current limitations, stated rather than left for a reviewer to find

- Credentials are stored in a local, AES-256-GCM encrypted file
  (`.pleiades/credentials.yaml`). There is no Vault, KMS, or other external secrets
  manager integration today.
- SSH host key verification is on by default and fails closed
  (`internal/transport/ssh`), but the CLI does not yet expose a flag to point it at a
  non-default `known_hosts` file.
- The product is not FIPS-validated.
