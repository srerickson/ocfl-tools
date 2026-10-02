package run

import (
	"context"
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

func (cmd *LsCmd) Run(ctx context.Context, env *cmdEnv) error {
	if cmd.ID == "" && cmd.ObjPath == "" {
		// list object ids in root
		root, err := env.getRoot(ctx)
		if err != nil {
			return err
		}
		for obj, err := range root.Objects(ctx) {
			if err != nil {
				return fmt.Errorf("while listing objects in root: %w", err)
			}
			fmt.Fprintln(env.stdout, obj.ID())
		}
		return nil
	}
	obj, err := env.newObject(ctx, cmd.ID, cmd.ObjPath, ocfl.ObjectMustExist())
	if err != nil {
		return err
	}
	ver := obj.Version(cmd.Version)
	if ver == nil {
		err := fmt.Errorf("version %d not found in object %q", cmd.Version, cmd.ID)
		return err
	}
	for path, digest := range ver.State().PathMap().SortedPaths() {
		if cmd.WithDigests {
			fmt.Fprintln(env.stdout, digest, path)
			continue
		}
		fmt.Fprintln(env.stdout, path)
	}
	return nil
}
