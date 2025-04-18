package server

import (
	"context"
	"errors"
	"iter"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
	ocflfs "github.com/srerickson/ocfl-go/fs"
	"golang.org/x/sync/errgroup"
)

func NewObject(ctx context.Context, base *ocfl.Object) (*Object, error) {
	inv := base.Inventory()
	if inv == nil {
		return nil, errors.New("object doesn't exist")
	}
	vnum := inv.Head()
	ver := base.Inventory().Version(vnum.Num())
	if ver == nil {
		return nil, errors.New("object version doesn't exist")
	}
	files := fileTree(base, vnum.Num())
	if err := files.statFiles(ctx, 5); err != nil {
		return nil, err
	}
	obj := &Object{
		ID:              inv.ID(),
		DigestAlgorithm: inv.DigestAlgorithm().ID(),
		Num:             vnum.String(),
		Message:         ver.Message(),
		Created:         ver.Created(),
		User:            ver.User(),
		Files:           files,
		//Versions:        make([]ObjectVersion, len(vers)-1),
	}
	// slices.Reverse(vers)
	// for i, vnum := range vers {
	// 	verFS, err := base.OpenVersion(ctx, vnum.Num())
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	objVersion := ObjectVersion{
	// 		Num:     vnum.String(),
	// 		Message: verFS.Message(),
	// 		Created: verFS.Created(),
	// 		User:    verFS.User(),
	// 		Files:   fileTree(ctx, base, vnum.Num()),
	// 	}
	// 	if i == 0 {
	// 		obj.Head = objVersion
	// 		continue
	// 	}
	// 	obj.Versions[i-1] = objVersion
	// }
	return obj, nil
}

type Object struct {
	ID              string
	DigestAlgorithm string
	Num             string
	Message         string
	User            *ocfl.User
	Created         time.Time
	Files           *FileTree
}

type FileTree struct {
	ObjectID string               // Object ID
	File     *digest.FileRef      // file contents
	children map[string]*FileTree // directory contents
}

func (n *FileTree) add(logicalName string, contentFile *digest.FileRef) {
	childName, nextLogicalName, noSlash := strings.Cut(logicalName, "/")
	if childName == "" {
		return
	}
	if n.children == nil {
		n.children = map[string]*FileTree{}
	}
	if n.children[childName] == nil {
		n.children[childName] = &FileTree{ObjectID: n.ObjectID}
	}
	child := n.children[childName]
	if !noSlash || nextLogicalName == "" {
		child.File = contentFile
		return
	}
	child.add(nextLogicalName, contentFile)
}

func (n *FileTree) statFiles(ctx context.Context, conc int) error {
	grp, ctx := errgroup.WithContext(ctx)
	grp.SetLimit(conc)
	vistitFileTree(n, func(ft *FileTree) error {
		if ft.File == nil {
			return nil
		}
		return ft.File.Stat(ctx)
	})
	return grp.Wait()
}

func (n *FileTree) IsDir() bool { return n.File == nil }

func (n *FileTree) Children() iter.Seq2[string, *FileTree] {
	return func(yield func(string, *FileTree) bool) {
		names := slices.Collect(maps.Keys(n.children))
		slices.Sort(names)
		for _, name := range names {
			if !yield(name, n.children[name]) {
				return
			}
		}
	}
}

func (n *FileTree) Dirs() iter.Seq2[string, *FileTree] {
	return func(yield func(string, *FileTree) bool) {
		names := slices.Collect(maps.Keys(n.children))
		slices.Sort(names)
		for _, name := range names {
			child := n.children[name]
			if !child.IsDir() {
				continue
			}
			if !yield(name, child) {
				return
			}
		}
	}
}

func (n *FileTree) Files() iter.Seq2[string, *FileTree] {
	return func(yield func(string, *FileTree) bool) {
		names := slices.Collect(maps.Keys(n.children))
		slices.Sort(names)
		for _, name := range names {
			child := n.children[name]
			if child.IsDir() {
				continue
			}
			if !yield(name, child) {
				return
			}
		}
	}
}

func fileTree(obj *ocfl.Object, num int) *FileTree {
	inv := obj.Inventory()
	if inv == nil {
		return nil
	}
	version := inv.Version(num)
	manifest := inv.Manifest()
	if version == nil || manifest == nil {
		return nil
	}
	root := &FileTree{
		ObjectID: obj.ID(),
	}
	statePathMap := version.State().PathMap()
	fileRefs := map[string]*digest.FileRef{}
	for logicalPath, dig := range statePathMap.SortedPaths() {
		fileref := fileRefs[dig]
		if fileref == nil {
			contentPaths := manifest[dig]
			if len(contentPaths) < 1 {
				continue
			}
			fileref = &digest.FileRef{
				FileRef: ocflfs.FileRef{
					FS:      obj.FS(),
					BaseDir: obj.Path(),
					Path:    contentPaths[0],
				},
				Algorithm: inv.DigestAlgorithm(),
				Digests:   inv.GetFixity(dig),
			}
			fileref.Digests[inv.DigestAlgorithm().ID()] = dig
			fileRefs[dig] = fileref
		}
		root.add(logicalPath, fileref)
	}
	return root
}

func vistitFileTree(node *FileTree, run func(*FileTree) error) error {
	for _, child := range node.children {
		if err := vistitFileTree(child, run); err != nil {
			return err
		}
	}
	return run(node)
}
