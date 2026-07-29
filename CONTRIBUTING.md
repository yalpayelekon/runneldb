# Contributing

RunnelDB is experimental and deliberately small. Issues, design discussions,
tests, documentation, and focused pull requests are welcome.

1. Open an issue before large architectural changes.
2. Keep commits focused and add tests for behavior changes.
3. Run `go test -race ./...` and `go vet ./...`.
4. For changes that touch the WAL or recovery, also run a short fuzz:
   `go test -fuzz=FuzzWALReplay -fuzztime=20s`.
5. For SQL parser changes, run:
   `go test -fuzz=FuzzSQLParse -fuzztime=10s`.
6. The background checkpoint worker uses a 1 s ticker; tests that exercise it
   lower the thresholds and poll with a deadline—always use `-race` when
   touching checkpoint or compaction code.
7. Avoid dependencies unless they clearly earn their place.

By participating, you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
