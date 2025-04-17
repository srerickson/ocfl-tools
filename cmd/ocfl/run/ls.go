package run

import (
	"fmt"
	"io/fs"

	"github.com/srerickson/ocfl-go"
)

const lsHelp = "List objects in a storage root or files in an object"

type LsCmd struct {
	ID          string `name:"id" short:"i" optional:"" help:"The id of object to list contents from."`
	ObjPath     string `name:"object" help:"full path to object root. If set, --root and --id are ignored."`
	Version     int    `name:"version" short:"v" default:"0" help:"The object version number (unpadded) to list contents from. The default (0) lists the latest version."`
	WithDigests bool   `name:"digests" short:"d" help:"Show digests when listing contents of an object version."`
}

func (cmd *LsCmd) Run(g *globals) error {
	if cmd.ID == "" && cmd.ObjPath == "" {
		// list object ids in root
		root, err := g.getRoot()
		if err != nil {
			return err
		}
		for obj, err := range root.Objects(g.ctx) {
			if err != nil {
				return fmt.Errorf("while listing objects in root: %w", err)
			}
			fmt.Fprintln(g.stdout, obj.Inventory().ID())
		}
		return nil
	}
	obj, err := g.newObject(cmd.ID, cmd.ObjPath, ocfl.ObjectMustExist())
	if err != nil {
		return err
	}
	verF, err := obj.OpenVersion(g.ctx, cmd.Version)
	if err != nil {
		return err
	}
	fs.WalkDir(verF, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(g.stdout, "%s %d\n", path, info.Size())
		return nil
	})
	return nil
}
