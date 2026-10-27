The Windows ARM check builds with cgo through llvm-mingw, so SQLite-backed packages run against the real driver
there, and the Windows checks turn on the race detector only on amd64, since Go has none for windows/arm64.
`tools/testgate`'s own tests leave `-race` off on any platform without a race detector.
