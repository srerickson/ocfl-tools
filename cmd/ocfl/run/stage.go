package run

import (
	"encoding/json"
	"os"

	"github.com/srerickson/ocfl-go"
)

const (
	stageHelp = "commands for working with stages (i.e., object updates)"
)

type StageCmd struct {
	New NewStageCmd `cmd:"new" help:"create a new stage file for preparing updates to an object"`
}

type NewStageCmd struct {
	File string `name:"file" short:"f" default:"ocfl-stage.json" help:"stage file path"`
	ID   string `name:"object_id" arg:"" help:"object id for the new stage"`
	Spec string `name:"ocflv" default:"1.1" help:"OCFL spec fo the new object"`
	Alg  string `name:"alg" default:"sha512" help:"Digest Algorithm used to digest content. Ignored for existing objects."`
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
	var stage = struct {
		ID      string
		Version ocfl.VNum
		State   ocfl.DigestMap
	}{
		ID:      cmd.ID,
		Version: ocfl.V(1),
		State:   ocfl.DigestMap{},
	}
	if obj.Exists() {
		next, err := obj.Inventory().Head().Next()
		if err != nil {
			return err
		}
		stage.Version = next
		stage.State = obj.Inventory().Version(0).State()
	}
	stageBytes, err := json.Marshal(&stage)
	if err != nil {
		return err
	}
	if err := os.WriteFile(cmd.File, stageBytes, 0644); err != nil {
		return err
	}
	return nil
}
