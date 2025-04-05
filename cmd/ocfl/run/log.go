package run

import (
	"errors"
	"fmt"

	"github.com/srerickson/ocfl-go"
)

const logHelp = "Show an object's revision log"

type LogCmd struct {
	ID      string `name:"id" short:"i" help:"The id for object to show revision logs from"`
	ObjPath string `name:"object" help:"full path to object root. If set, --root and --id are ignored."`
}

func (cmd *LogCmd) Run(g *globals) error {
	obj, err := g.newObject(cmd.ID, cmd.ObjPath, ocfl.ObjectMustExist())
	if err != nil {
		return err
	}
	inv := obj.Inventory()
	if inv == nil {
		return errors.New("object has no inventory")
	}
	for _, vnum := range inv.Head().Lineage() {
		version := inv.Version(vnum.Num())
		if version == nil {
			return errors.New("inventory is missing entry for " + vnum.String())
		}
		fmt.Fprintf(g.stdout, "%s (%s): %q", vnum.String(), version.Created(), version.Message())
		if version.User() != nil {
			fmt.Fprintf(g.stdout, " %s <%s>", version.User().Name, version.User().Address)
		}
		fmt.Fprintln(g.stdout, "")
	}
	return nil

}
