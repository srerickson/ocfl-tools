package server

import (
	"context"
	"errors"
	"io/fs"
	"iter"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
	ocflfs "github.com/srerickson/ocfl-go/fs"
	"golang.org/x/sync/errgroup"
)

func NewObject(ctx context.Context, base *ocfl.Object, v string, logicalPath string) (*Object, error) {
	var vnum ocfl.VNum
	ocfl.ParseVNum(v, &vnum)
	ver := base.Version(vnum.Num())
	if ver == nil {
		return nil, errors.New("object version doesn't exist")
	}
	rootTree := NewFileTree(base, vnum.Num())
	if fs.ValidPath(logicalPath) {
		rootTree.ActivePath = logicalPath
	}
	if err := rootTree.Active().statFiles(ctx, 5); err != nil {
		return nil, err
	}
	obj := &Object{
		ID:              base.ID(),
		DigestAlgorithm: base.DigestAlgorithm().ID(),
		Num:             vnum.String(),
		Message:         ver.Message(),
		Created:         ver.Created(),
		User:            ver.User(),
		FileTree:        rootTree,
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
	// Path            string
	FileTree *FileTree // tree for entire root
}

type FileTree struct {
	*FileTreeNode // root
	ObjectID      string
	ActivePath    string
}

func (ft FileTree) Active() *FileTree {
	node := ft.FileTreeNode.get(ft.ActivePath)
	return &FileTree{
		FileTreeNode: node,
		ObjectID:     ft.ObjectID,
		ActivePath:   ft.ActivePath,
	}
}

func (ft FileTree) IsActive() bool {
	if ft.Path() == ft.ActivePath {
		return true
	}
	return strings.HasPrefix(ft.ActivePath, ft.Path()+"/")
}

func (ft *FileTree) Children() iter.Seq2[string, *FileTree] {
	return func(yield func(string, *FileTree) bool) {
		for name, node := range ft.FileTreeNode.Children() {
			subtree := &FileTree{FileTreeNode: node, ObjectID: ft.ObjectID, ActivePath: ft.ActivePath}
			if !yield(name, subtree) {
				return
			}
		}
	}
}

type FileTreeNode struct {
	//ObjectID string          // Object ID
	File     *digest.FileRef // file contents
	Name     string
	Parent   *FileTreeNode
	children map[string]*FileTreeNode // directory contents
}

func (n *FileTreeNode) add(logicalName string, contentFile *digest.FileRef) {
	childName, nextLogicalName, noSlash := strings.Cut(logicalName, "/")
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
	for _, f := range n.Files() {
		grp.Go(func() error {
			return f.File.Stat(ctx)
		})
	}
	// vistitFileTree(n, func(ft *FileTree) error {
	// 	if ft.File == nil {
	// 		return nil
	// 	}
	// 	return ft.File.Stat(ctx)
	// })
	return grp.Wait()
}

// Iterate over children: first all directories, then all files.
func (n *FileTreeNode) Children() iter.Seq2[string, *FileTreeNode] {
	return func(yield func(string, *FileTreeNode) bool) {
		for name, dir := range n.Dirs() {
			if !yield(name, dir) {
				return
			}
		}
		for name, dir := range n.Files() {
			if !yield(name, dir) {
				return
			}
		}
	}
}

func (n *FileTreeNode) IsDir() bool { return n.File == nil }

// iterate over the children that are directories in sorted order
func (n *FileTreeNode) Dirs() iter.Seq2[string, *FileTreeNode] {
	return func(yield func(string, *FileTreeNode) bool) {
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

// iterate over the children that are files is sorted order
func (n *FileTreeNode) Files() iter.Seq2[string, *FileTreeNode] {
	return func(yield func(string, *FileTreeNode) bool) {
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

func (n *FileTreeNode) Path() string {
	if n.Parent == nil {
		return "."
	}
	return path.Join(n.Parent.Path(), n.Name)
}

func NewFileTree(obj *ocfl.Object, num int) *FileTree {
	version := obj.Version(num)
	manifest := obj.Manifest()
	if version == nil || manifest == nil {
		return nil
	}
	root := &FileTreeNode{
		// ObjectID: obj.ID(),
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
	}
}

// func vistitFileTree(node *FileTree, run func(*FileTree) error) error {
// 	for _, child := range node.children {
// 		if err := vistitFileTree(child, run); err != nil {
// 			return err
// 		}
// 	}
// 	return run(node)
// }
