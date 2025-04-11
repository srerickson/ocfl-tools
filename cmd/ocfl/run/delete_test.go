package run_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-go"
	ocflfs "github.com/srerickson/ocfl-go/fs"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

func TestDelete(t *testing.T) {

	t.Run("delete object with confirmation", func(t *testing.T) {
		_, fixtures := testutil.TempDirTestData(t,
			`testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root`,
		)
		env := map[string]string{"OCFL_ROOT": fixtures[0]}

		// confirm without 'y' doesn't delete the object
		args := []string{`delete`, `--id`, "ark:123/abc"}
		testutil.RunCLIInput(args, env, "\n", func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.In(t, "not deleted", stdout)
		})

		// confim with 'y' deletes the object
		args = []string{`delete`, `--id`, "ark:123/abc"}
		testutil.RunCLIInput(args, env, "y\n", func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.In(t, "deleted object", stderr)
		})

		// object is deleted
		args = []string{`ls`, `--id`, "ark:123/abc"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.Nonzero(t, err)
			be.True(t, errors.Is(err, fs.ErrNotExist))
		})
	})

	t.Run("delete partial object", func(t *testing.T) {
		ctx := context.Background()
		id := "ark:123/abc"
		_, fixtures := testutil.TempDirTestData(t,
			`testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root`,
		)
		ocflRoot := fixtures[0]
		root, err := ocfl.NewRoot(ctx, ocflfs.DirFS(ocflRoot), ".")
		be.NilErr(t, err)

		// delete the object's inventory file.
		objPath, err := root.ResolveID(id)
		be.NilErr(t, err)

		err = os.Remove(filepath.Join(ocflRoot, filepath.FromSlash(objPath), `inventory.json`))
		be.NilErr(t, err)

		// delete without --not-object fails
		env := map[string]string{"OCFL_ROOT": ocflRoot}
		args := []string{`delete`, `--id`, id, `--yes`}
		testutil.RunCLIInput(args, env, "", func(err error, stdout string, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "no such file or directory", stderr)
		})

		// delete with --not-object works
		args = []string{`delete`, `--id`, id, `--not-object`, `--yes`}
		testutil.RunCLIInput(args, env, "", func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.In(t, "deleted object", stderr)
		})

		// object is deleted
		args = []string{`ls`, `--id`, "ark:123/abc"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.Nonzero(t, err)
			be.True(t, errors.Is(err, fs.ErrNotExist))
		})
	})
}
