package run_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

func TestLS(t *testing.T) {
	t.Run("object url", func(t *testing.T) {
		srv := httptest.NewServer(http.FileServer(http.FS(testutil.TestDataFS())))
		defer srv.Close()
		objURL, err := url.JoinPath(srv.URL, "testdata", "object-fixtures", "1.1", "good-objects", "spec-ex-full")
		be.NilErr(t, err)
		cmd := []string{"ls", "--object", objURL}
		testutil.RunCLI(cmd, nil, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.In(t, `empty2.txt`, stdout)
			be.In(t, `foo/bar.xml`, stdout)
			be.In(t, `image.tiff`, stdout)
		})
		// missing
		cmd = []string{"ls", "--object", objURL + "/missing"}
		testutil.RunCLI(cmd, nil, func(err error, stdout string, stderr string) {
			be.True(t, err != nil)
			be.In(t, "does not exist", stderr)
		})

	})
}
