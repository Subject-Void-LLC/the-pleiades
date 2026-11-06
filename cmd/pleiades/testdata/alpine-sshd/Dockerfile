# An Alpine device for the file.remove check gate: BusyBox's sh is /bin/sh, so
# a login's shell is the one whose test reads mode bits itself and cannot say
# whether root may remove a path. Root logs in directly, so the check runs as
# root under BusyBox. The deploy account logs in without root, for the case
# the device can answer: a directory it may not write. A throwaway test
# container, never a device.
FROM alpine:3.22.8
RUN apk add --no-cache openssh \
 && ssh-keygen -A \
 && echo 'root:release-gate-alpine-password' | chpasswd \
 && adduser -D -s /bin/sh deploy \
 && echo 'deploy:release-gate-deploy-password' | chpasswd \
 && printf 'PermitRootLogin yes\nPasswordAuthentication yes\n' > /etc/ssh/sshd_config.d/gate.conf
CMD ["/usr/sbin/sshd", "-D", "-e"]
