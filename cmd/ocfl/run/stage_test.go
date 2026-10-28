package run_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-go"
	ocflfs "github.com/srerickson/ocfl-go/fs"
	"github.com/srerickson/ocfl-go/fs/local"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/stage"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

func TestStage_Example(t *testing.T) {
	tmpDir, fixtures := testutil.TempDirTestData(t, `testdata/content-fixture`)
	contentFixture := fixtures[0]
	rootPath := filepath.Join(tmpDir, "ocfl")
	stagePath := filepath.Join(tmpDir, "my-stage.json")
	objID := "ark://my-object-01"
	name := "Mr. Dibbs"
	email := "dibbs@mr.com"
	env := map[string]string{"OCFL_ROOT": rootPath}
	// create storage root
	testutil.RunCLI([]string{
		"init-root",
		"--description", "test stage command",
		"--layout", "0003-hash-and-id-n-tuple-storage-layout",
	}, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
	})
	// v1 stage
	cmd := []string{"stage", "new", "--file", stagePath, "--alg", "sha256", "--id", objID}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		be.In(t, stagePath, stderr)
	})
	// add content to the stage
	v1Dir := filepath.Join(contentFixture, "folder1", "folder2")
	cmd = []string{"stage", "add", "--file", stagePath, "--as", "new-stuff", v1Dir}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
	})
	// list stage content
	cmd = []string{"stage", "ls", "--file", stagePath}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		expect := "new-stuff/.hidden_dir/note.txt\nnew-stuff/.hidden_file\nnew-stuff/file2.txt\nnew-stuff/sculpture-stone-face-head-888027.jpg\n"
		be.Equal(t, expect, stdout) // stdout has four items (including hidden)
	})
	// --file can also come before the subcommand
	cmd = []string{"stage", "-f", stagePath, "ls"}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		be.In(t, "new-stuff/file2.txt\n", stdout)
	})
	// commit stage
	cmd = []string{"stage", "commit", "--file", stagePath, "-m", "first commit", "-n", name, "-e", email}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
	})
	// check new content for v1
	cmd = []string{"ls", "--version", "1", "--id", objID}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		expect := "new-stuff/.hidden_dir/note.txt\nnew-stuff/.hidden_file\nnew-stuff/file2.txt\nnew-stuff/sculpture-stone-face-head-888027.jpg\n"
		be.Equal(t, expect, stdout) // stdout has four items (including hidden)
	})
	// stage file should be deleted
	if _, err := os.Stat(stagePath); err == nil {
		t.Fatal("stage file wasn't deleted after commit")
	}

	// v2 stage
	cmd = []string{"stage", "new", "--file", stagePath, "--id", objID}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		be.In(t, stagePath, stderr)
	})
	// add content to the stage
	v2File := filepath.Join(contentFixture, "hello.csv")
	cmd = []string{"stage", "add", "--file", stagePath, "--as", "tmp/data.csv", v2File}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
	})
	// commit stage
	// an email that already has the "email:" prefix is used as-is
	cmd = []string{"stage", "commit", "--file", stagePath, "-m", "commit 2", "-n", name, "-e", "email:" + email}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
	})
	cmd = []string{"log", "--id", objID}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		be.Equal(t, 2, strings.Count(stdout, "<email:"+email+">"))
	})
	// check content for v2
	cmd = []string{"ls", "--version", "2", "--id", objID}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.In(t, "new-stuff/.hidden_dir/note.txt\n", stdout)
		be.In(t, "new-stuff/.hidden_file\n", stdout)
		be.In(t, "new-stuff/file2.txt\n", stdout)
		be.In(t, "new-stuff/sculpture-stone-face-head-888027.jpg\n", stdout)
		be.In(t, "tmp/data.csv\n", stdout)
		be.Equal(t, 5, strings.Count(stdout, "\n")) // five items in v2 state (including hidden)
	})
}

func TestStage_New(t *testing.T) {
	t.Run("id required", func(t *testing.T) {
		tmpDir := t.TempDir()
		stagePath := filepath.Join(tmpDir, "my-stage.json")
		env := map[string]string{"OCFL_ROOT": tmpDir}
		// stage new without --id flag should return an error
		cmd := []string{"stage", "new", "--file", stagePath}
		testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
		})
	})

	t.Run("creates stagefile for new object with defaults", func(t *testing.T) {
		tmpDir := t.TempDir()
		rootPath := filepath.Join(tmpDir, "ocfl")
		stagePath := filepath.Join(tmpDir, "my-stage.json")
		objID := "ark://test-object"
		env := map[string]string{"OCFL_ROOT": rootPath}
		// initialize OCFL storage root
		testutil.RunCLI([]string{
			"init-root",
			"--description", "test stagefile creation",
			"--layout", "0003-hash-and-id-n-tuple-storage-layout",
		}, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
		})

		// verify stagefile doesn't exist before
		_, err := os.Stat(stagePath)
		be.True(t, errors.Is(err, fs.ErrNotExist))

		// run stage new command
		cmd := []string{"stage", "new", "--file", stagePath, "--id", objID}
		testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, stagePath, stderr)
		})
		// verify stagefile was created with defaults
		s := readStage(t, stagePath)
		be.Equal(t, "sha512", s.DigestAlgorithm().ID())
	})

	t.Run("ignore alg flag for existing objects", func(t *testing.T) {
		tmpDir, fixtures := testutil.TempDirTestData(t,
			`testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root`,
		)
		ocflPath := fixtures[0]
		stagePath := filepath.Join(tmpDir, "my-stage.json")
		objID := "ark:123/abc"
		env := map[string]string{"OCFL_ROOT": ocflPath}
		// object uses sha512; specify sha256
		cmd := []string{"stage", "new", "--file", stagePath, "--id", objID, "--alg", "sha256"}
		testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, stagePath, stderr)
		})
		s := readStage(t, stagePath)
		// stagefile uses sha512
		be.Equal(t, "sha512", s.DigestAlgorithm().ID())
		be.Equal(t, ocfl.V(2), s.NextHead())
	})
}

func TestStage_Rm(t *testing.T) {
	tmpDir, fixtures := testutil.TempDirTestData(t,
		`testdata/content-fixture`,
		`testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root`,
	)
	contentFixture := fixtures[0]
	ocflPath := fixtures[1]
	stagePath := filepath.Join(tmpDir, "my-stage.json")
	contentFile := filepath.Join(contentFixture, "hello.csv")
	env := map[string]string{"OCFL_ROOT": ocflPath}
	objID := "ark:123/abc"
	cmd := []string{"stage", "new", "--file", stagePath, "--alg", "sha256", "--id", objID}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		be.In(t, stagePath, stderr)
	})
	// add additional file
	cmd = []string{"stage", "add", "--file", stagePath, contentFile, "--as", "tmp/hello.csv"}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
	})
	// rm without path arg should be an error
	cmd = []string{"stage", "rm", "--file", stagePath}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.Nonzero(t, err)
	})
	// remove a_file.txt
	cmd = []string{"stage", "rm", "--file", stagePath, "a_file.txt"}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		be.In(t, "a_file.txt", stderr)
	})
	// only csv file should exist
	cmd = []string{"stage", "ls", "--file", stagePath}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		be.Equal(t, "tmp/hello.csv\n", stdout)
	})
	// remove directory
	cmd = []string{"stage", "rm", "--file", stagePath, "-r", "tmp"}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
	})
	// only csv file should exist
	cmd = []string{"stage", "ls", "--file", stagePath}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		be.Equal(t, "", stdout)
	})
}

func TestStage_Commit(t *testing.T) {
	tmpDir, fixtures := testutil.TempDirTestData(t,
		`testdata/content-fixture`,
	)
	contentFixture := fixtures[0]
	rootPath := filepath.Join(tmpDir, "ocfl")
	stagePath := filepath.Join(tmpDir, "my-stage.json")
	objID := "ark://my-object-01"
	name := "Mr. Dibbs"
	email := "dibbs@mr.com"
	env := map[string]string{"OCFL_ROOT": rootPath}
	// create storage root
	testutil.RunCLI([]string{
		"init-root",
		"--description", "test stage command",
		"--layout", "0003-hash-and-id-n-tuple-storage-layout",
	}, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
	})
	// v1 stage
	cmd := []string{"stage", "new", "--file", stagePath, "--alg", "sha512", "--id", objID}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		be.In(t, stagePath, stderr)
	})
	// add content to the stage
	v1Dir := filepath.Join(contentFixture, "folder1", "folder2")
	cmd = []string{"stage", "add", "--file", stagePath, "--as", "new-stuff", v1Dir}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
	})
	// commit without file is an error
	cmd = []string{"stage", "commit"}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.Nonzero(t, err)
	})
	// commit without message is an error
	cmd = []string{"stage", "commit", "--file", stagePath}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.Nonzero(t, err)
	})
	// commit without name is an error
	cmd = []string{"stage", "commit", "--file", stagePath, "-m", "message"}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.Nonzero(t, err)
	})
	// name and email can be set with env variables
	env["OCFL_USER_NAME"] = name
	env["OCFL_USER_EMAIL"] = email
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
	})
	// name and email appear in the logs
	cmd = []string{"log", "--id", objID}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		be.In(t, "email:"+email, stdout)
		be.In(t, name, stdout)
	})
	// object has not validation errors or warnings
	cmd = []string{"validate", "--id", objID}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		be.Zero(t, stdout)
		be.Zero(t, stderr)
	})
}

func TestStage_Diff(t *testing.T) {
	_, fixtures := testutil.TempDirTestData(t,
		`testdata/content-fixture`,
		`testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root`,
	)
	contentFixture := fixtures[0]
	ocflPath := fixtures[1]
	env := map[string]string{"OCFL_ROOT": ocflPath}
	t.Run("existing object", func(t *testing.T) {
		stageDir := t.TempDir()
		stagePath := filepath.Join(stageDir, "my-stage.json")
		existingID := "ark:123/abc"
		// create the stage file
		cmd := []string{"stage", "new", "--file", stagePath, "--id", existingID}
		testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, stagePath, stderr)
		})
		// no changes for fress stage
		cmd = []string{"stage", "diff", "--file", stagePath}
		testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.Equal(t, "", stdout)
		})
		// stage fixture and remove existing content
		cmd = []string{"stage", "add", "--remove", "--file", stagePath, contentFixture}
		testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
		})
		// show diff
		cmd = []string{"stage", "diff", "--file", stagePath}
		testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			expect := []string{
				"add: folder1/file.txt",
				"add: folder1/folder2/.hidden_dir/note.txt",
				"add: folder1/folder2/.hidden_file",
				"add: folder1/folder2/file2.txt",
				"add: folder1/folder2/sculpture-stone-face-head-888027.jpg",
				"add: hello.csv",
				"rem: a_file.txt",
			}
			for i, out := range strings.Split(strings.TrimSpace(stdout), "\n") {
				be.Equal(t, expect[i], out)
			}
		})

	})

	t.Run("new object", func(t *testing.T) {
		stageDir := t.TempDir()
		stagePath := filepath.Join(stageDir, "my-stage.json")
		newID := "ark:xyz/678"
		// create the stage file
		cmd := []string{"stage", "new", "--file", stagePath, "--id", newID}
		testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, stagePath, stderr)
		})
		// no changes for fress stage
		cmd = []string{"stage", "diff", "--file", stagePath}
		testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.Equal(t, "", stdout)
		})
		// stage new content
		cmd = []string{"stage", "add", "--file", stagePath, contentFixture}
		testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
		})
		// show diff
		cmd = []string{"stage", "diff", "--file", stagePath}
		testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			expect := []string{
				"add: folder1/file.txt",
				"add: folder1/folder2/.hidden_dir/note.txt",
				"add: folder1/folder2/.hidden_file",
				"add: folder1/folder2/file2.txt",
				"add: folder1/folder2/sculpture-stone-face-head-888027.jpg",
				"add: hello.csv",
			}
			for i, out := range strings.Split(strings.TrimSpace(stdout), "\n") {
				be.Equal(t, expect[i], out)
			}
		})

	})
}

