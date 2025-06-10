package model

import (
	"context"
	"io/fs"
	"iter"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
	ocflfs "github.com/srerickson/ocfl-go/fs"
	"golang.org/x/sync/errgroup"
)

// FileTree represents the logical state of an OCFL object as a hiearchical
// tree.
type FileTree struct {
	*FileTreeNode
	ObjectID string
	VNum     ocfl.VNum
}

func NewFileTree(obj *ocfl.Object, vnum int) *FileTree {
	version := obj.Version(vnum)
	manifest := obj.Manifest()
	if version == nil || manifest == nil {
		return nil
	}
	root := &FileTreeNode{}
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
				Algorithm: obj.DigestAlgorithm(),
				Digests:   obj.GetFixity(dig),
			}
			fileref.Digests[obj.DigestAlgorithm().ID()] = dig
			fileRefs[dig] = fileref
		}
		root.add(logicalPath, fileref)
	}
	return &FileTree{
		FileTreeNode: root,
		ObjectID:     obj.ID(),
		VNum:         version.VNum(),
	}
}

// Sub returns a subtree of ft for the logicalPath. If an error is returned, it
// is an fs.PathError.
func (ft FileTree) Sub(logicalPath string) (*FileTree, error) {
	if !fs.ValidPath(logicalPath) {
		return nil, &fs.PathError{
			Path: logicalPath,
			Op:   "get",
			Err:  fs.ErrInvalid,
		}
	}
	node := ft.FileTreeNode.get(logicalPath)
	if node == nil {
		return nil, &fs.PathError{
			Path: logicalPath,
			Op:   "get",
			Err:  fs.ErrNotExist,
		}
	}
	return &FileTree{
		FileTreeNode: node,
		ObjectID:     ft.ObjectID,
		VNum:         ft.VNum,
	}, nil
}

func (ft *FileTree) Children() iter.Seq[*FileTree] {
	return func(yield func(*FileTree) bool) {
		for node := range ft.FileTreeNode.Children() {
			subtree := &FileTree{
				FileTreeNode: node,
				ObjectID:     ft.ObjectID,
				VNum:         ft.VNum,
			}
			if !yield(subtree) {
				return
			}
		}
	}
}

func (ft FileTree) Version() string { return ft.VNum.String() }

type FileTreeNode struct {
	File     *digest.FileRef // file contents
	Name     string          // logical name (base)
	Parent   *FileTreeNode
	children map[string]*FileTreeNode // directory contents by name
}

func (n *FileTreeNode) add(logicalPath string, contentFile *digest.FileRef) {
	childName, nextLogicalName, noSlash := strings.Cut(logicalPath, "/")
	if childName == "" {
		return
	}
	if n.children == nil {
		n.children = map[string]*FileTreeNode{}
	}
	if n.children[childName] == nil {
		n.children[childName] = &FileTreeNode{
			Parent: n,
			Name:   childName,
		}
	}
	child := n.children[childName]
	if !noSlash || nextLogicalName == "" {
		child.File = contentFile
		return
	}
	child.add(nextLogicalName, contentFile)
}

func (n *FileTreeNode) get(name string) *FileTreeNode {
	if name == "." || name == "" {
		return n
	}
	if n.children == nil {
		return nil
	}
	childName, nextName, _ := strings.Cut(name, "/")
	child := n.children[childName]
	if child == nil {
		return nil
	}
	return child.get(nextName)
}

func (n *FileTreeNode) statFiles(ctx context.Context, conc int) error {
	grp, ctx := errgroup.WithContext(ctx)
	grp.SetLimit(conc)
	for f := range n.Files() {
		grp.Go(func() error {
			return f.File.Stat(ctx)
		})
	}
	return grp.Wait()
}

// Iterate over children: first all directories, then all files.
func (n *FileTreeNode) Children() iter.Seq[*FileTreeNode] {
	return func(yield func(*FileTreeNode) bool) {
		for dir := range n.Dirs() {
			if !yield(dir) {
				return
			}
		}
		for file := range n.Files() {
			if !yield(file) {
				return
			}
		}
	}
}

func (n *FileTreeNode) IsDir() bool { return n.File == nil }

// iterate over the children that are directories in sorted order
func (n *FileTreeNode) Dirs() iter.Seq[*FileTreeNode] {
	return func(yield func(*FileTreeNode) bool) {
		names := slices.Collect(maps.Keys(n.children))
		slices.Sort(names)
		for _, name := range names {
			child := n.children[name]
			if !child.IsDir() {
				continue
			}
			if !yield(child) {
				return
			}
		}
	}
}

// iterate over the children that are files is sorted order
func (n *FileTreeNode) Files() iter.Seq[*FileTreeNode] {
	return func(yield func(*FileTreeNode) bool) {
		names := slices.Collect(maps.Keys(n.children))
		slices.Sort(names)
		for _, name := range names {
			child := n.children[name]
			if child.IsDir() {
				continue
			}
			if !yield(child) {
				return
			}
		}
	}
}

func (n *FileTreeNode) Path() string {
	if n.Parent == nil {
		return "."
	}
	return path.Join(n.Parent.Path(), n.Name)
}

// func vistitFileTree(node *FileTree, run func(*FileTree) error) error {
// 	for _, child := range node.children {
// 		if err := vistitFileTree(child, run); err != nil {
// 			return err
// 		}
// 	}
// 	return run(node)
// }
