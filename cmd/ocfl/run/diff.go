package run

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/diff"
)

const diffHelp = "Show changed files between versions of an object"

type DiffCmd struct {
	ID string `name:"id" short:"i" optional:"" help:"The id for object to diff"`
	Vs []int  `name:"versions" short:"v" default:"-1,0" help:"Object versions to compare, separated by commas. 0 refers to HEAD, negative numbers match versions before HEAD."`
}

func (cmd *DiffCmd) Run(g *globals) error {
	root, err := g.getRoot()
	if err != nil {
		return err
	}
	obj, err := root.NewObject(g.ctx, cmd.ID)
	if err != nil {
		return fmt.Errorf("reading object id: %q: %w", cmd.ID, err)
	}
	if !obj.Exists() {
		err := fmt.Errorf("object %q not found at root path %s: %w", cmd.ID, obj.Path(), fs.ErrNotExist)
		return err
	}
	var v1, v2 int
	switch len(cmd.Vs) {
	case 0:
		return errors.New("missing version numbers to compare")
	case 1:
		// compare version to head
		v1 = cmd.Vs[0]
		v2 = 0
	default:
		v1 = cmd.Vs[0]
		v2 = cmd.Vs[1]
	}
	head := obj.Head().Num()
	if v1 > head || (head+v1 < 1) {
		return fmt.Errorf("version %d is out of range (HEAD=%d)", v1, head)
	}
	if v2 > head || (head+v2 < 1) {
		return fmt.Errorf("version %d is out of range (HEAD=%d)", v2, head)
	}
	if v1 < 0 {
		v1 = head + v1
	}
	if v2 < 0 {
		v2 = head + v2
	}
	var v1Paths, v2Paths ocfl.PathMap
	if v := obj.Version(v1); v != nil && v.State() != nil {
		v1Paths = v.State().PathMap()
	}
	if v := obj.Version(v2); v != nil && v.State() != nil {
		v2Paths = v.State().PathMap()
	}
	if v1Paths == nil {
		return fmt.Errorf("version not found: %d", v1)
	}
	if v2Paths == nil {
		return fmt.Errorf("version not found: %d", v2)
	}
	result, err := diff.Diff(v1Paths, v2Paths)
	if err != nil {
		return err
	}
	if !result.Empty() {
		fmt.Fprint(g.stdout, result.String())
	}
	return nil
}
