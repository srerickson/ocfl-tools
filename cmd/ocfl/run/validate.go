package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/srerickson/ocfl-go"
)

const validateHelp = "Validate an object (TODO: or an entire storage root)"

type ValidateCmd struct {
	ID         string `name:"id" short:"i" optional:"" help:"The id of object to validate"`
	ObjPath    string `name:"object" help:"full path to object root. If set, --root and --id are ignored."`
	SkipDigest bool   `name:"skip-digest" help:"skip digest (checksum) validation"`
}

func (cmd *ValidateCmd) Run(ctx context.Context, root *ocfl.Root, stdout io.Writer, logger *slog.Logger, getenv func(string) string) error {
	if cmd.ObjPath != "" {
		fsys, dir, err := parseLocation(ctx, cmd.ObjPath, logger, getenv)
		if err != nil {
			return fmt.Errorf("in object path: %w", err)
		}
		obj, err := ocfl.NewObject(ctx, fsys, dir)
		if err != nil {
			return fmt.Errorf("reading object: %w", err)
		}
		return cmd.validateObject(ctx, obj, logger.With("object_path", cmd.ObjPath))
	}
	if root == nil {
		return errors.New("storage root not set")
	}
	if cmd.ID != "" {
		obj, err := root.NewObject(ctx, cmd.ID)
		if err != nil {
			return fmt.Errorf("reading object id: %q: %w", cmd.ID, err)
		}
		return cmd.validateObject(ctx, obj, logger.With("object_id", cmd.ID))
	}
	// validate full storage root
	return errors.New("full storage root validation not implemented")

}

func (cmd *ValidateCmd) validateObject(ctx context.Context, obj *ocfl.Object, logger *slog.Logger) error {
	opts := []ocfl.ObjectValidationOption{
		ocfl.ValidationLogger(logger),
	}
	if cmd.SkipDigest {
		opts = append(opts, ocfl.ValidationSkipDigest())
	}
	if obj.Validate(ctx, opts...).Err() != nil {
		return errors.New("object has validation errors")
	}
	return nil
}
