package run_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

func TestLog(t *testing.T) {
	_, fixtures := testutil.TempDirTestData(t,
		`testdata/store-fixtures/1.0/good-stores`,
		`testdata/object-fixtures/1.1/good-objects`,
	)
	goodStoreFixtures := fixtures[0]
	goodObjectFixtures := fixtures[1]
	t.Run("--id", func(t *testing.T) {
		env := map[string]string{
			"OCFL_ROOT": filepath.Join(goodStoreFixtures, `reg-extension-dir-root`),
		}
		args := []string{`log`, `--id`, "ark:123/abc"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.True(t, strings.Contains(stdout, "An version with one file"))
		})
	})
	t.Run("--object", func(t *testing.T) {
		// using object path
		args := []string{`log`, `--object`, filepath.Join(goodObjectFixtures, "spec-ex-full")}
		testutil.RunCLI(args, nil, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			lines := strings.Split(stdout, "\n")
			be.In(t, "Initial import", lines[0])
			be.In(t, "Fix bar.xml, remove image.tiff, add empty2.txt", lines[1])
			be.In(t, "Reinstate image.tiff, delete empty.txt", lines[2])
		})
	})
	t.Run("missing args", func(t *testing.T) {
		testutil.RunCLI([]string{"log"}, nil, func(err error, stdout string, stderr string) {
			be.True(t, err != nil)
			be.In(t, "missing required flag", stderr)
		})
		args := []string{"log", "--root", filepath.Join(goodStoreFixtures, `reg-extension-dir-root`)}
		testutil.RunCLI(args, nil, func(err error, stdout string, stderr string) {
			be.True(t, err != nil)
			be.In(t, "missing required flag", stderr)
		})
	})
}
