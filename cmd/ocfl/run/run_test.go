package run_test

import (
	"path/filepath"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

var (
	testDataPath       = filepath.Join(`..`, `..`, `..`, `testdata`)
	goodObjectFixtures = filepath.Join(testDataPath, `object-fixtures`, `1.1`, `good-objects`)
	badObjectFixtures  = filepath.Join(testDataPath, `object-fixtures`, `1.1`, `bad-objects`)
	goodStoreFixtures  = filepath.Join(testDataPath, `store-fixtures`, `1.0`, `good-stores`)
	badStoreFixtures   = filepath.Join(testDataPath, `store-fixtures`, `1.0`, `bad-stores`)
	contentFixture     = filepath.Join(testDataPath, `content-fixture`)
)

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
