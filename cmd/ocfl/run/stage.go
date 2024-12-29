package run

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
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
	New NewStageCmd `cmd:"new" help:"create a new stage file for preparing updates to an object"`
	Add StageAddCmd `cmd:"add" help:"add files or directories to a stage"`
}

type NewStageCmd struct {
	File string `name:"file" short:"f" default:"ocfl-stage.json" help:"stage file path"`
	Spec string `name:"ocflv" default:"1.1" help:"OCFL spec for the new object version"`
	Alg  string `name:"alg" default:"sha512" help:"Digest Algorithm used to digest content. Ignored for existing objects."`
	ID   string `name:"object_id" arg:"" help:"object id for the new stage"`
}

func (cmd *NewStageCmd) Run(g *globals) error {
	root, err := g.getRoot()
	if err != nil {
		return err
	}
	obj, err := root.NewObject(g.ctx, cmd.ID)
	if err != nil {
		return err
	}
	if _, err := readStage(cmd.File); err == nil {
		err := fmt.Errorf("stage file exists: %s", cmd.File)
		return err
	}
	var stage = stage{
		ID:      cmd.ID,
		Version: ocfl.V(1),
		State:   ocfl.DigestMap{},
		AlgID:   cmd.Alg,
	}
	if obj.Exists() {
		inv := obj.Inventory()
		next, err := inv.Head().Next()
		if err != nil {
			return err
		}
		stage.Version = next
		stage.State = inv.Version(0).State()
		stage.AlgID = inv.DigestAlgorithm().ID()
	}
	if err := writeStage(&stage, cmd.File); err != nil {
		return err
	}
	g.logger.Info("new stage file created", "path", cmd.File)
	return nil
}

type StageAddCmd struct {
	File string `name:"file" short:"f" default:"ocfl-stage.json" help:"stage file path"`
	As   string `name:"as" help:"logical path for added content"`
	Path string `arg:"" help:"files or directory paths to add to the stage"`
}

func (cmd *StageAddCmd) Run(g *globals) error {
	ctx := g.ctx
	stage, err := readStage(cmd.File)
	if err != nil {
		return err
	}
	if cmd.As != "" && !fs.ValidPath(cmd.As) {
		return fmt.Errorf("invalid logical path for new content: %s", cmd.As)
	}
	alg := stage.Alg()
	absPath, err := filepath.Abs(cmd.Path)
	if err != nil {
		return err
	}
	ftype, err := getType(absPath)
	if err != nil {
		return err
	}
	added := []stageFile{}
	switch {
	case ftype.IsDir():
		as := "."
		if cmd.As != "" {
			as = cmd.As
		}
		fsys := ocfl.DirFS(absPath)
		files, walkErr := ocfl.WalkFiles(ctx, fsys, ".")
		for digests, err := range files.IgnoreHidden().DigestBatch(ctx, 5, alg) {
			if err != nil {
				return err
			}
			newFile := stageFile{
				ManifestPath: digests.FullPath(),
				StatePath:    path.Join(as, digests.FullPath()),
				Digests:      digests.Digests,
				Size:         digests.Info.Size(),
				Modtime:      digests.Info.ModTime(),
			}
			added = append(added, newFile)
			fmt.Println(newFile.ManifestPath)
		}
		if err := walkErr(); err != nil {
			return err
		}
	// case ftype.IsRegular():
	//
	default:
		return errors.New("path has unsupported file type")
		// ignore it
	}
	if stage.Added == nil {
		stage.Added = map[string][]stageFile{}
	}
	stage.Added[absPath] = added
	if err := writeStage(stage, cmd.File); err != nil {
		return err
	}
	return nil
}

func readStage(name string) (*stage, error) {
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

func writeStage(s *stage, name string) error {
	stageBytes, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.WriteFile(name, stageBytes, 0644); err != nil {
		return err
	}
	return nil
}

func getType(name string) (fs.FileMode, error) {
	info, err := os.Stat(name)
	if err != nil {
		return 0, err
	}
	return info.Mode().Type(), nil
}

type stage struct {
	ID      string
	Version ocfl.VNum
	State   ocfl.DigestMap
	AlgID   string
	Added   map[string][]stageFile
}

type stageFile struct {
	ManifestPath string     `json:"content"`
	StatePath    string     `json:"state"`
	Digests      digest.Set `json:"digests"`
	Size         int64      `json:"size"`
	Modtime      time.Time  `json:"modtime"`
}
