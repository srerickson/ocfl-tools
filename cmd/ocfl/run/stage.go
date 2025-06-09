package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
	ocflfs "github.com/srerickson/ocfl-go/fs"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/diff"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/stage"
)

const (
	stageHelp = "commands for working with stages (i.e., object updates)"
)

type StageCmd struct {
	Add    StageAddCmd    `cmd:"" help:"Add a file or directory to the stage"`
	Commit StageCommitCmd `cmd:"" help:"Commit the stage as a new object version"`
	Diff   StageDiffCmd   `cmd:"" help:"Show changes between an upstream object or directory and the stage"`
	Ls     StageListCmd   `cmd:"" help:"List files in the stage state"`
	New    NewStageCmd    `cmd:"" help:"Create a new stage for preparing updates to an object"`
	Rm     StageRmCmd     `cmd:"" help:"Remove a file or directory from the stage"`
	Status StageStatusCmd `cmd:"" help:"Show stage details and report any errors"`
}

// shared fields used by all stage sub-commands
type stageCmdBase struct {
	File string `name:"file" short:"f" default:"ocfl-stage.json" help:"path to stage file"`
}

// stage new
type NewStageCmd struct {
	stageCmdBase
	Alg string `name:"alg" default:"sha512" help:"Digest Algorithm used to digest content. Ignored for existing objects."`
	ID  string `name:"id" short:"i" required:"" help:"object id for the new stage"`
}

func (cmd *NewStageCmd) Run(g *globals) error {
	if _, err := stage.ReadStageFile(cmd.File); err == nil {
		err := fmt.Errorf("stage file already exists: %s", cmd.File)
		return err
	}
	root, err := g.getRoot()
	if err != nil {
		return err
	}
	obj, err := root.NewObject(g.ctx, cmd.ID)
	if err != nil {
		return err
	}
	stage, err := stage.NewStageFile(obj, cmd.Alg)
	if err != nil {
		return err
	}
	if err := stage.Write(cmd.File); err != nil {
		return err
	}
	g.logger.Info("stage file created", "path", cmd.File, "object_id", stage.ID, "object_version", stage.NextHead)
	return nil
}

// stage add
type StageAddCmd struct {
	stageCmdBase
	All    bool   `name:"all" help:"include hidden files (.*) in path. Ignored if path is a file."`
	As     string `name:"as" help:"logical name for the new content. Default: base name if path is a file; '.' if path is a directory."`
	Jobs   int    `name:"jobs" short:"j" default:"0" help:"number of files to digest concurrently. Defaults to the number of CPU cores."`
	Remove bool   `name:"remove" help:"also remove staged files not found in the path. Ignored if path is a file."`
	Path   string `arg:"" help:"file or parent directory for content to add to the stage"`
}

