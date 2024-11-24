package run

import (
	"errors"
	"fmt"
	"io"

	"github.com/srerickson/ocfl-go"
)

const logHelp = "Show an object's revision log"

type LogCmd struct {
	ID      string `name:"id" short:"i" optional:"" help:"The id for object to show revision logs from"`
	ObjPath string `name:"object" help:"full path to object root. If set, --root and --id are ignored."`
}

func (cmd *LogCmd) Run(g *globals) error {
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
		return printVersionLog(obj, g.stdout)
	case cmd.ID != "":
		root, err := g.getRoot()
		if err != nil {
			return err
		}
		obj, err := root.NewObject(g.ctx, cmd.ID)
		if err != nil {
			return err
		}
		return printVersionLog(obj, g.stdout)
	}
	return errors.New("missing required flag: --id or --object")
}

func printVersionLog(obj *ocfl.Object, stdout io.Writer) error {
	inv := obj.Inventory()
	if inv == nil {
		return errors.New("object has no inventory")
	}
	for _, vnum := range inv.Head().Lineage() {
		version := inv.Version(vnum.Num())
		if version == nil {
			return errors.New("inventory is missing entry for " + vnum.String())
		}
		fmt.Fprintf(stdout, "%s (%s): %q", vnum.String(), version.Created(), version.Message())
		if version.User() != nil {
			fmt.Fprintf(stdout, " %s <%s>", version.User().Name, version.User().Address)
		}
		fmt.Fprintln(stdout, "")
	}
	return nil
}
