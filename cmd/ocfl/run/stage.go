package run

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"

	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
)

const (
	stageHelp = "commands for working with stages (i.e., object updates)"
)

type stage struct {
	ID      string
	Version ocfl.VNum
	State   ocfl.DigestMap
	AlgID   string
}

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
	return nil
}

type StageAddCmd struct {
	File  string   `name:"file" short:"f" default:"ocfl-stage.json" help:"stage file path"`
	Paths []string `arg:"" help:"files or directory paths to add to the stage"`
}

func (cmd *StageAddCmd) Run(g *globals) error {
	ctx := g.ctx
	stage, err := readStage(cmd.File)
	if err != nil {
		return err
	}
	alg := stage.Alg()
	var change bool
	for _, p := range cmd.Paths {
		ftype, err := getType(p)
		if err != nil {
			return err
		}
		switch {
		case ftype.IsDir():
			fsys := ocfl.DirFS(p)
			files, walkErr := ocfl.WalkFiles(ctx, fsys, ".")
			files.IgnoreHidden().DigestBatch(ctx, 5, alg).Stage()
			if err := walkErr(); err != nil {
				return err
			}
		case ftype.IsRegular():
			//
		default:
			// ignore it
		}
		fmt.Println(p, ftype)
	}
	if change {
		if err := writeStage(stage, cmd.File); err != nil {
			return err
		}
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
