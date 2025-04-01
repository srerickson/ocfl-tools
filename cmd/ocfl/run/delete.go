package run

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/srerickson/ocfl-go"
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
	writeFS, ok := obj.FS().(ocfl.WriteFS)
	if !ok {
		return fmt.Errorf("storage backend doesn't support deletion")
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
	g.logger.Info("deleting object", "object_id", obj.ID(), "object_path", obj.Path())
	if err := writeFS.RemoveAll(g.ctx, obj.Path()); err != nil {
		return fmt.Errorf("deleting %q: %w", obj.ID(), err)
	}
	return nil
}
