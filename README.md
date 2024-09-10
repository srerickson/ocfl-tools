# OCFL Tools

This repository provides a command line tool (`ocfl`) for working with
[OCFL-based repositories](http://ocfl.io). It supports basic operations for
creating, accessing, and updating objects in an OCFL storage root:

```
Usage: ocfl <command> [flags]

tools for working with OCFL repositories

Flags:
  -h, --help           Show context-sensitive help.
  -r, --root=STRING    The prefix/directory of the OCFL storage root used for the command ($OCFL_ROOT)
      --debug          enable debug log messages

Commands:
  init-root    Create a new OCFL storage root
  commit       Create or update an object in a storage root
  ls           List objects in a storage root or contents of an object version
  export       Export object contents to the local filesystem
  diff         Show changed files between versions of an object
  validate     Validate an object or an entire storage root
  version      Print ocfl-tools version information

Run "ocfl <command> --help" for more information on a command.
```

## Usage

### S3 Configuration

To access OCFL storage roots on S3, set `--root` or `$OCFL_ROOT` with the bucket and prefix:

```sh
export OCFL_ROOT="s3://my-bucket/my-root"
ocfl ls # list objects in the root
```

The S3 client is configurable using AWS configuration files (e.g., `~/.aws/credentials`) and environment variables:

```sh
export AWS_ENDPOINT_URL="..."
export AWS_REGION="..."
export AWS_ACCESS_KEY_ID="..."
export AWS_SECRET_ACCESS_KEY="..."
```

## Installation

The CLI is distributed in an Ubuntu-based docker image:
[`docker.io/srerickson/ocfl-tools`](/hub.docker.com/repository/docker/srerickson/ocfl-tools/)

```sh
# start container shell with mounted data volume
docker run -it -v /tmp:/data docker.io/srerickson/ocfl-tools
# run `ocfl`
ocfl --help
```

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