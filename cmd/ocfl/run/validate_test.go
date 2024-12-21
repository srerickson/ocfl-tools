package run_test

import (
	"path/filepath"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

func TestValidate(t *testing.T) {
	t.Run("object fixtures", func(t *testing.T) {
		// bad object
		obj := filepath.Join(badObjectFixtures, `E010_missing_versions`)
		args := []string{`validate`, `--object`, obj}
		testutil.RunCLI(args, nil, func(err error, stdout string, stderr string) {
			be.True(t, err != nil)
			be.In(t, "ocfl_code=E010", stderr)
		})
		obj = filepath.Join(badObjectFixtures, `E023_extra_file`)
		args = []string{`validate`, `--object`, obj}
		testutil.RunCLI(args, nil, func(err error, stdout string, stderr string) {
			be.True(t, err != nil)
			be.In(t, "ocfl_code=E023", stderr)
		})
		// good object
		obj = filepath.Join(goodObjectFixtures, `spec-ex-full`)
		args = []string{`validate`, `--object`, obj}
		testutil.RunCLI(args, nil, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
		})
	})
	t.Run("object in store fixtures", func(t *testing.T) {
		env := map[string]string{
			"OCFL_ROOT": filepath.Join(goodStoreFixtures, `reg-extension-dir-root`),
		}
		args := []string{`validate`, `--id`, "ark:123/abc"}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
		})
	})
	t.Run("good store fixtures", func(t *testing.T) {
		env := map[string]string{
			"OCFL_ROOT": filepath.Join(goodStoreFixtures, `reg-extension-dir-root`),
		}
		args := []string{`validate`}
		testutil.RunCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
		})
	})
	t.Run("bad store fixtures", func(t *testing.T) {
		args := []string{`validate`, `--root`, filepath.Join(badStoreFixtures, `multi_level_errors`)}
		testutil.RunCLI(args, nil, func(err error, stdout string, stderr string) {
			be.True(t, err != nil)
			be.In(t, "object(s) with errors", stderr)
		})
	})
}
