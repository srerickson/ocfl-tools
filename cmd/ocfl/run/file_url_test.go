package run_test

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

// fileURL returns the file:// url for the local path p.
func fileURL(p string) string {
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // windows: C:/dir -> /C:/dir
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// TestFileURL checks that file:// urls work as storage root and object
// locations, and that the printed location is the same url.
func TestFileURL(t *testing.T) {
	tmpDir, fixtures := testutil.TempDirTestData(t, `testdata/content-fixture`)
	contentFixture := fixtures[0]
	// the root doesn't exist yet: init-root must create it. The space and
	// colon are escaped in, or allowed by, a file url, but a colon in the
	// first segment of a relative path would be mistaken for a url scheme.
	rootURL := fileURL(filepath.Join(tmpDir, "ab:c", "my root"))
	env := map[string]string{"OCFL_ROOT": rootURL}
	objID := "object-01"

	testutil.RunCLI([]string{"init-root"}, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		be.In(t, "storage root: "+rootURL+"\n", stdout)
	})
	testutil.RunCLI([]string{
		"commit", contentFixture,
		"--id", objID,
		"--message", "first commit",
		"--name", "Me",
		"--email", "me@domain.net",
	}, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
	})
	testutil.RunCLI([]string{"ls"}, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		be.In(t, objID, stdout)
	})
	// --root as a flag rather than from the environment
	testutil.RunCLI([]string{"info", "--root", rootURL}, nil, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		be.In(t, "storage root: "+rootURL+"\n", stdout)
	})
	// the object path printed by info is a file url that works as --object
	var objURL string
	testutil.RunCLI([]string{"info", "--id", objID}, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		for line := range strings.Lines(stdout) {
			if loc, ok := strings.CutPrefix(line, "object path: "); ok {
				objURL = strings.TrimSpace(loc)
			}
		}
	})
	be.True(t, strings.HasPrefix(objURL, rootURL+"/"))
	testutil.RunCLI([]string{"validate", "--object", objURL}, nil, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
	})
	testutil.RunCLI([]string{"log", "--object", objURL}, nil, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		be.In(t, "first commit", stdout)
	})
}
