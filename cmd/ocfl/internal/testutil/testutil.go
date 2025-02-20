package testutil

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

//go:embed all:testdata/*
var testDataFS embed.FS

func TestDataFS() fs.FS { return testDataFS }

// TempDirTestData creates a temporary directory and copies testdata into it. If
// subdirs are included, only those subdirectories are copied; the second
// return value is a slice of temporary filepaths for each subdirectory.
// The directory is automatically cleaned-up at the end of the test.
func TempDirTestData(t *testing.T, subdirs ...string) (string, []string) {
	tmpDir := t.TempDir()
	if len(subdirs) < 1 {
		if err := os.CopyFS(tmpDir, testDataFS); err != nil {
			t.Fatal("creating temp testdata directory: ", err)
			return "", nil
		}
		return tmpDir, nil
	}
	var tmpSubs []string
	for _, srcDir := range subdirs {
		subfs, err := fs.Sub(testDataFS, srcDir)
		if err != nil {
			t.Fatalf("creating temp testdata directory: %s: %s", srcDir, err)
		}
		dstDir := filepath.Join(tmpDir, filepath.FromSlash(srcDir))
		tmpSubs = append(tmpSubs, dstDir)
		if err := os.CopyFS(dstDir, subfs); err != nil {
			t.Fatalf("creating temp testdata directory: %s: %s", srcDir, err)
			return "", nil
		}
	}
	return tmpDir, tmpSubs
}