func TestStage_Status(t *testing.T) {
	_, fixtures := testutil.TempDirTestData(t,
		`testdata/content-fixture`,
		`testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root`,
	)
	contentFixture := fixtures[0]
	env := map[string]string{"OCFL_ROOT": fixtures[1]}
	t.Run("new object", func(t *testing.T) {
		newID := "ark:xyz/678"
		stageDir := t.TempDir()
		stagePath := filepath.Join(stageDir, "my-stage.json")
		cmd := []string{"stage", "new", "--file", stagePath, "--id", newID}
		testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, stagePath, stderr)
		})
		cmd = []string{"stage", "status", "--file", stagePath}
		testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, newID+" (v1)", stdout)
		})
		cmd = []string{"stage", "add", "--file", stagePath, contentFixture}
		testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
		})
		cmd = []string{"stage", "status", "--file", stagePath}
		testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, newID+" (v1)", stdout)
			be.In(t, "stage has changes", stdout)
			be.In(t, "\nupstream:     stage head matches upstream object\n", stdout)
		})
		// report errors
		be.NilErr(t, os.Remove(filepath.Join(contentFixture, "hello.csv")))
		cmd = []string{"stage", "status", "--file", stagePath}
		testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.True(t, errors.Is(err, ocfl.ErrMissingContent))
			be.In(t, "missing:  hello.csv ("+filepath.Join(contentFixture, "hello.csv")+")", stdout)
			be.In(t, "the stage can't be committed", stderr)
			be.In(t, "hello.csv", stderr)
		})
	})
}

// 'stage status' checks whether the object has changed in the storage root
// since the stage was created.
func TestStage_StatusUpstream(t *testing.T) {
	const objID = "ark:xyz/678"
	// setup creates a storage root and a directory of files to commit. If
	// versions isn't zero, it commits that many versions of the object.
	// Then it creates a stage for the object with a new file.
	setup := func(t *testing.T, versions int) (env map[string]string, stagePath, dir string) {
		tmpDir := t.TempDir()
		dir = filepath.Join(tmpDir, "content")
		be.NilErr(t, os.Mkdir(dir, 0o755))
		be.NilErr(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
		env = map[string]string{"OCFL_ROOT": filepath.Join(tmpDir, "ocfl")}
		runOK(t, env, "init-root", "--layout", "0003-hash-and-id-n-tuple-storage-layout")
		for i := range versions {
			commitVersion(t, env, objID, fmt.Sprintf("version %d", i+1))
		}
		stagePath = filepath.Join(tmpDir, "stage.json")
		runOK(t, env, "stage", "new", "-f", stagePath, "--id", objID)
		staged := filepath.Join(tmpDir, "staged.txt")
		be.NilErr(t, os.WriteFile(staged, []byte("staged"), 0o644))
		runOK(t, env, "stage", "add", "-f", stagePath, staged)
		return env, stagePath, dir
	}
	statusArgs := func(stagePath string) []string {
		return []string{"stage", "status", "-f", stagePath}
	}
	// stale checks the result of 'stage status' for a stale draft stage,
	// with the upstream line line.
	stale := func(t *testing.T, stagePath, line string) func(err error, stdout, stderr string) {
		return func(err error, stdout, stderr string) {
			t.Helper()
			be.True(t, errors.Is(err, ocfl.ErrUpdateConflict))
			be.In(t, "\nupstream:     "+line+"\n", stdout)
			be.In(t, "the stage for "+objID+" ", stderr)
			be.In(t, "is stale: the object has changed since the stage was created", stderr)
			be.In(t, "the stage can't be committed: delete it and create a new one", stderr)
			be.In(t, "to delete it: ocfl stage delete -f "+stagePath+"\n", stderr)
		}
	}

	t.Run("unchanged", func(t *testing.T) {
		env, stagePath, _ := setup(t, 1)
		out := runOK(t, env, statusArgs(stagePath)...)
		be.In(t, "\nupstream:     stage head matches upstream object\n", out)
		be.False(t, strings.Contains(out, "stale"))
	})

	t.Run("new version committed", func(t *testing.T) {
		env, stagePath, _ := setup(t, 1)
		commitVersion(t, env, objID, "other commit")
		testutil.RunCLI(statusArgs(stagePath), env, stale(t, stagePath, "v2 (changed since the stage was created)"))
	})

	t.Run("object deleted", func(t *testing.T) {
		env, stagePath, _ := setup(t, 1)
		runOK(t, env, "delete", "--id", objID, "--yes")
		testutil.RunCLI(statusArgs(stagePath), env, stale(t, stagePath, "no object (deleted since the stage was created)"))
	})

	t.Run("object created", func(t *testing.T) {
		env, stagePath, _ := setup(t, 0)
		commitVersion(t, env, objID, "other commit")
		testutil.RunCLI(statusArgs(stagePath), env, stale(t, stagePath, "v1 (created since the stage was created)"))
	})

	t.Run("another commit to the new object interrupted", func(t *testing.T) {
		env, stagePath, dir := setup(t, 0)
		// the other commit is interrupted before the root inventory sidecar
		// is written
		otherStage := filepath.Join(t.TempDir(), "other.json")
		runOK(t, env, "stage", "new", "-f", otherStage, "--id", objID)
		runOK(t, env, "stage", "add", "-f", otherStage, dir)
		commit := []string{"stage", "commit", "-f", otherStage, "-m", "other", "-n", "name"}
		testutil.RunCLIInterrupt(commit, env, "write inventory.json.sha512", func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, context.Canceled))
		})
		testutil.RunCLI(statusArgs(stagePath), env, func(err error, stdout, stderr string) {
			stale(t, stagePath, "head unknown (object directory created since the stage was created)")(err, stdout, stderr)
			be.In(t, "if a commit to the object was interrupted, resume or revert it using its stage file", stderr)
		})
	})

	t.Run("object replaced", func(t *testing.T) {
		env, stagePath, _ := setup(t, 2)
		runOK(t, env, "delete", "--id", objID, "--yes")
		commitVersion(t, env, objID, "other commit")
		testutil.RunCLI(statusArgs(stagePath), env, stale(t, stagePath, "v1 (replaced since the stage was created)"))
	})

	t.Run("check fails", func(t *testing.T) {
		env, stagePath, _ := setup(t, 1)
		be.NilErr(t, os.Rename(env["OCFL_ROOT"], env["OCFL_ROOT"]+"-moved"))
		testutil.RunCLI(statusArgs(stagePath), env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.False(t, errors.Is(err, ocfl.ErrUpdateConflict))
			// the local details are printed first
			be.In(t, "object:       "+objID+" (v2)\n", stdout)
			be.In(t, "\nupstream:     unknown (couldn't check the object)\n", stdout)
			be.In(t, "stage has changes to commit", stdout)
			be.In(t, "couldn't check the object in the storage root: ", stderr)
			be.In(t, "to skip the check: ocfl stage status -f "+stagePath+" --offline", stderr)
		})
		out := runOK(t, env, "stage", "status", "-f", stagePath, "--offline")
		be.In(t, "\nupstream:     not checked (--offline)\n", out)
	})

	t.Run("stale with missing staged file", func(t *testing.T) {
		env, stagePath, dir := setup(t, 1)
		commitVersion(t, env, objID, "other commit")
		be.NilErr(t, os.Remove(filepath.Join(filepath.Dir(dir), "staged.txt")))
		testutil.RunCLI(statusArgs(stagePath), env, func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, ocfl.ErrUpdateConflict))
			be.True(t, errors.Is(err, ocfl.ErrMissingContent))
			be.In(t, "is stale", stderr)
			be.In(t, "content for 1 file(s) has changed or is missing: staged.txt", stderr)
		})
	})
}

// 'stage status' works while another command has the stage file locked, but
// it doesn't save refreshed file stats.
func TestStage_StatusLocked(t *testing.T) {
	_, fixtures := testutil.TempDirTestData(t, `testdata/content-fixture`)
	contentDir := fixtures[0]
	stagePath := filepath.Join(t.TempDir(), "stage.json")
	lockPath := stagePath + ".lock"
	env := map[string]string{"OCFL_ROOT": filepath.Join(t.TempDir(), "ocfl")}
	runOK(t, env, "init-root", "--layout", "0003-hash-and-id-n-tuple-storage-layout")
	runOK(t, env, "stage", "new", "-f", stagePath, "--id", "object-01")
	runOK(t, env, "stage", "add", "-f", stagePath, contentDir)
	// a staged file's stat changes, so status refreshes it
	mtime := time.Now().Add(-time.Hour)
	be.NilErr(t, os.Chtimes(filepath.Join(contentDir, "hello.csv"), mtime, mtime))
	before, err := os.ReadFile(stagePath)
	be.NilErr(t, err)
	be.NilErr(t, os.WriteFile(lockPath, []byte("pid 1 on other-host\n"), 0o644))
	out := runOK(t, env, "stage", "status", "-f", stagePath)
	be.In(t, "stage has changes to commit", out)
	after, err := os.ReadFile(stagePath)
	be.NilErr(t, err)
	be.Equal(t, string(before), string(after))
	_, err = os.Stat(lockPath)
	be.NilErr(t, err)
	// once the lock is released, the refreshed stats are saved
	be.NilErr(t, os.Remove(lockPath))
	runOK(t, env, "stage", "status", "-f", stagePath)
	after, err = os.ReadFile(stagePath)
	be.NilErr(t, err)
	be.Unequal(t, string(before), string(after))
	noLocks(t, filepath.Dir(stagePath))
}

