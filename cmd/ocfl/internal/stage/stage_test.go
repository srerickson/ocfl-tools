package stage_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
	ocflfs "github.com/srerickson/ocfl-go/fs"
	"github.com/srerickson/ocfl-go/fs/local"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/stage"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/statcache"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

func TestNew(t *testing.T) {
	ctx := context.Background()
	_, fixtures := testutil.TempDirTestData(t,
		"testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root",
	)
	root, err := ocfl.NewRoot(ctx, ocflfs.DirFS(fixtures[0]), ".")
	be.NilErr(t, err)

	t.Run("new object uses alg", func(t *testing.T) {
		s, err := stage.New(ctx, root, testRootLoc, "ark:xyz/987", digest.SHA256)
		be.NilErr(t, err)
		be.Equal(t, "sha256", s.DigestAlgorithm().ID())
		be.Equal(t, ocfl.V(1), s.NextHead())
		be.Equal(t, testRootLoc, s.Root())
	})

	t.Run("existing object ignores alg", func(t *testing.T) {
		s, err := stage.New(ctx, root, testRootLoc, "ark:123/abc", digest.SHA256)
		be.NilErr(t, err)
		be.Equal(t, "sha512", s.DigestAlgorithm().ID())
		be.Equal(t, ocfl.V(2), s.NextHead())
	})

	t.Run("root location required", func(t *testing.T) {
		_, err := stage.New(ctx, root, "", "ark:123/abc", digest.SHA256)
		be.Nonzero(t, err)
	})
}

