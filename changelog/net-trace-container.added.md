`net.trace` traces from a container: with `container: <name>` it runs `tracepath` inside that container on a
`container_host` device through the engine's own exec, as `container.exec` does, so a lab host with no SSH
(a containerlab end host) can be traced from. `pleiades validate` holds each call to the shape it chose: a
`container` call needs a container host, and a call without one a Linux device over SSH; an empty `container`
is refused rather than traced over SSH. `pleiades doc` and
the module reference list both shapes' capabilities and transports.
