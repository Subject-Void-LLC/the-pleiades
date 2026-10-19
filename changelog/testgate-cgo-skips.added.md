`tools/testgate`, in a run built with cgo off (a Windows machine with no C compiler), reports a test that failed
only on go-sqlite3's cgo stub as a skip that `needs cgo`, listed in the skip ledger, rather than as a failure; any
other failure still fails. The Windows lab check hit this on every SQLite-backed package.
