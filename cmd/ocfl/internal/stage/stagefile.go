package stage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/util"
)

// StageFile reprepresent a local stage file for building updates
// to OCFL ojects.
type StageFile struct {
	// Object ID
	ID string `json:"object_id"`

	// number of new object version
	NextHead ocfl.VNum `json:"next_head"`

	// State for new object versions
	NextState ocfl.PathMap `json:"next_state"`

	// digests that are already part of the object, don't need to be uploaded.
	ExistingDigests []string `json:"existing"`

	// Primary digest algorithm (sha512 or sha256)
	AlgID string `json:"digest_algorithm"`

	// LocalContent maps (primary) digest values to local files
	LocalContent map[string]*LocalFile `json:"local_content"`

	// Fixity Algorithms
	FixityIDs []string `json:"fixity_algorithms"`

	// Fixity maps digests to alternate digests (for new content)
	Fixity map[string]digest.Set `json:"fixity"`

	// optional logger
	logger *slog.Logger
}

// NewStageFile creates a new stage for the object. The newAlg argument can be used
// to set non-standard digest algorithm if the object does not exist. newID is
// for the id on an object if it doesn't exist (The ocfl-go Object api should
// really support this, but not yet: https://github.com/srerickson/ocfl-go/issues/114)
func NewStageFile(obj *ocfl.Object, newAlg string) (*StageFile, error) {
	objID := obj.ID()
	if objID == "" {
		return nil, errors.New("object ID not set")
	}
	var stage = &StageFile{
		ID:              obj.ID(),
		NextHead:        ocfl.V(1),
		NextState:       ocfl.PathMap{},
		Fixity:          map[string]digest.Set{},
		ExistingDigests: []string{},
		LocalContent:    map[string]*LocalFile{},
		AlgID:           newAlg,
	}
	if obj.Exists() {
		inv := obj.Inventory()
		next, err := inv.Head().Next()
		if err != nil {
			return nil, err
		}
		stage.NextHead = next
		stage.NextState = inv.Version(0).State().PathMap()
		stage.AlgID = inv.DigestAlgorithm().ID()
		stage.ExistingDigests = inv.Manifest().Digests()
	}
	return stage, nil
}