// Commands that change a stage file lock it, so they fail if another command
// has it locked, and they release the lock when they're done.
func TestStage_Locked(t *testing.T) {
	tmpDir, fixtures := testutil.TempDirTestData(t, `testdata/content-fixture`)
	contentDir := fixtures[0]
	stageCache := filepath.Join(tmpDir, "stage-cache")
	env := map[string]string{
		"OCFL_ROOT":        filepath.Join(tmpDir, "ocfl"),
		"OCFL_STAGE_CACHE": stageCache,
	}
	runOK(t, env, "init-root", "--layout", "0003-hash-and-id-n-tuple-storage-layout")
	id := strings.TrimSpace(runOK(t, env, "stage", "new", "--id", "object-01"))
	noLocks(t, stageCache)
	runOK(t, env, "stage", "add", "-s", id, contentDir)
	noLocks(t, stageCache)
	runOK(t, env, "stage", "rm", "-s", id, "hello.csv")
	noLocks(t, stageCache)
	ids := stageIDs(t, stageCache)
	be.Equal(t, 1, len(ids))
	stagePath := filepath.Join(stageCache, ids[0]+".json")
	lockPath := stagePath + ".lock"

	// a stage for another object, whose stage file doesn't exist, but whose
	// lock file does
	newID := strings.TrimSpace(runOK(t, env, "stage", "new", "--id", "object-02"))
	newIDs := stageIDs(t, stageCache)
	be.Equal(t, 2, len(newIDs))
	newPath := stagePath
	for _, other := range newIDs {
		if other != ids[0] {
			newPath = filepath.Join(stageCache, other+".json")
		}
	}
	runOK(t, env, "stage", "delete", "-s", newID)
	newLockPath := newPath + ".lock"

	t.Run("writers fail", func(t *testing.T) {
		for _, lock := range []string{lockPath, newLockPath} {
			be.NilErr(t, os.WriteFile(lock, []byte("pid 1 on other-host\n"), 0o644))
			defer os.Remove(lock)
		}
		before, err := os.ReadFile(stagePath)
		be.NilErr(t, err)
		// the commands wait for the lock, so they run in parallel
		t.Run("commands", func(t *testing.T) {
			for _, tt := range []struct {
				name  string
				args  []string
				lock  string
				stage string // the stage's ID in the command to clear the lock
			}{
				{"stage add", []string{"stage", "add", "-s", id, contentDir}, lockPath, id},
				{"stage rm", []string{"stage", "rm", "-s", id, "folder1", "-r"}, lockPath, id},
				{"stage commit", []string{"stage", "commit", "-s", id, "-m", "message", "-n", "name"}, lockPath, id},
				{"stage commit --resume", []string{"stage", "commit", "-s", id, "--resume"}, lockPath, id},
				{"stage commit --revert", []string{"stage", "commit", "-s", id, "--revert"}, lockPath, id},
				{"stage delete", []string{"stage", "delete", "-s", id, "--force"}, lockPath, id},
				// creating the stage file fails
				{"stage new", []string{"stage", "new", "--id", "object-02"}, newLockPath, newID},
				{"commit", []string{"commit", contentDir, "--id", "object-02", "-m", "message", "-n", "name"}, newLockPath, newID},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					testutil.RunCLI(tt.args, env, func(err error, stdout, stderr string) {
						if !errors.Is(err, stage.ErrLocked) {
							t.Fatalf("ocfl %s: want ErrLocked, got: %v", strings.Join(tt.args, " "), err)
						}
						be.In(t, "locked by another command (pid 1 on other-host)", stderr)
						be.In(t, tt.lock+" exists", stderr)
						be.In(t, "\n  if no other ocfl command is using the stage, clear the lock: ocfl stage delete -s "+tt.stage+" --lock\n", stderr)
						be.False(t, strings.Contains(stderr, "already exists"))
					})
				})
			}
		})
		// no stage file is created for the other object
		_, err = os.Stat(newPath)
		be.True(t, errors.Is(err, fs.ErrNotExist))
		_, err = os.Stat(newLockPath)
		be.NilErr(t, err)
		after, err := os.ReadFile(stagePath)
		be.NilErr(t, err)
		be.Equal(t, string(before), string(after))
		// the lock isn't removed
		_, err = os.Stat(lockPath)
		be.NilErr(t, err)
		// commands that don't change the stage work
		be.In(t, "stage has changes", runOK(t, env, "stage", "status", "-s", id))
		be.In(t, "folder1/file.txt", runOK(t, env, "stage", "ls", "-s", id))
	})

	t.Run("released after errors", func(t *testing.T) {
		testutil.RunCLI([]string{"stage", "rm", "-s", id, "missing.txt"}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
		})
		noLocks(t, stageCache)
		testutil.RunCLI([]string{"stage", "commit", "-s", id, "--resume"}, env, func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, stage.ErrNotFrozen))
		})
		noLocks(t, stageCache)
		testutil.RunCLIInterrupt([]string{"stage", "commit", "-s", id, "-m", "message", "-n", "name"}, env, "starting object update", func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, context.Canceled))
		})
		noLocks(t, stageCache)
		testutil.RunCLI([]string{"stage", "add", "-s", id, contentDir}, env, func(err error, stdout, stderr string) {
			be.In(t, "the stage can't be changed", stderr)
		})
		noLocks(t, stageCache)
		runOK(t, env, "stage", "commit", "-s", id, "--revert")
		noLocks(t, stageCache)
		runOK(t, env, "stage", "commit", "-s", id, "-m", "message", "-n", "name")
		noLocks(t, stageCache)
		be.Equal(t, 0, len(stageIDs(t, stageCache)))
	})
}

// 'stage delete --lock' removes only the lock file of a stage, so that a lock
// left behind by a command that was killed can be cleared.
func TestStage_DeleteLock(t *testing.T) {
	tmpDir, fixtures := testutil.TempDirTestData(t, `testdata/content-fixture`)
	contentDir := fixtures[0]
	stageCache := filepath.Join(tmpDir, "stage-cache")
	env := map[string]string{
		"OCFL_ROOT":        filepath.Join(tmpDir, "ocfl"),
		"OCFL_STAGE_CACHE": stageCache,
	}
	runOK(t, env, "init-root", "--layout", "0003-hash-and-id-n-tuple-storage-layout")
	id := strings.TrimSpace(runOK(t, env, "stage", "new", "--id", "object-01"))
	stagePath := filepath.Join(stageCache, stageIDs(t, stageCache)[0]+".json")
	lockPath := stagePath + ".lock"

	// unchanged checks that the stage file has the contents before, and that
	// it and dirFiles are the only files in its directory.
	unchanged := func(t *testing.T, before []byte, dirFiles ...string) {
		t.Helper()
		after, err := os.ReadFile(stagePath)
		be.NilErr(t, err)
		be.Equal(t, string(before), string(after))
		entries, err := os.ReadDir(stageCache)
		be.NilErr(t, err)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		be.AllEqual(t, append([]string{filepath.Base(stagePath)}, dirFiles...), names)
	}

	t.Run("stale lock", func(t *testing.T) {
		before, err := os.ReadFile(stagePath)
		be.NilErr(t, err)
		be.NilErr(t, os.WriteFile(lockPath, []byte("pid 1 on other-host, since 2026-01-01T00:00:00Z\n"), 0o644))
		var clear string
		testutil.RunCLI([]string{"stage", "add", "-s", id, contentDir}, env, func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, stage.ErrLocked))
			clear = "ocfl stage delete -s " + id + " --lock"
			be.In(t, "clear the lock: "+clear, stderr)
		})
		out := runOK(t, env, strings.Fields(clear)[1:]...)
		be.Equal(t, "removed lock file "+lockPath+" (held by pid 1 on other-host, since 2026-01-01T00:00:00Z)\n", out)
		unchanged(t, before)
		// a later write succeeds
		runOK(t, env, "stage", "add", "-s", id, contentDir)
		noLocks(t, stageCache)
		be.In(t, "hello.csv", runOK(t, env, "stage", "ls", "-s", id))
	})

	t.Run("held lock", func(t *testing.T) {
		before, err := os.ReadFile(stagePath)
		be.NilErr(t, err)
		f, err := stage.OpenLocked(stagePath)
		be.NilErr(t, err)
		out := runOK(t, env, "stage", "delete", "-s", id, "--lock")
		be.In(t, "removed lock file "+lockPath+fmt.Sprintf(" (held by pid %d on ", os.Getpid()), out)
		unchanged(t, before)
		// the command that held the lock can't release it
		be.Nonzero(t, f.Close())
	})

	t.Run("unknown holder", func(t *testing.T) {
		before, err := os.ReadFile(stagePath)
		be.NilErr(t, err)
		be.NilErr(t, os.WriteFile(lockPath, nil, 0o644))
		out := runOK(t, env, "stage", "delete", "-f", stagePath, "--lock")
		be.Equal(t, "removed lock file "+lockPath+" (held by unknown process)\n", out)
		unchanged(t, before)
	})

	t.Run("no lock", func(t *testing.T) {
		before, err := os.ReadFile(stagePath)
		be.NilErr(t, err)
		for _, args := range [][]string{
			{"stage", "delete", "-s", id, "--lock"},
			{"stage", "delete", "-f", stagePath, "--lock"},
		} {
			out := runOK(t, env, args...)
			be.Equal(t, "the stage isn't locked: there's no lock file "+lockPath+"\n", out)
		}
		unchanged(t, before)
	})

	t.Run("lock without a stage file", func(t *testing.T) {
		before, err := os.ReadFile(stagePath)
		be.NilErr(t, err)
		// a command creating a stage for another object was killed before it
		// created the stage file
		newID := strings.TrimSpace(runOK(t, env, "stage", "new", "--id", "object-02"))
		var otherLock string
		for _, other := range stageIDs(t, stageCache) {
			if other+".json" != filepath.Base(stagePath) {
				otherLock = filepath.Join(stageCache, other+".json.lock")
			}
		}
		runOK(t, env, "stage", "delete", "-s", newID)
		clear := "ocfl stage delete -s " + newID + " --lock"
		be.NilErr(t, os.WriteFile(otherLock, []byte("pid 1 on other-host\n"), 0o644))
		testutil.RunCLI([]string{"stage", "new", "--id", "object-02"}, env, func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, stage.ErrLocked))
			be.In(t, "clear the lock: "+clear, stderr)
		})
		out := runOK(t, env, strings.Fields(clear)[1:]...)
		be.Equal(t, "removed lock file "+otherLock+" (held by pid 1 on other-host)\n", out)
		unchanged(t, before)
		runOK(t, env, "stage", "new", "--id", "object-02")
		noLocks(t, stageCache)
	})

	// the lock of a running command is cleared, and another command locks
	// the stage before the first one finishes
	t.Run("broken lock", func(t *testing.T) {
		var other *stage.File
		breakLock := func() {
			runOK(t, env, "stage", "delete", "-s", id, "--lock")
			var err error
			other, err = stage.OpenLocked(stagePath)
			be.NilErr(t, err)
		}
		newDir := t.TempDir()
		be.NilErr(t, os.WriteFile(filepath.Join(newDir, "new.txt"), []byte("new"), 0o644))
		args := []string{"stage", "add", "-s", id, newDir}
		testutil.RunCLIOnLog(context.Background(), args, env, "file added", breakLock, func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, stage.ErrLockBroken))
			be.In(t, "lock was removed while the command held it", stderr)
			be.In(t, "\n  another command may have changed the stage at the same time: check it with: ocfl stage status -s "+id+"\n", stderr)
		})
		be.True(t, other != nil)
		// the other command's lock is kept
		_, err := os.Stat(lockPath)
		be.NilErr(t, err)
		testutil.RunCLI([]string{"stage", "rm", "-s", id, "hello.csv"}, env, func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, stage.ErrLocked))
		})
		be.NilErr(t, other.Close())
		noLocks(t, stageCache)
	})

	// the stage ID in the command to clear a lock is unique among stages
	// with stage files or lock files
	t.Run("short ID with a lock-only stage", func(t *testing.T) {
		full := strings.TrimSuffix(filepath.Base(stagePath), ".json")
		// another stage, with only a lock file, whose ID shares the short ID
		c := "a"
		if full[7] == 'a' {
			c = "b"
		}
		otherLock := filepath.Join(stageCache, full[:7]+c+strings.Repeat("a", len(full)-8)+".json.lock")
		be.NilErr(t, os.WriteFile(otherLock, []byte("pid 2 on other-host\n"), 0o644))
		defer os.Remove(otherLock)
		be.NilErr(t, os.WriteFile(lockPath, []byte("pid 1 on other-host\n"), 0o644))
		clear := "ocfl stage delete -s " + full[:8] + " --lock"
		testutil.RunCLI([]string{"stage", "add", "-s", id, contentDir}, env, func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, stage.ErrLocked))
			be.In(t, "clear the lock: "+clear+"\n", stderr)
		})
		out := runOK(t, env, strings.Fields(clear)[1:]...)
		be.Equal(t, "removed lock file "+lockPath+" (held by pid 1 on other-host)\n", out)
		// the other stage's lock is kept
		_, err := os.Stat(otherLock)
		be.NilErr(t, err)
	})

	t.Run("flags", func(t *testing.T) {
		be.NilErr(t, os.WriteFile(lockPath, []byte("pid 1 on other-host\n"), 0o644))
		defer os.Remove(lockPath)
		testutil.RunCLI([]string{"stage", "delete", "-s", id, "--lock", "--force"}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "--force and --lock can't be used together", stderr)
		})
		testutil.RunCLI([]string{"stage", "delete", "--lock"}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "a stage is required", stderr)
		})
		testutil.RunCLI([]string{"stage", "delete", "-s", "zzzzzzz", "--lock"}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "no stage", stderr)
		})
		// the lock and the stage are kept
		_, err := os.Stat(lockPath)
		be.NilErr(t, err)
		_, err = os.Stat(stagePath)
		be.NilErr(t, err)
	})

	// A command whose lock is removed after it removed the stage file
	// succeeds, with a warning: there's nothing left for another command to
	// have changed.
	t.Run("lock removed after the stage is deleted", func(t *testing.T) {
		otherID := strings.TrimSpace(runOK(t, env, "stage", "new", "--id", "object-03"))
		clearLock := func() { runOK(t, env, "stage", "delete", "-s", otherID, "--lock") }
		testutil.RunCLIOnLog(context.Background(), []string{"stage", "delete", "-s", otherID}, env, "stage deleted", clearLock, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, "the stage was deleted, but another command cleared the stage's lock while this one held it", stderr)
		})
		noLocks(t, stageCache)
		testutil.RunCLI([]string{"stage", "status", "-s", otherID}, env, func(err error, stdout, stderr string) {
			be.In(t, "no stage", stderr)
		})
	})

	// A commit whose lock is removed while the object is updated finishes,
	// but it doesn't remove the stage file, which another command may have
	// changed.
	t.Run("lock removed during commit", func(t *testing.T) {
		otherID := strings.TrimSpace(runOK(t, env, "stage", "new", "--id", "object-04"))
		runOK(t, env, "stage", "add", "-s", otherID, contentDir)
		clearLock := func() { runOK(t, env, "stage", "delete", "-s", otherID, "--lock") }
		args := []string{"stage", "commit", "-s", otherID, "-m", "message", "-n", "name"}
		testutil.RunCLIOnLog(context.Background(), args, env, "object update complete", clearLock, func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, stage.ErrNotRemoved))
			be.True(t, errors.Is(err, stage.ErrLockBroken))
			be.In(t, "the commit is complete, but the stage file couldn't be removed", stderr)
			be.In(t, "\n  to remove it: ocfl stage commit -s "+otherID+" --resume\n", stderr)
			be.In(t, "\n  another command may have changed the stage at the same time: check it with: ocfl stage status -s "+otherID+"\n", stderr)
		})
		noLocks(t, stageCache)
		be.In(t, "commit is complete", runOK(t, env, "stage", "status", "-s", otherID))
		runOK(t, env, "stage", "commit", "-s", otherID, "--resume")
		noLocks(t, stageCache)
		be.Equal(t, "", runOK(t, env, "validate", "--id", "object-04"))
	})

	// A command that can't release its lock says how to clear it. This
	// subtest sets the process's umask: TestStage_DeleteLock isn't parallel.
	t.Run("lock not released, frozen stage", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root can read a lock file without read permission")
		}
		runOK(t, env, "stage", "add", "-s", id, contentDir)
		testutil.RunCLIInterrupt([]string{"stage", "commit", "-s", id, "-m", "message", "-n", "name"}, env, "starting object update", func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, context.Canceled))
		})
		// the lock file can't be read, so it isn't removed
		testutil.SetUmask(t, 0o444)
		testutil.RunCLI([]string{"stage", "add", "-s", id, contentDir}, env, func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, stage.ErrNotUnlocked))
			be.In(t, "the stage can't be changed", stderr)
			be.In(t, "the stage file's lock couldn't be released: ", stderr)
			be.In(t, "\n  to clear the lock: ocfl stage delete -s "+id+" --lock\n", stderr)
		})
		testutil.SetUmask(t, 0o022)
		be.In(t, "removed lock file", runOK(t, env, "stage", "delete", "-s", id, "--lock"))
		runOK(t, env, "stage", "commit", "-s", id, "--revert")
		noLocks(t, stageCache)
	})
}

