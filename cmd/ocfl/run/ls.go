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
	WithSize    bool   `name:"size" short:"s"`
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
			fmt.Fprintln(g.stdout, obj.ID())
		}
		return nil
	}
	obj, err := g.newObject(cmd.ID, cmd.ObjPath, ocfl.ObjectMustExist())
	if err != nil {
		return err
	}
	vfs, err := obj.VersionFS(g.ctx, cmd.Version)
	if err != nil {
		err := fmt.Errorf("version %d not found in object %q", cmd.Version, cmd.ID)
		return err
	}
	versionDigests := obj.Version(cmd.Version).State().PathMap()
	err = fs.WalkDir(vfs, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		toPrint := []any{path}
		if cmd.WithSize {
			info, err := d.Info()
			if err != nil {
				return err
			}
			toPrint = append(toPrint, info.Size())
		}
		if cmd.WithDigests {
			toPrint = append(toPrint, versionDigests[path])
		}
		fmt.Fprintln(g.stdout, toPrint...)
		return nil
	})
	if err != nil {
		return fmt.Errorf("while traversing version files: %w", err)
	}
	return nil
}
