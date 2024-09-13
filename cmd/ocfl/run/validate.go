package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/srerickson/ocfl-go"
)

const validateHelp = "Validate an object or all objects in the storage root"

type ValidateCmd struct {
	ID         string `name:"id" short:"i" optional:"" help:"The id of object to validate"`
	ObjPath    string `name:"object" help:"full path to object root. If set, --root and --id are ignored."`
	SkipDigest bool   `name:"skip-digest" help:"skip digest (checksum) validation"`
}

func (cmd *ValidateCmd) Run(ctx context.Context, root *ocfl.Root, stdout io.Writer, logger *slog.Logger, getenv func(string) string) error {
	switch {
	case cmd.ObjPath != "":
		fsys, dir, err := parseLocation(ctx, cmd.ObjPath, logger, getenv)
		if err != nil {
			return fmt.Errorf("in object path: %w", err)
		}
		logger := logger.With("object_path", locationString(fsys, dir))
		result := ocfl.ValidateObject(ctx, fsys, dir, cmd.validationOptions(logger)...)
		if result.Err() != nil {
			return errors.New("object has errors")
		}
	case root == nil:
		return errors.New("storage root not set")
	case cmd.ID != "":
		logger := logger.With("object_id", cmd.ID)
		result := root.ValidateObject(ctx, cmd.ID, cmd.validationOptions(logger)...)
		if result.Err() != nil {
			return errors.New("object has errors")
		}
	default:
		// FIXME
		logger.Warn("root validation is not fully implemented: validating all objects in the root, but not conformance of root structure itself. [https://github.com/srerickson/ocfl-go/issues/98]")
		var badObjs []string
		for obj, err := range root.Objects(ctx) {
			if err != nil {
				return fmt.Errorf("finding objects in the storage root: %w", err)
			}
			logger = logger.With("object_path", obj.Path())
			result := ocfl.ValidateObject(ctx, obj.FS(), obj.Path(), cmd.validationOptions(logger)...)
			if result.Err() != nil {
				badObjs = append(badObjs, obj.Path())
			}
		}
		if l := len(badObjs); l > 0 {
			return fmt.Errorf("found %d object(s) with errors", l)
		}
	}
	return nil
}

func (cmd *ValidateCmd) validationOptions(logger *slog.Logger) []ocfl.ObjectValidationOption {
	opts := []ocfl.ObjectValidationOption{
		ocfl.ValidationLogger(logger),
	}
	if cmd.SkipDigest {
		opts = append(opts, ocfl.ValidationSkipDigest())
	}
	return opts
}
