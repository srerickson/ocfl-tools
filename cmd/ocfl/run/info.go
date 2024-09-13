package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/srerickson/ocfl-go"
)

const infoHelp = "Show OCFL-specific information about an object or the active storage root"

type InfoCmd struct {
	ID      string `name:"id" short:"i" optional:"" help:"The id for object to show information about"`
	ObjPath string `name:"object" help:"full path to object root. If set, --root and --id are ignored."`
}

func (cmd *InfoCmd) Run(ctx context.Context, root *ocfl.Root, stdout io.Writer, logger *slog.Logger, getenv func(string) string) error {
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
		return printObjectInfo(obj, stdout)
	case root == nil:
		return errors.New("storage root not set")
	case cmd.ID != "":
		obj, err := root.NewObject(ctx, cmd.ID)
		if err != nil {
			return err
		}
		return printObjectInfo(obj, stdout)
	default:
		printRootInfo(root, stdout, logger)
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
	fmt.Fprintln(stdout, "inventory.json", inv.DigestAlgorithm()+":", inv.Digest())
	return nil
}
