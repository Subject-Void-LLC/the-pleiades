The packaging release gates in `tests/e2e` drop the settings a parent make passes down (`MAKEFLAGS` and its kin)
before running their own `make`, so a gate run under `make --trace` or other make flags behaves as it does when an
operator runs it. The upgrade gate had failed under `make --trace` on a line make printed but never ran.
