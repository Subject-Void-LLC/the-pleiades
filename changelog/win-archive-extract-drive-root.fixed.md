`win.archive.extract` can extract straight into a drive root such as `C:\`. The `tar.exe` in Windows 11 build 26300
cannot create a folder directly under a drive's root, so the method now makes the archive's top folders first, refusing
any named `..` or `.` or holding a drive colon or wildcard. Proven on a Windows 11 ARM machine with Go's own archive.
