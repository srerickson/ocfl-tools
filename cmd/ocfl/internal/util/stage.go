package util

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"time"

	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
)

type LocalStage struct {
	// Object ID
	ID string `json:"object_id"`

	// number of new object version
	Version ocfl.VNum

	// State for new object versions
	NewState ocfl.PathMap

	// digests that are already part of the object, don't need to be uploaded.
	ExistingContent []string

	// Primary digest algorithm (sha512 or sha256)
	AlgID string

	// Fixity Algorithms
	FixityIDs []string

	// NewContent maps (primary) digest values to local files
	NewContent map[string]*LocalFile

	// NewFixity maps digests to alternate digests (for new content)
	NewFixity map[string]digest.Set
}

// NewLocalStage creates a new stage for the object. The newAlg argument can be used
// to set non-standard digest algorithm if the object does not exist. newID is
// for the id on an object if it doesn't exist (The ocfl-go Object api should
// really support this, but not yet: https://github.com/srerickson/ocfl-go/issues/114)
func NewLocalStage(obj *ocfl.Object, newAlg string) (*LocalStage, error) {
	objID := obj.ID()
	if objID == "" {
		return nil, errors.New("object ID not set")
	}
	var stage = &LocalStage{
		ID:              obj.ID(),
		Version:         ocfl.V(1),
		NewState:        ocfl.PathMap{},
		NewFixity:       map[string]digest.Set{},
		ExistingContent: []string{},
		NewContent:      map[string]*LocalFile{},
		AlgID:           newAlg,
	}
	if obj.Exists() {
		inv := obj.Inventory()
		next, err := inv.Head().Next()
		if err != nil {
			return nil, err
		}
		stage.Version = next
		stage.NewState = inv.Version(0).State().PathMap()
		stage.AlgID = inv.DigestAlgorithm().ID()
		stage.ExistingContent = inv.Manifest().Digests()
	}
	return stage, nil
}

func ReadStageFile(name string) (*LocalStage, error) {
	var stage LocalStage
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

func (s LocalStage) Algs() ([]digest.Algorithm, error) {
	var err error
	algs := make([]digest.Algorithm, 1+len(s.FixityIDs))
	algs[0], err = digest.DefaultRegistry().Get(s.AlgID)
	if err != nil {
		return nil, err
	}
	for i, algID := range s.FixityIDs {
		algs[i+1], err = digest.DefaultRegistry().Get(algID)
		if err != nil {
			return nil, err
		}
	}
	return algs, nil
}

func (s *LocalStage) AddFile(localPath string, logicalPath string) error {
	if !filepath.IsAbs(localPath) {
		absPath, err := filepath.Abs(localPath)
		if err != nil {
			return err
		}
		localPath = absPath
	}
	algs, err := s.Algs()
	if err != nil {
		return err
	}
	digester := digest.NewMultiDigester(algs...)
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", localPath)
	}
	if _, err := io.Copy(digester, f); err != nil {
		return err
	}
	localFile := &LocalFile{
		Path:    localPath,
		Size:    info.Size(),
		Modtime: info.ModTime(),
	}
	s.add(logicalPath, localFile, digester.Sums())
	return nil
}

// Add adds a digestsed file to the stage as logical path.
func (s *LocalStage) AddDir(ctx context.Context, localDir, logicalDir string, withHidden bool) error {
	if !filepath.IsAbs(localDir) {
		absLocalDir, err := filepath.Abs(localDir)
		if err != nil {
			return err
		}
		localDir = absLocalDir
	}
	algs, err := s.Algs()
	if err != nil {
		return err
	}
	alg := algs[0]
	var fixity []digest.Algorithm
	if len(algs) > 1 {
		fixity = algs[1:]
	}
	filesIter, walkErr := ocfl.WalkFiles(ctx, ocfl.DirFS(localDir), ".")
	if withHidden {
		filesIter = filesIter.IgnoreHidden()
	}
	for result, err := range filesIter.Digest(ctx, alg, fixity...) {
		if err != nil {
			return err
		}
		// convert result path back to os-specific path
		logicalFilePath := path.Join(logicalDir, result.FullPath())
		localFilePath := filepath.Join(localDir, filepath.FromSlash(result.FullPath()))
		localFile := &LocalFile{
			Path:    localFilePath,
			Size:    result.Info.Size(),
			Modtime: result.Info.ModTime(),
		}
		s.add(logicalFilePath, localFile, result.Digests)
	}
	if err := walkErr(); err != nil {
		return err
	}
	return nil
}

// Write the LocalStage to file name as json
func (s LocalStage) Write(name string) error {
	stageBytes, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.WriteFile(name, stageBytes, 0644); err != nil {
		return err
	}
	return nil
}

// BuildCommit returns s as an ocfl.BuildCommit
func (s *LocalStage) BuildCommit() (*ocfl.Commit, error) {
	state := s.NewState.DigestMap()
	if err := state.Valid(); err != nil {
		return nil, err
	}
	algs, err := s.Algs()
	if err != nil {
		return nil, err
	}
	return &ocfl.Commit{
		ID: s.ID,
		Stage: &ocfl.Stage{
			State:           state,
			DigestAlgorithm: algs[0],
			ContentSource:   s,
			FixitySource:    s,
		},
	}, nil
}

// stage implements ocfl.ContentSource
func (s LocalStage) GetContent(digest string) (ocfl.FS, string) {
	localFile := s.NewContent[digest]
	if localFile == nil {
		return nil, ""
	}
	dir := filepath.Dir(localFile.Path)
	name := filepath.Base(localFile.Path)
	return ocfl.DirFS(dir), name
}

// stage implements ocfl.FixitySource
func (s LocalStage) GetFixity(digest string) digest.Set {
	return s.NewFixity[digest]
}

// Add adds a digestsed file to the stage as logical path.
func (s *LocalStage) add(logical string, local *LocalFile, digests digest.Set) {
	prevDigest := s.NewState[logical]
	newDigest := digests.Delete(s.AlgID)
	if prevDigest != newDigest {
		s.NewState[logical] = newDigest
	}
	if len(digests) > 0 {
		s.NewFixity[newDigest] = digests
	}
	alreadyCommitted := slices.Contains(s.ExistingContent, newDigest)
	_, alreadyStaged := s.NewContent[newDigest]
	if !alreadyCommitted && !alreadyStaged {
		s.NewContent[newDigest] = local
	}
}

type LocalFile struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Modtime time.Time `json:"modtime"`
}
