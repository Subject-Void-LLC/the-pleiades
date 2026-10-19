`win.archive.extract` extracts a `.zip`, `.tar` or `.tar.gz` already on a Windows host into a folder there, with the
`tar.exe` Windows ships, making the folder when it is missing. Like `archive.extract`, it is idempotent through
`creates` and can remove the archive afterwards; it supports check mode. `forge migrate-playbook` converts
`win_unzip` to it, and the VirtualBox lab's Windows tools runbook uses it in place of a shell command.
