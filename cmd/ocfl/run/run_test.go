package run_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/run"
)

var (
	testDataPath       = filepath.Join(`..`, `..`, `..`, `testdata`)
	goodObjectFixtures = filepath.Join(testDataPath, `object-fixtures`, `1.1`, `good-objects`)
	badObjectFixtures  = filepath.Join(testDataPath, `object-fixtures`, `1.1`, `bad-objects`)
	goodStoreFixtures  = filepath.Join(testDataPath, `store-fixtures`, `1.0`, `good-stores`)
	contentFixture     = filepath.Join(testDataPath, `content-fixture`)

	allLayouts = []string{
		"0002-flat-direct-storage-layout",
		"0003-hash-and-id-n-tuple-storage-layout",
		"0004-hashed-n-tuple-storage-layout",
		// "0006-flat-omit-prefix-storage-layout",
		"0007-n-tuple-omit-prefix-storage-layout",
	}
)

func runCLI(args []string, env map[string]string, expect func(err error, stdout, stderr string)) {
	ctx := context.Background()
	if env == nil {
		env = map[string]string{}
	}
	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	getenv := func(key string) string { return env[key] }
	args = append([]string{"ocfl"}, args...)
	// configure s3 test endpoint if enabled
	if testutil.S3Enabled() {
		env["AWS_ENDPOINT_URL"] = testutil.S3Endpoint()
		env["OCFL_S3_PATHSTYLE"] = "true"
	}
	err := run.CLI(ctx, args, stdout, stderr, getenv)
	expect(err, stdout.String(), stderr.String())
}

func TestInitRoot(t *testing.T) {
	t.Run("all layouts", func(t *testing.T) {
		testLayout := func(t *testing.T, root string, layout string) {
			env := map[string]string{"OCFL_ROOT": root}
			rootDesc := "test description"
			args := []string{
				"init-root",
				"--description", rootDesc,
				"--layout", layout,
			}
			runCLI(args, env, func(err error, stdout string, stderr string) {
				be.NilErr(t, err)
				be.True(t, strings.Contains(stdout, root))
				be.True(t, strings.Contains(stdout, layout))
				be.True(t, strings.Contains(stdout, rootDesc))
			})
			// ocfl commit
			objID := "object-01"
			args = []string{
				"commit",
				contentFixture,
				"--id", objID,
				"--message", "my message",
				"--name", "Me",
				"--email", "me@domain.net",
			}
			runCLI(args, env, func(err error, _ string, _ string) {
				be.NilErr(t, err)
			})
			// ocfl validate
			args = []string{
				"validate",
				"--id", objID,
			}
			runCLI(args, env, func(err error, stdout string, _ string) {
				be.NilErr(t, err)
			})
		}
		for _, l := range allLayouts {
			t.Run(l, func(t *testing.T) {
				testLayout(t, t.TempDir(), l)
				// again with S3 if enabled
				if testutil.S3Enabled() {
					t.Run("s3", func(t *testing.T) {
						testLayout(t, testutil.TempS3Location(t, "new-root"), l)
					})
				}
			})
		}
	})
}

func TestValidate(t *testing.T) {
	t.Run("object fixtures", func(t *testing.T) {
		// bad object
		obj := filepath.Join(badObjectFixtures, `E023_old_manifest_missing_entries`)
		args := []string{`validate`, `--object`, obj}
		runCLI(args, nil, func(err error, stdout string, stderr string) {
			be.True(t, err != nil)
			be.True(t, strings.Contains(stderr, "E023"))
		})
		// good object
		obj = filepath.Join(goodObjectFixtures, `spec-ex-full`)
		args = []string{`validate`, `--object`, obj}
		runCLI(args, nil, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
		})
	})
	t.Run("store fixtures", func(t *testing.T) {
		env := map[string]string{
			"OCFL_ROOT": filepath.Join(goodStoreFixtures, `reg-extension-dir-root`),
		}
		args := []string{`validate`, `--id`, "ark:123/abc"}
		runCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
		})
	})
}

func TestRootNotSet(t *testing.T) {
	// test commands fail if root is not set
	cmds := []string{"init-root", "ls", "commit", "validate", "export", "diff"}
	for _, cmd := range cmds {
		// should return an error
		args := []string{cmd}
		runCLI(args, nil, func(err error, stdout string, stderr string) {
			be.True(t, err != nil)
		})
	}
}
