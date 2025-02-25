package run_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

func TestStage(t *testing.T) {
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
	cmd := []string{"stage", "new", "--file", stagePath, "--ocflv", "1.0", "--alg", "sha256", objID}
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
		expect := "new-stuff/file2.txt\nnew-stuff/sculpture-stone-face-head-888027.jpg\n"
		be.Equal(t, expect, stdout) // stdout only has two items
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
		expect := "new-stuff/file2.txt\nnew-stuff/sculpture-stone-face-head-888027.jpg\n"
		be.Equal(t, expect, stdout) // stdout only has two items
	})
	// stage file should be deleted
	if _, err := os.Stat(stagePath); err == nil {
		t.Fatal("stage file wasn't deleted after commit")
	}

	// v2 stage
	cmd = []string{"stage", "new", "--file", stagePath, objID}
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
	cmd = []string{"stage", "commit", "--file", stagePath, "-m", "commit 2", "-n", name, "-e", email}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
	})
	// check content for v2
	cmd = []string{"ls", "--version", "2", "--id", objID}
	testutil.RunCLI(cmd, env, func(err error, stdout, stderr string) {
		be.In(t, "new-stuff/file2.txt\n", stdout)
		be.In(t, "new-stuff/sculpture-stone-face-head-888027.jpg\n", stdout)
		be.In(t, "tmp/data.csv\n", stdout)
		be.Equal(t, 3, strings.Count(stdout, "\n")) // three items in v2 state
	})
}

func TestStageRm(t *testing.T) {
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
	cmd := []string{"stage", "new", "--file", stagePath, "--ocflv", "1.0", "--alg", "sha256", objID}
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

func TestStageCommit(t *testing.T) {
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
	cmd := []string{"stage", "new", "--file", stagePath, "--ocflv", "1.0", "--alg", "sha512", objID}
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

func TestStageDiff(t *testing.T) {
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
		cmd := []string{"stage", "new", "--file", stagePath, existingID}
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
		cmd := []string{"stage", "new", "--file", stagePath, newID}
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
		cmd = []string{"stage", "add", "--all", "--file", stagePath, contentFixture}
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

func TestStageStatus(t *testing.T) {
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
		cmd := []string{"stage", "new", "--file", stagePath, newID}
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
			be.In(t, "file is missing", stderr)
		})
	})
}