func TestStage_AddDir(t *testing.T) {
	ctx := context.Background()
	_, fixtures := testutil.TempDirTestData(t,
		"testdata/content-fixture",
		"testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root",
	)
	contentFixture := fixtures[0]
	root, err := ocfl.NewRoot(ctx, ocflfs.DirFS(fixtures[1]), ".")
	be.NilErr(t, err)

	t.Run("stage new object", func(t *testing.T) {
		newID := "ark:xyz/987"

		t.Run("defaults", func(t *testing.T) {
			s := newStage(t, root, newID)
			be.NilErr(t, s.AddDir(ctx, contentFixture, nil))
			stageStateMatchesDir(t, s, contentFixture, true, ".")
			stageHasContent(t, s)
		})

		t.Run("without hidden", func(t *testing.T) {
			s := newStage(t, root, newID)
			be.NilErr(t, s.AddDir(ctx, contentFixture, nil, stage.AddWithoutHidden()))
			stageStateMatchesDir(t, s, contentFixture, false, ".")
			stageHasContent(t, s)
		})

		t.Run("with digest jobs", func(t *testing.T) {
			s := newStage(t, root, newID)
			be.NilErr(t, s.AddDir(ctx, contentFixture, nil, stage.AddDigestJobs(2)))
			stageStateMatchesDir(t, s, contentFixture, true, ".")
			stageHasContent(t, s)
		})

		t.Run("with remove", func(t *testing.T) {
			s := newStage(t, root, newID)
			be.NilErr(t, s.AddDir(ctx, contentFixture, nil, stage.AddAndRemove()))
			stageStateMatchesDir(t, s, contentFixture, true, ".")
			stageHasContent(t, s)
		})

		t.Run("with as", func(t *testing.T) {
			s := newStage(t, root, newID)
			be.NilErr(t, s.AddDir(ctx, contentFixture, nil, stage.AddAs("tmp")))
			stageStateMatchesDir(t, s, contentFixture, true, "tmp")
			stageHasContent(t, s)
		})

		t.Run("relative path", func(t *testing.T) {
			t.Chdir(filepath.Dir(contentFixture))
			s := newStage(t, root, newID)
			be.NilErr(t, s.AddDir(ctx, filepath.Base(contentFixture), nil))
			stageStateMatchesDir(t, s, contentFixture, true, ".")
			src, ok := s.SourceFile("hello.csv")
			be.True(t, ok)
			be.Equal(t, filepath.Join(contentFixture, "hello.csv"), src)
		})

		t.Run("missing directory", func(t *testing.T) {
			s := newStage(t, root, newID)
			be.Nonzero(t, s.AddDir(ctx, filepath.Join(contentFixture, "missing"), nil))
			be.Equal(t, 0, len(s.Update().NewState()))
		})

		t.Run("as path invalid", func(t *testing.T) {
			s := newStage(t, root, newID)
			be.Nonzero(t, s.AddDir(ctx, contentFixture, nil, stage.AddAs("../tmp")))
			be.Equal(t, 0, len(s.Update().NewState()))
		})

		t.Run("as path conflict", func(t *testing.T) {
			s := newStage(t, root, newID)
			be.NilErr(t, s.AddFile(ctx, filepath.Join(contentFixture, "hello.csv"), nil))
			// add dir with same name as existing file
			be.Nonzero(t, s.AddDir(ctx, contentFixture, nil, stage.AddAs("hello.csv")))
			be.Equal(t, 1, len(s.Update().NewState().PathMap()))
		})

		t.Run("remove conflicting", func(t *testing.T) {
			s := newStage(t, root, newID)
			// add a file that will result in a conflict later
			be.NilErr(t, s.AddFile(ctx, filepath.Join(contentFixture, "hello.csv"), nil, stage.AddAs("tmp/hello.csv/file")))
			// this would result in a conflict, however AddAndRemove() should
			// remove the conflicting path before new files are added.
			be.NilErr(t, s.AddDir(ctx, contentFixture, nil, stage.AddAs("tmp"), stage.AddAndRemove()))
			stageStateMatchesDir(t, s, contentFixture, true, "tmp")
			stageHasContent(t, s)
		})

		t.Run("unchanged after error with remove", func(t *testing.T) {
			s := newStage(t, root, newID)
			be.NilErr(t, s.AddFile(ctx, filepath.Join(contentFixture, "hello.csv"), nil, stage.AddAs("a/b.csv")))
			// the directory's files can't be added under a file name
			be.Nonzero(t, s.AddDir(ctx, contentFixture, nil, stage.AddAs("a/b.csv/c"), stage.AddAndRemove()))
			be.Equal(t, "a/b.csv\n", list(s))
			stageHasContent(t, s)
		})

		t.Run("symlink", func(t *testing.T) {
			dir := t.TempDir()
			be.NilErr(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
			if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
				t.Skip("symlinks not supported:", err)
			}
			s := newStage(t, root, newID)
			err := s.AddDir(ctx, dir, nil)
			be.True(t, errors.Is(err, statcache.ErrNotRegular))
			be.Equal(t, 0, len(s.Update().NewState()))
		})

		t.Run("logs changes", func(t *testing.T) {
			s := newStage(t, root, newID)
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			be.NilErr(t, s.AddFile(ctx, filepath.Join(contentFixture, "hello.csv"), logger, stage.AddAs("a.csv")))
			be.In(t, "file added", logs.String())
			be.In(t, "a.csv", logs.String())
			logs.Reset()
			be.NilErr(t, s.AddFile(ctx, filepath.Join(contentFixture, "folder1", "file.txt"), logger, stage.AddAs("a.csv")))
			be.In(t, "file updated", logs.String())
			logs.Reset()
			be.NilErr(t, s.AddDir(ctx, filepath.Join(contentFixture, "folder1"), logger, stage.AddAndRemove()))
			be.In(t, "file removed", logs.String())
		})
	})

	t.Run("stage existing object", func(t *testing.T) {
		existingID := "ark:123/abc"
		aFile := `a_file.txt` // there is only a single file in the object

		t.Run("defaults", func(t *testing.T) {
			s := newStage(t, root, existingID)
			// stage state includes a single file
			be.Nonzero(t, s.Update().NewState().PathMap()[aFile])
			be.NilErr(t, s.AddDir(ctx, contentFixture, nil))
			// file is still there
			be.Nonzero(t, s.Update().NewState().PathMap()[aFile])
			stageHasContent(t, s)
		})

		t.Run("with as", func(t *testing.T) {
			s := newStage(t, root, existingID)
			be.NilErr(t, s.AddDir(ctx, contentFixture, nil, stage.AddAs("newstuff")))
			// file is still there
			be.Nonzero(t, s.Update().NewState().PathMap()[aFile])
			stageHasContent(t, s)
		})

		t.Run("with remove", func(t *testing.T) {
			s := newStage(t, root, existingID)
			be.NilErr(t, s.AddDir(ctx, contentFixture, nil, stage.AddAndRemove()))
			// the file has been removed
			stageStateMatchesDir(t, s, contentFixture, true, ".")
			stageHasContent(t, s)
		})
	})
}

