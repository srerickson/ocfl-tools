package run

import (
	"context"
	"fmt"

	"github.com/srerickson/ocfl-go"
)

type InfoCmd struct {
	objectFlags
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
	obj, err := cmd.open(ctx, env, ocfl.ObjectMustExist())
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
