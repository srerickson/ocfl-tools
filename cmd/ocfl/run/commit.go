package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/srerickson/ocfl-go/digest"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/stage"
)

type CommitCmd struct {
	ID       string `name:"id" short:"i" help:"The ID for the object to create or update"`
	Message  string `name:"message" short:"m" help:"Message to include in the object version metadata"`
	Name     string `name:"name" short:"n" ocflenv:"OCFL_USER_NAME" help:"Username to include in the object version metadata"`
	Email    string `name:"email" short:"e" ocflenv:"OCFL_USER_EMAIL" help:"User email to include in the object version metadata"`
	Alg      string `name:"alg" default:"sha512" help:"Digest algorithm (ignored for commits to existing objects)"`
	NoHidden bool   `name:"no-hidden" help:"exclude hidden files and directories (.*)"`
	Path     string `arg:"" name:"path" help:"local directory with object state to commit"`
}

func (cmd *CommitCmd) Help() string {
	return `The new version's state is the contents of the directory. The commit is
saved in a temporary stage file before the object is changed. If the commit
is interrupted or fails, the stage file is kept, and the error message gives
the 'ocfl stage commit' commands that resume or revert the commit.`
}

func (cmd *CommitCmd) Run(ctx context.Context, env *cmdEnv) error {
	alg, err := digest.DefaultRegistry().Get(cmd.Alg)
	if err != nil {
		return err
	}
	absPath, err := filepath.Abs(cmd.Path)
	if err != nil {
		return err
	}
	root, err := env.getRoot(ctx)
	if err != nil {
		return err
	}
	s, err := newStage(ctx, root, cmd.ID, alg)
	if err != nil {
		return err
	}
	opts := []stage.AddOption{stage.AddAndRemove()}
	if cmd.NoHidden {
		opts = append(opts, stage.AddWithoutHidden())
	}
	if err := s.AddDir(ctx, absPath, env.logger, opts...); err != nil {
		return err
	}
	content, err := stagedContent(ctx, env, s)
	if err != nil {
		return err
	}
	if err := s.Finalize(cmd.Message, newUser(cmd.Name, cmd.Email)); err != nil {
		return err
	}
	stagePath, err := tempStageFile()
	if err != nil {
		return err
	}
	if err := stage.Write(stagePath, s); err != nil {
		os.Remove(stagePath)
		return fmt.Errorf("saving the stage file before commit: %w", err)
	}
	return applyStage(ctx, env, root, s, stagePath, false, content)
}

// tempStageFile creates an empty stage file in the temporary directory and
// returns its path.
func tempStageFile() (string, error) {
	f, err := os.CreateTemp("", "ocfl-commit-*.json")
	if err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return f.Name(), nil
}
