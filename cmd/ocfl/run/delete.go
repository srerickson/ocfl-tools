package run

import (
	"bufio"
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/srerickson/ocfl-go"
	ocflfs "github.com/srerickson/ocfl-go/fs"
)

type DeleteCmd struct {
	ID        string `name:"id" short:"i" help:"The ID for the object to delete" required:""`
	NoConfirm bool   `name:"yes" short:"y" help:"skip delete confirmation."`
	NotObject bool   `name:"not-object" help:"skip check that files for ID are those of an OCFL object."`
}

func (cmd *DeleteCmd) Run(ctx context.Context, env *cmdEnv) error {
	root, err := env.getRoot(ctx)
	if err != nil {
		return err
	}
	var deletePath string // path in root.FS() we will delete
	switch {
	case cmd.NotObject:
		objPath, err := root.ResolveID(cmd.ID)
		if err != nil {
			return fmt.Errorf("cannot delete %q: %w", cmd.ID, err)
		}
		deletePath = path.Join(root.Path(), objPath)
	default:
		obj, err := root.NewObject(ctx, cmd.ID, ocfl.ObjectMustExist())
		if err != nil {
			return fmt.Errorf("cannot delete %q: %w", cmd.ID, err)
		}
		deletePath = obj.Path()
	}
	if !cmd.NoConfirm {
		fmt.Fprintf(env.stdout, "do you really want to delete all files for %q? [y/N]: ", cmd.ID)
		reader := bufio.NewReader(env.stdin)
		line, err := reader.ReadString('\n')
		response := strings.ToLower(strings.Trim(line, " \n"))
		if err != nil || response != "y" {
			fmt.Fprintln(env.stdout, "object not deleted")
			return nil
		}
	}
	if err := ocflfs.RemoveAll(ctx, root.FS(), deletePath); err != nil {
		return fmt.Errorf("deleting %q: %w", cmd.ID, err)
	}
	env.logger.Info("deleted object", "object_id", cmd.ID, "object_path", deletePath)
	return nil
}
