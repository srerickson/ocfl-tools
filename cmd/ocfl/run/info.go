package run

import (
	"errors"
	"fmt"
	"io"

	"github.com/srerickson/ocfl-go"
)

const infoHelp = "Show information about an object or the active storage root"

type InfoCmd struct {
	ID      string `name:"id" short:"i" optional:"" help:"The id for object to show information about"`
	ObjPath string `name:"object" help:"full path to object root. If set, --root and --id are ignored."`
}

func (cmd *InfoCmd) Run(g *globals) error {
	switch {
	case cmd.ObjPath != "":
		fsys, dir, err := g.parseLocation(cmd.ObjPath)
		if err != nil {
			return err
		}
		obj, err := ocfl.NewObject(g.ctx, fsys, dir)
		if err != nil {
			return err
		}
		return printObjectInfo(obj, g.stdout)
	case cmd.ID != "":
		root, err := g.getRoot()
		if err != nil {
			return err
		}
		obj, err := root.NewObject(g.ctx, cmd.ID)
		if err != nil {
			return err
		}
		return printObjectInfo(obj, g.stdout)
	default:
		root, err := g.getRoot()
		if err != nil {
			return err
		}
		printRootInfo(root, g.stdout, g.logger)
	}
	return nil
}

func printObjectInfo(obj *ocfl.Object, stdout io.Writer) error {
	inv := obj.Inventory()
	if inv == nil {
		return errors.New("object has no inventory")
	}
	fmt.Fprintln(stdout, "object path:", locationString(obj.FS(), obj.Path()))
	fmt.Fprintln(stdout, "id:", inv.ID())
	fmt.Fprintln(stdout, "digest algorithm:", inv.DigestAlgorithm())
	fmt.Fprintln(stdout, "head:", inv.Head())
	fmt.Fprintln(stdout, "OCFL version:", inv.Spec())
	fmt.Fprintln(stdout, "inventory.json", inv.DigestAlgorithm().ID()+":", inv.Digest())
	return nil
}
