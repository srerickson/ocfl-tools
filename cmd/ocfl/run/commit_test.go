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
	"github.com/srerickson/ocfl-go"
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
		be.In(t, "to resume: ocfl stage commit -f "+files[0]+" --resume\n", stderrOut)
		be.In(t, "to revert: ocfl stage commit -f "+files[0]+" --revert", stderrOut)
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
		testutil.RunCLI([]string{"stage", "commit", "-f", files[0], "--revert"}, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, "ocfl stage delete -f "+files[0], stderr)
		})
		be.False(t, strings.Contains(runOK(t, env, "ls"), id))
		// the reverted stage is a draft that can be deleted
		runOK(t, env, "stage", "delete", "-f", files[0])
		be.Equal(t, 0, len(stageFiles(t)))
	})

	t.Run("object changed during commit", func(t *testing.T) {
		const id = "object-04"
		// another commit to the object, with different content, finishes
		// first
		otherCommit := func() {
			runOK(t, env, "commit", filepath.Join(contentDir, "folder1"), "--id", id, "-m", "other commit",
				"-n", "Other")
		}
		testutil.RunCLIOnLog(context.Background(), commitArgs(id, "first commit"), env, "starting object update", otherCommit, func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, ocfl.ErrUpdateConflict))
			be.In(t, "failed before the object was changed", stderr)
			be.In(t, "run the command again", stderr)
		})
		// the stage file is removed, and the commit can be run again
		be.Equal(t, 0, len(stageFiles(t)))
		runOK(t, env, commitArgs(id, "first commit")...)
		be.Equal(t, "", runOK(t, env, "validate", "--id", id))
		log := runOK(t, env, "log", "--id", id)
		be.In(t, "other commit", log)
		be.In(t, "first commit", log)
	})

	t.Run("object changed, draft not saved", func(t *testing.T) {
		const id = "object-05"
		lock, unlock := testutil.LockDir(t, stageDir)
		// the object changes, and the stage file can't be changed, after
		// the frozen stage is saved
		changeObject := func() {
			runOK(t, env, "commit", filepath.Join(contentDir, "folder1"), "--id", id, "-m", "other commit",
				"-n", "Other")
			lock()
		}
		testutil.RunCLIOnLog(context.Background(), commitArgs(id, "first commit"), env, "starting object update", changeObject, func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, ocfl.ErrUpdateConflict))
			be.In(t, "commit of "+id+" (v1) failed before the object was changed: ", stderr)
			be.In(t, "\n  the stage file couldn't be restored: ", stderr)
			be.In(t, "\n  to delete it: ocfl stage delete -f ", stderr)
			be.In(t, " --force\n", stderr)
		})
		unlock()
		files := stageFiles(t)
		be.Equal(t, 1, len(files))
		runOK(t, env, "stage", "delete", "-f", files[0], "--force")
		be.Equal(t, 0, len(stageFiles(t)))
		be.Equal(t, "", runOK(t, env, "validate", "--id", id))
	})

	t.Run("unchanged state", func(t *testing.T) {
		const id = "object-06"
		runOK(t, env, commitArgs(id, "first commit")...)
		testutil.RunCLI(commitArgs(id, "second commit"), env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "unchanged version state", stderr)
		})
		// the commit failed before the stage was frozen: it's removed
		be.Equal(t, 0, len(stageFiles(t)))
		be.False(t, strings.Contains(runOK(t, env, "log", "--id", id), "second commit"))
	})
}
