`make bsd-check`, `make win-check` and the Windows ARM check now clear their login's Go leftovers before every run on
a lab VM they start and put back: the default Go build cache, the `go-build` and `Test*` folders interrupted test runs
leave in its temporary directory, and, while still short of room, its module cache. A machine that is not such a VM,
such as the Mac, is left untouched.
