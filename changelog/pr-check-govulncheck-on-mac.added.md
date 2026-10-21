`make pr-check` runs `govulncheck` on the Mac `tests/mac/.env` names, between that Mac's two phases, scanning for
this machine's platform and Go release so its answer is the one a scan here gives; with no Mac ready it runs here as
before. A scan that traces a vulnerable call builds a call graph of the whole module, which took 7 GB on a Mac.
