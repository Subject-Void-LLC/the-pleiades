The record a `tools/testgate` run writes keeps 400 lines of each failure's output, while the console still prints 40,
so a test that logs its own teardown no longer loses the line that says why it failed.