func TestStage_AddFile(t *testing.T) {
	ctx := context.Background()
	_, fixtures := testutil.TempDirTestData(t,
		"testdata/content-fixture",
		"testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root",
	)
	contentFixture := fixtures[0]
	root, err := ocfl.NewRoot(ctx, ocflfs.DirFS(fixtures[1]), ".")
	be.NilErr(t, err)

	t.Run("base name", func(t *testing.T) {
		s := newStage(t, root, "ark:xyz/987")
		be.NilErr(t, s.AddFile(ctx, filepath.Join(contentFixture, "folder1", "file.txt"), nil))
		be.Nonzero(t, s.Update().NewState().PathMap()["file.txt"])
		stageHasContent(t, s)
	})

	t.Run("as", func(t *testing.T) {
		s := newStage(t, root, "ark:xyz/987")
		be.NilErr(t, s.AddFile(ctx, filepath.Join(contentFixture, "hello.csv"), nil, stage.AddAs("a/b.csv")))
		be.Nonzero(t, s.Update().NewState().PathMap()["a/b.csv"])
		stageHasContent(t, s)
	})

	t.Run("invalid as", func(t *testing.T) {
		s := newStage(t, root, "ark:xyz/987")
		name := filepath.Join(contentFixture, "hello.csv")
		be.Nonzero(t, s.AddFile(ctx, name, nil, stage.AddAs("../b.csv")))
		be.Nonzero(t, s.AddFile(ctx, name, nil, stage.AddAs(".")))
	})

	t.Run("missing", func(t *testing.T) {
		s := newStage(t, root, "ark:xyz/987")
		be.Nonzero(t, s.AddFile(ctx, filepath.Join(contentFixture, "missing.txt"), nil))
	})

	t.Run("symlink is followed", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "link.csv")
		if err := os.Symlink(filepath.Join(contentFixture, "hello.csv"), link); err != nil {
			t.Skip("symlinks not supported:", err)
		}
		s := newStage(t, root, "ark:xyz/987")
		be.NilErr(t, s.AddFile(ctx, link, nil))
		be.Equal(t, "link.csv\n", list(s))
		src, _ := s.SourceFile("link.csv")
		be.Equal(t, "hello.csv", filepath.Base(src))
		stageHasContent(t, s)
	})
}

func TestStage_Remove(t *testing.T) {
	ctx := context.Background()
	_, fixtures := testutil.TempDirTestData(t,
		"testdata/content-fixture",
		"testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root",
	)
	contentFixture := fixtures[0]
	root, err := ocfl.NewRoot(ctx, ocflfs.DirFS(fixtures[1]), ".")
	be.NilErr(t, err)
	setup := func(t *testing.T) *stage.Stage {
		s := newStage(t, root, "ark:xyz/987")
		be.NilErr(t, s.AddDir(ctx, contentFixture, nil))
		return s
	}

	t.Run("file", func(t *testing.T) {
		s := setup(t)
		be.NilErr(t, s.Remove("hello.csv", false, nil))
		_, exists := s.Update().NewState().PathMap()["hello.csv"]
		be.False(t, exists)
		_, hasSource := s.SourceFile("hello.csv")
		be.False(t, hasSource)
		stageHasContent(t, s)
	})

	t.Run("directory requires recursive", func(t *testing.T) {
		s := setup(t)
		be.Nonzero(t, s.Remove("folder1", false, nil))
		be.Nonzero(t, s.Remove(".", false, nil))
		stageStateMatchesDir(t, s, contentFixture, true, ".")
	})

	t.Run("recursive", func(t *testing.T) {
		s := setup(t)
		be.NilErr(t, s.Remove("folder1", true, nil))
		be.Equal(t, 1, len(s.Update().NewState().PathMap()))
		stageHasContent(t, s)
	})

	t.Run("everything", func(t *testing.T) {
		s := setup(t)
		be.NilErr(t, s.Remove(".", true, nil))
		be.Equal(t, 0, len(s.Update().NewState()))
		content, err := s.Content(ctx, openLocal(t))
		be.NilErr(t, err)
		be.Equal(t, 0, len(content.Digests()))
	})

	t.Run("missing", func(t *testing.T) {
		s := setup(t)
		be.Nonzero(t, s.Remove("missing.txt", false, nil))
	})
}

