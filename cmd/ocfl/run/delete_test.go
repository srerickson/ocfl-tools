package run_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/carlmjohnson/be"
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
		})

		// object is deleted
		args = []string{`ls`, `--id`, "ark:123/abc"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.Nonzero(t, err)
			be.True(t, errors.Is(err, fs.ErrNotExist))
		})
	})
}
