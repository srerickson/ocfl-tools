package run

import (
	"context"
	"errors"
	"io"
	"log/slog"

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

func (cmd *commitCmd) Run(ctx context.Context, root *ocfl.Root, stdout io.Writer, logger *slog.Logger, getenv func(string) string) error {
	if root == nil {
		return errors.New("storage root not set")
	}
	readFS := ocfl.DirFS(cmd.Path)
	obj, err := root.NewObject(ctx, cmd.ID)
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
	stage, err := ocfl.StageDir(ctx, readFS, ".", alg)
	if err != nil {
		return err
	}
	userName := cmd.Name
	if userName == "" {
		userName = getenv(envVarUserName)
	}
	userEmail := cmd.Email
	if userEmail == "" {
		userEmail = getenv(envVarUserEmail)
	}
	return obj.Commit(ctx, &ocfl.Commit{
		ID:      cmd.ID,
		Stage:   stage,
		Message: cmd.Message,
		User:    ocfl.User{Name: userName, Address: userEmail},
		Spec:    ocfl.Spec(cmd.Spec),
	})
}
