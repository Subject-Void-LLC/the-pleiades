`make pr-check`, `make test-full` and CI's test jobs report a package that fails to build with what the compiler said.
They used to print only "build failed", or nothing for a dependency that failed, so a full disk or a compile error
on another operating system showed no reason anywhere in the report.
