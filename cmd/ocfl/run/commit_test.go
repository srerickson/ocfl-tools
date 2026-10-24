package run_test

import (
	"context"
	"errors"
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
	stageCache := filepath.Join(tmpDir, "stage-cache")
	env := map[string]string{
		"OCFL_ROOT":        filepath.Join(tmpDir, "ocfl"),
		"OCFL_STAGE_CACHE": stageCache,
	}
	runOK(t, env, "init-root", "--layout", "0003-hash-and-id-n-tuple-storage-layout")
	commitArgs := func(id, msg string) []string {
		return []string{"commit", contentDir, "--id", id, "-m", msg, "-n", "Me", "-e", "me@example.net"}
	}

	t.Run("removes stage file", func(t *testing.T) {
		runOK(t, env, commitArgs("object-01", "first commit")...)
		be.Equal(t, 0, len(stageIDs(t, stageCache)))
		be.Equal(t, "", runOK(t, env, "validate", "--id", "object-01"))
	})

	t.Run("interrupted", func(t *testing.T) {
		const id = "object-02"
		var stderrOut string
		testutil.RunCLIInterrupt(commitArgs(id, "first commit"), env, "starting object update", func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, context.Canceled))
			stderrOut = stderr
		})
		// the stage is kept in the stage cache, and the message says how to
		// use it
		ids := stageIDs(t, stageCache)
		be.Equal(t, 1, len(ids))
		short := ids[0][:7]
		be.In(t, "commit of "+id+" (v1) was interrupted", stderrOut)
		be.In(t, "to resume: ocfl stage commit -s "+short+" --resume\n", stderrOut)
		be.In(t, "to revert: ocfl stage commit -s "+short+" --revert", stderrOut)
		// resume
		runOK(t, env, "stage", "commit", "-s", short, "--resume")
		be.Equal(t, 0, len(stageIDs(t, stageCache)))
		be.Equal(t, "", runOK(t, env, "validate", "--id", id))
		be.In(t, "first commit", runOK(t, env, "log", "--id", id))
	})

	t.Run("interrupted and reverted", func(t *testing.T) {
		const id = "object-03"
		testutil.RunCLIInterrupt(commitArgs(id, "first commit"), env, "starting object update", func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, context.Canceled))
		})
		ids := stageIDs(t, stageCache)
		be.Equal(t, 1, len(ids))
		testutil.RunCLI([]string{"stage", "commit", "-s", ids[0], "--revert"}, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, "ocfl stage delete -s "+ids[0][:7], stderr)
		})
		be.False(t, strings.Contains(runOK(t, env, "ls"), id))
		// the reverted stage is a draft that can be deleted
		runOK(t, env, "stage", "delete", "-s", ids[0])
		be.Equal(t, 0, len(stageIDs(t, stageCache)))
	})

	t.Run("stage cache flag", func(t *testing.T) {
		const id = "object-04"
		otherCache := filepath.Join(tmpDir, "other-cache")
		args := append(commitArgs(id, "first commit"), "--stage-cache", otherCache)
		var stderrOut string
		testutil.RunCLIInterrupt(args, env, "starting object update", func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, context.Canceled))
			stderrOut = stderr
		})
		be.Equal(t, 0, len(stageIDs(t, stageCache)))
		ids := stageIDs(t, otherCache)
		be.Equal(t, 1, len(ids))
		// --stage-cache is in the commands because it was given on the
		// command line
		resume := "ocfl stage commit --stage-cache " + otherCache + " -s " + ids[0][:7] + " --resume"
		be.In(t, "to resume: "+resume+"\n", stderrOut)
		runOK(t, env, strings.Fields(resume)[1:]...)
		be.Equal(t, 0, len(stageIDs(t, otherCache)))
		be.Equal(t, "", runOK(t, env, "validate", "--id", id))
	})

	t.Run("existing stage", func(t *testing.T) {
		const id = "object-05"
		short := strings.TrimSpace(runOK(t, env, "stage", "new", "--id", id))
		testutil.RunCLI(commitArgs(id, "first commit"), env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "a stage for "+id+" already exists", stderr)
			be.In(t, "ocfl stage status -s "+short, stderr)
			be.In(t, "ocfl stage delete -s "+short, stderr)
		})
		be.False(t, strings.Contains(runOK(t, env, "ls"), id))
		// the existing stage wasn't changed
		be.Equal(t, "", runOK(t, env, "stage", "ls", "-s", short))
		runOK(t, env, "stage", "delete", "-s", short)
		runOK(t, env, commitArgs(id, "first commit")...)
	})

	t.Run("object changed during commit", func(t *testing.T) {
		const id = "object-06"
		// another commit to the object, with different content, finishes
		// first
		otherCommit := func() {
			runOK(t, env, "commit", filepath.Join(contentDir, "folder1"), "--id", id, "-m", "other commit",
				"-n", "Other", "--stage-cache", t.TempDir())
		}
		testutil.RunCLIOnLog(context.Background(), commitArgs(id, "first commit"), env, "starting object update", otherCommit, func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, ocfl.ErrUpdateConflict))
			be.In(t, "failed before the object was changed", stderr)
			be.In(t, "run the command again", stderr)
		})
		// the stage is removed, and the commit can be run again
		be.Equal(t, 0, len(stageIDs(t, stageCache)))
		runOK(t, env, commitArgs(id, "first commit")...)
		be.Equal(t, "", runOK(t, env, "validate", "--id", id))
		log := runOK(t, env, "log", "--id", id)
		be.In(t, "other commit", log)
		be.In(t, "first commit", log)
	})

}

// stageIDs returns the IDs of the stage files in the stage cache dir.
func stageIDs(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	be.NilErr(t, err)
	ids := make([]string, len(files))
	for i, f := range files {
		ids[i] = strings.TrimSuffix(filepath.Base(f), ".json")
	}
	return ids
}