// 'stage status' reports staged files whose content has changed since they
// were added, like 'git status'. Files whose stat changed without a change to
// their content aren't reported.
func TestStage_StatusChangedFiles(t *testing.T) {
	// setup creates a stage for a new object with the files in a new
	// directory. a.txt and b.txt have the same content.
	setup := func(t *testing.T) (env map[string]string, stagePath, dir string) {
		tmpDir := t.TempDir()
		dir = filepath.Join(tmpDir, "content")
		be.NilErr(t, os.Mkdir(dir, 0755))
		for name, data := range map[string]string{"a.txt": "same", "b.txt": "same", "c.txt": "other"} {
			be.NilErr(t, os.WriteFile(filepath.Join(dir, name), []byte(data), 0644))
		}
		stagePath = filepath.Join(tmpDir, "stage.json")
		env = map[string]string{"OCFL_ROOT": filepath.Join(tmpDir, "ocfl")}
		runOK(t, env, "init-root", "--layout", "0003-hash-and-id-n-tuple-storage-layout")
		runOK(t, env, "stage", "new", "-f", stagePath, "--id", "ark:xyz/678")
		runOK(t, env, "stage", "add", "-f", stagePath, dir)
		return env, stagePath, dir
	}
	statusArgs := func(stagePath string) []string {
		return []string{"stage", "status", "-f", stagePath}
	}

	t.Run("touched file", func(t *testing.T) {
		env, stagePath, dir := setup(t)
		before, err := os.ReadFile(stagePath)
		be.NilErr(t, err)
		later := time.Now().Add(time.Hour)
		be.NilErr(t, os.Chtimes(filepath.Join(dir, "c.txt"), later, later))
		out := runOK(t, env, statusArgs(stagePath)...)
		be.False(t, strings.Contains(out, "changed since"))
		// the file's new stat is saved
		after, err := os.ReadFile(stagePath)
		be.NilErr(t, err)
		be.Unequal(t, string(before), string(after))
	})

	for _, tt := range []struct {
		desc string
		// re-add the changed file, rather than the directory
		addFile bool
	}{
		{desc: "re-add directory"},
		{desc: "re-add file", addFile: true},
	} {
		t.Run("modified file, "+tt.desc, func(t *testing.T) {
			env, stagePath, dir := setup(t)
			changed := filepath.Join(dir, "c.txt")
			be.NilErr(t, os.WriteFile(changed, []byte("changed"), 0644))
			testutil.RunCLI(statusArgs(stagePath), env, func(err error, stdout, stderr string) {
				be.True(t, errors.Is(err, ocfl.ErrMissingContent))
				be.In(t, "modified: c.txt ("+changed+")", stdout)
				be.In(t, "ocfl stage add", stdout)
				be.In(t, "content for 1 file(s) has changed or is missing: c.txt", stderr)
			})
			// commit fails without finalizing the stage
			commit := []string{"stage", "commit", "-f", stagePath, "-m", "msg", "-n", "name", "-e", "a@b.c"}
			testutil.RunCLI(commit, env, func(err error, stdout, stderr string) {
				be.True(t, errors.Is(err, ocfl.ErrMissingContent))
				be.In(t, "the stage can't be committed", stderr)
			})
			addPath := dir
			if tt.addFile {
				addPath = changed
			}
			runOK(t, env, "stage", "add", "-f", stagePath, addPath)
			out := runOK(t, env, statusArgs(stagePath)...)
			be.False(t, strings.Contains(out, "changed since"))
			be.Equal(t, "a.txt\nb.txt\nc.txt\n", runOK(t, env, "stage", "ls", "-f", stagePath))
			runOK(t, env, commit...)
		})
	}

	t.Run("modified file with duplicate content", func(t *testing.T) {
		env, stagePath, dir := setup(t)
		be.NilErr(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed"), 0644))
		// the change is reported, but b.txt has the staged content
		out := runOK(t, env, statusArgs(stagePath)...)
		be.In(t, "modified: a.txt", out)
		runOK(t, env, "stage", "commit", "-f", stagePath, "-m", "msg", "-n", "name", "-e", "a@b.c")
		be.Equal(t, "a.txt\nb.txt\nc.txt\n", runOK(t, env, "ls", "--id", "ark:xyz/678"))
		be.Equal(t, "", runOK(t, env, "validate", "--id", "ark:xyz/678"))
	})

	t.Run("check-stat minimal", func(t *testing.T) {
		env, stagePath, dir := setup(t)
		be.NilErr(t, os.WriteFile(filepath.Join(dir, "c.txt"), []byte("changed"), 0644))
		testutil.RunCLI([]string{"stage", "--check-stat", "minimal", "status", "-f", stagePath}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "modified: c.txt", stdout)
		})
		env["OCFL_CHECK_STAT"] = "invalid"
		testutil.RunCLI(statusArgs(stagePath), env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "check-stat", stderr)
		})
	})
}