func TestStage_List(t *testing.T) {
	ctx := context.Background()
	_, fixtures := testutil.TempDirTestData(t,
		"testdata/content-fixture",
		"testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root",
	)
	root, err := ocfl.NewRoot(ctx, ocflfs.DirFS(fixtures[1]), ".")
	be.NilErr(t, err)
	s := newStage(t, root, "ark:xyz/987")
	be.NilErr(t, s.AddDir(ctx, filepath.Join(fixtures[0], "folder1"), nil, stage.AddWithoutHidden()))
	be.Equal(t, "file.txt\nfolder2/file2.txt\nfolder2/sculpture-stone-face-head-888027.jpg\n", list(s))
	var out bytes.Buffer
	s.List(&out, true)
	dig := s.Update().NewState().PathMap()["file.txt"]
	be.In(t, dig+" file.txt\n", out.String())
}

func TestStage_Check(t *testing.T) {
	ctx := context.Background()
	// setup returns a stage for a new object with files in a new directory.
	// a.txt and b.txt have the same content.
	setup := func(t *testing.T) (*stage.Stage, string) {
		dir := writeFiles(t, map[string]string{"a.txt": "same", "b.txt": "same", "c.txt": "other"})
		s := newStage(t, newRoot(t), "ark:xyz/987")
		be.NilErr(t, s.AddDir(ctx, dir, nil))
		return s, dir
	}

	t.Run("unchanged", func(t *testing.T) {
		s, _ := setup(t)
		result, err := s.Check(ctx)
		be.NilErr(t, err)
		be.Equal(t, 0, len(result.Changed))
		be.Equal(t, 0, len(result.Unavailable))
	})

	t.Run("touched file is refreshed", func(t *testing.T) {
		s, dir := setup(t)
		later := time.Now().Add(time.Hour)
		be.NilErr(t, os.Chtimes(filepath.Join(dir, "c.txt"), later, later))
		result, err := s.Check(ctx)
		be.NilErr(t, err)
		be.Equal(t, 0, len(result.Changed))
		be.True(t, result.Refreshed)
	})

	t.Run("modified file", func(t *testing.T) {
		s, dir := setup(t)
		be.NilErr(t, os.WriteFile(filepath.Join(dir, "c.txt"), []byte("changed"), 0o644))
		result, err := s.Check(ctx)
		be.NilErr(t, err)
		be.Equal(t, 1, len(result.Changed))
		be.Equal(t, "c.txt", result.Changed[0].Path)
		be.Equal(t, filepath.Join(dir, "c.txt"), result.Changed[0].Source)
		be.Equal(t, statcache.StatusModified, result.Changed[0].Status)
		be.AllEqual(t, []string{"c.txt"}, result.Unavailable)
		// the stage still has the content that was added
		before := s.Update().NewState().PathMap()["c.txt"]
		be.NilErr(t, s.AddDir(ctx, dir, nil))
		be.Unequal(t, before, s.Update().NewState().PathMap()["c.txt"])
		result, err = s.Check(ctx)
		be.NilErr(t, err)
		be.Equal(t, 0, len(result.Changed))
	})

	t.Run("missing file", func(t *testing.T) {
		s, dir := setup(t)
		be.NilErr(t, os.Remove(filepath.Join(dir, "c.txt")))
		result, err := s.Check(ctx)
		be.NilErr(t, err)
		be.Equal(t, 1, len(result.Changed))
		be.Equal(t, statcache.StatusMissing, result.Changed[0].Status)
		be.AllEqual(t, []string{"c.txt"}, result.Unavailable)
	})

	t.Run("modified file with duplicate content", func(t *testing.T) {
		s, dir := setup(t)
		be.NilErr(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed"), 0o644))
		result, err := s.Check(ctx)
		be.NilErr(t, err)
		be.Equal(t, 1, len(result.Changed))
		be.Equal(t, "a.txt", result.Changed[0].Path)
		// b.txt has the content
		be.Equal(t, 0, len(result.Unavailable))
	})

	// unchanged checks that s has no changed files or unavailable content.
	unchanged := func(t *testing.T, s *stage.Stage) {
		t.Helper()
		result, err := s.Check(ctx)
		be.NilErr(t, err)
		be.Equal(t, 0, len(result.Changed))
		be.Equal(t, 0, len(result.Unavailable))
	}

	t.Run("file added again after changes", func(t *testing.T) {
		s, dir := setup(t)
		name := filepath.Join(dir, "c.txt")
		be.NilErr(t, os.WriteFile(name, []byte("changed"), 0o644))
		be.NilErr(t, s.AddFile(ctx, name, nil))
		unchanged(t, s)
		later := time.Now().Add(time.Hour)
		be.NilErr(t, os.Chtimes(name, later, later))
		be.NilErr(t, s.AddDir(ctx, dir, nil))
		unchanged(t, s)
	})

	t.Run("modified duplicate added again", func(t *testing.T) {
		// either file with the duplicate content can change
		for _, name := range []string{"a.txt", "b.txt"} {
			s, dir := setup(t)
			be.NilErr(t, os.WriteFile(filepath.Join(dir, name), []byte("changed"), 0o644))
			be.NilErr(t, s.AddDir(ctx, dir, nil))
			unchanged(t, s)
			stageHasContent(t, s)
		}
	})

	t.Run("file removed from stage and deleted", func(t *testing.T) {
		s, dir := setup(t)
		be.NilErr(t, s.Remove("c.txt", false, nil))
		be.NilErr(t, os.Remove(filepath.Join(dir, "c.txt")))
		unchanged(t, s)
	})

	t.Run("content in object isn't unavailable", func(t *testing.T) {
		dir := writeFiles(t, map[string]string{"a_file.txt": "Hello! I am a file.\n"})
		s := newStage(t, newRoot(t), "ark:123/abc")
		be.NilErr(t, s.AddDir(ctx, dir, nil))
		be.NilErr(t, os.Remove(filepath.Join(dir, "a_file.txt")))
		result, err := s.Check(ctx)
		be.NilErr(t, err)
		be.Equal(t, 1, len(result.Changed))
		be.Equal(t, 0, len(result.Unavailable))
	})

	t.Run("minimal compare", func(t *testing.T) {
		s, _ := setup(t)
		s.Compare = statcache.CompareMinimal
		result, err := s.Check(ctx)
		be.NilErr(t, err)
		be.Equal(t, 0, len(result.Changed))
	})
}

func TestStage_Content(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*stage.Stage, string) {
		dir := writeFiles(t, map[string]string{"a.txt": "same", "b.txt": "same", "c.txt": "other"})
		s := newStage(t, newRoot(t), "ark:xyz/987")
		be.NilErr(t, s.AddDir(ctx, dir, nil))
		return s, dir
	}

	t.Run("unchanged", func(t *testing.T) {
		s, _ := setup(t)
		stageHasContent(t, s)
	})

	t.Run("duplicate content from unchanged file", func(t *testing.T) {
		s, dir := setup(t)
		be.NilErr(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed"), 0o644))
		content, err := s.Content(ctx, openLocal(t))
		be.NilErr(t, err)
		_, name := content.GetContent(s.Update().NewState().PathMap()["a.txt"])
		be.Equal(t, "b.txt", name)
	})

	t.Run("unavailable", func(t *testing.T) {
		s, dir := setup(t)
		be.NilErr(t, os.Remove(filepath.Join(dir, "c.txt")))
		_, err := s.Content(ctx, openLocal(t))
		var unavailable *stage.UnavailableError
		be.True(t, errors.As(err, &unavailable))
		be.AllEqual(t, []string{"c.txt"}, unavailable.Paths)
		be.True(t, errors.Is(err, ocfl.ErrMissingContent))
		be.In(t, "c.txt", err.Error())
	})

	t.Run("open error", func(t *testing.T) {
		s, _ := setup(t)
		openErr := errors.New("can't open")
		_, err := s.Content(ctx, func(string) (ocflfs.FS, error) { return nil, openErr })
		be.True(t, errors.Is(err, openErr))
	})
}

func TestReadWrite(t *testing.T) {
	ctx := context.Background()
	_, fixtures := testutil.TempDirTestData(t,
		"testdata/content-fixture",
		"testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root",
	)
	root, err := ocfl.NewRoot(ctx, ocflfs.DirFS(fixtures[1]), ".")
	be.NilErr(t, err)
	s := newStage(t, root, "ark:123/abc")
	be.NilErr(t, s.AddDir(ctx, fixtures[0], nil))
	dir := t.TempDir()
	name := filepath.Join(dir, "stage.json")

	t.Run("draft", func(t *testing.T) {
		be.NilErr(t, stage.Write(name, s))
		got, err := stage.Read(name)
		be.NilErr(t, err)
		be.False(t, got.Finalized())
		be.Equal(t, testRootLoc, got.Root())
		be.True(t, got.Update().NewState().Eq(s.Update().NewState()))
		for p := range s.Update().NewState().PathMap() {
			want, wantOK := s.SourceFile(p)
			src, ok := got.SourceFile(p)
			be.Equal(t, wantOK, ok)
			be.Equal(t, want, src)
		}
		// files that were added aren't read again
		result, err := got.Check(ctx)
		be.NilErr(t, err)
		be.Equal(t, 0, len(result.Changed))
	})

	t.Run("finalized", func(t *testing.T) {
		be.NilErr(t, s.Finalize("message", ocfl.User{Name: "name"}))
		be.NilErr(t, stage.Write(name, s))
		got, err := stage.Read(name)
		be.NilErr(t, err)
		be.True(t, got.Finalized())
		be.Equal(t, s.Update().NewInventoryDigest(), got.Update().NewInventoryDigest())
	})

	t.Run("no temp files left", func(t *testing.T) {
		entries, err := os.ReadDir(dir)
		be.NilErr(t, err)
		be.Equal(t, 1, len(entries))
		be.Equal(t, "stage.json", entries[0].Name())
	})

	t.Run("invalid", func(t *testing.T) {
		bad := filepath.Join(dir, "bad.json")
		be.NilErr(t, os.WriteFile(bad, []byte(`{"object_id":"old stage file"}`), 0644))
		_, err := stage.Read(bad)
		be.Nonzero(t, err)
	})

	t.Run("no root", func(t *testing.T) {
		var saved map[string]any
		b, err := json.Marshal(s)
		be.NilErr(t, err)
		be.NilErr(t, json.Unmarshal(b, &saved))
		delete(saved, "root")
		b, err = json.Marshal(saved)
		be.NilErr(t, err)
		bad := filepath.Join(dir, "bad.json")
		be.NilErr(t, os.WriteFile(bad, b, 0644))
		_, err = stage.Read(bad)
		be.In(t, "no storage root", err.Error())
	})

	t.Run("staged file doesn't match update", func(t *testing.T) {
		var saved map[string]any
		b, err := json.Marshal(s)
		be.NilErr(t, err)
		be.NilErr(t, json.Unmarshal(b, &saved))
		files := saved["files"].(map[string]any)
		files["hello.csv"].(map[string]any)["digest"] = strings.Repeat("0", 128)
		b, err = json.Marshal(saved)
		be.NilErr(t, err)
		bad := filepath.Join(dir, "bad.json")
		be.NilErr(t, os.WriteFile(bad, b, 0644))
		_, err = stage.Read(bad)
		be.In(t, "hello.csv", err.Error())
	})
}

func TestWriteNew(t *testing.T) {
	root := newRoot(t)
	dir := t.TempDir()
	name := filepath.Join(dir, "stage.json")
	be.NilErr(t, stage.WriteNew(name, newStage(t, root, "ark:123/abc")))
	got, err := stage.Read(name)
	be.NilErr(t, err)
	be.Equal(t, "ark:123/abc", got.ID())
	// an existing file isn't replaced
	err = stage.WriteNew(name, newStage(t, root, "ark:xyz/987"))
	be.True(t, errors.Is(err, fs.ErrExist))
	got, err = stage.Read(name)
	be.NilErr(t, err)
	be.Equal(t, "ark:123/abc", got.ID())
	// no temp files left
	entries, err := os.ReadDir(dir)
	be.NilErr(t, err)
	be.Equal(t, 1, len(entries))
}

// testRootLoc is the storage root location used for stages in tests.
const testRootLoc = "file:///ocfl-root"

// newRoot returns a copy of a storage root with the object ark:123/abc, which
// has the file a_file.txt.
func newRoot(t *testing.T) *ocfl.Root {
	t.Helper()
	_, fixtures := testutil.TempDirTestData(t, "testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root")
	root, err := ocfl.NewRoot(context.Background(), ocflfs.DirFS(fixtures[0]), ".")
	be.NilErr(t, err)
	return root
}

// newStage returns a new stage for the object id in root.
func newStage(t *testing.T, root *ocfl.Root, id string) *stage.Stage {
	t.Helper()
	s, err := stage.New(context.Background(), root, testRootLoc, id, digest.SHA512)
	be.NilErr(t, err)
	return s
}

// writeFiles writes files with the names and contents in a new directory and
// returns the directory.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		be.NilErr(t, os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644))
	}
	return dir
}

