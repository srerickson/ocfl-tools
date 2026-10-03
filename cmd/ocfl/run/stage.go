package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
	ocflfs "github.com/srerickson/ocfl-go/fs"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/diff"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/stage"
)

type StageCmd struct {
	File string `name:"file" short:"f" default:"ocfl-stage.json" help:"path to stage file"`

	Add    StageAddCmd    `cmd:"" help:"Add a file or directory to the stage"`
	Commit StageCommitCmd `cmd:"" help:"Commit the stage as a new object version"`
	Diff   StageDiffCmd   `cmd:"" help:"Show changes between an upstream object or directory and the stage"`
	Ls     StageListCmd   `cmd:"" help:"List files in the stage state"`
	New    NewStageCmd    `cmd:"" help:"Create a new stage for preparing updates to an object"`
	Rm     StageRmCmd     `cmd:"" help:"Remove a file or directory from the stage"`
	Status StageStatusCmd `cmd:"" help:"Show stage details and report any errors"`
}

func (cmd *StageCmd) Help() string {
	return `A stage file records changes to an object that can be committed as a new
version. Create one with 'stage new', change it with 'stage add' and 'stage
rm', and commit it with 'stage commit', which removes the stage file. Staged
content is digested when it's added, but it isn't copied until the stage is
committed.

    ocfl stage new --id my-object
    ocfl stage add my-files/
    ocfl stage rm old-file.txt
    ocfl stage commit -m "update data" -n "My Name" -e me@example.net`
}

// readFile reads the stage file named by the --file flag.
func (cmd *StageCmd) readFile(env *cmdEnv) (*stage.StageFile, error) {
	stageFile, err := stage.ReadStageFile(cmd.File)
	if err != nil {
		return nil, err
	}
	stageFile.SetLogger(env.logger)
	return stageFile, nil
}

// stage new
type NewStageCmd struct {
	Alg string `name:"alg" default:"sha512" help:"Digest Algorithm used to digest content. Ignored for existing objects."`
	ID  string `name:"id" short:"i" required:"" help:"object id for the new stage"`
}

func (cmd *NewStageCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd) error {
	if _, err := stage.ReadStageFile(parent.File); err == nil {
		err := fmt.Errorf("stage file already exists: %s", parent.File)
		return err
	}
	root, err := env.getRoot(ctx)
	if err != nil {
		return err
	}
	obj, err := root.NewObject(ctx, cmd.ID)
	if err != nil {
		return err
	}
	stage, err := stage.NewStageFile(obj, cmd.Alg)
	if err != nil {
		return err
	}
	if err := stage.Write(parent.File); err != nil {
		return err
	}
	env.logger.Info("stage file created", "path", parent.File, "object_id", stage.ID, "object_version", stage.NextHead)
	return nil
}

// stage add
type StageAddCmd struct {
	NoHidden bool   `name:"no-hidden" help:"exclude hidden files and directories (.*). Ignored if path is a file."`
	As       string `name:"as" help:"logical name for the new content. Default: base name if path is a file; '.' if path is a directory."`
	Jobs     int    `name:"jobs" short:"j" default:"0" help:"number of files to digest concurrently. Defaults to the number of CPU cores."`
	Remove   bool   `name:"remove" help:"also remove staged files not found in the path. Ignored if path is a file."`
	Path     string `arg:"" help:"file or parent directory for content to add to the stage"`
}

func (cmd *StageAddCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd) error {
	changes, err := parent.readFile(env)
	if err != nil {
		return err
	}
	absPath, err := filepath.Abs(cmd.Path)
	if err != nil {
		return err
	}
	// get file type
	info, err := os.Stat(absPath)
	if err != nil {
		return err
	}
	ftype := info.Mode().Type()
	switch {
	case ftype.IsDir():
		opts := []stage.AddOption{
			stage.AddAs(cmd.As),
			stage.AddDigestJobs(cmd.Jobs),
		}
		if cmd.Remove {
			opts = append(opts, stage.AddAndRemove())
		}
		if cmd.NoHidden {
			opts = append(opts, stage.AddWithoutHidden())
		}
		err = changes.AddDir(ctx, absPath, opts...)
	case ftype.IsRegular():
		err = changes.AddFile(absPath, stage.AddAs(cmd.As))
	default:
		err = errors.New("unsupported file type for: " + absPath)
	}
	if err != nil {
		return err
	}
	if err := changes.Write(parent.File); err != nil {
		return err
	}
	return nil
}

// stage commit
type StageCommitCmd struct {
	Message string `name:"message" short:"m" help:"Message to include in the object version metadata"`
	Name    string `name:"name" short:"n" ocflenv:"OCFL_USER_NAME" help:"Username to include in the object version metadata"`
	Email   string `name:"email" short:"e" ocflenv:"OCFL_USER_EMAIL" help:"User email to include in the object version metadata"`
}

func (cmd *StageCommitCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd) error {
	root, err := env.getRoot(ctx)
	if err != nil {
		return err
	}
	stageFile, err := parent.readFile(env)
	if err != nil {
		return err
	}
	obj, err := root.NewObject(ctx, stageFile.ID)
	if err != nil {
		return err
	}
	stage, err := stageFile.Stage()
	if err != nil {
		return fmt.Errorf("stage has errors: %w", err)
	}
	updated, err := objectUpdateOrRevert(
		ctx,
		obj,
		stage,
		cmd.Message,
		newUser(cmd.Name, cmd.Email),
		env.logger)
	if err != nil {
		return err
	}
	if updated {
		env.logger.Info("removing stage file", "path", parent.File)
		if err := os.Remove(parent.File); err != nil {
			return fmt.Errorf("removing stage file: %w", err)
		}
	}
	return nil
}

