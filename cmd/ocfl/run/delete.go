package run

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/srerickson/ocfl-go"
	ocflfs "github.com/srerickson/ocfl-go/fs"
)

const deleteHelp = "Delete an object in the storage root"

type DeleteCmd struct {
	ID        string `name:"id" short:"i" help:"The ID for the object to delete" required:""`
	NoConfirm bool   `name:"yes" short:"y" help:"skip delete confirmation."`
}

func (cmd *DeleteCmd) Run(g *globals) error {
	root, err := g.getRoot()
	if err != nil {
		return err
	}
	obj, err := root.NewObject(g.ctx, cmd.ID, ocfl.ObjectMustExist())
	if err != nil {
		return fmt.Errorf("reading object id: %q: %w", cmd.ID, err)
	}
	if !cmd.NoConfirm {
		fmt.Fprintf(g.stdout, "do you really want to delete %q [y/N]: ", obj.ID())
		reader := bufio.NewReader(g.stdin)
		line, err := reader.ReadString('\n')
		response := strings.ToLower(strings.Trim(line, " \n"))
		if err != nil || response != "y" {
			fmt.Fprintln(g.stdout, "object not deleted")
			return nil
		}
	}
	if err := ocflfs.RemoveAll(g.ctx, obj.FS(), obj.Path()); err != nil {
		return fmt.Errorf("deleting %q: %w", obj.ID(), err)
	}
	g.logger.Info("deleted object", "object_id", obj.ID(), "object_path", obj.Path())
	return nil
}
