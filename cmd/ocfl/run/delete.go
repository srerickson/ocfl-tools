package run

import (
	"bufio"
	"fmt"
	"path"
	"strings"

	"github.com/srerickson/ocfl-go"
	ocflfs "github.com/srerickson/ocfl-go/fs"
)

const deleteHelp = "Delete an object in the storage root"

type DeleteCmd struct {
	ID        string `name:"id" short:"i" help:"The ID for the object to delete" required:""`
	NoConfirm bool   `name:"yes" short:"y" help:"skip delete confirmation."`
	NotObject bool   `name:"not-object" help:"skip check that files for ID are those of an OCFL object."`
}

func (cmd *DeleteCmd) Run(g *globals) error {
	root, err := g.getRoot()
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
		obj, err := root.NewObject(g.ctx, cmd.ID, ocfl.ObjectMustExist())
		if err != nil {
			return fmt.Errorf("cannot delete %q: %w", cmd.ID, err)
		}
		deletePath = obj.Path()
	}
	if !cmd.NoConfirm {
		fmt.Fprintf(g.stdout, "do you really want to delete all files for %q? [y/N]: ", cmd.ID)
		reader := bufio.NewReader(g.stdin)
		line, err := reader.ReadString('\n')
		response := strings.ToLower(strings.Trim(line, " \n"))
		if err != nil || response != "y" {
			fmt.Fprintln(g.stdout, "object not deleted")
			return nil
		}
	}
	if err := ocflfs.RemoveAll(g.ctx, root.FS(), deletePath); err != nil {
		return fmt.Errorf("deleting %q: %w", cmd.ID, err)
	}
	g.logger.Info("deleted object", "object_id", cmd.ID, "object_path", deletePath)
	return nil
}
