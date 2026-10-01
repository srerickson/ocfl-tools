package run_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

func TestInitRoot(t *testing.T) {
	tmpDirs, fixtures := testutil.TempDirTestData(t, `testdata/content-fixture`)
	ocflRoot := filepath.Join(tmpDirs, `root`)
	contentFixture := fixtures[0]
	t.Run("existing OK", func(t *testing.T) {
		env := map[string]string{"OCFL_ROOT": ocflRoot}
		args := []string{"init-root"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err) // ok the first time
		})
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.True(t, err != nil) // error because existing
			be.In(t, `already exists`, stderr)
		})
		args = []string{"init-root", "--existing-ok"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
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
			testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
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
			testutil.RunCLI(args, env, func(err error, _ string, _ string) {
				be.NilErr(t, err)
			})
			// ocfl validate
			args = []string{
				"validate",
				"--id", objID,
			}
			testutil.RunCLI(args, env, func(err error, stdout string, _ string) {
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

func TestInitRoot_FailureLeavesNoDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	for name, args := range map[string][]string{
		"invalid layout":     {"--layout", "bogus"},
		"invalid spec":       {"--ocflv", "x"},
		"unsupported spec":   {"--ocflv", "9.9"},
		"layout and spec ok": nil, // control: succeeds
	} {
		t.Run(name, func(t *testing.T) {
			parent := filepath.Join(tmpDir, filepath.FromSlash(name))
			root := filepath.Join(parent, "a", "b", "root")
			env := map[string]string{"OCFL_ROOT": root}
			testutil.RunCLI(append([]string{"init-root"}, args...), env, func(err error, stdout, stderr string) {
				_, statErr := os.Stat(parent)
				if args == nil {
					be.NilErr(t, err)
					be.NilErr(t, statErr)
					return
				}
				be.True(t, err != nil)
				be.True(t, os.IsNotExist(statErr)) // nothing created
			})
		})
	}
	t.Run("existing directories are kept", func(t *testing.T) {
		parent := filepath.Join(tmpDir, "existing")
		be.NilErr(t, os.MkdirAll(filepath.Join(parent, "keep"), 0o777))
		root := filepath.Join(parent, "a", "root")
		env := map[string]string{"OCFL_ROOT": root}
		testutil.RunCLI([]string{"init-root", "--layout", "bogus"}, env, func(err error, stdout, stderr string) {
			be.True(t, err != nil)
		})
		_, err := os.Stat(filepath.Join(parent, "keep"))
		be.NilErr(t, err) // preexisting directory untouched
		_, err = os.Stat(filepath.Join(parent, "a"))
		be.True(t, os.IsNotExist(err)) // created directory removed
	})
	t.Run("file url", func(t *testing.T) {
		root := filepath.Join(tmpDir, "fileurl", "root")
		env := map[string]string{"OCFL_ROOT": fileURL(root)}
		testutil.RunCLI([]string{"init-root", "--layout", "bogus"}, env, func(err error, stdout, stderr string) {
			be.True(t, err != nil)
		})
		_, err := os.Stat(filepath.Join(tmpDir, "fileurl"))
		be.True(t, os.IsNotExist(err))
	})
	t.Run("existing storage root is kept", func(t *testing.T) {
		root := filepath.Join(tmpDir, "kept", "root")
		env := map[string]string{"OCFL_ROOT": root}
		testutil.RunCLI([]string{"init-root"}, env, func(err error, _, _ string) { be.NilErr(t, err) })
		testutil.RunCLI([]string{"init-root"}, env, func(err error, _, _ string) { be.True(t, err != nil) })
		_, err := os.Stat(filepath.Join(root, "0=ocfl_1.1"))
		be.NilErr(t, err)
	})
}
