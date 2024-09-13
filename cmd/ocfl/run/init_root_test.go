package run_test

import (
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

func TestInitRoot(t *testing.T) {
	t.Run("existing OK", func(t *testing.T) {
		env := map[string]string{"OCFL_ROOT": t.TempDir()}
		args := []string{"init-root"}
		runCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err) // ok the first time
		})
		runCLI(args, env, func(err error, stdout string, stderr string) {
			be.True(t, err != nil) // error because existing
			be.In(t, `already exists`, stderr)
		})
		args = []string{"init-root", "--existing-ok"}
		runCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err) // no error if --existing-ok
			be.In(t, `already exists`, stderr)

		})
	})
	t.Run("all layouts", func(t *testing.T) {
		// layout name -> default layout config is valid
		layouts := map[string]bool{
			"0002-flat-direct-storage-layout":         true,
			"0003-hash-and-id-n-tuple-storage-layout": true,
			"0004-hashed-n-tuple-storage-layout":      true,
			"0006-flat-omit-prefix-storage-layout":    false,
			"0007-n-tuple-omit-prefix-storage-layout": true,
		}
		testLayout := func(t *testing.T, root string, layout string, defaultOK bool) {
			env := map[string]string{"OCFL_ROOT": root}
			rootDesc := "test description"
			args := []string{
				"init-root",
				"--description", rootDesc,
				"--layout", layout,
			}
			runCLI(args, env, func(err error, stdout string, stderr string) {
				be.NilErr(t, err)
				be.In(t, root, stdout)
				be.In(t, layout, stdout)
				be.In(t, rootDesc, stdout)
				if defaultOK {
					be.True(t, stderr == "")
				} else {
					be.In(t, "layout has configuration errors", stderr)
				}
			})
			if !defaultOK {
				// only continue if the layout's default config is OK
				return
			}
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
		for l, defaultOK := range layouts {
			t.Run(l, func(t *testing.T) {
				testLayout(t, t.TempDir(), l, defaultOK)
				// again with S3 if enabled
				if testutil.S3Enabled() {
					t.Run("s3", func(t *testing.T) {
						testLayout(t, testutil.TempS3Location(t, "new-root"), l, defaultOK)
					})
				}
			})
		}
	})
}
