package run_test

import (
	"path/filepath"
	"testing"

	"github.com/carlmjohnson/be"
)

func TestValidate(t *testing.T) {
	t.Run("object fixtures", func(t *testing.T) {
		// bad object
		obj := filepath.Join(badObjectFixtures, `E023_old_manifest_missing_entries`)
		args := []string{`validate`, `--object`, obj}
		runCLI(args, nil, func(err error, stdout string, stderr string) {
			be.True(t, err != nil)
		})
		// good object
		obj = filepath.Join(goodObjectFixtures, `spec-ex-full`)
		args = []string{`validate`, `--object`, obj}
		runCLI(args, nil, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
		})
	})
	t.Run("object in store fixtures", func(t *testing.T) {
		env := map[string]string{
			"OCFL_ROOT": filepath.Join(goodStoreFixtures, `reg-extension-dir-root`),
		}
		args := []string{`validate`, `--id`, "ark:123/abc"}
		runCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
		})
	})
	t.Run("good store fixtures", func(t *testing.T) {
		env := map[string]string{
			"OCFL_ROOT": filepath.Join(goodStoreFixtures, `reg-extension-dir-root`),
		}
		args := []string{`validate`}
		runCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
		})
	})
	t.Run("bad store fixtures", func(t *testing.T) {
		args := []string{`validate`, `--root`, filepath.Join(badStoreFixtures, `multi_level_errors`)}
		runCLI(args, nil, func(err error, stdout string, stderr string) {
			be.True(t, err != nil)
			be.In(t, "object(s) with errors", stderr)
		})
	})
}
