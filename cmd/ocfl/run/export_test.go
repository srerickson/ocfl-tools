package run_test

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

func TestExport(t *testing.T) {
	_, fixtures := testutil.TempDirTestData(t,
		"testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root",
		"testdata/object-fixtures/1.1/good-objects/spec-ex-full",
	)
	goodStoreFixture := fixtures[0]
	goodObjectFixture := fixtures[1]
	t.Run("object id", func(t *testing.T) {
		to := t.TempDir()
		id := `ark:123/abc`
		args := []string{`export`,
			`--root`, goodStoreFixture,
			`--id`, id,
			`--to`, to,
		}
		testutil.RunCLI(args, nil, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			// exported file exists
			_, err = fs.Stat(os.DirFS(to), "a_file.txt")
			be.NilErr(t, err)
		})
	})

	t.Run("object path", func(t *testing.T) {
		to := t.TempDir()
		args := []string{`export`,
			`--object`, goodObjectFixture,
			`--to`, to,
		}
		testutil.RunCLI(args, nil, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			outFS := os.DirFS(to)
			for _, name := range []string{`empty2.txt`, `foo/bar.xml`, `image.tiff`} {
				_, err = fs.Stat(outFS, name)
				be.NilErr(t, err)
			}
		})
	})

	t.Run("object url", func(t *testing.T) {
		srv := httptest.NewServer(http.FileServer(http.FS(os.DirFS(goodObjectFixture))))
		defer srv.Close()
		to := t.TempDir()
		args := []string{`export`,
			`--object`, srv.URL,
			`--to`, to,
		}
		testutil.RunCLI(args, nil, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			outFS := os.DirFS(to)
			for _, name := range []string{`empty2.txt`, `foo/bar.xml`, `image.tiff`} {
				_, err = fs.Stat(outFS, name)
				be.NilErr(t, err)
			}
		})
	})
}
