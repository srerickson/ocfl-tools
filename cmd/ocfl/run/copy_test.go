package run_test

import (
	"path/filepath"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

func TestCopy(t *testing.T) {
	tmpDir, fixtures := testutil.TempDirTestData(t,
		`testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root`,
		`testdata/object-fixtures/1.1/good-objects/spec-ex-full`,
	)
	goodStoreFixture := fixtures[0]
	goodObjectFixture := fixtures[1]

	t.Run("copy object by id", func(t *testing.T) {
		srcID := "ark:123/abc"
		dstID := "ark:123/copy"
		env := map[string]string{"OCFL_ROOT": goodStoreFixture}

		// Copy the object
		args := []string{"copy", "--id", srcID, "--to", dstID, "-n", "tester", "-e", "test@example.com"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.In(t, "object copied", stderr)
		})

		// Verify the copy exists
		args = []string{"ls", "--id", dstID}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.In(t, "a_file.txt", stdout)
		})

		// Verify the copy is valid
		args = []string{"validate", "--id", dstID}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
		})

		// Verify the log message
		args = []string{"log", "--id", dstID}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.In(t, "Copied from", stdout)
			be.In(t, srcID, stdout)
		})
	})

	t.Run("copy object by path", func(t *testing.T) {
		rootPath := filepath.Join(tmpDir, "new-root")
		dstID := "my-copy"
		env := map[string]string{"OCFL_ROOT": rootPath}

		// Initialize a new root
		args := []string{"init-root"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
		})

		// Copy from object path
		args = []string{"copy", "--object", goodObjectFixture, "--to", dstID, "-n", "tester", "-e", "test@example.com"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.In(t, "object copied", stderr)
		})

		// Verify the copy
		args = []string{"ls", "--id", dstID}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.In(t, "empty2.txt", stdout)
			be.In(t, "foo/bar.xml", stdout)
			be.In(t, "image.tiff", stdout)
		})
	})

	t.Run("copy specific version", func(t *testing.T) {
		rootPath := filepath.Join(tmpDir, "version-root")
		dstID := "v1-copy"
		env := map[string]string{"OCFL_ROOT": rootPath}

		// Initialize a new root
		args := []string{"init-root"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
		})

		// Copy v1 from multi-version object (spec-ex-full has 3 versions)
		args = []string{"copy", "--object", goodObjectFixture, "--to", dstID, "--version", "1", "-n", "tester", "-e", "test@example.com"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
		})

		// v1 of spec-ex-full has: empty.txt, foo/bar.xml, image.tiff
		args = []string{"ls", "--id", dstID}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.In(t, "empty.txt", stdout)
			be.In(t, "foo/bar.xml", stdout)
			be.In(t, "image.tiff", stdout)
		})
	})

	t.Run("copy to self fails", func(t *testing.T) {
		srcID := "ark:123/abc"
		env := map[string]string{"OCFL_ROOT": goodStoreFixture}

		// Try to copy to itself
		args := []string{"copy", "--id", srcID, "--to", srcID, "-n", "tester", "-e", "test@example.com"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "cannot copy object to itself", stderr)
		})
	})

	t.Run("copy to existing fails", func(t *testing.T) {
		rootPath := filepath.Join(tmpDir, "existing-root")
		env := map[string]string{"OCFL_ROOT": rootPath}

		// Initialize root and create two objects
		args := []string{"init-root"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
		})

		// Create first object
		args = []string{"copy", "--object", goodObjectFixture, "--to", "obj-1", "-n", "tester", "-e", "test@example.com"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
		})

		// Create second object
		args = []string{"copy", "--object", goodObjectFixture, "--to", "obj-2", "-n", "tester", "-e", "test@example.com"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
		})

		// Try to copy obj-1 to obj-2 (already exists)
		args = []string{"copy", "--id", "obj-1", "--to", "obj-2", "-n", "tester", "-e", "test@example.com"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "already exists", stderr)
		})
	})

	t.Run("copy invalid version fails", func(t *testing.T) {
		srcID := "ark:123/abc"
		dstID := "ark:123/invalid-ver"
		env := map[string]string{"OCFL_ROOT": goodStoreFixture}

		// Try to copy with an invalid version
		args := []string{"copy", "--id", srcID, "--to", dstID, "--version", "999", "-n", "tester", "-e", "test@example.com"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "out of range", stderr)
		})
	})

	t.Run("copy without source fails", func(t *testing.T) {
		env := map[string]string{"OCFL_ROOT": goodStoreFixture}

		// Try to copy without --id or --object
		args := []string{"copy", "--to", "new-id", "-n", "tester", "-e", "test@example.com"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.Nonzero(t, err)
		})
	})
}
