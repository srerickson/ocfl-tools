package run

import (
	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
)

const commitHelp = "Create or update an object in a storage root"

type commitCmd struct {
	ID      string `name:"id" short:"i" help:"The ID for the object to create or update"`
	Message string `name:"message" short:"m" help:"Message to include in the object version metadata"`
	Name    string `name:"name" short:"n" help:"Username to include in the object version metadata ($$${env_user_name})"`
	Email   string `name:"email" short:"e" help:"User email to include in the object version metadata ($$${env_user_email})"`
	Spec    string `name:"ocflv" default:"1.1" help:"OCFL spec fo the new object"`
	Alg     string `name:"alg" default:"sha512" help:"Digest Algorithm used to digest content. Ignored for commit to an existing object."`
	Path    string `arg:"" name:"path" help:"local directory with object state to commit"`
}

func (cmd *commitCmd) Run(g *globals) error {
	root, err := g.getRoot()
	if err != nil {
		return err
	}
	readFS := ocfl.DirFS(cmd.Path)
	obj, err := root.NewObject(g.ctx, cmd.ID)
	if err != nil {
		return err
	}
	alg, err := digest.DefaultRegistry().Get(cmd.Alg)
	if err != nil {
		return err
	}
	if obj.Exists() {
		// use existing object's digest algorithm
		alg = obj.Inventory().DigestAlgorithm()
	}
	stage, err := ocfl.StageDir(g.ctx, readFS, ".", alg)
	if err != nil {
		return err
	}
	userName := cmd.Name
	if userName == "" {
		userName = g.getenv(envVarUserName)
	}
	userEmail := cmd.Email
	if userEmail == "" {
		userEmail = g.getenv(envVarUserEmail)
	}
	return obj.Commit(g.ctx, &ocfl.Commit{
		ID:      cmd.ID,
		Stage:   stage,
		Message: cmd.Message,
		User:    ocfl.User{Name: userName, Address: userEmail},
		Spec:    ocfl.Spec(cmd.Spec),
	})
}
