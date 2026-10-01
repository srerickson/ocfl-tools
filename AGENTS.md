# AGENTS.md

## Testing

- Every `_test.go` file should have a corresponding `.go` file of the same
  name (`foo_test.go` tests `foo.go`). Don't create one-off test files for a
  feature or a bug; add tests to the test file for the code under test.
- Tests of functionality that isn't specific to one subcommand (location
  parsing, `file://` urls, global flags, etc.) go in
  `cmd/ocfl/run/run_test.go`.
- S3 tests run when `$OCFL_TEST_S3` is set (see README.md, "Testing with S3").
