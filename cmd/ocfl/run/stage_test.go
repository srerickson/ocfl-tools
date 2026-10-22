package run_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
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
		s, err := stage.Read(stagePath)
		be.NilErr(t, err)
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
		s, err := stage.Read(stagePath)
		be.NilErr(t, err)
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
			be.In(t, "to resume: ocfl --root "+env["OCFL_ROOT"]+" stage commit -f "+stagePath+" --resume\n", stderr)
			be.In(t, "to revert: ocfl --root "+env["OCFL_ROOT"]+" stage commit -f "+stagePath+" --revert", stderr)
		})
		s, err := stage.Read(stagePath)
		be.NilErr(t, err)
		be.True(t, s.Finalized())
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
		s, err := stage.Read(stagePath)
		be.NilErr(t, err)
		be.True(t, s.Finalized())
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
		s, err := stage.Read(stagePath)
		be.NilErr(t, err)
		be.True(t, s.Finalized())
	})

	t.Run("resume draft", func(t *testing.T) {
		env, stagePath, _ := setup(t)
		testutil.RunCLI([]string{"stage", "commit", "-f", stagePath, "--resume"}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "stage isn't finalized: nothing to resume", stderr)
		})
	})

	t.Run("revert", func(t *testing.T) {
		env, stagePath, _ := setup(t)
		interrupt(t, env, stagePath, commitArgs(stagePath, "first commit"), "v1")
		runOK(t, env, "stage", "commit", "-f", stagePath, "--revert")
		// the stage is a draft with the staged files
		s, err := stage.Read(stagePath)
		be.NilErr(t, err)
		be.False(t, s.Finalized())
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
			be.In(t, "stage isn't finalized: nothing to revert", stderr)
		})
	})

	t.Run("revert completed commit", func(t *testing.T) {
		env, stagePath, _ := setup(t)
		interrupt(t, env, stagePath, commitArgs(stagePath, "first commit"), "v1")
		// the commit completes, but the stage file isn't removed
		stageBytes, err := os.ReadFile(stagePath)
		be.NilErr(t, err)
		runOK(t, env, "stage", "commit", "-f", stagePath, "--resume")
		be.NilErr(t, os.WriteFile(stagePath, stageBytes, 0644))
		testutil.RunCLI([]string{"stage", "commit", "-f", stagePath, "--revert"}, env, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "is complete and can't be reverted", stderr)
			be.In(t, "stage commit -f "+stagePath+" --resume\n", stderr)
		})
		// resuming removes the stage file
		runOK(t, env, "stage", "commit", "-f", stagePath, "--resume")
		_, err = os.Stat(stagePath)
		be.True(t, errors.Is(err, fs.ErrNotExist))
		be.Equal(t, "", runOK(t, env, "validate", "--id", objID))
	})

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

// applyWithoutRootSidecar applies the finalized update in the stage file
// stagePath to the object in the storage root at rootPath, failing when it
// writes the object's root inventory sidecar.
func applyWithoutRootSidecar(t *testing.T, rootPath, stagePath string) {
	t.Helper()
	ctx := context.Background()
	s, err := stage.Read(stagePath)
	be.NilErr(t, err)
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
