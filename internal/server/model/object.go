package model

import (
	"context"
	"errors"
	"time"

	"github.com/srerickson/ocfl-go"
)

type Object struct {
	ID              string
	DigestAlgorithm string
	Num             string
	Message         string
	User            *ocfl.User
	Created         time.Time
	Files           *FileTree // tree for entire root
}

func NewObject(ctx context.Context, obj *ocfl.Object, v string, logicalPath string) (*Object, error) {
	var vnum ocfl.VNum
	ocfl.ParseVNum(v, &vnum)
	ver := obj.Version(vnum.Num())
	if ver == nil {
		return nil, errors.New("object version doesn't exist")
	}

	tree := NewFileTree(obj, vnum.Num())
	if logicalPath != "" {
		var err error
		tree, err = tree.SubTree(logicalPath)
		if err != nil {
			return nil, err
		}
	}
	if err := tree.FileTreeNode.statFiles(ctx, 5); err != nil {
		return nil, err
	}
	return &Object{
		ID:              obj.ID(),
		DigestAlgorithm: obj.DigestAlgorithm().ID(),
		Num:             ver.VNum().String(),
		Message:         ver.Message(),
		Created:         ver.Created(),
		User:            ver.User(),
		Files:           tree,
	}, nil
}
