package run_test

import (
	"maps"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

// TestHelp checks that --help prints usage and returns without an error, for
// the top-level command, subcommands, and nested subcommands.
func TestHelp(t *testing.T) {
	for _, cmd := range []string{"", "ls", "stage", "stage add"} {
		name := strings.TrimSpace("ocfl " + cmd)
		t.Run(name, func(t *testing.T) {
			args := append(strings.Fields(cmd), "--help")
			testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
				be.NilErr(t, err)
				be.In(t, "Usage: "+name, stdout)
				be.Equal(t, "", stderr)
			})
		})
	}
}

func TestRootNotSet(t *testing.T) {
	// test commands fail if root is not set
	cmds := []string{"init-root", "ls", "stage", "validate", "export", "diff"}
	for _, cmd := range cmds {
		// should return an error
		args := []string{cmd}
		testutil.RunCLI(args, nil, func(err error, stdout string, stderr string) {
			be.True(t, err != nil)
		})
	}
}

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
	env := map[string]string{"OCFL_ROOT": rootURL, "OCFL_STAGE_CACHE": t.TempDir()}
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

// TestFlagOverridesEnv checks that flags take precedence over the environment
// variables that set their defaults.
func TestFlagOverridesEnv(t *testing.T) {
	_, fixtures := testutil.TempDirTestData(t,
		`testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root`,
	)
	rootPath := fixtures[0]
	env := map[string]string{"OCFL_ROOT": filepath.Join(t.TempDir(), "missing")}
	testutil.RunCLI([]string{"info", "--root", rootPath}, env, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
		be.In(t, "storage root: "+fileURL(rootPath)+"\n", stdout)
	})
}

// The default stage cache is in the user's cache directory.
func TestStage_DefaultCache(t *testing.T) {
	switch runtime.GOOS {
	case "windows", "darwin", "ios", "plan9":
		t.Skip("uses XDG_CACHE_HOME")
	}
	tmpDir := t.TempDir()
	home := filepath.Join(tmpDir, "home")
	rootEnv := map[string]string{"OCFL_ROOT": filepath.Join(tmpDir, "ocfl")}
	runOK(t, rootEnv, "init-root", "--layout", "0003-hash-and-id-n-tuple-storage-layout")
	withEnv := func(kv ...string) map[string]string {
		env := maps.Clone(rootEnv)
		for i := 0; i < len(kv); i += 2 {
			env[kv[i]] = kv[i+1]
		}
		return env
	}

	t.Run("XDG_CACHE_HOME", func(t *testing.T) {
		cacheHome := filepath.Join(tmpDir, "xdg-cache")
		env := withEnv("HOME", home, "XDG_CACHE_HOME", cacheHome)
		runOK(t, env, "stage", "new", "--id", "object-01")
		be.Equal(t, 1, len(stageIDs(t, filepath.Join(cacheHome, "ocfl-tools", "stage"))))
	})

	t.Run("HOME", func(t *testing.T) {
		env := withEnv("HOME", home)
		runOK(t, env, "stage", "new", "--id", "object-01")
		be.Equal(t, 1, len(stageIDs(t, filepath.Join(home, ".cache", "ocfl-tools", "stage"))))
	})

	t.Run("no cache directory", func(t *testing.T) {
		for _, env := range []map[string]string{
			withEnv(),
			withEnv("HOME", home, "XDG_CACHE_HOME", "relative/cache"),
		} {
			testutil.RunCLI([]string{"stage", "new", "--id", "object-02"}, env, func(err error, stdout, stderr string) {
				be.Nonzero(t, err)
				be.In(t, "set --stage-cache or $OCFL_STAGE_CACHE", stderr)
			})
		}
	})
}
