# OCFL Tools Command Line Documentation

A comprehensive guide to using the `ocfl` command line tool for working with [OCFL-based repositories](http://ocfl.io).

## Overview

The `ocfl` command provides a collection of subcommands for different operations, supporting basic operations such as creating, accessing, updating, and removing objects in an OCFL storage root. Multiple storage backends are supported, including the local filesystem, S3, and http (read-only).

## Installation

### Using Homebrew (macOS/Linux)
```bash
brew install srerickson/ocfl-tools/ocfl
```

### Using Go
```bash
go install github.com/srerickson/ocfl-tools/cmd/ocfl@latest
```

### Pre-compiled Binaries
Download from the [Releases page](https://github.com/srerickson/ocfl-tools/releases)

## Quick Start

Get help for any command:
```bash
ocfl --help
ocfl <command> --help
```

## Global Configuration

### Storage Root Configuration

Configure the storage root location using the `$OCFL_ROOT` environment variable or by setting it explicitly with the `--root` flag:

```bash
# Using environment variable
export OCFL_ROOT=/mnt/data/my-root
ocfl log --id ark://abc/123

# Using --root flag
ocfl log --root /mnt/data/my-root --id ark://abc/123
```

### Global Flags

- `--root=STRING`: The prefix/directory of the OCFL storage root
- `--debug`: Enable debug log messages
- `--help`: Show context-sensitive help

## Core Commands Reference

### Repository Management

#### `init-root` - Create a New Storage Root

Use `ocfl init-root` to create a new storage root. A root path must be set with `$OCFL_ROOT`, or `--root`.

```bash
# Create a local storage root
ocfl init-root --root /path/to/storage --description "My OCFL Repository"

# Create an S3-based storage root
ocfl init-root --root s3://my-bucket/my-root --description "S3 OCFL Repository"
```

**Layout Options:**
- `0002-flat-direct-storage-layout`
- `0003-hash-and-id-n-tuple-storage-layout`
- `0004-hashed-n-tuple-storage-layout` (default)
- `0006-flat-omit-prefix-storage-layout`
- `0007-n-tuple-omit-prefix-storage-layout`

```bash
ocfl init-root --root /path/to/storage --layout 0002-flat-direct-storage-layout
```

#### `validate` - Validate Objects or Storage Root

```bash
# Validate entire storage root
ocfl validate

# Validate specific object
ocfl validate --id my-object-id

# Validate object by path
ocfl validate --object /path/to/object
```

### Object Operations

#### `commit` - Create or Update Objects

Create new objects or update existing ones using local directory contents:

```bash
# Create a new object
ocfl commit --id my-object-id --message "Initial version" /path/to/source/files

# Update an existing object
ocfl commit --id my-object-id --message "Updated content" /path/to/updated/files

# Commit with author information
ocfl commit --id my-object-id --message "Added documentation" \
  --user-name "John Doe" --user-address "john@example.com" \
  /path/to/files
```

#### `export` - Export Object Contents

Export object contents to the local filesystem:

```bash
# Export latest version to directory
ocfl export --id my-object-id /path/to/export/directory

# Export specific version
ocfl export --id my-object-id --version v2 /path/to/export/directory

# Export using object path
ocfl export --object /path/to/object /path/to/export/directory
```

#### `ls` - List Objects and Contents

List objects in a storage root or files in an object:

```bash
# List all objects in storage root
ocfl ls

# List files in an object (latest version)
ocfl ls --id my-object-id

# List files in specific version
ocfl ls --id my-object-id --version v1

# List with detailed information
ocfl ls --id my-object-id --long
```

#### `delete` - Remove Objects

Delete an object in the storage root:

```bash
# Delete an object by ID
ocfl delete --id my-object-id

# Force deletion without confirmation
ocfl delete --id my-object-id --force
```

### Information and History

#### `info` - Object and Repository Information

Show information about an object or the active storage root:

```bash
# Show storage root information
ocfl info

# Show object information
ocfl info --id my-object-id

# Show object information by path
ocfl info --object /path/to/object
```

#### `log` - Version History

Show an object's revision log:

```bash
# Show version history
ocfl log --id my-object-id

# Show detailed log with file changes
ocfl log --id my-object-id --verbose

# Show log for specific version range
ocfl log --id my-object-id --from v1 --to v3
```

#### `diff` - Compare Versions

Show changed files between versions of an object:

```bash
# Compare current version with previous
ocfl diff --id my-object-id

# Compare specific versions
ocfl diff --id my-object-id --from v1 --to v2

# Show detailed diff with file contents
ocfl diff --id my-object-id --verbose
```

### Staging Workflow

The staging commands provide a workflow for preparing object updates before committing them.

#### `stage new` - Create New Stage

Create a new stage for preparing updates to an object:

```bash
# Create stage for new object
ocfl stage new --id new-object-id

# Create stage for updating existing object
ocfl stage new --id existing-object-id
```

#### `stage add` - Add Files to Stage

Add a file or directory to the stage:

```bash
# Add a single file
ocfl stage add /path/to/file.txt

# Add a directory
ocfl stage add /path/to/directory/

# Add with specific logical path
ocfl stage add /path/to/file.txt --logical-path docs/readme.txt
```

#### `stage rm` - Remove Files from Stage

Remove a file or directory from the stage:

```bash
# Remove file from stage
ocfl stage rm file.txt

# Remove directory from stage
ocfl stage rm directory/
```

#### `stage ls` - List Staged Files

List files in the stage state:

```bash
# List all staged files
ocfl stage ls

# List with detailed information
ocfl stage ls --long
```

#### `stage diff` - Show Staged Changes

Show changes between an upstream object or directory and the stage:

```bash
# Show differences between stage and current object
ocfl stage diff

# Compare stage with specific version
ocfl stage diff --version v2

# Compare stage with local directory
ocfl stage diff /path/to/directory
```

#### `stage status` - Check Stage Status

Show stage details and report any errors:

```bash
# Show current stage status
ocfl stage status
```

#### `stage commit` - Commit Staged Changes

Commit the stage as a new object version:

```bash
# Commit staged changes
ocfl stage commit --message "Updated files via staging"

# Commit with author information
ocfl stage commit --message "Added new features" \
  --user-name "Jane Smith" --user-address "jane@example.com"
```

## Storage Backends

### Local Filesystem

```bash
# Set local storage root
export OCFL_ROOT=/mnt/data/my-repository
ocfl ls
```

### S3 Storage

To use S3-based storage, the storage root or object path should have the format: `s3://<bucket>/<prefix>`:

```bash
# List objects in S3 storage root
ocfl ls --root s3://my-bucket/my-root

# Work with S3 object directly
ocfl log --object s3://my-bucket/my-root/my-object
```

**S3 Configuration:**

The S3 client can be configured using AWS configuration files (e.g., `~/.aws/credentials`) or environment variables:

```bash
export AWS_ENDPOINT_URL="https://s3.amazonaws.com"
export AWS_REGION="us-west-2"
export AWS_ACCESS_KEY_ID="your-access-key"
export AWS_SECRET_ACCESS_KEY="your-secret-key"
```

**Additional S3 Options:**
- `OCFL_S3_PATHSTYLE=true`: Enable path-style S3 requests
- `OCFL_S3_MD5_CHECKSUMS=true`: Use MD5 instead of CRC32 for checksums

### HTTP (Read-only)

For read-only access to OCFL objects over http, you can use the URL of the object's root directory:

```bash
ocfl ls --object https://example.com/ocfl/my-object
```

## Common Workflows

### Creating Your First Object

```bash
# 1. Initialize a storage root
ocfl init-root --root /path/to/repository --description "My OCFL Repository"

# 2. Create an object from local files
ocfl commit --id ark://example/123 --message "Initial deposit" \
  --user-name "Repository Manager" --user-address "manager@example.com" \
  /path/to/source/files

# 3. Verify the object was created
ocfl ls
ocfl info --id ark://example/123
```

### Updating an Object

```bash
# Method 1: Direct commit
ocfl commit --id ark://example/123 --message "Added new files" /path/to/updated/files

# Method 2: Using staging workflow
ocfl stage new --id ark://example/123
ocfl stage add /path/to/new/file.txt
ocfl stage add /path/to/another/directory/
ocfl stage status
ocfl stage commit --message "Added files via staging"
```

### Examining Object History

```bash
# View all versions
ocfl log --id ark://example/123

# Compare versions
ocfl diff --id ark://example/123 --from v1 --to v2

# Export specific version
ocfl export --id ark://example/123 --version v1 /path/to/export/v1
```

### Working with Object Paths

Many commands accept an `--object` flag that allows you to specify an object using its full path, rather than its ID. This is helpful when the storage root's layout isn't defined or if the object isn't part of a storage root:

```bash
# Local filesystem object
ocfl log --object /mnt/data/my-root/my-object

# S3 object
ocfl log --object s3://my-bucket/my-root/my-object

# HTTP object (read-only)
ocfl info --object https://example.com/ocfl/objects/my-object
```

## Error Handling and Validation

### Object Validation

Always validate objects after major operations:

```bash
# Validate specific object
ocfl validate --id my-object-id

# Validate entire repository
ocfl validate

# Get detailed validation information
ocfl validate --verbose
```

### Common Issues and Solutions

**Layout Configuration Problems:**
Some layouts require manual configuration after initialization. Check with:
```bash
ocfl info
```

**Permission Issues:**
Ensure proper read/write permissions for the storage root directory.

**S3 Connectivity:**
Test S3 connectivity with a simple list operation:
```bash
ocfl ls --root s3://your-bucket/your-root
```

## Best Practices

1. **Always validate** objects after creation or updates
2. **Use meaningful commit messages** to track changes over time
3. **Set up proper layouts** before adding objects to storage roots
4. **Test S3 configuration** with simple operations before complex workflows
5. **Use staging workflow** for complex updates that need review
6. **Regular validation** of the entire storage root for integrity checking
7. **Backup storage roots** regularly, especially before major migrations

## Advanced Usage

### Batch Operations

Create multiple objects in a loop:
```bash
for dir in /source/objects/*; do
  object_id="ark://example/$(basename "$dir")"
  ocfl commit --id "$object_id" --message "Bulk import" "$dir"
done
```

### Integration with Scripts

Check if object exists before creating:
```bash
if ocfl info --id "$object_id" >/dev/null 2>&1; then
  echo "Object exists, updating..."
  ocfl commit --id "$object_id" --message "Update" "$source_dir"
else
  echo "Creating new object..."
  ocfl commit --id "$object_id" --message "Initial" "$source_dir"
fi
```

## Troubleshooting

### Debug Mode

Enable debug logging for detailed operation information:
```bash
ocfl --debug command --options
```

### Common Error Messages

- **"storage root not found"**: Check `--root` flag or `$OCFL_ROOT` environment variable
- **"object not found"**: Verify object ID or use `ocfl ls` to list available objects
- **"invalid layout configuration"**: Check and manually edit `config.json` in the storage root
- **"S3 access denied"**: Verify AWS credentials and bucket permissions

For additional help, consult the [OCFL specification](https://ocfl.io/) and the [project repository](https://github.com/srerickson/ocfl-tools).