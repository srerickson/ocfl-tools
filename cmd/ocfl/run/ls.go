package run

import (
	"fmt"

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
	ver := obj.Inventory().Version(cmd.Version)
	if ver == nil {
		err := fmt.Errorf("version %d not found in object %q", cmd.Version, cmd.ID)
		return err
	}
	for path, digest := range ver.State().PathMap().SortedPaths() {
		if cmd.WithDigests {
			fmt.Fprintln(g.stdout, digest, path)
			continue
		}
		fmt.Fprintln(g.stdout, path)
	}
	return nil
}