func TestStage_CommitInterrupted(t *testing.T) {
	const (
		objID = "ark://my-object-01"
		name  = "Mr. Dibbs"
		email = "dibbs@mr.com"
		// the log message before the object is changed
		interruptAt = "starting object update"
	)
	// setup creates a storage root and a stage file for a new object with a
	// copy of the content fixture.
	setup := func(t *testing.T) (env map[string]string, stagePath, contentDir string) {
		tmpDir, fixtures := testutil.TempDirTestData(t, `testdata/content-fixture`)
		contentDir = fixtures[0]
		stagePath = filepath.Join(tmpDir, "stage.json")
		env = map[string]string{"OCFL_ROOT": filepath.Join(tmpDir, "ocfl")}
		runOK(t, env, "init-root", "--layout", "0003-hash-and-id-n-tuple-storage-layout")
		runOK(t, env, "stage", "new", "-f", stagePath, "--id", objID)
		runOK(t, env, "stage", "add", "-f", stagePath, contentDir)
		return env, stagePath, contentDir
	}
	commitArgs := func(stagePath, msg string) []string {
		return []string{"stage", "commit", "-f", stagePath, "-m", msg, "-n", name, "-e", email}
	}
	// interrupt runs args, interrupting the commit of version head before the
	// object is changed, and checks the result.
	interrupt := func(t *testing.T, env map[string]string, stagePath string, args []string, head string) {
		t.Helper()
		testutil.RunCLIInterrupt(args, env, interruptAt, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.True(t, errors.Is(err, context.Canceled))
			be.In(t, "commit of "+objID+" ("+head+") was interrupted", stderr)
			be.In(t, "object path: ", stderr)
			be.In(t, "to resume: ocfl stage commit -f "+stagePath+" --resume\n", stderr)
			be.In(t, "to revert: ocfl stage commit -f "+stagePath+" --revert", stderr)
		})
		s := readStage(t, stagePath)
		be.True(t, s.Frozen())
	}

	t.Run("resume", func(t *testing.T) {
		env, stagePath, _ := setup(t)
		interrupt(t, env, stagePath, commitArgs(stagePath, "first commit"), "v1")
		runOK(t, env, "stage", "commit", "-f", stagePath, "--resume")
		_, err := os.Stat(stagePath)
		be.True(t, errors.Is(err, fs.ErrNotExist))
		be.Equal(t, "", runOK(t, env, "validate", "--id", objID))
		// the saved version metadata is used
		log := runOK(t, env, "log", "--id", objID)
		be.In(t, "first commit", log)
		be.In(t, name, log)
	})

	t.Run("commit finalized stage", func(t *testing.T) {
		env, stagePath, _ := setup(t)
		interrupt(t, env, stagePath, commitArgs(stagePath, "first commit"), "v1")
		for _, args := range [][]string{
			{"stage", "commit", "-f", stagePath},
			commitArgs(stagePath, "other message"),
		} {
			testutil.RunCLI(args, env, func(err error, stdout, stderr string) {
				be.Nonzero(t, err)
				be.In(t, "a commit of "+objID+" (v1) is in progress", stderr)
				be.In(t, "stage commit -f "+stagePath+" --resume", stderr)
				be.In(t, "stage commit -f "+stagePath+" --revert", stderr)
			})
		}
		s := readStage(t, stagePath)
		be.True(t, s.Frozen())
	})

	t.Run("resume with flags", func(t *testing.T) {
		env, stagePath, _ := setup(t)
		interrupt(t, env, stagePath, commitArgs(stagePath, "first commit"), "v1")
		for _, flag := range [][]string{{"-m", "msg"}, {"-n", name}, {"--email", email}} {
			args := append([]string{"stage", "commit", "-f", stagePath, "--resume"}, flag...)
			testutil.RunCLI(args, env, func(err error, stdout, stderr string) {
				be.Nonzero(t, err)
				be.In(t, "--resume can't be used with", stderr)
			})
		}
		// name and email from the environment are ignored
		userEnv := maps.Clone(env)
		userEnv["OCFL_USER_NAME"] = "Someone Else"
		userEnv["OCFL_USER_EMAIL"] = "someone@example.com"
		runOK(t, userEnv, "stage", "commit", "-f", stagePath, "--resume")
		log := runOK(t, env, "log", "--id", objID)
		be.In(t, name, log)
		be.False(t, strings.Contains(log, "Someone Else"))
	})

	t.Run("resume and revert", func(t *testing.T) {
		env, stagePath, _ := setup(t)
		interrupt(t, env, stagePath, commitArgs(stagePath, "first commit"), "v1")
		testutil.RunCLI([]string{"stage", "commit", "-f", stagePath, "--resume", "--revert"}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "--resume", stderr)
			be.In(t, "--revert", stderr)
		})
		s := readStage(t, stagePath)
		be.True(t, s.Frozen())
	})

	t.Run("resume draft", func(t *testing.T) {
		env, stagePath, _ := setup(t)
		testutil.RunCLI([]string{"stage", "commit", "-f", stagePath, "--resume"}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "the stage has no commit in progress: nothing to resume", stderr)
		})
	})

	t.Run("revert", func(t *testing.T) {
		env, stagePath, _ := setup(t)
		interrupt(t, env, stagePath, commitArgs(stagePath, "first commit"), "v1")
		runOK(t, env, "stage", "commit", "-f", stagePath, "--revert")
		// the stage is a draft with the staged files
		s := readStage(t, stagePath)
		be.False(t, s.Frozen())
		be.Equal(t, 6, len(s.Update().NewState().PathMap()))
		// the new object was removed
		be.Equal(t, "", runOK(t, env, "ls"))
		// the stage can be changed and committed again
		runOK(t, env, "stage", "rm", "-f", stagePath, "hello.csv")
		runOK(t, env, commitArgs(stagePath, "second try")...)
		be.In(t, "second try", runOK(t, env, "log", "--id", objID))
		be.Equal(t, "", runOK(t, env, "validate", "--id", objID))
	})

	t.Run("revert with flags", func(t *testing.T) {
		env, stagePath, _ := setup(t)
		interrupt(t, env, stagePath, commitArgs(stagePath, "first commit"), "v1")
		for _, flag := range [][]string{{"-m", "msg"}, {"-n", name}, {"--email", email}} {
			args := append([]string{"stage", "commit", "-f", stagePath, "--revert"}, flag...)
			testutil.RunCLI(args, env, func(err error, stdout, stderr string) {
				be.Nonzero(t, err)
				be.In(t, "--revert can't be used with", stderr)
			})
		}
		// name and email from the environment are ignored
		userEnv := maps.Clone(env)
		userEnv["OCFL_USER_NAME"] = name
		userEnv["OCFL_USER_EMAIL"] = email
		runOK(t, userEnv, "stage", "commit", "-f", stagePath, "--revert")
	})

	t.Run("revert draft", func(t *testing.T) {
		env, stagePath, _ := setup(t)
		testutil.RunCLI([]string{"stage", "commit", "-f", stagePath, "--revert"}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "the stage has no commit in progress: nothing to revert", stderr)
		})
	})

	// the commit completes, but the stage file isn't removed. Afterwards,
	// the staged files may change: they aren't needed to finish the commit.
	for _, tt := range []struct {
		desc   string
		change func(name string) error
	}{
		{"", func(string) error { return nil }},
		{", staged file deleted", os.Remove},
		{", staged file modified", func(name string) error {
			return os.WriteFile(name, []byte("changed"), 0o644)
		}},
	} {
		t.Run("revert completed commit"+tt.desc, func(t *testing.T) {
			env, stagePath, contentDir := setup(t)
			interrupt(t, env, stagePath, commitArgs(stagePath, "first commit"), "v1")
			stageBytes, err := os.ReadFile(stagePath)
			be.NilErr(t, err)
			runOK(t, env, "stage", "commit", "-f", stagePath, "--resume")
			be.NilErr(t, os.WriteFile(stagePath, stageBytes, 0644))
			be.NilErr(t, tt.change(filepath.Join(contentDir, "hello.csv")))
			testutil.RunCLI([]string{"stage", "commit", "-f", stagePath, "--revert"}, env, func(err error, stdout, stderr string) {
				be.Nonzero(t, err)
				be.In(t, "is complete and can't be reverted", stderr)
				be.In(t, "stage commit -f "+stagePath+" --resume\n", stderr)
			})
			// resuming, as the revert error says, removes the stage file
			runOK(t, env, "stage", "commit", "-f", stagePath, "--resume")
			_, err = os.Stat(stagePath)
			be.True(t, errors.Is(err, fs.ErrNotExist))
			be.Equal(t, "", runOK(t, env, "validate", "--id", objID))
		})
	}

	t.Run("resume after content changed", func(t *testing.T) {
		env, stagePath, contentDir := setup(t)
		interrupt(t, env, stagePath, commitArgs(stagePath, "first commit"), "v1")
		be.NilErr(t, os.WriteFile(filepath.Join(contentDir, "hello.csv"), []byte("changed"), 0644))
		testutil.RunCLI([]string{"stage", "commit", "-f", stagePath, "--resume"}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.True(t, errors.Is(err, ocfl.ErrMissingContent))
			// the interrupted attempt may have changed the object
			be.In(t, "the object may be partially updated", stderr)
			be.False(t, strings.Contains(stderr, "before the object was changed"))
			be.In(t, "object path: ", stderr)
			be.In(t, "hello.csv", stderr)
			be.In(t, "--revert", stderr)
		})
		runOK(t, env, "stage", "commit", "-f", stagePath, "--revert")
	})

	t.Run("content changed during commit", func(t *testing.T) {
		env, stagePath, contentDir := setup(t)
		// with --check-stat=minimal, a change that keeps the size and mtime
		// isn't found until the content is copied.
		file := filepath.Join(contentDir, "hello.csv")
		info, err := os.Stat(file)
		be.NilErr(t, err)
		mtime := time.Now().Add(-time.Hour)
		be.NilErr(t, os.Chtimes(file, mtime, mtime))
		runOK(t, env, "stage", "add", "-f", stagePath, contentDir)
		be.NilErr(t, os.WriteFile(file, []byte(strings.Repeat("x", int(info.Size()))), 0644))
		be.NilErr(t, os.Chtimes(file, mtime, mtime))
		args := []string{"stage", "--check-stat", "minimal", "commit", "-f", stagePath, "-m", "first commit", "-n", name, "-e", email}
		testutil.RunCLI(args, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.True(t, errors.Is(err, ocfl.ErrContentChanged))
			// other content may have been copied before the change was found
			be.In(t, "the object may be partially updated", stderr)
			be.False(t, strings.Contains(stderr, "before the object was changed"))
			be.In(t, "object path: ", stderr)
			be.In(t, "--revert", stderr)
		})
		runOK(t, env, "stage", "commit", "-f", stagePath, "--revert")
	})

	t.Run("other stage commands", func(t *testing.T) {
		env, stagePath, contentDir := setup(t)
		interrupt(t, env, stagePath, commitArgs(stagePath, "first commit"), "v1")
		for _, args := range [][]string{
			{"stage", "add", "-f", stagePath, contentDir},
			{"stage", "rm", "-f", stagePath, "hello.csv"},
			{"stage", "delete", "-f", stagePath},
		} {
			testutil.RunCLI(args, env, func(err error, stdout, stderr string) {
				be.Nonzero(t, err)
				be.In(t, "a commit of "+objID+" (v1) is in progress", stderr)
				be.In(t, "--revert", stderr)
			})
		}
		out := runOK(t, env, "stage", "status", "-f", stagePath)
		be.In(t, "commit in progress: v1", out)
		be.In(t, "stage commit -f "+stagePath+" --revert", out)
		be.In(t, "hello.csv", runOK(t, env, "stage", "ls", "-f", stagePath))
	})

	// otherCommit commits contentDir to the object, with a separate stage
	// cache, as some other process might.
	otherCommit := func(t *testing.T, env map[string]string, contentDir string) {
		t.Helper()
		runOK(t, env, "commit", contentDir, "--id", objID, "-m", "other commit", "-n", name,
			"--stage-cache", t.TempDir())
	}

	t.Run("object changed before commit", func(t *testing.T) {
		env, stagePath, contentDir := setup(t)
		otherCommit(t, env, contentDir)
		testutil.RunCLI(commitArgs(stagePath, "first commit"), env, func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, ocfl.ErrUpdateConflict))
			be.In(t, "failed before the object was changed", stderr)
			be.In(t, "the stage can't be committed", stderr)
			be.In(t, "to delete it: ocfl stage delete -f "+stagePath+"\n", stderr)
		})
		// the stage is a draft again, so it can be deleted
		s := readStage(t, stagePath)
		be.False(t, s.Frozen())
		runOK(t, env, "stage", "delete", "-f", stagePath)
		be.Equal(t, "", runOK(t, env, "validate", "--id", objID))
		be.False(t, strings.Contains(runOK(t, env, "log", "--id", objID), "first commit"))
	})

	t.Run("object changed, draft not saved", func(t *testing.T) {
		env, stagePath, contentDir := setup(t)
		lock, unlock := testutil.LockDir(t, filepath.Dir(stagePath))
		// the object changes, and the stage file can't be changed, after
		// the frozen stage is saved
		changeObject := func() {
			otherCommit(t, env, contentDir)
			lock()
		}
		testutil.RunCLIOnLog(context.Background(), commitArgs(stagePath, "first commit"), env, interruptAt, changeObject, func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, ocfl.ErrUpdateConflict))
			be.In(t, "commit of "+objID+" (v1) failed before the object was changed: ", stderr)
			be.In(t, "\n  the stage file couldn't be restored: ", stderr)
			be.In(t, "\n  to delete it: ocfl stage delete -f "+stagePath+" --force\n", stderr)
			be.False(t, strings.Contains(stderr, "can't be resumed or reverted"))
			// the lock can't be released either
			be.In(t, "the stage file's lock couldn't be released: ", stderr)
			be.In(t, "\n  to clear the lock: ocfl stage delete -f "+stagePath+" --lock\n", stderr)
		})
		unlock()
		removeStaleLock(t, filepath.Dir(stagePath))
		be.True(t, readStage(t, stagePath).Frozen())
		runOK(t, env, "stage", "delete", "-f", stagePath, "--force")
		be.Equal(t, "", runOK(t, env, "validate", "--id", objID))
		be.False(t, strings.Contains(runOK(t, env, "log", "--id", objID), "first commit"))
	})

	t.Run("stage file not removed", func(t *testing.T) {
		env, stagePath, _ := setup(t)
		lock, unlock := testutil.LockDir(t, filepath.Dir(stagePath))
		testutil.RunCLIOnLog(context.Background(), commitArgs(stagePath, "first commit"), env, "object update complete", lock, func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, stage.ErrNotRemoved))
			be.In(t, "commit of "+objID+" (v1): the commit is complete, but the stage file couldn't be removed: ", stderr)
			be.Equal(t, 1, strings.Count(stderr, "couldn't be removed"))
			be.In(t, "to remove it: ocfl stage commit -f "+stagePath+" --resume\n", stderr)
			be.In(t, "the stage file's lock couldn't be released: ", stderr)
		})
		unlock()
		removeStaleLock(t, filepath.Dir(stagePath))
		runOK(t, env, "stage", "commit", "-f", stagePath, "--resume")
		_, err := os.Stat(stagePath)
		be.True(t, errors.Is(err, fs.ErrNotExist))
		be.Equal(t, "", runOK(t, env, "validate", "--id", objID))
	})

	t.Run("revert, draft not saved", func(t *testing.T) {
		env, stagePath, _ := setup(t)
		interrupt(t, env, stagePath, commitArgs(stagePath, "first commit"), "v1")
		lock, unlock := testutil.LockDir(t, filepath.Dir(stagePath))
		// the stage file can't be changed once it's locked
		testutil.RunCLIOnLog(context.Background(), []string{"stage", "commit", "-f", stagePath, "--revert"}, env, "reverting object update", lock, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "revert of "+objID+" (v1): the commit was reverted, but the stage file couldn't be saved: ", stderr)
			be.In(t, "don't resume it", stderr)
			be.In(t, "to finish reverting: ocfl stage commit -f "+stagePath+" --revert\n", stderr)
			be.In(t, "the stage file's lock couldn't be released: ", stderr)
		})
		unlock()
		removeStaleLock(t, filepath.Dir(stagePath))
		be.True(t, readStage(t, stagePath).Frozen())
		runOK(t, env, "stage", "commit", "-f", stagePath, "--revert")
		be.False(t, readStage(t, stagePath).Frozen())
		be.Equal(t, "", runOK(t, env, "ls"))
	})

	t.Run("object changed before resume", func(t *testing.T) {
		env, stagePath, contentDir := setup(t)
		interrupt(t, env, stagePath, commitArgs(stagePath, "first commit"), "v1")
		otherCommit(t, env, contentDir)
		for _, args := range [][]string{
			{"stage", "commit", "-f", stagePath, "--resume"},
			{"stage", "commit", "-f", stagePath, "--revert"},
		} {
			testutil.RunCLI(args, env, func(err error, stdout, stderr string) {
				be.True(t, errors.Is(err, ocfl.ErrUpdateConflict))
				be.In(t, "can't be", stderr)
				be.In(t, "then delete the stage: ocfl stage delete -f "+stagePath+" --force", stderr)
			})
		}
		runOK(t, env, "stage", "delete", "-f", stagePath, "--force")
		_, err := os.Stat(stagePath)
		be.True(t, errors.Is(err, fs.ErrNotExist))
		be.Equal(t, "", runOK(t, env, "validate", "--id", objID))
	})

	t.Run("status, commit in progress", func(t *testing.T) {
		env, stagePath, _ := setup(t)
		interrupt(t, env, stagePath, commitArgs(stagePath, "first commit"), "v1")
		out := runOK(t, env, "stage", "status", "-f", stagePath)
		be.In(t, "\nupstream:     commit of v1 isn't complete\n", out)
		be.In(t, "commit in progress: v1\n", out)
		be.In(t, "to resume: ocfl stage commit -f "+stagePath+" --resume\n", out)
		be.In(t, "to revert: ocfl stage commit -f "+stagePath+" --revert\n", out)
		be.False(t, strings.Contains(out, "stale"))
	})

	t.Run("status, object changed after commit interrupted", func(t *testing.T) {
		env, stagePath, contentDir := setup(t)
		interrupt(t, env, stagePath, commitArgs(stagePath, "first commit"), "v1")
		otherCommit(t, env, contentDir)
		testutil.RunCLI([]string{"stage", "status", "-f", stagePath}, env, func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, ocfl.ErrUpdateConflict))
			be.In(t, "\nupstream:     v1 (created since the stage was created)\n", stdout)
			be.False(t, strings.Contains(stdout, "commit in progress"))
			be.In(t, "commit of "+objID+" (v1) can't be resumed or reverted: ", stderr)
			be.In(t, "object path: ", stderr)
			be.In(t, "then delete the stage: ocfl stage delete -f "+stagePath+" --force", stderr)
		})
	})

	// completeCommit interrupts a commit, resumes it, and restores the
	// stage file, as if it couldn't be removed when the commit completed.
	completeCommit := func(t *testing.T) (env map[string]string, stagePath, contentDir string) {
		env, stagePath, contentDir = setup(t)
		interrupt(t, env, stagePath, commitArgs(stagePath, "first commit"), "v1")
		stageBytes, err := os.ReadFile(stagePath)
		be.NilErr(t, err)
		runOK(t, env, "stage", "commit", "-f", stagePath, "--resume")
		be.NilErr(t, os.WriteFile(stagePath, stageBytes, 0o644))
		return env, stagePath, contentDir
	}

	t.Run("status, commit complete", func(t *testing.T) {
		env, stagePath, contentDir := completeCommit(t)
		out := runOK(t, env, "stage", "status", "-f", stagePath)
		be.In(t, "\nupstream:     v1 (commit is complete)\n", out)
		be.In(t, "commit complete: v1 is committed, but the stage file wasn't removed\n", out)
		be.In(t, "to remove it: ocfl stage commit -f "+stagePath+" --resume\n", out)
		be.False(t, strings.Contains(out, "--revert"))
		// the staged files aren't needed
		be.NilErr(t, os.Remove(filepath.Join(contentDir, "hello.csv")))
		testutil.RunCLI([]string{"stage", "status", "-f", stagePath}, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			out = stdout
			be.False(t, strings.Contains(stdout, "hello.csv"))
			be.False(t, strings.Contains(stderr, "restore the files"))
		})
		// the command that status prints removes the stage file
		_, cmdLine, ok := strings.Cut(out, "to remove it: ")
		be.True(t, ok)
		cmdLine, _, _ = strings.Cut(cmdLine, "\n")
		args := strings.Fields(cmdLine)
		be.Equal(t, "ocfl", args[0])
		runOK(t, env, args[1:]...)
		_, err := os.Stat(stagePath)
		be.True(t, errors.Is(err, fs.ErrNotExist))
		be.Equal(t, "", runOK(t, env, "validate", "--id", objID))
	})

	t.Run("status, commit complete, offline", func(t *testing.T) {
		env, stagePath, contentDir := completeCommit(t)
		be.NilErr(t, os.Remove(filepath.Join(contentDir, "hello.csv")))
		testutil.RunCLI([]string{"stage", "status", "-f", stagePath, "--offline"}, env, func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, ocfl.ErrMissingContent))
			be.In(t, "\nupstream:     not checked (--offline)\n", stdout)
			be.In(t, "commit in progress or complete: v1 (storage root not checked)\n", stdout)
			be.In(t, "to resume: ocfl stage commit -f "+stagePath+" --resume\n", stdout)
			be.In(t, "to revert: ocfl stage commit -f "+stagePath+" --revert\n", stdout)
			be.In(t, "restore the files to resume the commit, or revert it; if the commit is already complete, --resume only removes the stage file", stderr)
		})
	})

	// the object is partially updated: everything is written except the
	// root inventory sidecar.
	partialUpdate := func(t *testing.T) (env map[string]string, stagePath string) {
		env, stagePath, contentDir := setup(t)
		runOK(t, env, commitArgs(stagePath, "first commit")...)
		runOK(t, env, "stage", "new", "-f", stagePath, "--id", objID)
		runOK(t, env, "stage", "add", "-f", stagePath, "--as", "new.csv", filepath.Join(contentDir, "hello.csv"))
		runOK(t, env, "stage", "rm", "-f", stagePath, "-r", "folder1")
		interrupt(t, env, stagePath, commitArgs(stagePath, "second commit"), "v2")
		applyWithoutRootSidecar(t, env["OCFL_ROOT"], stagePath)
		testutil.RunCLI([]string{"validate", "--id", objID}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
		})
		return env, stagePath
	}

	t.Run("status, partial update", func(t *testing.T) {
		env, stagePath := partialUpdate(t)
		// the stage's own partial update isn't a change to the object
		out := runOK(t, env, "stage", "status", "-f", stagePath)
		be.In(t, "\nupstream:     commit of v2 isn't complete\n", out)
		be.In(t, "commit in progress: v2\n", out)
	})

	t.Run("resume partial update", func(t *testing.T) {
		env, stagePath := partialUpdate(t)
		runOK(t, env, "stage", "commit", "-f", stagePath, "--resume")
		be.Equal(t, "", runOK(t, env, "validate", "--id", objID))
		be.Equal(t, "hello.csv\nnew.csv\n", runOK(t, env, "ls", "--id", objID))
	})

	t.Run("revert partial update", func(t *testing.T) {
		env, stagePath := partialUpdate(t)
		runOK(t, env, "stage", "commit", "-f", stagePath, "--revert")
		be.Equal(t, "", runOK(t, env, "validate", "--id", objID))
		log := runOK(t, env, "log", "--id", objID)
		be.In(t, "first commit", log)
		be.False(t, strings.Contains(log, "second commit"))
	})
}

