`make win-check` and `make bsd-check` ride out a lab box that misses connections in its first minutes after being
resumed from its saved state: a call whose device never answered is tried again for up to three minutes, a Windows
box is waited for until it answers six times in a row over two minutes, and wincheck sets a resumed guest's clock.
Each check, `make mac-check` included, refuses to start on a machine with under 4 GB free, naming what frees space.
