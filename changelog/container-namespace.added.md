The `container.*` namespace manages containers on Docker or Podman through the engine's own API:

- `container.run` and `container.create` converge a container to its declaration. Either one refuses a container that differs from its declaration and names `container.recreate`.
- `container.start`, `container.stop`, `container.restart` and `container.remove` change a container's state.
- `container.exec` runs a command in a running container.
- `container.info`, `container.list` and `container.logs` read containers.
- `container.prune` removes unused containers, images, anonymous volumes or networks, narrowed by labels.
- `container.copy_in` and `container.copy_out` move one file into or out of a container.

Every method except `container.exec` can run in check mode. Parameters that give a container access to the host (privileged mode, added capabilities, devices, host volumes) are marked as host escapes in the reference.