func (cmd *StageAddCmd) Run(g *globals) error {
	ctx := g.ctx
	changes, err := stage.ReadStageFile(cmd.File)
	if err != nil {
		return err
	}
	changes.SetLogger(g.logger)
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
		if cmd.All {
			opts = append(opts, stage.AddWithHidden())
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
	if err := changes.Write(cmd.File); err != nil {
		return err
	}
	return nil
}

// stage commit
type StageCommitCmd struct {
	stageCmdBase
	Message string `name:"message" short:"m" help:"Message to include in the object version metadata"`
	Name    string `name:"name" short:"n" help:"Username to include in the object version metadata ($$${env_user_name})"`
	Email   string `name:"email" short:"e" help:"User email to include in the object version metadata ($$${env_user_email})"`
}

func (cmd *StageCommitCmd) Run(g *globals) error {
	ctx := g.ctx
	root, err := g.getRoot()
	if err != nil {
		return err
	}
	stageFile, err := stage.ReadStageFile(cmd.File)
	if err != nil {
		return err
	}
	obj, err := root.NewObject(g.ctx, stageFile.ID)
	if err != nil {
		return err
	}
	if cmd.Name == "" {
		cmd.Name = g.getenv(envVarUserName)
	}
	if cmd.Email == "" {
		cmd.Email = g.getenv(envVarUserEmail)
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
		g.logger)
	if err != nil {
		return err
	}
	if updated {
		g.logger.Info("removing stage file", "path", cmd.File)
		if err := os.Remove(cmd.File); err != nil {
			return fmt.Errorf("removing stage file: %w", err)
		}
	}
	return nil
}

// stage diff
type StageDiffCmd struct {
	stageCmdBase
	All bool   `name:"all" help:"include hidden files when used with --dir"`
	Dir string `name:"dir" help:"use a local directory rather than upstream object as basis for comparison to the stage."`
}

func (cmd *StageDiffCmd) Run(g *globals) error {
	ctx := g.ctx
	baseState := ocfl.PathMap{}
	stageFile, err := stage.ReadStageFile(cmd.File)
	if err != nil {
		return err
	}
	stageFile.SetLogger(g.logger)
	stageState := stageFile.NextState
	switch {
	case cmd.Dir != "":
		algs, err := stageFile.Algs()
		if err != nil {
			return err
		}
		alg := algs[0]
		filesIter, walkErr := ocflfs.UntilErr(ocflfs.WalkFiles(ctx, ocflfs.DirFS(cmd.Dir), "."))
		if !cmd.All {
			filesIter = ocflfs.FilterFiles(filesIter, ocflfs.IsNotHidden)
		}
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
		root, err := g.getRoot()
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
	fmt.Fprint(g.stdout, diffs.String())
	return nil
}

// 'stage list' command
type StageListCmd struct {
	stageCmdBase
	WithDigests bool `name:"digests" short:"d" help:"include file digests in output"`
}

func (cmd *StageListCmd) Run(g *globals) error {
	stage, err := stage.ReadStageFile(cmd.File)
	if err != nil {
		return err
	}
	stage.SetLogger(g.logger)
	stage.List(g.stdout, cmd.WithDigests)
	return nil
}

// 'stage rm' command
type StageRmCmd struct {
	stageCmdBase
	Recursive bool   `name:"recursive" short:"r" help:"remove all files in the directory"`
	Path      string `arg:"" name:"path" help:"file or directory to remove"`
}

func (cmd *StageRmCmd) Run(g *globals) error {
	stage, err := stage.ReadStageFile(cmd.File)
	if err != nil {
		return err
	}
	stage.SetLogger(g.logger)
	if err := stage.Remove(cmd.Path, cmd.Recursive); err != nil {
		return err
	}
	if err := stage.Write(cmd.File); err != nil {
		return err
	}
	return nil
}

// 'stage status' command
type StageStatusCmd struct {
	stageCmdBase
}

func (cmd *StageStatusCmd) Run(g *globals) error {
	ctx := g.ctx
	stageFile, err := stage.ReadStageFile(cmd.File)
	if err != nil {
		return err
	}
	stageFile.SetLogger(g.logger)
	fmt.Fprintf(g.stdout, "object:      %s (%s)\n", stageFile.ID, stageFile.NextHead)
	fmt.Fprintf(g.stdout, "digest alg:  %s\n", stageFile.AlgID)
	fmt.Fprintf(g.stdout, "fixity algs: %s\n", stageFile.FixityIDs)
	fmt.Fprintf(g.stdout, "state size:  %d files\n", len(stageFile.NextState))
	root, err := g.getRoot()
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
		fmt.Fprintln(g.stdout, "stage has changes to commit")
	default:
		fmt.Fprintln(g.stdout, "stage is unchanged and/or empty")
	}
	hasErrors := false
	// check stage content
	for err := range stageFile.ContentErrors() {
		hasErrors = true
		g.logger.Error(err.Error())
	}
	for err := range stageFile.StateErrors() {
		hasErrors = true
		g.logger.Error(err.Error())
	}
	if hasErrors {
		return errors.New("stage has errors")
	}
	return nil
}

func newUser(name string, email string) ocfl.User {
	if email != "" && !strings.HasPrefix(`email:`, email) {
		email = "email:" + email
	}
	return ocfl.User{Name: name, Address: email}
}

// objectUpdateOrRevert does an object update, reverting partial updates if
// os.Interupt is received. The returned bool indicates if the update completed
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
	updateCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	logger.Info("starting object update", "object_id", obj.ID())
	opts = append(opts, ocfl.UpdateWithLogger(logger))
	update, err := obj.Update(updateCtx, stage, msg, user, opts...)
	if err != nil {
		if errors.Is(err, context.Canceled) && update != nil {
			logger.Info("object update interrupted: reverting to last valid state")
			err = update.Revert(ctx, obj.FS(), obj.Path(), stage.ContentSource)
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
