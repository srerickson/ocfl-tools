package stage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
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
// basename. Use the [AddAs] option to change the logical name for the staged
// file.
func (s *StageFile) AddFile(localPath string, opts ...AddOption) error {
	addConf := addConfig{as: filepath.Base(localPath)}
	for _, o := range opts {
		o(&addConf)
	}
	if !filepath.IsAbs(localPath) {
		absPath, err := filepath.Abs(localPath)
		if err != nil {
			return err
		}
		localPath = absPath
	}
	if addConf.as == "." || !fs.ValidPath(addConf.as) {
		return fmt.Errorf("invalid file name: %s", addConf.as)
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
	return s.add(addConf.as, localFile, digester.Sums())
}

// AddDir walks files in a localDir and generates digests for files using the
// stage's digest algorithm. File names are added to the stage state under a the
// directory 'as' which defaults to the logical root for the object. Hidden
// files and directories are ignored unless withHidden is true. if remove is
// true, staged files under 'as' are removed from the stage state if they are
// not found in localDir. The number of concurrent digest worker goroutines is
// set with gos.
func (s *StageFile) AddDir(ctx context.Context, localDir string, opts ...AddOption) error {
	addCong := addConfig{
		as: ".",
	}
	for _, o := range opts {
		o(&addCong)
	}
	if !filepath.IsAbs(localDir) {
		absLocalDir, err := filepath.Abs(localDir)
		if err != nil {
			return err
		}
		localDir = absLocalDir
	}
	if !fs.ValidPath(addCong.as) {
		return fmt.Errorf("invalid directory name: %s", addCong.as)
	}
	localFS := ocfl.DirFS(localDir)
	if addCong.gos < 1 {
		addCong.gos = runtime.NumCPU()
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
	if !addCong.withHidden {
		filesIter = filesIter.IgnoreHidden()
	}
	for result, err := range filesIter.DigestBatch(ctx, addCong.gos, alg, fixity...) {
		if err != nil {
			return err
		}
		// convert result path back to os-specific path
		logicalPath := path.Join(addCong.as, result.FullPath())
		localPath := filepath.Join(localDir, filepath.FromSlash(result.FullPath()))
		localFile := &LocalFile{
			Path:    localPath,
			Size:    result.Info.Size(),
			Modtime: result.Info.ModTime(),
		}
		if err := s.add(logicalPath, localFile, result.Digests); err != nil {
			return err
		}
	}
	if err := walkErr(); err != nil {
		return err
	}
	if !addCong.remove {
		return nil
	}
	// remove files in new state (under 'as') that aren't in localDir
	for p := range s.NextState {
		statName := p
		if addCong.as != "." {
			if !strings.HasPrefix(p, addCong.as+"/") {
				continue // ignore files that aren't inside 'as'
			}
			// stat name relative to 'as'
			statName = strings.TrimPrefix(p, addCong.as+"/")
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

// StateErrors return an iterator that yields non-nil errors in the stage state.
// This includes validation errors and digest with no associated content.
func (s StageFile) StateErrors() iter.Seq[error] {
	return func(yield func(error) bool) {
		if err := s.NextState.DigestMap().Valid(); err != nil {
			if !yield(err) {
				return
			}
		}
		for _, digest := range s.NextState {
			if s.LocalContent[digest] != nil {
				continue
			}
			if slices.Contains(s.ExistingDigests, digest) {
				continue
			}
			err := fmt.Errorf("stage state includes a digest with no associated content: %q", digest)
			if !yield(err) {
				return
			}
		}
	}
}

// ContentErrors returns an iterator that yields non-nil errors for local files
// referenced in the stage that are either no longer readable or have changed
// size or modtime.
func (s StageFile) ContentErrors() iter.Seq[error] {
	return func(yield func(error) bool) {
		for _, file := range s.LocalContent {
			name := file.Path
			info, err := os.Stat(name)
			if err == nil {
				if info.Size() != file.Size {
					err = fmt.Errorf("file has changed size: %q", name)
				}
				if info.ModTime().Compare(file.Modtime) != 0 {
					err = fmt.Errorf("file has changed modtime: %q", name)
				}
			}
			if err != nil {
				if !yield(err) {
					return
				}
			}
		}
	}
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
func (s *StageFile) add(logical string, local *LocalFile, digests digest.Set) error {
	prevDigest := s.NextState[logical]
	newDigest := digests.Delete(s.AlgID)
	if prevDigest != newDigest {
		if conflict := pathConflict(s.NextState, logical); conflict != "" {
			err := fmt.Errorf("can't add %q because of conflict with %q", logical, conflict)
			return err
		}
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
	return nil
}

type LocalFile struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Modtime time.Time `json:"modtime"`
}

// AddOption is a function that can be used to configure the behavior of
// [AddDir] or [AddFile]
type AddOption func(c *addConfig)

type addConfig struct {
	as         string
	withHidden bool
	remove     bool
	gos        int
}

// AddAs sets the logical name for staged content. When used with [AddDir], name
// is treated as a directory in which files from the source directory are added.
// For [AddFile], name is treated as a logical filename.
func AddAs(name string) AddOption {
	return func(c *addConfig) {
		c.as = name
	}
}

// AddAndRemove is an option for [AddDir] to also remove files in the stage that
// aren't included in the source directory. If used with [AddAs], only files
// under the directory named with [AddAs] are removed. This option is ignored if
// used with [AddFile].
func AddAndRemove() AddOption {
	return func(c *addConfig) {
		c.remove = true
	}
}

// AddWithHidden is an option for [AddDir] to included hidden files and directories
// from the source directory. This options is ignored if used with [AddFile].
func AddWithHidden() AddOption {
	return func(c *addConfig) {
		c.withHidden = true
	}
}

// AddDigestJobs is an option for [AddDir] that sets the number of goroutines used
// to digest files in the source directory.
func AddDigestJobs(num int) AddOption {
	return func(c *addConfig) {
		c.gos = num
	}
}

// chek
// return any keys in state that would conflict with newName
func pathConflict(state ocfl.PathMap, newName string) string {
	for name := range state {
		if strings.HasPrefix(name, newName+"/") || strings.HasPrefix(newName, name+"/") {
			return name
		}
	}
	return ""
}
