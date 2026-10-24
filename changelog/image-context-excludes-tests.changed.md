The controller and runner images' build context leaves out every `_test.go` file and `testdata` directory, which no build reads: it is 18.7 MiB rather than 32.6, back under its 32 MiB ceiling.
