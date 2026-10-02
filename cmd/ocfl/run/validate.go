package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/srerickson/ocfl-go"
)

const validateHelp = "Validate an object or all objects in the storage root"

type ValidateCmd struct {
	ID         string `name:"id" short:"i" optional:"" help:"The id of object to validate"`
	ObjPath    string `name:"object" help:"full path to object root. If set, --root and --id are ignored."`
	SkipDigest bool   `name:"skip-digest" help:"skip digest (checksum) validation"`
}

func (cmd *ValidateCmd) Run(ctx context.Context, env *cmdEnv) error {
	switch {
	case cmd.ObjPath != "":
		fsys, dir, err := env.parseLocation(ctx, cmd.ObjPath)
		if err != nil {
			return fmt.Errorf("in object path: %w", err)
		}
		logger := env.logger.With("object_path", locationString(fsys, dir))
		result := ocfl.ValidateObject(ctx, fsys, dir, cmd.validationOptions(logger)...)
		if result.Err() != nil {
			return errors.New("object has errors")
		}
	case cmd.ID != "":
		root, err := env.getRoot(ctx)
		if err != nil {
			return err
		}
		logger := env.logger.With("object_id", cmd.ID)
		result := root.ValidateObject(ctx, cmd.ID, cmd.validationOptions(logger)...)
		if result.Err() != nil {
			return errors.New("object has errors")
		}
	default:
		// FIXME
		env.logger.Warn("root validation is not fully implemented: validating all objects in the root, but not conformance of root structure itself. [https://github.com/srerickson/ocfl-go/issues/98]")
		root, err := env.getRoot(ctx)
		if err != nil {
			return err
		}
		var badObjs []string
		for decl, err := range root.ObjectDeclarations(ctx) {
			if err != nil {
				return fmt.Errorf("finding objects in the storage root: %w", err)
			}
			objFS := decl.FS
			objDir := decl.FullPathDir()
			logger := env.logger.With("object_path", objDir)
			result := ocfl.ValidateObject(ctx, objFS, objDir, cmd.validationOptions(logger)...)
			if result.Err() != nil {
				badObjs = append(badObjs, objDir)
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
