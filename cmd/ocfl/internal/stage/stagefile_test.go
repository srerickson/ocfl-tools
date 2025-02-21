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

	t.Run("stage new object", func(t *testing.T) {
		newObj, err := root.NewObject(ctx, "ark:xyz/987")
		be.NilErr(t, err)

		t.Run("defaults", func(t *testing.T) {
			changes, err := stage.NewStageFile(newObj, "sha512")
			be.NilErr(t, err)
			changes.AddDir(ctx, contentFixture)
		})
	})

	t.Run("stage existing object", func(t *testing.T) {
		existingObject, err := root.NewObject(ctx, "ark:123/abc")
		be.NilErr(t, err)
		aFile := `a_file.txt` // there is only a single file in the object
		t.Run("defaults", func(t *testing.T) {
			stage, err := stage.NewStageFile(existingObject, "")
			be.NilErr(t, err)
			// stage state includes a single file
			be.Nonzero(t, stage.NextState[aFile])
			be.NilErr(t, stage.AddDir(ctx, contentFixture))
			// file is still there
			be.Nonzero(t, stage.NextState[aFile])
		})
		t.Run("with as", func(t *testing.T) {
			changes, err := stage.NewStageFile(existingObject, "")
			be.NilErr(t, err)
			// stage state includes a single file
			be.Nonzero(t, changes.NextState[aFile])
			be.NilErr(t, changes.AddDir(ctx, contentFixture, stage.AddAs("newstuff")))
			// file is still there
			be.Nonzero(t, changes.NextState[aFile])
		})
		t.Run("with remove", func(t *testing.T) {
			changes, err := stage.NewStageFile(existingObject, "")
			be.NilErr(t, err)
			// stage state includes a single file
			be.Nonzero(t, changes.NextState[aFile])
			be.NilErr(t, changes.AddDir(ctx, contentFixture, stage.AddAndRemove()))
			// the file has been removed
			be.Zero(t, changes.NextState[aFile])
		})
	})
}
