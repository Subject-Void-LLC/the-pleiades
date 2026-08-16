The Helm chart refuses to render three configurations rather than installing something
that fails later. Setting `runner.playbookDir` is refused because the legacy Ansible
path starts a sibling container through a Docker daemon, and the only ways to give a pod
one are mounting the node's container runtime socket or running a privileged
Docker-in-Docker sidecar. A self-provisioning controller is refused past one replica,
because two of them either race over one certificate file or serve two different
untrusted certificates. An image tagged `latest`, untagged, or pinned by digest is
refused as well. Every rendered container runs non-root with a numeric uid, a read-only
root filesystem, no capabilities and the default seccomp profile, and `make helm-lint`
proves all of it on every build.
