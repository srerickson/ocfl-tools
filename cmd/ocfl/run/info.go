package run

import (
	"errors"
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
	inv := obj.Inventory()
	if inv == nil {
		return errors.New("object has no inventory")
	}
	fmt.Fprintln(g.stdout, "object path:", locationString(obj.FS(), obj.Path()))
	fmt.Fprintln(g.stdout, "id:", inv.ID())
	fmt.Fprintln(g.stdout, "digest algorithm:", inv.DigestAlgorithm())
	fmt.Fprintln(g.stdout, "head:", inv.Head())
	fmt.Fprintln(g.stdout, "OCFL version:", inv.Spec())
	fmt.Fprintln(g.stdout, "inventory.json", inv.DigestAlgorithm().ID()+":", inv.Digest())
	return nil
}