func ReadStageFile(name string) (*StageFile, error) {
	var stage StageFile
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

// Algs returns the stage's digest algorithms as a slice. The primary algorithm
// is first, the rest are fixity.
func (s StageFile) Algs() ([]digest.Algorithm, error) {
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

// AddFile digests a local file and adds it to the stage using the file's
// basename or 'as'.
func (s *StageFile) AddFile(localPath string, as string) error {
	if !filepath.IsAbs(localPath) {
		absPath, err := filepath.Abs(localPath)
		if err != nil {
			return err
		}
		localPath = absPath
	}
	if as == "" {
		as = filepath.Base(localPath)
	}
	if as == "." || !fs.ValidPath(as) {
		return fmt.Errorf("invalid file name: %s", as)
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
	s.add(as, localFile, digester.Sums())
	return nil
}

// AddDir walks files in a localDir and generates digests for files using the
// stage's digest algorithm. File names are added to the stage state under a the
// directory 'as' which defaults to the logical root for the object. Hidden
// files and directories are ignored unless withHidden is true. if remove is
// true, staged files under 'as' are removed from the stage state if they are
// not found in localDir. The number of concurrent digest worker goroutines is
// set with gos.
func (s *StageFile) AddDir(ctx context.Context, localDir, as string, withHidden bool, remove bool, gos int) error {
	if !filepath.IsAbs(localDir) {
		absLocalDir, err := filepath.Abs(localDir)
		if err != nil {
			return err
		}
		localDir = absLocalDir
	}
	if as == "" {
		as = "."
	}
	if !fs.ValidPath(as) {
		return fmt.Errorf("invalid directory name: %s", as)
	}
	localFS := ocfl.DirFS(localDir)
	if gos < 1 {
		gos = runtime.NumCPU()
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
	filesIter, walkErr := ocfl.WalkFiles(ctx, localFS, ".")
	if !withHidden {
		filesIter = filesIter.IgnoreHidden()
	}
	for result, err := range filesIter.DigestBatch(ctx, gos, alg, fixity...) {
		if err != nil {
			return err
		}
		// convert result path back to os-specific path
		logicalPath := path.Join(as, result.FullPath())
		localPath := filepath.Join(localDir, filepath.FromSlash(result.FullPath()))
		localFile := &LocalFile{
			Path:    localPath,
			Size:    result.Info.Size(),
			Modtime: result.Info.ModTime(),
		}
		s.add(logicalPath, localFile, result.Digests)
	}
	if err := walkErr(); err != nil {
		return err
	}
	if !remove {
		return nil
	}
	// remove files in new state (under 'as') that aren't in localDir
	for p := range s.NextState {
		statName := p
		if as != "." {
			if !strings.HasPrefix(p, as+"/") {
				continue // ignore files that aren't inside 'as'
			}
			// stat name relative to 'as'
			statName = strings.TrimPrefix(p, as+"/")
		}
		_, err := ocfl.StatFile(ctx, localFS, statName)
		if err == nil {
			continue
		}
		if errors.Is(err, fs.ErrNotExist) {
			delete(s.NextState, p)
			if s.logger != nil {
				s.logger.Info("file removed", "path", p)
			}
			continue
		}
		// other kinds of errors should be returned
		return err
	}
	return nil
}

// Write s to file name as json
func (s StageFile) Write(name string) error {
	stageBytes, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.WriteFile(name, stageBytes, 0644); err != nil {
		return err
	}
	return nil
}

// BuildCommit returns an ocfl.Commit baesd on stage state.
func (s *StageFile) BuildCommit(name, email, message string) (*ocfl.Commit, error) {
	if name == "" {
		return nil, fmt.Errorf("a name is required for the new object version")
	}
	if message == "" {
		return nil, fmt.Errorf("a message is required for the new object version")
	}
	if email != "" && !strings.HasPrefix(`email:`, email) {
		email = "email:" + email
	}
	state := s.NextState.DigestMap()
	if err := state.Valid(); err != nil {
		return nil, err
	}
	algs, err := s.Algs()
	if err != nil {
		return nil, err
	}
	return &ocfl.Commit{
		ID: s.ID,
		User: ocfl.User{
			Name:    name,
			Address: email,
		},
		Message: message,
		Stage: &ocfl.Stage{
			State:           state,
			DigestAlgorithm: algs[0],
			ContentSource:   s,
			FixitySource:    s,
		},
	}, nil
}

// stage implements ocfl.ContentSource
func (s StageFile) GetContent(digest string) (ocfl.FS, string) {
	localFile := s.LocalContent[digest]
	if localFile == nil {
		return nil, ""
	}
	dir := filepath.Dir(localFile.Path)
	name := filepath.Base(localFile.Path)
	return ocfl.DirFS(dir), name
}

// stage implements ocfl.FixitySource
func (s StageFile) GetFixity(digest string) digest.Set {
	return s.Fixity[digest]
}

// Write a list of filenames to the writer
func (s *StageFile) List(w io.Writer, withDigests bool) {
	for p, digest := range util.PathMapEachPath(s.NextState) {
		if withDigests {
			fmt.Fprintln(w, digest, p)
			continue
		}
		fmt.Fprintln(w, p)
	}
}

// Remove removes logicalPath from the stage state. If recursive is true,
// logicalPath is treated as a directory and all files under it are removed.
func (s *StageFile) Remove(logicalPath string, recursive bool) error {
	toDelete := path.Clean(logicalPath)
	switch {
	case recursive && toDelete == ".":
		// delete everything
		s.NextState = ocfl.PathMap{}
	default:
		for p := range util.PathMapEachPath(s.NextState) {
			recursiveMatch := recursive && (strings.HasPrefix(p, toDelete+"/"))
			if p == toDelete || recursiveMatch {
				delete(s.NextState, p)
				if s.logger != nil {
					s.logger.Info("file removed", "path", p)
				}
			}
		}
	}
	return nil
}

func (s *StageFile) SetLogger(l *slog.Logger) {
	s.logger = l
}

// Add adds a digestsed file to the stage as logical path.
func (s *StageFile) add(logical string, local *LocalFile, digests digest.Set) {
	prevDigest := s.NextState[logical]
	newDigest := digests.Delete(s.AlgID)
	if prevDigest != newDigest {
		if s.logger != nil {
			action := "file added"
			if prevDigest != "" {
				action = "file updated"
			}
			s.logger.Info(action, "path", logical)
		}
		s.NextState[logical] = newDigest
	}
	if len(digests) > 0 {
		s.Fixity[newDigest] = digests
	}
	alreadyCommitted := slices.Contains(s.ExistingDigests, newDigest)
	_, alreadyStaged := s.LocalContent[newDigest]
	if !alreadyCommitted && !alreadyStaged {
		s.LocalContent[newDigest] = local
	}
}

type LocalFile struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Modtime time.Time `json:"modtime"`
}
