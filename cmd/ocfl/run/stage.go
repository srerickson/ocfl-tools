package run

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
)

const (
	stageHelp = "commands for working with stages (i.e., object updates)"
)

func (s stage) Alg() digest.Algorithm {
	alg, err := digest.DefaultRegistry().Get(s.AlgID)
	if err != nil {
		panic(err)
	}
	return alg
}

type StageCmd struct {
	New    NewStageCmd    `cmd:"new" help:"create a new stage file for preparing updates to an object"`
	Add    StageAddCmd    `cmd:"add" help:"add files or directories to a stage"`
	Commit StageCommitCmd `cmd:"commit" help:"commit new version"`
}

type NewStageCmd struct {
	Stage string `name:"stage" short:"s" default:"ocfl-stage.json" help:"stage file path"`
	Spec  string `name:"ocflv" default:"1.1" help:"OCFL spec for the new object version"`
	Alg   string `name:"alg" default:"sha512" help:"Digest Algorithm used to digest content. Ignored for existing objects."`
	ID    string `name:"object_id" arg:"" help:"object id for the new stage"`
}

func (cmd *NewStageCmd) Run(g *globals) error {
	if _, err := openStageFile(cmd.Stage); err == nil {
		err := fmt.Errorf("stage file already exists: %s", cmd.Stage)
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
	var stage = stage{
		ID:              cmd.ID,
		Version:         ocfl.V(1),
		NewState:        ocfl.PathMap{},
		NewFixity:       map[string]digest.Set{},
		ExistingContent: []string{},
		NewContent:      map[string]localFile{},
		AlgID:           cmd.Alg,
	}
	if obj.Exists() {
		inv := obj.Inventory()
		next, err := inv.Head().Next()
		if err != nil {
			return err
		}
		stage.Version = next
		stage.NewState = inv.Version(0).State().PathMap()
		stage.AlgID = inv.DigestAlgorithm().ID()
	}
	if err := stage.write(cmd.Stage); err != nil {
		return err
	}
	g.logger.Info("stage file created", "path", cmd.Stage, "object_id", stage.ID, "object_version", stage.Version)
	return nil
}

type StageAddCmd struct {
	Stage string `name:"stage" short:"s" default:"ocfl-stage.json" help:"stage file path"`
	Jobs  int    `name:"jobs" short:"j" default:"0" help:"number of files to digest concurrently. Defaults to number of CPUs."`
	As    string `name:"as" help:"logical name/path for added content"`
	Path  string `arg:"" help:"file or directory path to add to the stage"`
}

func (cmd *StageAddCmd) Run(g *globals) error {
	ctx := g.ctx
	stage, err := openStageFile(cmd.Stage)
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
	alg := stage.Alg()
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
			primaryDigest := digests.Digests.Delete(digests.Algorithm.ID())
			addedDigest := stage.NewState[statePath]
			if addedDigest != primaryDigest {
				stage.NewState[statePath] = primaryDigest
				switch {
				case addedDigest == "":
					g.logger.Info("new file", "path", statePath)
				default:
					g.logger.Info("updated file", "path", statePath)
				}
			}
			if len(digests.Digests) > 0 {
				stage.NewFixity[primaryDigest] = digests.Digests
			}
			alreadyCommitted := slices.Contains(stage.ExistingContent, primaryDigest)
			_, isStaged := stage.NewContent[primaryDigest]
			if !alreadyCommitted && !isStaged {
				stage.NewContent[primaryDigest] = localFile{
					LocalDir:  absPath,
					LocalPath: digests.FullPath(),
					Size:      digests.Info.Size(),
					Modtime:   digests.Info.ModTime(),
				}
			}
		}
		if err := walkErr(); err != nil {
			return fmt.Errorf("while walking directory tree: %w", err)
		}
	case ftype.IsRegular():
		fsys := ocfl.DirFS(filepath.Dir(absPath))
		base := filepath.Base(absPath)
		statePath := base
		if cmd.As != "" {
			statePath = cmd.As
		}
		digestsSeq := ocfl.Files(fsys, base).Digest(ctx, alg)
		for digests, err := range digestsSeq {
			if err != nil {
				return err
			}
			primaryDigest := digests.Digests.Delete(digests.Algorithm.ID())
			addedDigest := stage.NewState[statePath]
			if addedDigest != primaryDigest {
				stage.NewState[statePath] = primaryDigest
				switch {
				case addedDigest == "":
					g.logger.Info("new file", "path", statePath)
				default:
					g.logger.Info("updated file", "path", statePath)
				}
			}
			if len(digests.Digests) > 0 {
				stage.NewFixity[primaryDigest] = digests.Digests
			}
			alreadyCommitted := slices.Contains(stage.ExistingContent, primaryDigest)
			_, isStaged := stage.NewContent[primaryDigest]
			if !alreadyCommitted && !isStaged {
				stage.NewContent[primaryDigest] = localFile{
					LocalDir:  absPath,
					LocalPath: digests.FullPath(),
					Size:      digests.Info.Size(),
					Modtime:   digests.Info.ModTime(),
				}
			}
		}
	default:
		return errors.New("path has unsupported file type")
	}
	if err := stage.write(cmd.Stage); err != nil {
		return err
	}
	return nil
}

