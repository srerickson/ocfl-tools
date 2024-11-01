package run

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/ui"
)

const stageHelp = "Digest files in a directory in preparation for a commit"

type stageCmd struct {
	Alg  string `name:"alg" default:"sha512" help:"Digest Algorithm used to digets files in the path"`
	Path string `arg:"" name:"path" help:"local directory of files to stage"`
}

func (cmd *stageCmd) Run(ctx context.Context, stdout io.Writer, logger *slog.Logger, getenv func(string) string) error {
	digests, err := ui.StagingUI(ctx, os.DirFS(cmd.Path), cmd.Alg)
	if err != nil {
		return err
	}
	sortedPaths := make([]string, 0, len(digests))
	for p := range digests {
		sortedPaths = append(sortedPaths, p)
	}
	sort.Strings(sortedPaths)
	for _, p := range sortedPaths {
		fmt.Println(digests[p], p)
	}
	return nil
}
