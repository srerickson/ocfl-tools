package run

import (
	"fmt"

	"github.com/srerickson/ocfl-go"
)

const infoHelp = "Show information about an object or the active storage root"

type InfoCmd struct {
	ID      string `name:"id" short:"i" optional:"" help:"The id for object to show information about"`
	ObjPath string `name:"object" help:"full path to object root. If set, --root and --id are ignored."`
}

func (cmd *InfoCmd) Run(g *globals) error {
	if cmd.ID == "" && cmd.ObjPath == "" {
		root, err := g.getRoot()
		if err != nil {
			return err
		}
		printRootInfo(root, g.stdout, g.logger)
		return nil
	}
	obj, err := g.newObject(cmd.ID, cmd.ObjPath, ocfl.ObjectMustExist())
	if err != nil {
		return err
	}
	fmt.Fprintln(g.stdout, "object path:", locationString(obj.FS(), obj.Path()))
	fmt.Fprintln(g.stdout, "id:", obj.ID())
	fmt.Fprintln(g.stdout, "digest algorithm:", obj.DigestAlgorithm())
	fmt.Fprintln(g.stdout, "head:", obj.Head())
	fmt.Fprintln(g.stdout, "OCFL version:", obj.Spec())
	fmt.Fprintln(g.stdout, "inventory.json", obj.DigestAlgorithm().ID()+":", obj.InventoryDigest())
	return nil
}
