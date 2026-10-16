package run

import (
	"context"
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
	s, err := stage.New(ctx, root, cmd.ID, alg)
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
	_, err = objectUpdateOrRevert(ctx, env, root, s, cmd.Message, newUser(cmd.Name, cmd.Email))
	return err
}