// Stages are saved in the stage cache by default, and named by their stage IDs
// with -s.
func TestStage_Cache(t *testing.T) {
	const objID = "ark://my-object-01"
	// setup creates a storage root and a stage cache. The stage cache
	// is set with $OCFL_STAGE_CACHE.
	setup := func(t *testing.T) (env map[string]string, stageCache, contentDir string) {
		tmpDir, fixtures := testutil.TempDirTestData(t, `testdata/content-fixture`)
		stageCache = filepath.Join(tmpDir, "stage-cache")
		env = map[string]string{
			"OCFL_ROOT":        filepath.Join(tmpDir, "ocfl"),
			"OCFL_STAGE_CACHE": stageCache,
		}
		runOK(t, env, "init-root", "--layout", "0003-hash-and-id-n-tuple-storage-layout")
		return env, stageCache, fixtures[0]
	}
	commitArgs := func(id string) []string {
		return []string{"stage", "commit", "-s", id, "-m", "message", "-n", "name", "-e", "a@b.c"}
	}

	t.Run("workflow", func(t *testing.T) {
		env, stageCache, contentDir := setup(t)
		var id string
		testutil.RunCLI([]string{"stage", "new", "--id", objID}, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			id = strings.TrimSpace(stdout)
			be.Equal(t, 7, len(id))
			be.In(t, id, stderr)
		})
		ids := stageIDs(t, stageCache)
		be.Equal(t, 1, len(ids))
		be.True(t, strings.HasPrefix(ids[0], id))
		runOK(t, env, "stage", "add", "-s", id, contentDir)
		be.In(t, "hello.csv\n", runOK(t, env, "stage", "ls", "-s", id))
		be.In(t, "add: hello.csv", runOK(t, env, "stage", "diff", "-s", id))
		status := runOK(t, env, "stage", "status", "-s", id)
		be.In(t, "stage has changes", status)
		be.In(t, "storage root: file://"+filepath.ToSlash(env["OCFL_ROOT"])+"\n", status)
		// the full stage ID works too
		be.In(t, "hello.csv\n", runOK(t, env, "stage", "-s", ids[0], "ls"))
		runOK(t, env, "stage", "rm", "-s", id, "hello.csv")
		// the commit is to the storage root saved in the stage, not the
		// one in $OCFL_ROOT
		otherEnv := maps.Clone(env)
		otherEnv["OCFL_ROOT"] = filepath.Join(t.TempDir(), "missing")
		runOK(t, otherEnv, commitArgs(id)...)
		be.Equal(t, 0, len(stageIDs(t, stageCache)))
		be.Equal(t, "", runOK(t, env, "validate", "--id", objID))
		be.False(t, strings.Contains(runOK(t, env, "ls", "--id", objID), "hello.csv"))
		// a new stage for the next version has the same ID
		be.Equal(t, id+"\n", runOK(t, env, "stage", "new", "--id", objID))
	})

	t.Run("existing stage", func(t *testing.T) {
		env, stageCache, contentDir := setup(t)
		id := strings.TrimSpace(runOK(t, env, "stage", "new", "--id", objID))
		runOK(t, env, "stage", "add", "-s", id, contentDir)
		testutil.RunCLI([]string{"stage", "new", "--id", objID}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "a stage for "+objID+" already exists", stderr)
			be.In(t, "ocfl stage status -s "+id+"\n", stderr)
			be.In(t, "ocfl stage delete -s "+id, stderr)
		})
		// the existing stage wasn't replaced
		be.In(t, "hello.csv\n", runOK(t, env, "stage", "ls", "-s", id))
		// a stage for another object has a different ID
		other := strings.TrimSpace(runOK(t, env, "stage", "new", "--id", objID+"-other"))
		be.Unequal(t, id, other)
		be.Equal(t, 2, len(stageIDs(t, stageCache)))
		// a commit in progress
		testutil.RunCLIInterrupt(commitArgs(id), env, "starting object update", func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, context.Canceled))
			be.In(t, "to resume: ocfl stage commit -s "+id+" --resume\n", stderr)
		})
		testutil.RunCLI([]string{"stage", "new", "--id", objID}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "a commit of "+objID+" (v1) is in progress", stderr)
			be.In(t, "ocfl stage commit -s "+id+" --resume\n", stderr)
			be.In(t, "ocfl stage commit -s "+id+" --revert", stderr)
		})
		be.In(t, "stage commit -s "+id+" --revert", runOK(t, env, "stage", "status", "-s", id))
	})

	t.Run("stage cache flag", func(t *testing.T) {
		env, stageCache, contentDir := setup(t)
		otherCache := filepath.Join(t.TempDir(), "other-cache")
		id := strings.TrimSpace(runOK(t, env, "stage", "--stage-cache", otherCache, "new", "--id", objID))
		be.Equal(t, 0, len(stageIDs(t, stageCache)))
		be.Equal(t, 1, len(stageIDs(t, otherCache)))
		testutil.RunCLI([]string{"stage", "ls", "-s", id}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "no stage with ID", stderr)
		})
		runOK(t, env, "stage", "add", "--stage-cache", otherCache, "-s", id, contentDir)
		// --stage-cache is in the commands because it was given on the
		// command line
		args := append(commitArgs(id), "--stage-cache", otherCache)
		testutil.RunCLIInterrupt(args, env, "starting object update", func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, context.Canceled))
			be.In(t, "to resume: ocfl stage commit --stage-cache "+otherCache+" -s "+id+" --resume\n", stderr)
		})
		// also when it's given before the subcommand
		out := runOK(t, env, "stage", "--stage-cache", otherCache, "status", "-s", id)
		be.In(t, "ocfl stage commit --stage-cache "+otherCache+" -s "+id+" --revert", out)
		runOK(t, env, "stage", "commit", "--stage-cache", otherCache, "-s", id, "--resume")
		be.Equal(t, 0, len(stageIDs(t, otherCache)))
	})

	t.Run("stage file", func(t *testing.T) {
		env, stageCache, _ := setup(t)
		stagePath := filepath.Join(t.TempDir(), "stage.json")
		testutil.RunCLI([]string{"stage", "new", "-f", stagePath, "--id", objID}, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.Equal(t, "", stdout)
			be.In(t, stagePath, stderr)
		})
		be.Equal(t, 0, len(stageIDs(t, stageCache)))
		// the stage file doesn't block a stage in the stage cache
		runOK(t, env, "stage", "new", "--id", objID)
		testutil.RunCLI([]string{"stage", "new", "-f", stagePath, "--id", objID}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "a stage for "+objID+" already exists: "+stagePath, stderr)
			be.In(t, "ocfl stage delete -f "+stagePath, stderr)
		})
	})

	t.Run("relative stage file", func(t *testing.T) {
		env, _, contentDir := setup(t)
		dir := t.TempDir()
		t.Chdir(dir)
		runOK(t, env, "stage", "new", "-f", "stage.json", "--id", objID)
		runOK(t, env, "stage", "add", "-f", "stage.json", contentDir)
		// commands name the stage file by its absolute path, so they work
		// from any directory
		args := []string{"stage", "commit", "-f", "stage.json", "-m", "message", "-n", "name"}
		testutil.RunCLIInterrupt(args, env, "starting object update", func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, context.Canceled))
			be.In(t, "to resume: ocfl stage commit -f "+filepath.Join(dir, "stage.json")+" --resume\n", stderr)
		})
	})

	t.Run("flag errors", func(t *testing.T) {
		env, _, _ := setup(t)
		id := strings.TrimSpace(runOK(t, env, "stage", "new", "--id", objID))
		stagePath := filepath.Join(t.TempDir(), "stage.json")
		runOK(t, env, "stage", "new", "-f", stagePath, "--id", objID)
		for _, tt := range []struct {
			args    []string
			wantErr string
		}{
			{args: []string{"stage", "ls"}, wantErr: "a stage is required"},
			{args: []string{"stage", "ls", "-s", id, "-f", stagePath}, wantErr: "--stage and --file can't be used together"},
			{args: []string{"stage", "new", "-s", id, "--id", objID}, wantErr: "--stage (-s) can't be used with 'stage new'"},
			{args: []string{"stage", "ls", "-s", "zzzzzzz"}, wantErr: "no stage with ID"},
			// --root is only for 'stage new'
			{args: []string{"stage", "ls", "-s", id, "--root", env["OCFL_ROOT"]}, wantErr: "--root can only be used with 'stage new'"},
			{args: []string{"--root", env["OCFL_ROOT"], "stage", "commit", "-f", stagePath, "-m", "msg", "-n", "name"}, wantErr: "--root can only be used with 'stage new'"},
			{args: []string{"--root", env["OCFL_ROOT"], "stage", "delete", "-s", id}, wantErr: "--root can only be used with 'stage new'"},
		} {
			testutil.RunCLI(tt.args, env, func(err error, stdout, stderr string) {
				be.Nonzero(t, err)
				be.In(t, tt.wantErr, stderr)
			})
		}
		// --root is fine with 'stage new'
		runOK(t, env, "--root", env["OCFL_ROOT"], "stage", "new", "--id", objID+"-other")
	})
}

