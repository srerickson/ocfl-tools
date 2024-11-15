package run_test

import (
	"testing"

	"github.com/carlmjohnson/be"
)

func TestCommit(t *testing.T) {
	env := map[string]string{"OCFL_ROOT": t.TempDir()}
	args := []string{"init-root"}
	runCLI(args, env, func(err error, stdout string, stderr string) {
		be.NilErr(t, err)
	})
	objID := "object-01"
	args = []string{
		"commit",
		contentFixture,
		"--id", objID,
		"--message", "my message",
		"--name", "Me",
		"--email", "me@domain.net",
	}
	runCLI(args, env, func(err error, _ string, _ string) {
		be.NilErr(t, err)
	})
}
