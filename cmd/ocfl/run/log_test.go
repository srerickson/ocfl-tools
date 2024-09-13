package run_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlmjohnson/be"
)

func TestLog(t *testing.T) {
	t.Run("--id", func(t *testing.T) {
		env := map[string]string{
			"OCFL_ROOT": filepath.Join(goodStoreFixtures, `reg-extension-dir-root`),
		}
		args := []string{`log`, `--id`, "ark:123/abc"}
		runCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.True(t, strings.Contains(stdout, "An version with one file"))
		})
	})
	t.Run("--object", func(t *testing.T) {
		// using object path
		args := []string{`log`, `--object`, filepath.Join(goodObjectFixtures, "spec-ex-full")}
		runCLI(args, nil, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			lines := strings.Split(stdout, "\n")
			be.True(t, strings.Contains(lines[0], "Initial import"))
			be.True(t, strings.Contains(lines[1], "Fix bar.xml, remove image.tiff, add empty2.txt"))
			be.True(t, strings.Contains(lines[2], "Reinstate image.tiff, delete empty.txt"))
		})
	})
	t.Run("missing args", func(t *testing.T) {
		runCLI([]string{"log"}, nil, func(err error, stdout string, stderr string) {
			be.True(t, err != nil)
		})
	})
}