func TestStage_Delete(t *testing.T) {
	tmpDir, fixtures := testutil.TempDirTestData(t, `testdata/content-fixture`)
	stageCache := filepath.Join(tmpDir, "stage-cache")
	env := map[string]string{
		"OCFL_ROOT":        filepath.Join(tmpDir, "ocfl"),
		"OCFL_STAGE_CACHE": stageCache,
	}
	runOK(t, env, "init-root", "--layout", "0003-hash-and-id-n-tuple-storage-layout")

	t.Run("stage cache", func(t *testing.T) {
		id := strings.TrimSpace(runOK(t, env, "stage", "new", "--id", "object-01"))
		runOK(t, env, "stage", "add", "-s", id, fixtures[0])
		runOK(t, env, "stage", "delete", "-s", id)
		be.Equal(t, 0, len(stageIDs(t, stageCache)))
		testutil.RunCLI([]string{"stage", "delete", "-s", id}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "no stage with ID", stderr)
		})
		// the object can be staged again
		runOK(t, env, "stage", "new", "--id", "object-01")
	})

	t.Run("stage file", func(t *testing.T) {
		stagePath := filepath.Join(t.TempDir(), "stage.json")
		runOK(t, env, "stage", "new", "-f", stagePath, "--id", "object-02")
		runOK(t, env, "stage", "delete", "-f", stagePath)
		_, err := os.Stat(stagePath)
		be.True(t, errors.Is(err, fs.ErrNotExist))
	})

	t.Run("finalized", func(t *testing.T) {
		id := strings.TrimSpace(runOK(t, env, "stage", "new", "--id", "object-03"))
		runOK(t, env, "stage", "add", "-s", id, fixtures[0])
		commit := []string{"stage", "commit", "-s", id, "-m", "message", "-n", "name"}
		testutil.RunCLIInterrupt(commit, env, "starting object update", func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, context.Canceled))
		})
		testutil.RunCLI([]string{"stage", "delete", "-s", id}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "the stage can't be deleted: a commit of object-03 (v1) is in progress", stderr)
			be.In(t, "ocfl stage commit -s "+id+" --resume\n", stderr)
			be.In(t, "ocfl stage commit -s "+id+" --revert", stderr)
		})
		be.In(t, "commit in progress", runOK(t, env, "stage", "status", "-s", id))
		testutil.RunCLI([]string{"stage", "delete", "-s", id, "--force"}, env, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, "deleted a stage with a commit in progress", stderr)
		})
		testutil.RunCLI([]string{"stage", "status", "-s", id}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "no stage with ID", stderr)
		})
	})
}

