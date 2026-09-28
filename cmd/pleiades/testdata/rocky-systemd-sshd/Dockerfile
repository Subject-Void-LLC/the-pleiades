# A Rocky Linux device for the check gates: systemd as PID 1, so sshd and
# firewalld run as the services they are on a real RHEL-family host, and
# dnf and rpm as that family ships them. Root logs in directly, because
# nothing in The Pleiades elevates a task: a method that needs root runs as
# the account it logs in as. A throwaway test container, never a device.
#
# Pinned to a point release so a gate does not move under a commit that
# changed nothing. It needs a writable cgroup tree and the network
# capabilities firewalld uses; the gate grants exactly those rather than
# --privileged (see systemdDevice in the gate).
FROM rockylinux/rockylinux:9.6
RUN dnf -y install systemd openssh-server firewalld \
 && dnf clean all \
 && echo 'root:release-gate-rocky-password' | chpasswd \
 && printf 'PermitRootLogin yes\nPasswordAuthentication yes\n' > /etc/ssh/sshd_config.d/gate.conf \
 && systemctl enable sshd firewalld
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
