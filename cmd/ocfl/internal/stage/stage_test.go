package stage_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
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

		t.Run("with remove without hidden", func(t *testing.T) {
			dir := writeFiles(t, map[string]string{
				"a.txt":   "a",
				".hidden": "h",
				".gone":   "g",
			})
			be.NilErr(t, os.Mkdir(filepath.Join(dir, ".dir"), 0o755))
			be.NilErr(t, os.WriteFile(filepath.Join(dir, ".dir", "x.txt"), []byte("x"), 0o644))
			// missing from dir, but not under 'as' unless it is "."
			otherDir := writeFiles(t, map[string]string{".gone": "o"})
			for _, as := range []string{".", "sub"} {
				t.Run("as "+as, func(t *testing.T) {
					s := newStage(t, root, newID)
					be.NilErr(t, s.AddDir(ctx, dir, nil, stage.AddAs(as)))
					// a staged file that isn't under 'as' isn't removed
					be.NilErr(t, s.AddFile(ctx, filepath.Join(otherDir, ".gone"), nil, stage.AddAs("other/.gone")))
					be.NilErr(t, os.Remove(filepath.Join(dir, ".gone")))
					be.NilErr(t, os.Remove(filepath.Join(dir, "a.txt")))
					be.NilErr(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644))
					t.Cleanup(func() {
						be.NilErr(t, os.WriteFile(filepath.Join(dir, ".gone"), []byte("g"), 0o644))
						be.NilErr(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
						be.NilErr(t, os.Remove(filepath.Join(dir, "b.txt")))
					})
					be.NilErr(t, s.AddDir(ctx, dir, nil, stage.AddAs(as), stage.AddAndRemove(), stage.AddWithoutHidden()))
					// hidden files that still exist are kept; missing files
					// are removed, hidden or not
					var want []string
					for _, p := range []string{".dir/x.txt", ".hidden", "b.txt"} {
						want = append(want, path.Join(as, p))
					}
					if as != "." {
						want = append(want, "other/.gone")
					}
					slices.Sort(want)
					be.Equal(t, strings.Join(want, "\n")+"\n", list(s))
					stageHasContent(t, s)
				})
			}
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

func TestStage_CheckUpstream(t *testing.T) {
	ctx := context.Background()
	for _, fx := range upstreamFixtures {
		t.Run(fx.desc, func(t *testing.T) {
			root, f := fx.build(t)
			up, err := f.Stage().CheckUpstream(ctx, root)
			be.NilErr(t, err)
			be.Equal(t, fx.want, up)
		})
	}

	t.Run("malformed sidecar", func(t *testing.T) {
		_, root := newRoots(t)
		f := draftFile(t, root, existingID)
		writeObjectFile(t, root, existingID, "inventory.json.sha512", "not a digest")
		_, err := f.Stage().CheckUpstream(ctx, root)
		be.True(t, errors.Is(err, ocfl.ErrInventorySidecarContents))
	})

	t.Run("canceled context", func(t *testing.T) {
		_, root := newRoots(t)
		f := draftFile(t, root, existingID)
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := f.Stage().CheckUpstream(ctx, root)
		be.True(t, errors.Is(err, context.Canceled))
	})

	t.Run("nil root", func(t *testing.T) {
		_, root := newRoots(t)
		f := draftFile(t, root, existingID)
		defer func() { be.Nonzero(t, recover()) }()
		f.Stage().CheckUpstream(ctx, nil)
	})
}

// TestStage_CheckUpstreamMatchesCommit checks that CheckUpstream agrees with
// the operations that decide whether the object has changed since the stage
// was created: File.Commit for a draft stage, and File.Resume and File.Revert
// for a frozen one. The documented differences are checked to still be
// there.
func TestStage_CheckUpstreamMatchesCommit(t *testing.T) {
	ctx := context.Background()
	for _, fx := range upstreamFixtures {
		t.Run(fx.desc, func(t *testing.T) {
			root, f := fx.build(t)
			up, err := f.Stage().CheckUpstream(ctx, root)
			be.NilErr(t, err)
			var got stage.UpstreamState // the state that ocfl-go decides
			if !f.Stage().Frozen() {
				root, f := fx.build(t)
				err := f.Commit(ctx, root, "message", ocfl.User{Name: "name"}, nil)
				got = stage.UpstreamAtBase
				if errors.Is(err, ocfl.ErrUpdateConflict) {
					got = stage.UpstreamChanged
				}
			} else {
				root, f := fx.build(t)
				revertErr := f.Revert(ctx, root, nil)
				switch {
				case revertErr == nil:
					got = stage.UpstreamAtBase
				case errors.Is(revertErr, ocfl.ErrUpdateCompleted):
					got = stage.UpstreamCommitted
				case errors.Is(revertErr, ocfl.ErrUpdateConflict):
					got = stage.UpstreamChanged
				default:
					t.Fatalf("unexpected revert error: %v", revertErr)
				}
				root, f = fx.build(t)
				resumeErr := f.Resume(ctx, root, nil)
				be.Equal(t, got == stage.UpstreamChanged, errors.Is(resumeErr, ocfl.ErrUpdateConflict))
				if got == stage.UpstreamCommitted {
					be.NilErr(t, resumeErr)
				}
			}
			if fx.exception != "" {
				t.Log("documented difference:", fx.exception)
				be.Unequal(t, got, up.State)
				return
			}
			be.Equal(t, got, up.State)
		})
	}
}

// object IDs used in upstream fixtures: the first is in the roots returned by
// newRoots, and the second isn't.
const (
	existingID = "ark:123/abc"
	newID      = "ark:xyz/987"
)

// upstreamFixture is a storage root and a stage file for one of its objects,
// in a state that CheckUpstream reports as want.
type upstreamFixture struct {
	desc  string
	build func(t *testing.T) (*ocfl.Root, *stage.File)
	want  stage.Upstream
	// exception describes a documented difference between CheckUpstream and
	// committing the stage. If it's set, they must disagree.
	exception string
}

var upstreamFixtures = []upstreamFixture{
	{
		desc: "draft, unchanged",
		build: func(t *testing.T) (*ocfl.Root, *stage.File) {
			_, root := newRoots(t)
			return root, draftFile(t, root, existingID)
		},
		want: stage.Upstream{State: stage.UpstreamAtBase, Exists: true},
	},
	{
		desc: "draft, version committed by another stage",
		build: func(t *testing.T) (*ocfl.Root, *stage.File) {
			_, root := newRoots(t)
			f := draftFile(t, root, existingID)
			commitOther(t, root, existingID)
			return root, f
		},
		want: stage.Upstream{State: stage.UpstreamChanged, Exists: true, Head: ocfl.V(2)},
	},
	{
		desc: "draft, object deleted",
		build: func(t *testing.T) (*ocfl.Root, *stage.File) {
			_, root := newRoots(t)
			f := draftFile(t, root, existingID)
			be.NilErr(t, os.RemoveAll(objectDir(t, root, existingID)))
			return root, f
		},
		want: stage.Upstream{State: stage.UpstreamChanged},
	},
	{
		desc: "draft, commit by another stage interrupted before the root sidecar",
		build: func(t *testing.T) (*ocfl.Root, *stage.File) {
			_, root := newRoots(t)
			f := draftFile(t, root, existingID)
			other := draftFile(t, root, existingID)
			interruptCommit(t, root, other)
			return root, f
		},
		want: stage.Upstream{State: stage.UpstreamAtBase, Exists: true},
	},
	{
		desc: "draft for a new object, no object",
		build: func(t *testing.T) (*ocfl.Root, *stage.File) {
			_, root := newRoots(t)
			return root, draftFile(t, root, newID)
		},
		want: stage.Upstream{State: stage.UpstreamAtBase},
	},
	{
		desc: "draft for a new object, object created by another stage",
		build: func(t *testing.T) (*ocfl.Root, *stage.File) {
			_, root := newRoots(t)
			f := draftFile(t, root, newID)
			commitOther(t, root, newID)
			return root, f
		},
		want: stage.Upstream{State: stage.UpstreamChanged, Exists: true, Head: ocfl.V(1)},
	},
	{
		desc: "draft for a new object, stray file",
		build: func(t *testing.T) (*ocfl.Root, *stage.File) {
			_, root := newRoots(t)
			f := draftFile(t, root, newID)
			writeObjectFile(t, root, newID, "stray.txt", "stray")
			return root, f
		},
		want: stage.Upstream{State: stage.UpstreamChanged, Exists: true},
	},
	{
		desc: "draft for a new object, files the update writes",
		build: func(t *testing.T) (*ocfl.Root, *stage.File) {
			_, root := newRoots(t)
			f := draftFile(t, root, newID)
			decl := ocfl.Namaste{Type: ocfl.NamasteTypeObject, Version: root.Spec()}
			writeObjectFile(t, root, newID, decl.Name(), decl.Body())
			be.NilErr(t, os.Mkdir(filepath.Join(objectDir(t, root, newID), "v1"), 0o755))
			return root, f
		},
		want:      stage.Upstream{State: stage.UpstreamChanged, Exists: true},
		exception: "a draft for a new object reports any content in the object directory; commit accepts files it would write",
	},
	{
		desc: "frozen, interrupted before writing",
		build: func(t *testing.T) (*ocfl.Root, *stage.File) {
			readOnly, root := newRoots(t)
			return root, frozenFile(t, readOnly, root, existingID)
		},
		want: stage.Upstream{State: stage.UpstreamAtBase, Exists: true},
	},
	{
		desc: "frozen, interrupted before the root sidecar",
		build: func(t *testing.T) (*ocfl.Root, *stage.File) {
			_, root := newRoots(t)
			f := draftFile(t, root, existingID)
			return root, interruptCommit(t, root, f)
		},
		want: stage.Upstream{State: stage.UpstreamAtBase, Exists: true},
	},
	{
		desc: "frozen for a new object, interrupted before writing",
		build: func(t *testing.T) (*ocfl.Root, *stage.File) {
			readOnly, root := newRoots(t)
			return root, frozenFile(t, readOnly, root, newID)
		},
		want: stage.Upstream{State: stage.UpstreamAtBase},
	},
	{
		desc: "frozen for a new object, interrupted before the root sidecar",
		build: func(t *testing.T) (*ocfl.Root, *stage.File) {
			_, root := newRoots(t)
			f := draftFile(t, root, newID)
			return root, interruptCommit(t, root, f)
		},
		want: stage.Upstream{State: stage.UpstreamAtBase, Exists: true},
	},
	{
		desc: "frozen, commit complete",
		build: func(t *testing.T) (*ocfl.Root, *stage.File) {
			readOnly, root := newRoots(t)
			f := frozenFile(t, readOnly, root, existingID)
			saved, err := os.ReadFile(f.Path())
			be.NilErr(t, err)
			be.NilErr(t, f.Resume(context.Background(), root, nil))
			// the stage file is left behind
			be.NilErr(t, os.WriteFile(f.Path(), saved, 0o644))
			return root, openFile(t, f.Path())
		},
		want: stage.Upstream{State: stage.UpstreamCommitted, Exists: true, Head: ocfl.V(2)},
	},
	{
		desc: "frozen, version committed by another stage",
		build: func(t *testing.T) (*ocfl.Root, *stage.File) {
			readOnly, root := newRoots(t)
			f := frozenFile(t, readOnly, root, existingID)
			commitOther(t, root, existingID)
			return root, f
		},
		want: stage.Upstream{State: stage.UpstreamChanged, Exists: true, Head: ocfl.V(2)},
	},
	{
		desc: "frozen for a new object, stray file",
		build: func(t *testing.T) (*ocfl.Root, *stage.File) {
			readOnly, root := newRoots(t)
			f := frozenFile(t, readOnly, root, newID)
			writeObjectFile(t, root, newID, "stray.txt", "stray")
			return root, f
		},
		want:      stage.Upstream{State: stage.UpstreamAtBase, Exists: true},
		exception: "a frozen stage for a new object with no sidecar is at base; commit rejects files it doesn't write",
	},
}

// draftFile returns a saved draft stage file for the object id in root, with
// a new staged file.
func draftFile(t *testing.T, root *ocfl.Root, id string) *stage.File {
	t.Helper()
	f := newFile(t, root, id)
	be.NilErr(t, f.Stage().AddDir(context.Background(), writeFiles(t, map[string]string{"a.txt": "a"}), nil))
	be.NilErr(t, f.Save())
	return f
}

// frozenFile returns a stage file for the object id in root whose commit
// failed before anything was written, using readOnly, a read-only root for
// the same storage root.
func frozenFile(t *testing.T, readOnly, root *ocfl.Root, id string) *stage.File {
	t.Helper()
	f := draftFile(t, root, id)
	be.Nonzero(t, f.Commit(context.Background(), readOnly, "message", ocfl.User{Name: "name"}, nil))
	return openFile(t, f.Path())
}

// interruptCommit commits the draft stage file f to root, interrupting it
// after the root inventory.json is written, before its sidecar, and returns
// the stage file with the frozen stage.
func interruptCommit(t *testing.T, root *ocfl.Root, f *stage.File) *stage.File {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// steps are logged before they run, and aren't run once ctx is canceled
	logger := slog.New(&cancelHandler{msg: "write inventory.json.sha512", cancel: cancel})
	head := f.Stage().NextHead()
	err := f.Commit(ctx, root, "message", ocfl.User{Name: "name"}, logger)
	be.True(t, errors.Is(err, context.Canceled))
	dir := objectDir(t, root, f.Stage().ID())
	rootInv, err := os.ReadFile(filepath.Join(dir, "inventory.json"))
	be.NilErr(t, err)
	verInv, err := os.ReadFile(filepath.Join(dir, head.String(), "inventory.json"))
	be.NilErr(t, err)
	be.Equal(t, string(verInv), string(rootInv))
	return openFile(t, f.Path())
}

// commitOther commits a new version of the object id in root with another
// stage.
func commitOther(t *testing.T, root *ocfl.Root, id string) {
	t.Helper()
	other := newFile(t, root, id)
	be.NilErr(t, other.Stage().AddDir(context.Background(), writeFiles(t, map[string]string{"other.txt": "other"}), nil))
	be.NilErr(t, other.Commit(context.Background(), root, "other", ocfl.User{Name: "other"}, nil))
}

// openFile opens the stage file name.
func openFile(t *testing.T, name string) *stage.File {
	t.Helper()
	f, err := stage.Open(name)
	be.NilErr(t, err)
	return f
}

// objectDir returns the local directory of the object id in root, which
// must use a local FS.
func objectDir(t *testing.T, root *ocfl.Root, id string) string {
	t.Helper()
	objPath, err := root.ResolveID(id)
	be.NilErr(t, err)
	return filepath.Join(root.FS().(*local.FS).Root(), filepath.FromSlash(path.Join(root.Path(), objPath)))
}

// writeObjectFile writes the file name, with data, in the directory of the
// object id in root, which must use a local FS.
func writeObjectFile(t *testing.T, root *ocfl.Root, id, name, data string) {
	t.Helper()
	dir := objectDir(t, root, id)
	be.NilErr(t, os.MkdirAll(dir, 0o755))
	be.NilErr(t, os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644))
}

// cancelHandler is a slog.Handler that calls cancel when a record with the
// message msg is handled.
type cancelHandler struct {
	msg    string
	cancel func()
}

func (h *cancelHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *cancelHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Message == h.msg {
		h.cancel()
	}
	return nil
}

func (h *cancelHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *cancelHandler) WithGroup(string) slog.Handler      { return h }

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
