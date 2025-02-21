package stage_test

import (
	"context"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-go"
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
	root, err := ocfl.NewRoot(ctx, ocfl.DirFS(fixtures[1]), ".")
	be.NilErr(t, err)
	obj, err := root.NewObject(ctx, "ark:123/abc")
	be.NilErr(t, err)
	aFile := `a_file.txt`
	t.Run("defaults", func(t *testing.T) {
		stage, err := stage.NewStageFile(obj, "")
		be.NilErr(t, err)
		// stage state includes a single file
		be.Nonzero(t, stage.NextState[aFile])
		be.NilErr(t, stage.AddDir(ctx, contentFixture, ".", false, false, 0))
		// file is still there
		be.Nonzero(t, stage.NextState[aFile])
	})
	t.Run("with as", func(t *testing.T) {
		stage, err := stage.NewStageFile(obj, "")
		be.NilErr(t, err)
		// stage state includes a single file
		be.Nonzero(t, stage.NextState[aFile])
		be.NilErr(t, stage.AddDir(ctx, contentFixture, "newstuff", false, false, 0))
		// file is still there
		be.Nonzero(t, stage.NextState[aFile])
	})
	t.Run("with remove", func(t *testing.T) {
		stage, err := stage.NewStageFile(obj, "")
		be.NilErr(t, err)
		// stage state includes a single file
		be.Nonzero(t, stage.NextState[aFile])
		be.NilErr(t, stage.AddDir(ctx, contentFixture, ".", false, true, 0))
		// the file has been removed
		be.Zero(t, stage.NextState[aFile])
	})
}
