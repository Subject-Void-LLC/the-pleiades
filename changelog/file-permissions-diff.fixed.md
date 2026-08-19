`file.permissions` reads the path's state back from the device after changing it, so its
recorded diff and returned mode report what the path really carries. It used to derive
them from the request, which is wrong whenever the kernel silently drops a setuid or
setgid bit.
