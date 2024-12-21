package run_test

import (
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/run"
)

func TestVersionCmd(t *testing.T) {
	args := []string{`version`}
	testutil.RunCLI(args, nil, func(err error, stdout string, stderr string) {
		be.NilErr(t, err)
		be.In(t, run.Version, stdout)
	})
}
