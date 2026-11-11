Reading a stored fact no longer depends on the Go release a build was made with. A fact key is read back, on the
command line and from `GET /inventory/devices/{name}/facts/{key}`, unless it is empty, too long, not UTF-8, or holds a
control character, the space U+0020 or a slash, so a key a build of another toolchain stored still reads and its chain still
verifies; what a new fact may be named is unchanged. A history answer for a key that would not show as itself, and that
no value is stored under, no longer repeats the key.
