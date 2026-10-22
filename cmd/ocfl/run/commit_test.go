package run_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

func TestCommit(t *testing.T) {
	tmpDir, fixtures := testutil.TempDirTestData(t, `testdata/content-fixture`)
	contentDir := fixtures[0]
	env := map[string]string{"OCFL_ROOT": filepath.Join(tmpDir, "ocfl")}
	runOK(t, env, "init-root", "--layout", "0003-hash-and-id-n-tuple-storage-layout")
	// stage files are created in the temporary directory
	stageDir := t.TempDir()
	t.Setenv("TMPDIR", stageDir)
	commitArgs := func(id, msg string) []string {
		return []string{"commit", contentDir, "--id", id, "-m", msg, "-n", "Me", "-e", "me@example.net"}
	}
	stageFiles := func(t *testing.T) []string {
		t.Helper()
		files, err := filepath.Glob(filepath.Join(stageDir, "ocfl-commit-*.json"))
		be.NilErr(t, err)
		return files
	}

	t.Run("removes stage file", func(t *testing.T) {
		runOK(t, env, commitArgs("object-01", "first commit")...)
		be.Equal(t, 0, len(stageFiles(t)))
		be.Equal(t, "", runOK(t, env, "validate", "--id", "object-01"))
	})

	t.Run("interrupted", func(t *testing.T) {
		const id = "object-02"
		var stderrOut string
		testutil.RunCLIInterrupt(commitArgs(id, "first commit"), env, "starting object update", func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, context.Canceled))
			stderrOut = stderr
		})
		// the stage file is kept, and the message says how to use it
		files := stageFiles(t)
		be.Equal(t, 1, len(files))
		be.In(t, "commit of "+id+" (v1) was interrupted", stderrOut)
		be.In(t, "to resume: ocfl --root "+env["OCFL_ROOT"]+" stage commit -f "+files[0]+" --resume\n", stderrOut)
		be.In(t, "stage commit -f "+files[0]+" --revert", stderrOut)
		// resume
		runOK(t, env, "stage", "commit", "-f", files[0], "--resume")
		_, err := os.Stat(files[0])
		be.True(t, errors.Is(err, fs.ErrNotExist))
		be.Equal(t, "", runOK(t, env, "validate", "--id", id))
		be.In(t, "first commit", runOK(t, env, "log", "--id", id))
	})

	t.Run("interrupted and reverted", func(t *testing.T) {
		const id = "object-03"
		testutil.RunCLIInterrupt(commitArgs(id, "first commit"), env, "starting object update", func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, context.Canceled))
		})
		files := stageFiles(t)
		be.Equal(t, 1, len(files))
		runOK(t, env, "stage", "commit", "-f", files[0], "--revert")
		be.False(t, strings.Contains(runOK(t, env, "ls"), id))
		be.NilErr(t, os.Remove(files[0]))
	})
}
