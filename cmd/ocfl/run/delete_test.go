package run_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

func TestDelete(t *testing.T) {
	_, fixtures := testutil.TempDirTestData(t,
		`testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root`,
	)
	env := map[string]string{"OCFL_ROOT": fixtures[0]}
	t.Run("delete object", func(t *testing.T) {
		args := []string{`delete`, `--id`, "ark:123/abc"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
		})
		args = []string{`ls`, `--id`, "ark:123/abc"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.Nonzero(t, err)
			be.True(t, errors.Is(err, fs.ErrNotExist))
		})
	})
}
