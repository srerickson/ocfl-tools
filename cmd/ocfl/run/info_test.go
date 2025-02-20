package run_test

import (
	"path/filepath"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

func TestInfo(t *testing.T) {
	_, fixtures := testutil.TempDirTestData(t,
		"testdata/store-fixtures/1.0/good-stores",
		"testdata/object-fixtures/1.1/good-objects",
	)
	goodStoreFixtures := fixtures[0]
	goodObjectFixtures := fixtures[1]
	t.Run("storage root with layout", func(t *testing.T) {
		args := []string{`info`, `--root`, filepath.Join(goodStoreFixtures, `reg-extension-dir-root`)}
		testutil.RunCLI(args, nil, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.In(t, `0003-hash-and-id-n-tuple-storage-layout`, stdout)
		})
	})
	t.Run("storage root without layout", func(t *testing.T) {
		args := []string{`info`, `--root`, filepath.Join(goodStoreFixtures, `simple-root`)}
		testutil.RunCLI(args, nil, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.In(t, `storage root has no layout`, stderr)
		})
	})
	t.Run("object id", func(t *testing.T) {
		id := `ark:123/abc`
		args := []string{`info`,
			`--root`, filepath.Join(goodStoreFixtures, `reg-extension-dir-root`),
			`--id`, id,
		}
		testutil.RunCLI(args, nil, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.In(t, id, stdout)
		})
	})
	t.Run("object path", func(t *testing.T) {
		fixture := `spec-ex-full`
		obj := filepath.Join(goodObjectFixtures, fixture)
		args := []string{`info`, `--object`, obj}
		testutil.RunCLI(args, nil, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.In(t, fixture, stdout)
		})
	})
}
