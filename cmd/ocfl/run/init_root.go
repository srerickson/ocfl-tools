package run

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/extension"
)

const initRootHelp = `Create a new OCFL storage root`

type initRootCmd struct {
	Layout      string `name:"layout" short:"l" optional:"" default:"0004-hashed-n-tuple-storage-layout"  help:"The storage root layout extension (see https://ocfl.github.io/extensions/)."`
	Description string `name:"description" short:"d" optional:"" help:"Description to include in the storage root metadata"`
	Spec        string `name:"ocflv" default:"1.1" help:"OCFL version for the storage root"`
}

func (cmd *initRootCmd) Run(ctx context.Context, fsysConfig string, stdout io.Writer, logger *slog.Logger, getenv func(string) string) error {
	fsys, dir, err := parseLocation(ctx, fsysConfig, logger, getenv)
	if err != nil {
		return err
	}
	spec := ocfl.Spec(cmd.Spec)
	reg := extension.DefaultRegister()
	layout, err := reg.NewLayout(cmd.Layout)
	if err != nil {
		return fmt.Errorf("could not initialize storage root: %w", err)
	}
	root, err := ocfl.NewRoot(ctx, fsys, dir, ocfl.InitRoot(spec, cmd.Description, layout))
	if err != nil {
		return fmt.Errorf("while initializing storage root: %w", err)
	}
	rootCfg := locationString(fsys, dir)
	fmt.Fprintln(stdout, "storage root:", rootCfg)
	if l := root.LayoutName(); l != "" {
		fmt.Fprintln(stdout, "layout:", root.LayoutName())
	}
	if d := root.Description(); d != "" {
		fmt.Fprintln(stdout, "description:", root.Description())
	}
	fmt.Fprintln(stdout, "OCFL version:", root.Spec())
	return nil
}
