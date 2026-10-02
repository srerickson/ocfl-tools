package run

import (
	"context"
	"fmt"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/stage"
)

const commitHelp = "Create or update an object using contents of a local directory"

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
	root, err := env.getRoot(ctx)
	if err != nil {
		return err
	}
	obj, err := root.NewObject(ctx, cmd.ID)
	if err != nil {
		return err
	}
	changes, err := stage.NewStageFile(obj, cmd.Alg)
	if err != nil {
		return err
	}
	changes.SetLogger(env.logger)
	opts := []stage.AddOption{stage.AddAndRemove()}
	if cmd.NoHidden {
		opts = append(opts, stage.AddWithoutHidden())
	}
	if err := changes.AddDir(ctx, cmd.Path, opts...); err != nil {
		return err
	}
	stage, err := changes.Stage()
	if err != nil {
		return fmt.Errorf("stage has errors: %w", err)
	}
	_, err = objectUpdateOrRevert(ctx, obj, stage, cmd.Message, newUser(cmd.Name, cmd.Email), env.logger)
	return err
}
