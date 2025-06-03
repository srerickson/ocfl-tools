package stage_test

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-go"
	ocflfs "github.com/srerickson/ocfl-go/fs"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/stage"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

func TestStageFile_AddDir(t *testing.T) {
	ctx := context.Background()
	_, fixtures := testutil.TempDirTestData(t,
		"testdata/content-fixture",
		"testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root",
	)
	contentFixture := fixtures[0]
	root, err := ocfl.NewRoot(ctx, ocflfs.DirFS(fixtures[1]), ".")
	be.NilErr(t, err)

	t.Run("stage new object", func(t *testing.T) {
		newObj, err := root.NewObject(ctx, "ark:xyz/987")
		be.NilErr(t, err)

		t.Run("defaults", func(t *testing.T) {
			changes, err := stage.NewStageFile(newObj, "sha512")
			be.NilErr(t, err)
			err = changes.AddDir(ctx, contentFixture)
			be.NilErr(t, err)
			stageStateMachesDir(t, changes, contentFixture, false, ".")
			be.NilErr(t, stageErrors(changes))
		})

		t.Run("with hidden", func(t *testing.T) {
			changes, err := stage.NewStageFile(newObj, "sha512")
			be.NilErr(t, err)
			err = changes.AddDir(ctx, contentFixture, stage.AddWithHidden())
			be.NilErr(t, err)
			stageStateMachesDir(t, changes, contentFixture, true, ".")
			be.NilErr(t, stageErrors(changes))
		})

		t.Run("with digest jobs", func(t *testing.T) {
			changes, err := stage.NewStageFile(newObj, "sha512")
			be.NilErr(t, err)
			err = changes.AddDir(ctx, contentFixture, stage.AddDigestJobs(2))
			be.NilErr(t, err)
			stageStateMachesDir(t, changes, contentFixture, false, ".")
			be.NilErr(t, stageErrors(changes))
		})

		t.Run("with remove", func(t *testing.T) {
			changes, err := stage.NewStageFile(newObj, "sha512")
			be.NilErr(t, err)
			err = changes.AddDir(ctx, contentFixture, stage.AddAndRemove())
			be.NilErr(t, err)
			stageStateMachesDir(t, changes, contentFixture, false, ".")
			be.NilErr(t, stageErrors(changes))
		})

		t.Run("with as", func(t *testing.T) {
			changes, err := stage.NewStageFile(newObj, "sha512")
			be.NilErr(t, err)
			err = changes.AddDir(ctx, contentFixture, stage.AddAs("tmp"))
			be.NilErr(t, err)
			stageStateMachesDir(t, changes, contentFixture, false, "tmp")
			be.NilErr(t, stageErrors(changes))
		})

		t.Run("missing directory", func(t *testing.T) {
			changes, err := stage.NewStageFile(newObj, "sha512")
			be.NilErr(t, err)
			err = changes.AddDir(ctx, "missing")
			be.Nonzero(t, err)
			be.NilErr(t, stageErrors(changes))
		})

		t.Run("as path invalid", func(t *testing.T) {
			changes, err := stage.NewStageFile(newObj, "sha512")
			be.NilErr(t, err)
			err = changes.AddDir(ctx, contentFixture, stage.AddAs("../tmp"))
			be.Nonzero(t, err)
			be.NilErr(t, stageErrors(changes))

		})
		t.Run("as path conflict", func(t *testing.T) {
			changes, err := stage.NewStageFile(newObj, "sha512")
			be.NilErr(t, err)
			err = changes.AddFile(filepath.Join(contentFixture, "hello.csv"))
			be.NilErr(t, err)
			// add dir with same name as existing file
			err = changes.AddDir(ctx, contentFixture, stage.AddAs("hello.csv"))
			be.Nonzero(t, err)
			be.NilErr(t, stageErrors(changes))
		})

		t.Run("remove conflicting", func(t *testing.T) {
			changes, err := stage.NewStageFile(newObj, "sha512")
			be.NilErr(t, err)
			// add a file that will result in a conflict later
			err = changes.AddFile(filepath.Join(contentFixture, "hello.csv"), stage.AddAs("tmp/hello.csv/file"))
			be.NilErr(t, err)
			// this would result in a conflict, howeve AddAndRemove() should remove the
			// conflicting path before new files are added.
			err = changes.AddDir(ctx, contentFixture, stage.AddAs("tmp"), stage.AddAndRemove())
			be.NilErr(t, err)
			be.NilErr(t, stageErrors(changes))
		})
	})

	t.Run("stage existing object", func(t *testing.T) {
		existingObject, err := root.NewObject(ctx, "ark:123/abc")
		be.NilErr(t, err)
		aFile := `a_file.txt` // there is only a single file in the object
		t.Run("defaults", func(t *testing.T) {
			changes, err := stage.NewStageFile(existingObject, "")
			be.NilErr(t, err)
			// stage state includes a single file
			be.Nonzero(t, changes.NextState[aFile])
			be.NilErr(t, changes.AddDir(ctx, contentFixture))
			// file is still there
			be.Nonzero(t, changes.NextState[aFile])
			be.NilErr(t, stageErrors(changes))

		})
		t.Run("with as", func(t *testing.T) {
			changes, err := stage.NewStageFile(existingObject, "")
			be.NilErr(t, err)
			// stage state includes a single file
			be.Nonzero(t, changes.NextState[aFile])
			be.NilErr(t, changes.AddDir(ctx, contentFixture, stage.AddAs("newstuff")))
			// file is still there
			be.Nonzero(t, changes.NextState[aFile])
			be.NilErr(t, stageErrors(changes))
		})
		t.Run("with remove", func(t *testing.T) {
			changes, err := stage.NewStageFile(existingObject, "")
			be.NilErr(t, err)
			// stage state includes a single file
			be.Nonzero(t, changes.NextState[aFile])
			be.NilErr(t, changes.AddDir(ctx, contentFixture, stage.AddAndRemove()))
			// the file has been removed
			stageStateMachesDir(t, changes, contentFixture, false, ".")
			be.NilErr(t, stageErrors(changes))
		})
	})
}

