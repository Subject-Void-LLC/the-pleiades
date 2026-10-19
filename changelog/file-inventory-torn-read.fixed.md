The file-backed inventory (`hosts.yaml` and its state file) reads both files under one lock. A read that ran beside
a save could take the hosts from before it and the state from after it, an item whose own save would then overwrite
the change it never saw; on Windows the same overlap failed the read outright with a sharing violation.
