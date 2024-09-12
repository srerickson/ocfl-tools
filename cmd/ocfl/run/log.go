package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/srerickson/ocfl-go"
)

const logHelp = "print revision log for an object"

type LogCmd struct {
	ID      string `name:"id" short:"i" optional:"" help:"The id for object to show revision logs from"`
	ObjPath string `name:"object" help:"full path to object root. If set, --root and --id are ignored."`
}

func (cmd *LogCmd) Run(ctx context.Context, root *ocfl.Root, stdout io.Writer, logger *slog.Logger, getenv func(string) string) error {
	switch {
	case cmd.ObjPath != "":
		fsys, dir, err := parseLocation(ctx, cmd.ObjPath, logger, getenv)
		if err != nil {
			return err
		}
		obj, err := ocfl.NewObject(ctx, fsys, dir)
		if err != nil {
			return err
		}
		return printVersionLog(obj, stdout)
	case root == nil:
		return errors.New("storage root not set")
	default:
		obj, err := root.NewObject(ctx, cmd.ID)
		if err != nil {
			return err
		}
		return printVersionLog(obj, stdout)
	}
}

func printVersionLog(obj *ocfl.Object, stdout io.Writer) error {
	inv := obj.Inventory()
	if inv == nil {
		return errors.New("object has no inventory!")
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