func TestStage_List(t *testing.T) {
	// setup creates two storage roots and a stage cache, which doesn't exist
	// yet. $OCFL_ROOT is the first storage root.
	setup := func(t *testing.T) (env map[string]string, cache stage.Cache, roots [2]string, contentDir string) {
		tmpDir, fixtures := testutil.TempDirTestData(t, `testdata/content-fixture`)
		cache = stage.Cache{Dir: filepath.Join(tmpDir, "stage-cache")}
		for i := range roots {
			roots[i] = filepath.Join(tmpDir, "root-"+strconv.Itoa(i))
			rootEnv := map[string]string{"OCFL_ROOT": roots[i]}
			runOK(t, rootEnv, "init-root", "--layout", "0003-hash-and-id-n-tuple-storage-layout")
		}
		env = map[string]string{
			"OCFL_ROOT":        roots[0],
			"OCFL_STAGE_CACHE": cache.Dir,
		}
		return env, cache, roots, fixtures[0]
	}
	// newStage creates a stage for the object id in the storage root and
	// returns its short ID.
	newStage := func(t *testing.T, env map[string]string, root, id string) string {
		t.Helper()
		rootEnv := maps.Clone(env)
		rootEnv["OCFL_ROOT"] = root
		return strings.TrimSpace(runOK(t, rootEnv, "stage", "new", "--id", id))
	}
	rootLoc := func(root string) string { return "file://" + filepath.ToSlash(root) }
	// checkRows checks that out has a row for each stage in want, in order,
	// with aligned columns and no trailing spaces.
	checkRows := func(t *testing.T, out string, want ...[3]string) {
		t.Helper()
		lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		be.Equal(t, len(want), len(lines))
		for i, line := range lines {
			be.Equal(t, strings.TrimRight(line, " \t"), line)
			be.AllEqual(t, want[i][:], strings.Fields(line))
			// columns start at the same offsets as in the first row
			be.Equal(t, strings.Index(lines[0], want[0][1]), strings.Index(line, want[i][1]))
			be.Equal(t, strings.Index(lines[0], want[0][2]), strings.Index(line, want[i][2]))
		}
	}
	// sortRows sorts rows by the stage IDs in cache that their short IDs
	// start.
	sortRows := func(t *testing.T, cache stage.Cache, rows ...[3]string) [][3]string {
		t.Helper()
		var sorted [][3]string
		for _, id := range stageIDs(t, cache.Dir) {
			for _, row := range rows {
				if strings.HasPrefix(id, row[0]) {
					sorted = append(sorted, row)
				}
			}
		}
		be.Equal(t, len(rows), len(sorted))
		return sorted
	}

	t.Run("empty", func(t *testing.T) {
		env, cache, _, _ := setup(t)
		be.Equal(t, "", runOK(t, env, "stage"))
		be.NilErr(t, os.MkdirAll(cache.Dir, 0o755))
		be.Equal(t, "", runOK(t, env, "stage"))
	})

	t.Run("stages", func(t *testing.T) {
		env, cache, roots, _ := setup(t)
		rows := sortRows(t, cache,
			[3]string{newStage(t, env, roots[0], "obj-a"), "obj-a", rootLoc(roots[0])},
			[3]string{newStage(t, env, roots[1], "longer-object-b"), "longer-object-b", rootLoc(roots[1])},
		)
		out := runOK(t, env, "stage")
		checkRows(t, out, rows...)
		// files in the stage cache that aren't stage files aren't listed
		id := stageIDs(t, cache.Dir)[0]
		for _, name := range []string{"." + id + ".json.123", id + ".json.lock"} {
			be.NilErr(t, os.WriteFile(filepath.Join(cache.Dir, name), []byte("{}"), 0o644))
		}
		be.Equal(t, out, runOK(t, env, "stage"))
		// --root, -s and -f are ignored
		be.Equal(t, out, runOK(t, env, "stage", "--root", filepath.Join(t.TempDir(), "x")))
		be.Equal(t, out, runOK(t, env, "stage", "-s", "zzzzzzz"))
		be.Equal(t, out, runOK(t, env, "stage", "-f", filepath.Join(t.TempDir(), "stage.json")))
		// the short IDs name the stages
		for _, row := range rows {
			status := runOK(t, env, "stage", "status", "-s", row[0])
			be.In(t, "object:       "+row[1]+" ", status)
		}
	})

	t.Run("commit in progress", func(t *testing.T) {
		env, cache, roots, contentDir := setup(t)
		frozen := newStage(t, env, roots[0], "obj-a")
		draft := newStage(t, env, roots[0], "obj-b")
		runOK(t, env, "stage", "add", "-s", frozen, contentDir)
		commit := []string{"stage", "commit", "-s", frozen, "-m", "message", "-n", "name", "-e", "a@b.c"}
		testutil.RunCLIInterrupt(commit, env, "starting object update", func(err error, stdout, stderr string) {
			be.True(t, errors.Is(err, context.Canceled))
		})
		checkRows(t, runOK(t, env, "stage"), sortRows(t, cache,
			[3]string{frozen, "obj-a", rootLoc(roots[0])},
			[3]string{draft, "obj-b", rootLoc(roots[0])},
		)...)
	})

	t.Run("unreadable stage", func(t *testing.T) {
		for name, content := range map[string]string{
			"invalid json": "{",
			"empty":        "",
		} {
			t.Run(name, func(t *testing.T) {
				env, cache, roots, _ := setup(t)
				good := newStage(t, env, roots[0], "obj-a")
				badID := strings.Repeat("c", len(stageIDs(t, cache.Dir)[0]))
				be.NilErr(t, os.WriteFile(cache.Path(badID), []byte(content), 0o644))
				testutil.RunCLI([]string{"stage"}, env, func(err error, stdout, stderr string) {
					be.Nonzero(t, err)
					be.In(t, "1 of 2 stage files can't be read", err.Error())
					checkRows(t, stdout, [3]string{good, "obj-a", rootLoc(roots[0])})
					be.In(t, "can't read stage "+badID[:7], stderr)
					be.In(t, "to start over, remove the stage file: "+cache.Path(badID), stderr)
				})
			})
		}
	})

	t.Run("no default stage cache", func(t *testing.T) {
		switch runtime.GOOS {
		case "windows", "darwin", "ios", "plan9":
			t.Skip("the user's cache directory isn't set by $XDG_CACHE_HOME or $HOME")
		}
		env, _, _, _ := setup(t)
		delete(env, "OCFL_STAGE_CACHE")
		testutil.RunCLI([]string{"stage"}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "set --stage-cache or $OCFL_STAGE_CACHE", stderr)
			be.Equal(t, "", stdout)
		})
	})

	t.Run("help", func(t *testing.T) {
		testutil.RunCLI([]string{"stage", "--help"}, nil, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, "Run 'ocfl stage' with no sub-command to list the stages", stdout)
			be.In(t, "stage ls ", stdout)
			be.False(t, strings.Contains(stdout, "stage stages"))
		})
	})
}

// runOK runs the ocfl command with args, checks that it succeeds, and returns
// its stdout.
func runOK(t *testing.T, env map[string]string, args ...string) string {
	t.Helper()
	var out string
	testutil.RunCLI(args, env, func(err error, stdout, stderr string) {
		if err != nil {
			t.Fatalf("ocfl %s: %v\n%s", strings.Join(args, " "), err, stderr)
		}
		out = stdout
	})
	return out
}

// commitVersion commits a new version of the object id, with a file whose
// content is msg, using a separate stage cache, as some other process might.
func commitVersion(t *testing.T, env map[string]string, id, msg string) {
	t.Helper()
	dir := t.TempDir()
	be.NilErr(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte(msg), 0o644))
	runOK(t, env, "commit", dir, "--id", id, "-m", msg, "-n", "name", "--stage-cache", t.TempDir())
}

// noLocks checks that there are no stage file locks in dir.
func noLocks(t *testing.T, dir string) {
	t.Helper()
	locks, err := filepath.Glob(filepath.Join(dir, "*.lock"))
	be.NilErr(t, err)
	be.Equal(t, 0, len(locks))
}

// removeStaleLock removes the stage file lock left in dir by a command that
// couldn't remove it, as a user would.
func removeStaleLock(t *testing.T, dir string) {
	t.Helper()
	locks, err := filepath.Glob(filepath.Join(dir, "*.lock"))
	be.NilErr(t, err)
	be.Equal(t, 1, len(locks))
	be.NilErr(t, os.Remove(locks[0]))
}

// readStage reads the stage in the stage file stagePath.
func readStage(t *testing.T, stagePath string) *stage.Stage {
	t.Helper()
	f, err := stage.Open(stagePath)
	be.NilErr(t, err)
	return f.Stage()
}

// applyWithoutRootSidecar applies the finalized update in the stage file
// stagePath to the object in the storage root at rootPath, failing when it
// writes the object's root inventory sidecar.
func applyWithoutRootSidecar(t *testing.T, rootPath, stagePath string) {
	t.Helper()
	ctx := context.Background()
	s := readStage(t, stagePath)
	content, err := s.Content(ctx, func(dir string) (ocflfs.FS, error) {
		fsys, err := local.NewFS(dir)
		if err == nil {
			t.Cleanup(func() { fsys.Close() })
		}
		return fsys, err
	})
	be.NilErr(t, err)
	rootFS, err := local.NewFS(rootPath)
	be.NilErr(t, err)
	t.Cleanup(func() { rootFS.Close() })
	root, err := ocfl.NewRoot(ctx, rootFS, ".")
	be.NilErr(t, err)
	objPath, err := root.ResolveID(s.ID())
	be.NilErr(t, err)
	sidecar := path.Join(objPath, "inventory.json."+s.DigestAlgorithm().ID())
	failFS := &failWriteFS{FS: rootFS, name: sidecar}
	_, err = s.Update().Apply(ctx, failFS, objPath, content)
	be.True(t, errors.Is(err, errWriteFailed))
}

var errWriteFailed = errors.New("write failed")

// failWriteFS is a local FS that fails to write the file name.
type failWriteFS struct {
	*local.FS
	name string
}

func (fsys *failWriteFS) Write(ctx context.Context, name string, r io.Reader) (int64, error) {
	if name == fsys.name {
		return 0, errWriteFailed
	}
	return fsys.FS.Write(ctx, name, r)
}
