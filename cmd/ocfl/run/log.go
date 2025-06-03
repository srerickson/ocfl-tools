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
	for _, vnum := range obj.Head().Lineage() {
		version := obj.Version(vnum.Num())
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
