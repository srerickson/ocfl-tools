package util

import (
	"encoding/json"
	"log/slog"
	"os"
	"slices"
	"time"

	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
)

// NewLocalStage creates a new stage for the object. The newAlg argument can be used
// to set non-standard digest algorithm if the object does not exist. newID is
// for the id on an object if it doesn't exist (The ocfl-go Object api should
// really support this, but not yet: https://github.com/srerickson/ocfl-go/issues/114)
func NewLocalStage(obj *ocfl.Object, newID string, newAlg string) (*LocalStage, error) {
	var stage = &LocalStage{
		ID:              newID,
		Version:         ocfl.V(1),
		NewState:        ocfl.PathMap{},
		NewFixity:       map[string]digest.Set{},
		ExistingContent: []string{},
		NewContent:      map[string]LocalFile{},
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

type LocalStage struct {
	// Object ID
	ID string

	// number of new object version
	Version ocfl.VNum

	// State for new object versions
	NewState ocfl.PathMap

	// digests that are already part of the object, don't need to be uploaded.
	ExistingContent []string

	// Primary digest algorithm (sha512 or sha256)
	AlgID string

	// NewContent maps (primary) digest values to local files
	NewContent map[string]LocalFile

	// NewFixity maps digests to alternate digests (for new content)
	NewFixity map[string]digest.Set
}

func (s LocalStage) Alg() (digest.Algorithm, error) {
	return digest.DefaultRegistry().Get(s.AlgID)
}

// AddFile adds a digestsed file to the stage as logical path.
func (stage *LocalStage) AddFile(logicalPath string, digests *ocfl.FileDigests, localDir string, logger *slog.Logger) error {
	oldDigest := stage.NewState[logicalPath]
	newDigest := digests.Digests.Delete(digests.Algorithm.ID())
	if oldDigest != newDigest {
		stage.NewState[logicalPath] = newDigest
		switch {
		case oldDigest == "":
			logger.Info("new file", "path", logicalPath)
		default:
			logger.Info("updated file", "path", logicalPath)
		}
	}
	if len(digests.Digests) > 0 {
		stage.NewFixity[newDigest] = digests.Digests
	}
	alreadyCommitted := slices.Contains(stage.ExistingContent, newDigest)
	_, alreadyStaged := stage.NewContent[newDigest]
	if !alreadyCommitted && !alreadyStaged {
		stage.NewContent[newDigest] = LocalFile{
			LocalDir:  localDir,
			LocalPath: digests.FullPath(),
			Size:      digests.Info.Size(),
			Modtime:   digests.Info.ModTime(),
		}
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
	alg, err := s.Alg()
	if err != nil {
		return nil, err
	}
	return &ocfl.Commit{
		ID: s.ID,
		Stage: &ocfl.Stage{
			State:           state,
			DigestAlgorithm: alg,
			ContentSource:   s,
			FixitySource:    s,
		},
	}, nil
}

// stage implements ocfl.ContentSource
func (s LocalStage) GetContent(digest string) (ocfl.FS, string) {
	localFile, exists := s.NewContent[digest]
	if !exists {
		return nil, ""
	}
	return ocfl.DirFS(localFile.LocalDir), localFile.LocalPath
}

// stage implements ocfl.FixitySource
func (s LocalStage) GetFixity(digest string) digest.Set {
	return s.NewFixity[digest]
}

type LocalFile struct {
	LocalDir  string    `json:"local"`
	LocalPath string    `json:"path"`
	Size      int64     `json:"size"`
	Modtime   time.Time `json:"modtime"`
}
