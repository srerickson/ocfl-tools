package run_test

import (
	"path/filepath"
	"testing"

	"github.com/carlmjohnson/be"
)

func TestInfo(t *testing.T) {
	t.Run("storage root with layout", func(t *testing.T) {
		args := []string{`info`, `--root`, filepath.Join(goodStoreFixtures, `reg-extension-dir-root`)}
		runCLI(args, nil, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.In(t, `0003-hash-and-id-n-tuple-storage-layout`, stdout)
		})
	})
	t.Run("storage root without layout", func(t *testing.T) {
		args := []string{`info`, `--root`, filepath.Join(goodStoreFixtures, `simple-root`)}
		runCLI(args, nil, func(err error, stdout string, stderr string) {
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
		runCLI(args, nil, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.In(t, id, stdout)
		})
	})
	t.Run("object path", func(t *testing.T) {
		fixture := `spec-ex-full`
		obj := filepath.Join(goodObjectFixtures, fixture)
		args := []string{`info`, `--object`, obj}
		runCLI(args, nil, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.In(t, fixture, stdout)
		})
	})
}
