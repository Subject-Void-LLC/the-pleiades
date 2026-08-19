The runner container can now verify device host keys, which it could not before. Every
SSH connection checks the device's key against a `known_hosts` file and fails closed
without one, but nothing could tell the runner where that file was: it fell back to
`$HOME/.ssh/known_hosts`, and the distroless image has no home directory, so every SSH
task refused unless it set `insecure_skip_host_key_verify`. The off switch was the only
working setting. A new `PLEIADES_KNOWN_HOSTS` environment variable names the file, the
runner image declares it and ships an empty `/app/ssh` to mount over, and the chart takes
a ConfigMap or Secret through `runner.knownHosts`. Nothing turns verification off
fleet-wide; skipping it stays a per-task parameter that shows up in review.