// openLocal returns a function that opens a local FS that is closed when the
// test ends.
func openLocal(t *testing.T) func(string) (ocflfs.FS, error) {
	return func(dir string) (ocflfs.FS, error) {
		fsys, err := local.NewFS(dir)
		if err != nil {
			return nil, err
		}
		t.Cleanup(func() { fsys.Close() })
		return fsys, nil
	}
}

// list returns the logical paths in the stage state.
func list(s *stage.Stage) string {
	var out bytes.Buffer
	s.List(&out, false)
	return out.String()
}

func stageStateMatchesDir(t *testing.T, s *stage.Stage, dir string, withHidden bool, as string) {
	t.Helper()
	state := s.Update().NewState().PathMap()
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
		if state[p] == "" {
			t.Error("stage doesnt include", p)
		}
		return nil
	})
	be.NilErr(t, walkErr)
	if l := len(state); l != count {
		t.Errorf("stage doesn't have same number of files as content fixture; want=%d, got=%d", count, l)
	}
}

// stageHasContent checks that s has content for every digest in its state
// that isn't in the object's head version.
func stageHasContent(t *testing.T, s *stage.Stage) {
	t.Helper()
	content, err := s.Content(context.Background(), openLocal(t))
	be.NilErr(t, err)
	base := s.Update().BaseState()
	for dig := range s.Update().NewState() {
		if _, inBase := base[dig]; inBase {
			continue
		}
		if fsys, _ := content.GetContent(dig); fsys == nil {
			t.Errorf("stage has no content for %s", dig)
		}
	}
}

func isHidden(n string) bool {
	for part := range strings.SplitSeq(n, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}
