`pleiades run --json` and `adhoc --json` mark a failed task `"unreached": true` when its device never answered: the
SSH or WinRM connection failed or timed out before anything was sent, so nothing ran and running the task again is
safe. A refused login, a refused host key or a TLS alert is an answer from the device and is not marked.