// stage diff
type StageDiffCmd struct {
	Dir string `name:"dir" help:"use a local directory rather than upstream object as basis for comparison to the stage."`
}

func (cmd *StageDiffCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd) error {
	baseState := ocfl.PathMap{}
	stageFile, err := parent.readFile(env)
	if err != nil {
		return err
	}
	stageState := stageFile.NextState
	switch {
	case cmd.Dir != "":
		algs, err := stageFile.Algs()
		if err != nil {
			return err
		}
		alg := algs[0]
		filesIter, walkErr := ocflfs.UntilErr(ocflfs.WalkFiles(ctx, ocflfs.DirFS(cmd.Dir), "."))
		for result, err := range digest.DigestFiles(ctx, filesIter, alg) {
			if err != nil {
				return err
			}
			baseState[result.FullPath()] = result.Digests[alg.ID()]
		}
		if err := walkErr(); err != nil {
			return err
		}
	default:
		root, err := env.getRoot(ctx)
		if err != nil {
			return err
		}
		obj, err := root.NewObject(ctx, stageFile.ID)
		if err != nil {
			return err
		}
		if obj.Exists() {
			baseState = obj.Version(0).State().PathMap()
		}
	}

	diffs, err := diff.Diff(baseState, stageState)
	if err != nil {
		return err
	}
	if diffs.Empty() {
		return nil
	}
	fmt.Fprint(env.stdout, diffs.String())
	return nil
}

// 'stage list' command
type StageListCmd struct {
	WithDigests bool `name:"digests" short:"d" help:"include file digests in output"`
}

func (cmd *StageListCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd) error {
	stage, err := parent.readFile(env)
	if err != nil {
		return err
	}
	stage.List(env.stdout, cmd.WithDigests)
	return nil
}

// 'stage rm' command
type StageRmCmd struct {
	Recursive bool   `name:"recursive" short:"r" help:"remove all files in the directory"`
	Path      string `arg:"" name:"path" help:"file or directory to remove"`
}

func (cmd *StageRmCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd) error {
	stage, err := parent.readFile(env)
	if err != nil {
		return err
	}
	if err := stage.Remove(cmd.Path, cmd.Recursive); err != nil {
		return err
	}
	if err := stage.Write(parent.File); err != nil {
		return err
	}
	return nil
}

// 'stage status' command
type StageStatusCmd struct{}

func (cmd *StageStatusCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd) error {
	stageFile, err := parent.readFile(env)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.stdout, "object:      %s (%s)\n", stageFile.ID, stageFile.NextHead)
	fmt.Fprintf(env.stdout, "digest alg:  %s\n", stageFile.AlgID)
	fmt.Fprintf(env.stdout, "fixity algs: %s\n", stageFile.FixityIDs)
	fmt.Fprintf(env.stdout, "state size:  %d files\n", len(stageFile.NextState))
	root, err := env.getRoot(ctx)
	if err != nil {
		return err
	}
	obj, err := root.NewObject(ctx, stageFile.ID)
	if err != nil {
		return err
	}
	baseState := ocfl.PathMap{}
	if obj.Exists() {
		baseState = obj.Version(0).State().PathMap()
	}
	stateDiff, err := diff.Diff(baseState, stageFile.NextState)
	if err != nil {
		return err
	}
	switch {
	case !stateDiff.Empty():
		fmt.Fprintln(env.stdout, "stage has changes to commit")
	default:
		fmt.Fprintln(env.stdout, "stage is unchanged and/or empty")
	}
	hasErrors := false
	// check stage content
	for err := range stageFile.ContentErrors() {
		hasErrors = true
		env.logger.Error(err.Error())
	}
	for err := range stageFile.StateErrors() {
		hasErrors = true
		env.logger.Error(err.Error())
	}
	if hasErrors {
		return errors.New("stage has errors")
	}
	return nil
}

func newUser(name string, email string) ocfl.User {
	if email != "" && !strings.HasPrefix(email, "email:") {
		email = "email:" + email
	}
	return ocfl.User{Name: name, Address: email}
}

// objectUpdateOrRevert does an object update, reverting partial updates if
// ctx is canceled. The returned bool indicates if the update completed
// without being interrupted.
func objectUpdateOrRevert(
	ctx context.Context,
	obj *ocfl.Object,
	stage *ocfl.Stage,
	msg string,
	user ocfl.User,
	logger *slog.Logger,
	opts ...ocfl.ObjectUpdateOption,
) (bool, error) {
	logger.Info("starting object update", "object_id", obj.ID())
	opts = append(opts, ocfl.UpdateWithLogger(logger))
	update, err := obj.Update(ctx, stage, msg, user, opts...)
	if err != nil {
		if errors.Is(err, context.Canceled) && update != nil {
			logger.Info("object update interrupted: reverting to last valid state")
			// ctx is already canceled: revert regardless.
			err = update.Revert(context.WithoutCancel(ctx), obj.FS(), obj.Path(), stage.ContentSource)
			if err != nil {
				return false, fmt.Errorf("while reverting object update: %w", err)
			}
			logger.Info("object update was interrupted and successfully reverted")
			return false, nil
		}
		return false, fmt.Errorf("during object update: %w", err)
	}
	logger.Info("object update complete", "object_id", obj.ID())
	return true, nil
}
