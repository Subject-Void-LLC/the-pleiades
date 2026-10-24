`make mac-check`, `make bsd-check` and `make win-check` build into a Go cache of their own beside their trees, and
make room before they refuse a machine under 4 GB free: each removes its own older trees and that cache, never anything
else, and refuses only a machine still short afterwards, naming what it removed.
