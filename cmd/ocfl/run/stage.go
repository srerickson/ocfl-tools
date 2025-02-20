package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/util"
)

const (
	stageHelp = "commands for working with stages (i.e., object updates)"
)

type StageCmd struct {
	Add    StageAddCmd    `cmd:"" help:"add a file or directory to the stage"`
	Commit StageCommitCmd `cmd:"" help:"commit the stage as a new object version"`
	Ls     StageListCmd   `cmd:"" help:"list files in the stage state"`
	New    NewStageCmd    `cmd:"" help:"create a new stage for preparing updates to an object"`
	Rm     StageRmCmd     `cmd:"" help:"remove a file or directory from the stage"`
}

// shared fields used by all stage sub-commands
type stageCmdBase struct {
	File string `name:"file" short:"f" default:"ocfl-stage.json" help:"path to stage file"`
}

type NewStageCmd struct {
	stageCmdBase
	Spec string `name:"ocflv" default:"1.1" help:"OCFL spec for the new object version"`
	Alg  string `name:"alg" default:"sha512" help:"Digest Algorithm used to digest content. Ignored for existing objects."`
	ID   string `name:"id" arg:"" help:"object id for the new stage"`
}

func (cmd *NewStageCmd) Run(g *globals) error {
	if _, err := util.ReadStageFile(cmd.File); err == nil {
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
	stage, err := util.NewLocalStage(obj, cmd.Alg)
	if err != nil {
		return err
	}
	if err := stage.Write(cmd.File); err != nil {
		return err
	}
	g.logger.Info("stage file created", "path", cmd.File, "object_id", stage.ID, "object_version", stage.Version)
	return nil
}

type StageAddCmd struct {
	stageCmdBase
	All    bool   `name:"all" help:"include hidden files (.*) in path. Ignored if path is a file."`
	As     string `name:"as" help:"logical name for the new content. Default: base name if path is a file; '.' if path is a directory."`
	Jobs   int    `name:"jobs" short:"j" default:"0" help:"number of files to digest concurrently. Defaults to the number of CPU cores."`
	Remove bool   `name:"rm" help:"also remove staged files not found in the path. Ignored if path is a file."`
	Path   string `arg:"" help:"file or parent directory for content to add to the stage"`
}

func (cmd *StageAddCmd) Run(g *globals) error {
	ctx := g.ctx
	stage, err := util.ReadStageFile(cmd.File)
	if err != nil {
		return err
	}
	stage.SetLogger(g.logger)
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
		err = stage.AddDir(ctx, absPath, cmd.As, cmd.All, cmd.Remove, cmd.Jobs)
	case ftype.IsRegular():
		err = stage.AddFile(absPath, cmd.As)
	default:
		err = errors.New("unsupported file type for: " + absPath)
	}
	if err != nil {
		return err
	}
	if err := stage.Write(cmd.File); err != nil {
		return err
	}
	return nil
}

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
	stage, err := util.ReadStageFile(cmd.File)
	if err != nil {
		return err
	}
	stage.SetLogger(g.logger)
	obj, err := root.NewObject(g.ctx, stage.ID)
	if err != nil {
		return err
	}
	if cmd.Name == "" {
		cmd.Name = g.getenv(envVarUserName)
	}
	if cmd.Email == "" {
		cmd.Email = g.getenv(envVarUserEmail)
	}
	commit, err := stage.BuildCommit(cmd.Name, cmd.Email, cmd.Message)
	if err != nil {
		return fmt.Errorf("stage has errors: %w", err)
	}
	if err := obj.Commit(ctx, commit); err != nil {
		return fmt.Errorf("creating new object version: %w", err)
	}
	if err := os.Remove(cmd.File); err != nil {
		return fmt.Errorf("removing stage file: %w", err)
	}
	return nil
}

type StageListCmd struct {
	stageCmdBase
	WithDigests bool `name:"digests" short:"d" help:"include file digests in output"`
}

func (cmd *StageListCmd) Run(g *globals) error {
	stage, err := util.ReadStageFile(cmd.File)
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
	stage, err := util.ReadStageFile(cmd.File)
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
