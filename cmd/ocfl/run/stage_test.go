package run_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-go"
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
