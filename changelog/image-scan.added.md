`make image-scan` scans the built container images for known vulnerabilities in
what the base image ships, using a scanner pinned in the `Makefile` alongside the
two Go scanners. It is not part of `make ci`, because `ci` builds no images and
the scanner needs a vulnerability database it downloads at run time. It closes a
real gap: `gosec` reads this project's source and `govulncheck` reads its module
graph, and neither can see the C libraries that arrive from the digest-pinned
distroless base.
