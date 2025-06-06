# OCFL Tools

This repo provides `ocfl`, a command line tool for working with [OCFL-based
repositories](http://ocfl.io). It supports basic operations, such as creating,
accessing, updating, and removing objects in an OCFL storage root. Multiple
storage backends are supported, including the local filesystem, S3, and http
(read-only).


## Installation

Using [Homebrew](https://brew.sh/) on MacOS or Linux: 

```sh
brew install srerickson/ocfl-tools/ocfl
```

If you have [Go](https://go.dev/dl) (v1.23 or greater) installed:

```sh
go install github.com/srerickson/ocfl-tools/cmd/ocfl@latest
```

You can also download and run the pre-compiled binaries on the [Releases
page](https://github.com/srerickson/ocfl-tools/releases)

## Usage

The `ocfl` command includes a collection of subcommands for different
operations. Use `ocfl --help` to see a list of available subcommands:

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
  delete          Delete an object in the storage root
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

### Configuration

To work with objects using their IDs, you need to specify an OCFL storage root.
(The storage must also define a storage root layout). You can configure the
storage root location using the `$OCFL_ROOT` environment variable or by setting it
explicitly with the `--root` flag, used by most commands. 

```sh
# configure storage root using an environment variable
export OCFL_ROOT=/mnt/data/my-root
ocfl log --id ark://abc/123

# or set using the --root flag
ocfl log --root /mnt/data/my-root --id ark://abc/123
```

#### Object paths

Many commands accepts an `--object` flag that allows you to specify an object
using its full path, rather than its  ID. This is helpful, for example, if the
storage root's layout isn't defined, isn't known, or if the object isn't part of
a storage root. The object path supports multiple protocols:

```sh
# object on local filesystem
ocfl log --object /mnt/data/my-root/my-object

# object stored in S3 
ocfl log --object s3://my-bucket/my-root/my-object
```

#### S3 configuration

To use S3-based storage, the storage root or object path should have the format:
`s3://<bucket>/<prefix>`. For example:

```sh
# list objects in the root
ocfl ls --root s3://my-bucket/my-root
```

The S3 client can be configured using AWS configuration files (e.g., `~/.aws/credentials`) or environment variables:

```sh
export AWS_ENDPOINT_URL="..."
export AWS_REGION="..."
export AWS_ACCESS_KEY_ID="..."
export AWS_SECRET_ACCESS_KEY="..."
```

Additional S3 configuration options:
- `OCFL_S3_PATHSTYLE=true`: enables [path-style S3 requests](https://docs.aws.amazon.com/AmazonS3/latest/userguide/VirtualHosting.html#path-style-access)

#### Read objects using HTTP

For read-only access to OCFL objects over http, you can use the URL of the object's root directory.

```sh
ocfl ls --object https://dreamlab-public.s3.us-west-2.amazonaws.com/ocfl/content-fixtures
```

### Creating a Storage Root

Use `ocfl init-root` to create a new storage root. A root path must be set with
`$OCFL_ROOT`, or `--root`. 

```sh
# create a new storage root using s3
ocfl init-root --root s3://my-bucket/my-root --description "my new root"
```

#### Storage root layouts

New storage roots use the hashed n-tuple layout
(`0004-hashed-n-tuple-storage-layout`) by default. The `--layout` flag can be
used to specify a different layout. The following layouts are supported:

- `0002-flat-direct-storage-layout`
- `0003-hash-and-id-n-tuple-storage-layout`
- `0004-hashed-n-tuple-storage-layout`
- `0006-flat-omit-prefix-storage-layout`
- `0007-n-tuple-omit-prefix-storage-layout`

See [OCFL's extensions
documentation](https://github.com/OCFL/extensions/tree/main/docs) for
descriptions of each.

Layouts are initialized with default configuration settings. If you need to
change the layout's configuration, you must manually edit the layout's
`config.json` file. It's very important to do this *before* adding objects to
the storage root! 

Some layouts do not have valid default configurations
(e.g.,`0006-flat-omit-prefix-storage-layout`). In these cases, you *must*
manually update the `config.json` before using the storage root. 

The command `ocfl info` will report errors if the storage root's layout
configuration is invalid.

## Development

### Testing with S3

To enable S3 tests, set `$OCFL_TEST_S3`.

```sh
# example using minio
export OCFL_TEST_S3="http://127.0.0.1:9000"
export AWS_SECRET_ACCESS_KEY=...
export AWS_ACCESS_KEY_ID=...
go test ./...
```
