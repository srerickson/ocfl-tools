# OCFL Tools

This repository provides a command line tool (`ocfl`) for working with
[OCFL-based repositories](http://ocfl.io). It supports basic operations for
creating, accessing, and updating objects in an OCFL storage root:

```
Usage: ocfl <command> [flags]

command line tool for working with OCFL repositories

Flags:
  -h, --help           Show context-sensitive help.
      --root=STRING    The prefix/directory of the OCFL storage root used for the command ($OCFL_ROOT)
      --debug          enable debug log messages

Commands:
  commit          Create or update an object using contents of a local directory
  diff            Show changed files between versions of an object
  export          Export object contents to the local filesystem
  info            Show information about an object or the active storage root
  init-root       Create a new OCFL storage root
  log             Show an object's revision log
  ls              List objects in a storage root or files in an object
  stage add       Add a file or directory to the stage
  stage commit    Commit the stage as a new object version
  stage diff      Show changes between an upstream object or directory and the stage
  stage ls        List files in the stage state
  stage new       Create a new stage for preparing updates to an object
  stage rm        Remove a file or directory from the stage
  stage status    Show stage details and report any errors
  validate        Validate an object or all objects in the storage root
  version         Print ocfl-tools version information

Run "ocfl <command> --help" for more information on a command.
```

## Usage

### S3 Configuration

To access OCFL storage roots on S3, set `--root` or `$OCFL_ROOT` with the bucket and prefix:

```sh
# set root with flag
ocfl ls --root="s3://my-bucket/my-root"

# OR set root with environment variable
export OCFL_ROOT="s3://my-bucket/my-root"
ocfl ls
```

The S3 client is configurable using AWS configuration files (e.g., `~/.aws/credentials`) and environment variables:

```sh
export AWS_ENDPOINT_URL="..."
export AWS_REGION="..."
export AWS_ACCESS_KEY_ID="..."
export AWS_SECRET_ACCESS_KEY="..."
```

[Path-style S3 requests](https://docs.aws.amazon.com/AmazonS3/latest/userguide/VirtualHosting.html#path-style-access) are enabled by setting `OCFL_S3_PATHSTYLE=true`.

## Installation

Using Homebrew: `brew install srerickson/ocfl-tools/ocfl`

You can also build and install `ocfl` locally using [Go](https://go.dev/dl) (v1.23 or greater):

```sh
go install github.com/srerickson/ocfl-tools/cmd/ocfl@latest
```

## Development

### Testing with S3

To enable S3 tests, set `$OCFL_TEST_S3`:

```sh
# example using minio
export OCFL_TEST_S3="http://127.0.0.1:9000"
export AWS_SECRET_ACCESS_KEY=...
export AWS_ACCESS_KEY_ID=...
go test ./...
```
