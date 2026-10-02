package run

import (
	"context"
	"fmt"

	"github.com/srerickson/ocfl-go"
)

const infoHelp = "Show information about an object or the active storage root"

type InfoCmd struct {
	ID      string `name:"id" short:"i" optional:"" help:"The id for object to show information about"`
	ObjPath string `name:"object" help:"full path to object root. If set, --root and --id are ignored."`
}

func (cmd *InfoCmd) Run(ctx context.Context, env *cmdEnv) error {
	if cmd.ID == "" && cmd.ObjPath == "" {
		root, err := env.getRoot(ctx)
		if err != nil {
			return err
		}
		printRootInfo(root, env.stdout, env.logger)
		return nil
	}
	obj, err := env.newObject(ctx, cmd.ID, cmd.ObjPath, ocfl.ObjectMustExist())
	if err != nil {
		return err
	}
	fmt.Fprintln(env.stdout, "object path:", locationString(obj.FS(), obj.Path()))
	fmt.Fprintln(env.stdout, "id:", obj.ID())
	fmt.Fprintln(env.stdout, "digest algorithm:", obj.DigestAlgorithm())
	fmt.Fprintln(env.stdout, "head:", obj.Head())
	fmt.Fprintln(env.stdout, "OCFL version:", obj.Spec())
	fmt.Fprintln(env.stdout, "inventory.json", obj.DigestAlgorithm().ID()+":", obj.InventoryDigest())
	return nil
}
