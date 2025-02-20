package run

import "github.com/srerickson/ocfl-tools/cmd/ocfl/internal/stage"

const commitHelp = "Create or update an object using contents of a local directory"

type CommitCmd struct {
	ID      string `name:"id" short:"i" help:"The ID for the object to create or update"`
	Message string `name:"message" short:"m" help:"Message to include in the object version metadata"`
	Name    string `name:"name" short:"n" help:"Username to include in the object version metadata ($$${env_user_name})"`
	Email   string `name:"email" short:"e" help:"User email to include in the object version metadata ($$${env_user_email})"`
	Alg     string `name:"alg" default:"sha512" help:"Digest algorithm (ignored for commits to existing objects)"`
	Path    string `arg:"" name:"path" help:"local directory with object state to commit"`
}

func (cmd *CommitCmd) Run(g *globals) error {
	ctx := g.ctx
	root, err := g.getRoot()
	if err != nil {
		return err
	}
	obj, err := root.NewObject(g.ctx, cmd.ID)
	if err != nil {
		return err
	}
	stage, err := stage.NewStageFile(obj, cmd.Alg)
	if err != nil {
		return err
	}
	stage.SetLogger(g.logger)
	if err := stage.AddDir(ctx, cmd.Path, ".", false, true, 0); err != nil {
		return err
	}
	if cmd.Name == "" {
		cmd.Name = g.getenv(envVarUserName)
	}
	if cmd.Email == "" {
		cmd.Email = g.getenv(envVarUserEmail)
	}
	commit, err := stage.BuildCommit(cmd.Name, cmd.Email, cmd.Message)
	if err != nil {
		return err
	}
	return obj.Commit(ctx, commit)
}
