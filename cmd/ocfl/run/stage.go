package run

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/srerickson/ocfl-go"
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
	stage, err := util.NewLocalStage(obj, cmd.ID, cmd.Alg)
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
	Jobs int    `name:"jobs" short:"j" default:"0" help:"number of files to digest concurrently. Defaults to number of CPUs."`
	As   string `name:"as" help:"logical name for the new content. Default: base name if path is a file; '.' if path is a directory."`
	Path string `arg:"" help:"file or parent directory for content to add to the stage"`
}

func (cmd *StageAddCmd) Run(g *globals) error {
	ctx := g.ctx
	stage, err := util.ReadStageFile(cmd.File)
	if err != nil {
		return err
	}
	if cmd.As != "" && !fs.ValidPath(cmd.As) {
		return fmt.Errorf("invalid logical path for new content: %s", cmd.As)
	}
	jobs := cmd.Jobs
	if jobs < 1 {
		jobs = runtime.NumCPU()
	}
	alg, err := stage.Alg()
	if err != nil {
		return err
	}
	absPath, err := filepath.Abs(cmd.Path)
	if err != nil {
		return err
	}
	ftype, err := getFileType(absPath)
	if err != nil {
		return err
	}
	switch {
	case ftype.IsDir():
		fsys := ocfl.DirFS(absPath)
		as := "."
		if cmd.As != "" {
			as = cmd.As
		}
		files, walkErr := ocfl.WalkFiles(ctx, fsys, ".")
		digestsSeq := files.IgnoreHidden().DigestBatch(ctx, jobs, alg)
		for digests, err := range digestsSeq {
			if err != nil {
				return err
			}
			statePath := path.Join(as, digests.FullPath())
			if err := stage.Add(statePath, digests, absPath, g.logger); err != nil {
				return err
			}
		}
		if err := walkErr(); err != nil {
			return fmt.Errorf("while walking directory tree: %w", err)
		}
	case ftype.IsRegular():
		localDir := filepath.Dir(absPath)
		fsys := ocfl.DirFS(localDir)
		base := filepath.Base(absPath)
		statePath := base
		if cmd.As != "" {
			statePath = cmd.As
		}
		fileSeq, errFn := ocfl.Files(fsys, base).Stat(ctx).UntilErr()
		for digests, err := range fileSeq.Digest(ctx, alg) {
			if err != nil {
				return err
			}
			if err := stage.Add(statePath, digests, localDir, g.logger); err != nil {
				return err
			}
		}
		if err := errFn(); err != nil {
			return err
		}
	default:
		return errors.New("path has unsupported file type")
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
	obj, err := root.NewObject(g.ctx, stage.ID)
	if err != nil {
		return err
	}
	if cmd.Message == "" {
		return fmt.Errorf("a message is required for the new object version")
	}
	userName := cmd.Name
	if userName == "" {
		userName = g.getenv(envVarUserName)
	}
	if userName == "" {
		return fmt.Errorf("a name is required for the new object version")
	}
	userEmail := cmd.Email
	if userEmail == "" {
		userEmail = g.getenv(envVarUserEmail)
	}
	if userEmail != "" {
		// make address a valid uri
		userEmail = "email:" + userEmail
	}
	commit, err := stage.BuildCommit()
	if err != nil {
		return fmt.Errorf("stage has errors: %w", err)
	}
	commit.Message = cmd.Message
	commit.User = ocfl.User{
		Name:    userName,
		Address: userEmail,
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
	for p, digest := range util.PathMapEachPath(stage.NewState) {
		if cmd.WithDigests {
			fmt.Fprintln(g.stdout, digest, p)
			continue
		}
		fmt.Fprintln(g.stdout, p)
	}
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
	toDelete := path.Clean(cmd.Path)
	switch {
	case cmd.Recursive && toDelete == ".":
		stage.NewState = ocfl.PathMap{}
	default:
		for p := range util.PathMapEachPath(stage.NewState) {
			recursiveMatch := cmd.Recursive && (strings.HasPrefix(p, toDelete+"/") || toDelete == ".")
			if p == toDelete || recursiveMatch {
				delete(stage.NewState, p)
				g.logger.Info("removed", "path", p)
			}
		}
	}
	if err := stage.Write(cmd.File); err != nil {
		return err
	}
	return nil
}

func getFileType(name string) (fs.FileMode, error) {
	info, err := os.Stat(name)
	if err != nil {
		return 0, err
	}
	return info.Mode().Type(), nil
}