type StageCommitCmd struct {
	File    string `name:"stage" short:"s" default:"ocfl-stage.json" help:"stage file path"`
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
	stage, err := openStageFile(cmd.File)
	if err != nil {
		return err
	}
	obj, err := root.NewObject(g.ctx, stage.ID)
	if err != nil {
		return err
	}
	commit := &ocfl.Commit{
		ID:      stage.ID,
		Message: cmd.Message,
		User: ocfl.User{
			Name:    cmd.Name,
			Address: cmd.Email,
		},
	}
	commit.Stage, err = stage.Stage()
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

func openStageFile(name string) (*stage, error) {
	var stage stage
	bytes, err := os.ReadFile(name)
	if err != nil {
		// have you created
		return nil, err
	}
	if err := json.Unmarshal(bytes, &stage); err != nil {
		return nil, err
	}
	return &stage, nil
}

type stage struct {
	ID              string
	Version         ocfl.VNum
	NewState        ocfl.PathMap
	ExistingContent []string // array for current digests
	AlgID           string
	NewContent      map[string]localFile // digest to local source
	NewFixity       map[string]digest.Set
}

func (s stage) write(name string) error {
	stageBytes, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.WriteFile(name, stageBytes, 0644); err != nil {
		return err
	}
	return nil
}

// Stage returns s as an ocfl.Stage
func (s *stage) Stage() (*ocfl.Stage, error) {
	state := s.NewState.DigestMap()
	if err := state.Valid(); err != nil {
		return nil, err
	}
	return &ocfl.Stage{
		State:           state,
		DigestAlgorithm: s.Alg(),
		ContentSource:   s,
		FixitySource:    s,
	}, nil
}

// stage implements ocfl.ContentSource
func (s stage) GetContent(digest string) (ocfl.FS, string) {
	localFile, exists := s.NewContent[digest]
	if !exists {
		return nil, ""
	}
	return ocfl.DirFS(localFile.LocalDir), localFile.LocalPath
}

// stage implements ocfl.FixitySource
func (s stage) GetFixity(digest string) digest.Set {
	return s.NewFixity[digest]
}

type localFile struct {
	LocalDir  string    `json:"local"`
	LocalPath string    `json:"path"`
	Size      int64     `json:"size"`
	Modtime   time.Time `json:"modtime"`
}

func getFileType(name string) (fs.FileMode, error) {
	info, err := os.Stat(name)
	if err != nil {
		return 0, err
	}
	return info.Mode().Type(), nil
}