func TestStageFile_ContentErrors(t *testing.T) {
	ctx := context.Background()
	_, fixtures := testutil.TempDirTestData(t,
		"testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root",
	)
	root, err := ocfl.NewRoot(ctx, ocflfs.DirFS(fixtures[0]), ".")
	be.NilErr(t, err)
	newObj, err := root.NewObject(ctx, "ark:xyz/987")
	be.NilErr(t, err)

	t.Run("missing content", func(t *testing.T) {
		_, fixtures := testutil.TempDirTestData(t, "testdata/content-fixture")
		contentFixture := fixtures[0]
		changes, err := stage.NewStageFile(newObj, "sha512")
		be.NilErr(t, err)
		err = changes.AddDir(ctx, contentFixture)
		be.NilErr(t, err)
		// remove a file
		err = os.Remove(filepath.Join(contentFixture, "hello.csv"))
		be.NilErr(t, err)
		// modify a file. Note: testdata/content-fixture includes two files with
		// identical content. This test would be flaky if we modified one of
		// those files StageFile's local content is deduplicated, and we might
		// not modify the file that is actually referenced in the StageFile.
		err = os.WriteFile(filepath.Join(contentFixture, "folder1", "folder2", "sculpture-stone-face-head-888027.jpg"), []byte("new content"), 0644)
		be.NilErr(t, err)
		// expect two errors
		count := 0
		for err := range changes.ContentErrors() {
			be.Nonzero(t, err)
			count++
		}
		be.Equal(t, 2, count)
	})
}

func stageStateMachesDir(t *testing.T, s *stage.StageFile, dir string, withHidden bool, as string) {
	t.Helper()
	count := 0
	walkErr := fs.WalkDir(os.DirFS(dir), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !withHidden && isHidden(p) {
			return nil
		}
		count++
		p = path.Join(as, p)
		if s.NextState[p] == "" {
			t.Error("stage doesnt include", p)
		}
		return nil
	})
	be.NilErr(t, walkErr)
	if l := len(s.NextState); l != count {
		t.Errorf("stage doesn't have same number of files as content fixture; want=%d, got=%d", count, l)
	}
}

// func stageIncludesHidden(s *stage.StageFile) bool {
// 	names := slices.Collect(maps.Keys(s.NextState))
// 	for _, n := range names {
// 		if isHidden(n) {
// 			return true
// 		}
// 	}
// 	return false
// }

func isHidden(n string) bool {
	for _, part := range strings.Split(n, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func stageErrors(stage *stage.StageFile) error {
	_, err := stage.Stage()
	return err
}
