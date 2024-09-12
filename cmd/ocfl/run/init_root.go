package run

import (
	"context"
	"errors"
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
	ExistingOK  bool   `name:"existing-ok" help:"don't return an error if a storage root already exists at the location"`
}

func (cmd *initRootCmd) Run(ctx context.Context, fsysConfig string, stdout io.Writer, logger *slog.Logger, getenv func(string) string) error {
	fsys, dir, err := parseLocation(ctx, fsysConfig, logger, getenv)
	if err != nil {
		return err
	}
	if fsys == nil {
		return errors.New("location for new storage root is required")
	}
	if _, err := ocfl.NewRoot(ctx, fsys, dir); err == nil {
		msg := "storage root already exists"
		if cmd.ExistingOK {
			logger.Warn(msg)
			return nil
		}
		return errors.New(msg)
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
	printRootInfo(root, stdout, logger)
	return nil
}

func printRootInfo(root *ocfl.Root, stdout io.Writer, logger *slog.Logger) {
	rootCfg := locationString(root.FS(), root.Path())
	layout := root.Layout()
	fmt.Fprintln(stdout, "storage root:", rootCfg)
	if layout != nil {
		fmt.Fprintln(stdout, "layout:", root.LayoutName())
	}
	if d := root.Description(); d != "" {
		fmt.Fprintln(stdout, "description:", root.Description())
	}
	fmt.Fprintln(stdout, "OCFL version:", root.Spec())
	if layout == nil {
		logger.Warn("storage root has no layout")
		return
	}
	if err := layout.Valid(); err != nil {
		logger.Warn("root's layout has configuration errors", "err", err.Error())
		logger.Warn("layout errors must be resolved before objects are created or updated")
	}
}
